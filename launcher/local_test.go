package launcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLocalLaunchAndCloseTemporaryProfile(t *testing.T) {
	local := Local{
		CommandContext: helperCommandContext,
		PollInterval:   time.Millisecond,
	}
	process, err := local.Launch(t.Context(), Options{
		ExecutablePath: os.Args[0],
		InitialURL:     "https://example.test/start",
		StartupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	profileDir := process.UserDataDir()
	if got, want := process.RemoteURL(), "http://127.0.0.1:43210"; got != want {
		t.Fatalf("RemoteURL() = %q, want %q", got, want)
	}
	if info, err := os.Stat(profileDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("profile stat = (%v, %v), want mode 0700", info, err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
		t.Fatalf("temporary profile still exists: %v", err)
	}
}

func TestLocalLaunchPreservesConfiguredProfile(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "profile")
	local := Local{CommandContext: helperCommandContext, PollInterval: time.Millisecond}
	process, err := local.Launch(t.Context(), Options{
		ExecutablePath: os.Args[0],
		UserDataDir:    profileDir,
		StartupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(profileDir); err != nil {
		t.Fatalf("configured profile was removed: %v", err)
	}
}

func TestLocalResolveExecutable(t *testing.T) {
	local := Local{}
	resolved, err := local.ResolveExecutable(os.Args[0])
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if resolved != os.Args[0] {
		t.Fatalf("ResolveExecutable() = %q, want %q", resolved, os.Args[0])
	}
}

func TestParseDevToolsActivePort(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid", input: "9222\n/devtools/browser/abc\n", want: "http://127.0.0.1:9222"},
		{name: "missing path", input: "9222\n", wantErr: true},
		{name: "bad port", input: "70000\n/devtools/browser/abc\n", wantErr: true},
		{name: "bad path", input: "9222\n/devtools/page/abc\n", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDevToolsActivePort([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseDevToolsActivePort() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("parseDevToolsActivePort() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLaunchArgsVisibleByDefault(t *testing.T) {
	got := launchArgs("/tmp/profile", Options{InitialURL: "https://example.test", Args: []string{"--lang=en-US"}})
	for _, want := range []string{"--remote-debugging-port=0", "--user-data-dir=/tmp/profile", "--lang=en-US", "https://example.test"} {
		if !slices.Contains(got, want) {
			t.Fatalf("launchArgs() = %q, missing %q", got, want)
		}
	}
	if slices.Contains(got, "--headless=new") {
		t.Fatalf("launchArgs() = %q, unexpectedly headless", got)
	}
}

func TestValidateAdditionalArgs(t *testing.T) {
	for _, arg := range []string{
		"--headless",
		"--headless=old",
		"--remote-debugging-address=0.0.0.0",
		"--remote-debugging-port=9222",
		"--user-data-dir=/tmp/shared",
	} {
		t.Run(arg, func(t *testing.T) {
			if err := validateAdditionalArgs([]string{arg}); err == nil {
				t.Fatalf("validateAdditionalArgs(%q) error = nil", arg)
			}
		})
	}
	if err := validateAdditionalArgs([]string{"--lang=en-US"}); err != nil {
		t.Fatalf("validateAdditionalArgs() error = %v", err)
	}
}

func helperCommandContext(ctx context.Context, _ string, args ...string) *exec.Cmd {
	cmdArgs := []string{"-test.run=TestLocalBrowserHelperProcess", "--"}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), "NAVIGO_HELPER_PROCESS=1")
	return cmd
}

func TestLocalBrowserHelperProcess(t *testing.T) {
	if os.Getenv("NAVIGO_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	separator := slices.Index(args, "--")
	if separator < 0 {
		os.Exit(2)
	}
	var profileDir string
	for _, arg := range args[separator+1:] {
		if strings.HasPrefix(arg, "--user-data-dir=") {
			profileDir = strings.TrimPrefix(arg, "--user-data-dir=")
		}
	}
	if profileDir == "" {
		os.Exit(3)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "DevToolsActivePort"), []byte("43210\n/devtools/browser/test\n"), 0o600); err != nil {
		os.Exit(4)
	}
	select {}
}
