package navigo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/albertocavalcante/navigo/cdp"
	cdpbrowser "github.com/albertocavalcante/navigo/cdp/proto/browser"
)

var (
	// ErrDownloadsNotEnabled is returned by WaitForDownload before
	// EnableDownloads has been called.
	ErrDownloadsNotEnabled = errors.New("downloads not enabled")
	// ErrDownloadCanceled reports that the browser canceled the download.
	ErrDownloadCanceled = errors.New("download canceled")
	// ErrDownloadIdle reports that a started download made no progress within
	// DownloadOptions.IdleTimeout. The download is not consumed: a later
	// WaitForDownload call can still pick it up.
	ErrDownloadIdle = errors.New("download idle")
	// ErrDownloadIncomplete reports a download the browser called completed
	// whose file is missing, partial or not a regular file. Such a file is
	// never returned as a success.
	ErrDownloadIncomplete = errors.New("download incomplete")
)

const (
	maxDownloadNameBytes = 200
	maxCollisionTries    = 10000
	downloadEventBuffer  = 256
)

// DownloadOptions tunes [Browser.WaitForDownload]. The zero value waits until
// ctx ends, does not hash and has no idle limit.
type DownloadOptions struct {
	// IdleTimeout fails the wait with ErrDownloadIdle when a started download
	// reports no event for this long. Zero disables the check.
	IdleTimeout time.Duration
	// ComputeSHA256 fills Download.SHA256.
	ComputeSHA256 bool
}

// Download describes one completed download.
type Download struct {
	GUID string
	URL  string
	// SuggestedFilename is the name the browser proposed, unmodified.
	SuggestedFilename string
	// GUIDPath is where the browser wrote the file. It no longer exists after
	// a successful wait because the file is renamed to Path.
	GUIDPath string
	// Path is the final location: the sanitised suggested name inside the
	// download directory, with a numeric suffix if the name was taken.
	Path string
	Size int64
	// SHA256 is the lowercase hex digest; empty unless requested.
	SHA256 string
}

type downloadItem struct {
	guid, url, name string
	state           cdpbrowser.DownloadProgressState
	received, total float64
	filePath        string
	dir             string
	last            time.Time
	consumed        bool
}

type downloadTracker struct {
	mu      sync.Mutex
	dir     string
	items   map[string]*downloadItem
	order   []string
	changed chan struct{}
	closed  bool
}

func (t *downloadTracker) notifyLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}

func (t *downloadTracker) run(events <-chan cdp.Event) {
	for event := range events {
		switch event.Method {
		case cdpbrowser.EventDownloadWillBegin:
			begin, err := cdpbrowser.DecodeDownloadWillBegin(event.Params)
			if err != nil {
				continue
			}
			t.mu.Lock()
			if item, ok := t.items[begin.GUID]; ok {
				// A repeated begin never resurrects or resets a known download;
				// it only fills in details a progress-first event lacked.
				if item.name == "" {
					item.name, item.url = begin.SuggestedFilename, begin.URL
				}
			} else {
				t.order = append(t.order, begin.GUID)
				t.items[begin.GUID] = &downloadItem{
					guid: begin.GUID, url: begin.URL, name: begin.SuggestedFilename,
					state: cdpbrowser.DownloadProgressStateInProgress, dir: t.dir, last: time.Now(),
				}
			}
			t.notifyLocked()
			t.mu.Unlock()
		case cdpbrowser.EventDownloadProgress:
			progress, err := cdpbrowser.DecodeDownloadProgress(event.Params)
			if err != nil {
				continue
			}
			t.mu.Lock()
			item, ok := t.items[progress.GUID]
			if !ok { // progress without a begin event: track it anyway
				item = &downloadItem{guid: progress.GUID, dir: t.dir}
				t.items[progress.GUID] = item
				t.order = append(t.order, progress.GUID)
			}
			item.state = progress.State
			item.received, item.total = progress.ReceivedBytes, progress.TotalBytes
			item.filePath = progress.FilePath
			item.last = time.Now()
			t.notifyLocked()
			t.mu.Unlock()
		}
	}

	t.mu.Lock()
	t.closed = true
	t.notifyLocked()
	t.mu.Unlock()
}

