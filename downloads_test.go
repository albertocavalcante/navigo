package navigo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpbrowser "github.com/albertocavalcante/navigo/cdp/proto/browser"
	"github.com/albertocavalcante/navigo/launcher"
)

func newFakeBrowser(t *testing.T) (*Browser, *fakeTransport) {
	t.Helper()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	browser := &Browser{
		conn:    conn,
		version: launcher.Version{Browser: "Chrome"},
		cancel:  func() {},
		done:    make(chan error, 1),
	}
	go func() { browser.done <- conn.Run(context.Background()) }()
	t.Cleanup(func() { _ = browser.Close() })

	return browser, transport
}

func enableDownloadsFake(t *testing.T, browser *Browser, transport *fakeTransport, dir string) {
	t.Helper()

	errCh := make(chan error, 1)
	go func() { errCh <- browser.EnableDownloads(context.Background(), dir) }()
	expectMethodAndReply(t, transport, 1, cdpbrowser.CommandSetDownloadBehavior, `{}`)
	if err := <-errCh; err != nil {
		t.Fatalf("EnableDownloads() error = %v", err)
	}
}

func sendBegin(transport *fakeTransport, guid, name string) {
	transport.reads <- []byte(`{"method":"Browser.downloadWillBegin","params":{"frameId":"f","guid":"` + guid + `","url":"http://x/","suggestedFilename":"` + name + `"}}`)
}

func sendProgress(transport *fakeTransport, guid, state string, received, total int) {
	transport.reads <- []byte(`{"method":"Browser.downloadProgress","params":{"guid":"` + guid + `","state":"` + state + `","receivedBytes":` + strconv.Itoa(received) + `,"totalBytes":` + strconv.Itoa(total) + `}}`)
}

func TestEnableDownloadsSendsAllowAndName(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	errCh := make(chan error, 1)
	go func() { errCh <- browser.EnableDownloads(context.Background(), dir) }()

	select {
	case sent := <-transport.sends:
		s := string(sent)
		for _, want := range []string{`"Browser.setDownloadBehavior"`, `"allowAndName"`, `"eventsEnabled":true`, `"downloadPath"`} {
			if !strings.Contains(s, want) {
				t.Fatalf("request %s missing %s", s, want)
			}
		}
		transport.reads <- []byte(`{"id":1,"result":{}}`)
	case <-time.After(time.Second):
		t.Fatal("no request sent")
	}
	if err := <-errCh; err != nil {
		t.Fatalf("EnableDownloads() error = %v", err)
	}
}

func TestEnableDownloadsRejectsBadDir(t *testing.T) {
	t.Parallel()

	browser, _ := newFakeBrowser(t)
	if err := browser.EnableDownloads(context.Background(), ""); err == nil {
		t.Fatal("empty dir: want error")
	}
	if err := browser.EnableDownloads(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing dir: want error")
	}
}

func TestWaitForDownloadRequiresEnable(t *testing.T) {
	t.Parallel()

	browser, _ := newFakeBrowser(t)
	if _, err := browser.WaitForDownload(context.Background(), DownloadOptions{}); !errors.Is(err, ErrDownloadsNotEnabled) {
		t.Fatalf("err = %v, want ErrDownloadsNotEnabled", err)
	}
}

func TestWaitForDownloadCompletedRenamesAndHashes(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)

	content := []byte("hello")
	guidPath := filepath.Join(dir, "guid-1")
	if err := os.WriteFile(guidPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	// Events arrive before WaitForDownload is called: must not be lost.
	sendBegin(transport, "guid-1", "../../etc/report.pdf")
	sendProgress(transport, "guid-1", "completed", 5, 5)
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := browser.WaitForDownload(ctx, DownloadOptions{ComputeSHA256: true})
	if err != nil {
		t.Fatalf("WaitForDownload() error = %v", err)
	}
	if got.SuggestedFilename != "../../etc/report.pdf" {
		t.Fatalf("SuggestedFilename = %q", got.SuggestedFilename)
	}
	if got.GUIDPath != guidPath {
		t.Fatalf("GUIDPath = %q", got.GUIDPath)
	}
	if want := filepath.Join(dir, "report.pdf"); got.Path != want {
		t.Fatalf("Path = %q, want %q", got.Path, want)
	}
	if got.Size != 5 {
		t.Fatalf("Size = %d", got.Size)
	}
	sum := sha256.Sum256(content)
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA256 = %q", got.SHA256)
	}
	if _, err := os.Stat(guidPath); !os.IsNotExist(err) {
		t.Fatalf("guid file should be gone, stat err = %v", err)
	}
	if data, err := os.ReadFile(got.Path); err != nil || string(data) != "hello" {
		t.Fatalf("final file = %q, %v", data, err)
	}
}

