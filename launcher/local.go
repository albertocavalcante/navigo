package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultStartupTimeout = 15 * time.Second
	defaultPollInterval   = 50 * time.Millisecond
)

// Local launches a system-installed Chromium browser with a dedicated CDP
// endpoint. CommandContext and LookPath are optional test seams.
type Local struct {
	CommandContext func(context.Context, string, ...string) *exec.Cmd
	LookPath       func(string) (string, error)
	PollInterval   time.Duration
}

// Options configures a local browser launch. The browser is visible unless
// Headless is explicitly enabled.
type Options struct {
	ExecutablePath string
	UserDataDir    string
	InitialURL     string
	Headless       bool
	Args           []string
	StartupTimeout time.Duration
	Stdout         io.Writer
	Stderr         io.Writer
}

// Process is a launched local browser. Close terminates only this process and
// removes its user-data directory only when Local created that directory.
type Process struct {
	cmd         *exec.Cmd
	remoteURL   string
	userDataDir string
	temporary   bool

	waitOnce sync.Once
	waitErr  error
}

// ResolveExecutable returns the explicit or discovered Chrome/Chromium binary
// without starting a process.
func (l Local) ResolveExecutable(explicit string) (string, error) {
	return l.executable(explicit)
}

// RemoteURL returns the HTTP discovery endpoint accepted by [Remote.Connect].
func (p *Process) RemoteURL() string {
	if p == nil {
		return ""
	}
	return p.remoteURL
}

// UserDataDir returns the dedicated browser profile directory.
func (p *Process) UserDataDir() string {
	if p == nil {
		return ""
	}
	return p.userDataDir
}

// Wait waits for the browser process to exit.
func (p *Process) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
	})
	return p.waitErr
}

// Close terminates the browser process and cleans up a temporary profile.
func (p *Process) Close() error {
	if p == nil {
		return nil
	}
	var closeErr error
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			closeErr = fmt.Errorf("interrupt browser process: %w", err)
		}
		done := make(chan error, 1)
		go func() { done <- p.Wait() }()
		select {
		case err := <-done:
			if err != nil && !isExpectedProcessExit(err) && closeErr == nil {
				closeErr = fmt.Errorf("wait for browser process: %w", err)
			}
		case <-time.After(2 * time.Second):
			if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && closeErr == nil {
				closeErr = fmt.Errorf("kill browser process: %w", err)
			}
			if err := <-done; err != nil && !isExpectedProcessExit(err) && closeErr == nil {
				closeErr = fmt.Errorf("wait for killed browser process: %w", err)
			}
		}
	}
	if p.temporary {
		if err := os.RemoveAll(p.userDataDir); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("remove temporary browser profile: %w", err)
		}
	}
	return closeErr
}

// Launch starts a browser and waits until its CDP discovery endpoint is ready.
func (l Local) Launch(ctx context.Context, opts Options) (*Process, error) {
	if err := validateAdditionalArgs(opts.Args); err != nil {
		return nil, err
	}
	executable, err := l.executable(opts.ExecutablePath)
	if err != nil {
		return nil, err
	}

	profileDir, temporary, err := prepareProfile(opts.UserDataDir)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		if temporary {
			_ = os.RemoveAll(profileDir)
		}
	}

	args := launchArgs(profileDir, opts)
	commandContext := l.CommandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(ctx, executable, args...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if startErr := cmd.Start(); startErr != nil {
		cleanup()
		return nil, fmt.Errorf("start browser process: %w", startErr)
	}

	process := &Process{cmd: cmd, userDataDir: profileDir, temporary: temporary}
	timeout := opts.StartupTimeout
	if timeout <= 0 {
		timeout = defaultStartupTimeout
	}
	pollInterval := l.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	remoteURL, err := waitForDevToolsActivePort(ctx, filepath.Join(profileDir, "DevToolsActivePort"), timeout, pollInterval)
	if err != nil {
		_ = process.Close()
		return nil, err
	}
	process.remoteURL = remoteURL
	return process, nil
}

func (l Local) executable(explicit string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("stat browser executable: %w", err)
		}
		if info.IsDir() {
			return "", errors.New("browser executable is a directory")
		}
		return explicit, nil
	}

	lookPath := l.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, candidate := range browserCandidates(runtime.GOOS) {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if resolved, err := lookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("find browser executable: Chrome or Chromium is not installed")
}

func browserCandidates(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
	case "windows":
		return []string{"chrome.exe", "msedge.exe", "chromium.exe"}
	default:
		return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	}
}

func prepareProfile(configured string) (string, bool, error) {
	if strings.TrimSpace(configured) == "" {
		dir, err := os.MkdirTemp("", "navigo-profile-")
		if err != nil {
			return "", false, fmt.Errorf("create temporary browser profile: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return "", false, fmt.Errorf("secure temporary browser profile: %w", err)
		}
		return dir, true, nil
	}

	dir, err := filepath.Abs(configured)
	if err != nil {
		return "", false, fmt.Errorf("resolve browser profile: %w", err)
	}
	if mkdirErr := os.MkdirAll(dir, 0o700); mkdirErr != nil {
		return "", false, fmt.Errorf("create browser profile: %w", mkdirErr)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", false, fmt.Errorf("inspect browser profile: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("browser profile must be a real directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", false, fmt.Errorf("secure browser profile: %w", err)
	}
	return dir, false, nil
}

func launchArgs(profileDir string, opts Options) []string {
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	}
	args = append(args, opts.Args...)
	if initialURL := strings.TrimSpace(opts.InitialURL); initialURL != "" {
		args = append(args, initialURL)
	}
	return args
}

func validateAdditionalArgs(args []string) error {
	reserved := []string{
		"--headless",
		"--remote-debugging-address",
		"--remote-debugging-port",
		"--user-data-dir",
	}
	for _, arg := range args {
		arg = strings.ToLower(strings.TrimSpace(arg))
		for _, flag := range reserved {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				return fmt.Errorf("browser argument %q is managed by launcher options", arg)
			}
		}
	}
	return nil
}

func waitForDevToolsActivePort(ctx context.Context, file string, timeout, pollInterval time.Duration) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastParseErr error
	for {
		data, err := os.ReadFile(file)
		if err == nil {
			remoteURL, parseErr := parseDevToolsActivePort(data)
			if parseErr == nil {
				return remoteURL, nil
			}
			// Chrome may expose the file before both lines are flushed. Keep
			// polling until the complete file is readable or startup expires.
			lastParseErr = parseErr
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read DevToolsActivePort: %w", err)
		}
		select {
		case <-waitCtx.Done():
			if lastParseErr != nil {
				return "", fmt.Errorf("wait for DevToolsActivePort: %w: %v", waitCtx.Err(), lastParseErr)
			}
			return "", fmt.Errorf("wait for DevToolsActivePort: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func parseDevToolsActivePort(data []byte) (string, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return "", errors.New("parse DevToolsActivePort: expected port and browser path")
	}
	port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || port <= 0 || port > 65535 {
		return "", errors.New("parse DevToolsActivePort: invalid port")
	}
	if browserPath := strings.TrimSpace(lines[1]); !strings.HasPrefix(browserPath, "/devtools/browser/") {
		return "", errors.New("parse DevToolsActivePort: invalid browser websocket path")
	}
	return "http://127.0.0.1:" + strconv.Itoa(port), nil
}

func isExpectedProcessExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