// EnableDownloads makes the browser save downloads into dir (which must
// exist) under GUID names and report their progress. Call it before the
// action that triggers a download; later calls only change the directory.
func (b *Browser) EnableDownloads(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("enable downloads: empty directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("enable downloads: resolve directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("enable downloads: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("enable downloads: %q is not a directory", abs)
	}

	b.downloadsMu.Lock()
	defer b.downloadsMu.Unlock()

	root := b.conn.RootSession()
	var (
		events <-chan cdp.Event
		cancel func()
	)
	if b.downloads == nil {
		events, cancel = root.Subscribe(downloadEventBuffer)
	}

	call := cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorParamsBehaviorAllowAndName).
		WithDownloadPath(abs).WithEventsEnabled(true)
	if err := call.Do(ctx, root); err != nil {
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("enable downloads: %w", err)
	}

	if b.downloads == nil {
		// The subscription ends when the connection closes its channel.
		b.downloads = &downloadTracker{dir: abs, items: map[string]*downloadItem{}, changed: make(chan struct{})}
		go b.downloads.run(events)
	} else {
		b.downloads.mu.Lock()
		b.downloads.dir = abs
		b.downloads.mu.Unlock()
	}

	return nil
}

// WaitForDownload waits for the first download, in start order, that has
// completed or been canceled and was not returned before, including one that
// finished before the call. A stalled earlier download never blocks a ready
// later one. It returns only after the browser reports completion and the file
// is verified, then renames it atomically to a sanitised, collision-free name.
// ctx bounds the whole wait; IdleTimeout applies only while nothing is ready.
func (b *Browser) WaitForDownload(ctx context.Context, opts DownloadOptions) (Download, error) {
	return b.waitForDownload(ctx, "", opts)
}

// WaitForDownloadGUID is like WaitForDownload for one specific download.
func (b *Browser) WaitForDownloadGUID(ctx context.Context, guid string, opts DownloadOptions) (Download, error) {
	if guid == "" {
		return Download{}, errors.New("wait for download: empty GUID")
	}

	return b.waitForDownload(ctx, guid, opts)
}

// DiscardDownload stops tracking a download (for example one that stalled and
// was reported as idle) so later waits skip it, and removes its GUID-named
// file if present. The browser may still be writing it; use a fresh directory
// via EnableDownloads if that matters.
func (b *Browser) DiscardDownload(guid string) error {
	tracker := b.currentDownloads()
	if tracker == nil {
		return fmt.Errorf("discard download: %w", ErrDownloadsNotEnabled)
	}

	tracker.mu.Lock()
	item, ok := tracker.items[guid]
	if !ok {
		tracker.mu.Unlock()
		return fmt.Errorf("discard download %q: unknown download", guid)
	}
	item.consumed = true
	dir := item.dir
	tracker.notifyLocked()
	tracker.mu.Unlock()

	if dir != "" && guid == filepath.Base(guid) && guid != "." && guid != ".." {
		if err := os.Remove(filepath.Join(dir, guid)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("discard download %q: %w", guid, err)
		}
	}

	return nil
}

func (b *Browser) currentDownloads() *downloadTracker {
	b.downloadsMu.Lock()
	defer b.downloadsMu.Unlock()

	return b.downloads
}

func isTerminal(state cdpbrowser.DownloadProgressState) bool {
	return state == cdpbrowser.DownloadProgressStateCompleted || state == cdpbrowser.DownloadProgressStateCanceled
}