func TestWaitForDownloadCollisionSafe(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "g2"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendBegin(transport, "g2", "a.txt")
	sendProgress(transport, "g2", "completed", 3, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := browser.WaitForDownload(ctx, DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "a (1).txt"); got.Path != want {
		t.Fatalf("Path = %q, want %q", got.Path, want)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "old" {
		t.Fatalf("existing file clobbered: %q", data)
	}
}

func TestWaitForDownloadCanceled(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	enableDownloadsFake(t, browser, transport, t.TempDir())
	sendBegin(transport, "g3", "x.bin")
	sendProgress(transport, "g3", "canceled", 1, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := browser.WaitForDownload(ctx, DownloadOptions{}); !errors.Is(err, ErrDownloadCanceled) {
		t.Fatalf("err = %v, want ErrDownloadCanceled", err)
	}
}

func TestWaitForDownloadPartialIsNeverSuccess(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	enableDownloadsFake(t, browser, transport, t.TempDir())
	// "completed" but the file is missing: must fail, not succeed.
	sendBegin(transport, "g4", "x.bin")
	sendProgress(transport, "g4", "completed", 10, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := browser.WaitForDownload(ctx, DownloadOptions{}); !errors.Is(err, ErrDownloadIncomplete) {
		t.Fatalf("missing file: err = %v, want ErrDownloadIncomplete", err)
	}
}

func TestWaitForDownloadSizeMismatchIsIncomplete(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)
	if err := os.WriteFile(filepath.Join(dir, "g5"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendBegin(transport, "g5", "x.bin")
	sendProgress(transport, "g5", "completed", 10, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := browser.WaitForDownload(ctx, DownloadOptions{}); !errors.Is(err, ErrDownloadIncomplete) {
		t.Fatalf("err = %v, want ErrDownloadIncomplete", err)
	}
}

func TestWaitForDownloadIdleTimeout(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	enableDownloadsFake(t, browser, transport, t.TempDir())
	sendBegin(transport, "g6", "x.bin")
	sendProgress(transport, "g6", "inProgress", 1, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := browser.WaitForDownload(ctx, DownloadOptions{IdleTimeout: 100 * time.Millisecond})
	if !errors.Is(err, ErrDownloadIdle) {
		t.Fatalf("err = %v, want ErrDownloadIdle", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("idle timeout did not fire promptly")
	}
}

func TestWaitForDownloadContextDeadline(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	enableDownloadsFake(t, browser, transport, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := browser.WaitForDownload(ctx, DownloadOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestSanitizeDownloadName(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 300) + ".txt"
	tests := []struct{ in, want string }{
		{"report.pdf", "report.pdf"},
		{"../../etc/passwd", "passwd"},
		{`..\..\win\evil.exe`, "evil.exe"},
		{"/abs/path.txt", "path.txt"},
		{"", "download"},
		{"..", "download"},
		{".", "download"},
		{".hidden", "hidden"},
		{"a\x00b\nc.txt", "abc.txt"},
		{"  spaced  .txt ", "spaced  .txt"},
		{"dir/", "download"},
	}
	for _, tt := range tests {
		if got := sanitizeDownloadName(tt.in); got != tt.want {
			t.Errorf("sanitizeDownloadName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := sanitizeDownloadName(long); len(got) > 200 || !strings.HasSuffix(got, ".txt") {
		t.Errorf("long name = %d bytes", len(got))
	}
}