func (b *Browser) waitForDownload(ctx context.Context, guid string, opts DownloadOptions) (Download, error) {
	tracker := b.currentDownloads()
	if tracker == nil {
		return Download{}, fmt.Errorf("wait for download: %w", ErrDownloadsNotEnabled)
	}

	for {
		tracker.mu.Lock()
		var ready *downloadItem
		var lastActivity time.Time
		var pending bool
		var pendingName string
		for _, g := range tracker.order {
			it := tracker.items[g]
			if it.consumed || (guid != "" && g != guid) {
				continue
			}
			if isTerminal(it.state) {
				ready = it
				break
			}
			pending = true
			if it.last.After(lastActivity) {
				lastActivity, pendingName = it.last, it.name
			}
		}
		changed, closed := tracker.changed, tracker.closed
		var snapshot downloadItem
		if ready != nil {
			snapshot = *ready
			ready.consumed = true
		}
		tracker.mu.Unlock()

		if ready != nil {
			label := snapshot.name
			if label == "" {
				label = snapshot.guid
			}
			if snapshot.state == cdpbrowser.DownloadProgressStateCanceled {
				return Download{}, fmt.Errorf("wait for download %q: %w", label, ErrDownloadCanceled)
			}

			return finalizeDownload(snapshot, opts)
		}
		if closed {
			return Download{}, errors.New("wait for download: browser connection closed")
		}

		var (
			idle  <-chan time.Time
			timer *time.Timer
		)
		if pending && opts.IdleTimeout > 0 {
			remaining := opts.IdleTimeout - time.Since(lastActivity)
			if remaining <= 0 {
				if pendingName == "" {
					pendingName = "download"
				}
				return Download{}, fmt.Errorf("wait for download %q: no progress for %s: %w", pendingName, opts.IdleTimeout, ErrDownloadIdle)
			}
			timer = time.NewTimer(remaining)
			idle = timer.C
		}

		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return Download{}, fmt.Errorf("wait for download: %w", context.Cause(ctx))
		case <-changed:
		case <-idle:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func finalizeDownload(item downloadItem, opts DownloadOptions) (Download, error) {
	incomplete := func(format string, args ...any) (Download, error) {
		label := item.name
		if label == "" {
			label = item.guid
		}

		return Download{}, fmt.Errorf("finish download %q: %s: %w", label, fmt.Sprintf(format, args...), ErrDownloadIncomplete)
	}
	dir := item.dir
	if dir == "" {
		return incomplete("download directory unknown")
	}

	if item.guid == "" || item.guid != filepath.Base(item.guid) || item.guid == "." || item.guid == ".." {
		return incomplete("unsafe download identifier")
	}
	guidPath := filepath.Join(dir, item.guid)
	// Prefer the browser-reported path when it is exactly where we expect.
	if item.filePath != "" && filepath.Clean(item.filePath) == guidPath {
		guidPath = item.filePath
	}
	if strings.HasSuffix(guidPath, ".crdownload") {
		return incomplete("temporary file")
	}

	info, err := os.Lstat(guidPath)
	if err != nil {
		return incomplete("file missing: %v", err)
	}
	if !info.Mode().IsRegular() {
		return incomplete("not a regular file")
	}
	if item.total > 0 && info.Size() != int64(item.total) {
		return incomplete("size %d does not match expected %d", info.Size(), int64(item.total))
	}

	result := Download{
		GUID: item.guid, URL: item.url, SuggestedFilename: item.name,
		GUIDPath: guidPath, Size: info.Size(),
	}
	if opts.ComputeSHA256 {
		sum, err := hashFile(guidPath)
		if err != nil {
			return Download{}, fmt.Errorf("finish download %q: hash: %w", item.guid, err)
		}
		result.SHA256 = sum
	}

	final, err := moveToUniqueName(guidPath, dir, sanitizeDownloadName(item.name))
	if err != nil {
		return Download{}, fmt.Errorf("finish download %q: %w", item.guid, err)
	}
	result.Path = final

	return result, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// moveToUniqueName claims a free name with O_EXCL and then atomically renames
// the download over that placeholder, so concurrent callers never clobber
// each other or an existing file.
func moveToUniqueName(src, dir, name string) (string, error) {
	ext := filepath.Ext(name)
	if strings.EqualFold(filepath.Ext(strings.TrimSuffix(name, ext)), ".tar") {
		ext = name[len(name)-len(ext)-len(".tar"):]
	}
	stem := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 1; i <= maxCollisionTries; i++ {
		dest := filepath.Join(dir, candidate)
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if err := f.Close(); err != nil {
				_ = os.Remove(dest)
				return "", err
			}
			if err := os.Rename(src, dest); err != nil {
				_ = os.Remove(dest)
				return "", err
			}

			return dest, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
	}

	return "", fmt.Errorf("no free name for %q", name)
}

// sanitizeDownloadName reduces a browser-suggested name to a single safe path
// element: no directories, control characters, leading dots or oversize names.
func sanitizeDownloadName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == utf8.RuneError,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return -1
		case strings.ContainsRune(`<>:"|?*`, r):
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ". ")
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "download"
	}
	if isWindowsReservedName(name) {
		name = "_" + name
	}

	if len(name) > maxDownloadNameBytes {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		cut := maxDownloadNameBytes - len(ext)
		stem := name[:len(name)-len(ext)]
		for cut > 0 && !utf8.ValidString(stem[:min(cut, len(stem))]) {
			cut--
		}
		name = stem[:min(cut, len(stem))] + ext
	}

	return name
}

// isWindowsReservedName reports whether the part before the first dot is a
// reserved device name (CON, PRN, AUX, NUL, COM1-9, LPT1-9), with any extension.
func isWindowsReservedName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(strings.TrimSpace(base)) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}

	return false
}
