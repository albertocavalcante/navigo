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
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
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
		{`a<b>c:d"e|f?g*h.txt`, "a_b_c_d_e_f_g_h.txt"},
		{"CON", "_CON"},
		{"con.txt", "_con.txt"},
		{"NUL.tar.gz", "_NUL.tar.gz"},
		{"COM1.pdf", "_COM1.pdf"},
		{"lpt9", "_lpt9"},
		{"console.txt", "console.txt"},
		{"a\u202eb\u2066c.txt", "abc.txt"},
		{"a\u0085b\u009fc.txt", "abc.txt"},
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

func TestMoveToUniqueNameMultiPartExtension(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for i, want := range []string{"x.tar.gz", "x (1).tar.gz", "x (2).tar.gz"} {
		src := filepath.Join(dir, "src"+strconv.Itoa(i))
		if err := os.WriteFile(src, []byte("d"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := moveToUniqueName(src, dir, "x.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(got) != want {
			t.Fatalf("got %q, want %q", filepath.Base(got), want)
		}
	}
}

func TestWaitForDownloadNoHeadOfLineBlocking(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)
	if err := os.WriteFile(filepath.Join(dir, "g2"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendBegin(transport, "g1", "slow.bin")
	sendProgress(transport, "g1", "inProgress", 1, 100)
	sendBegin(transport, "g2", "fast.bin")
	sendProgress(transport, "g2", "completed", 3, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// A short idle timeout must not fire while a completed download is ready.
	got, err := browser.WaitForDownload(ctx, DownloadOptions{IdleTimeout: time.Millisecond})
	if err != nil {
		t.Fatalf("WaitForDownload() error = %v", err)
	}
	if got.GUID != "g2" || filepath.Base(got.Path) != "fast.bin" {
		t.Fatalf("got %+v", got)
	}
	// Only the stalled one is left: now idle applies.
	if _, err := browser.WaitForDownload(ctx, DownloadOptions{IdleTimeout: 50 * time.Millisecond}); !errors.Is(err, ErrDownloadIdle) {
		t.Fatalf("err = %v, want ErrDownloadIdle", err)
	}
}

func TestWaitForDownloadGUIDAndDiscard(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)
	for _, g := range []string{"g1", "g2"} {
		if err := os.WriteFile(filepath.Join(dir, g), []byte(g), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sendBegin(transport, "g1", "one.bin")
	sendBegin(transport, "g2", "two.bin")
	sendProgress(transport, "g1", "completed", 2, 2)
	sendProgress(transport, "g2", "completed", 2, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := browser.WaitForDownloadGUID(ctx, "g2", DownloadOptions{})
	if err != nil || got.GUID != "g2" {
		t.Fatalf("GUID wait = %+v, %v", got, err)
	}
	if err := browser.DiscardDownload("g1"); err != nil {
		t.Fatalf("DiscardDownload() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "g1")); !os.IsNotExist(err) {
		t.Fatalf("discarded file should be removed, err = %v", err)
	}
	short, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	if _, err := browser.WaitForDownload(short, DownloadOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("after discard err = %v, want DeadlineExceeded", err)
	}
	if err := browser.DiscardDownload("nope"); err == nil {
		t.Fatal("unknown GUID: want error")
	}
}

func TestRepeatedBeginAfterReturnIsIgnored(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)
	if err := os.WriteFile(filepath.Join(dir, "g1"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendBegin(transport, "g1", "a.bin")
	sendProgress(transport, "g1", "completed", 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := browser.WaitForDownload(ctx, DownloadOptions{}); err != nil {
		t.Fatal(err)
	}
	sendBegin(transport, "g1", "a.bin") // duplicate begin must not resurrect it
	short, cancelShort := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancelShort()
	if _, err := browser.WaitForDownload(short, DownloadOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestProgressOnlyDownloadAndFilePath(t *testing.T) {
	t.Parallel()

	browser, transport := newFakeBrowser(t)
	dir := t.TempDir()
	enableDownloadsFake(t, browser, transport, dir)
	if err := os.WriteFile(filepath.Join(dir, "g9"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No begin event; FilePath points outside the directory and is ignored.
	transport.reads <- []byte(`{"method":"Browser.downloadProgress","params":{"guid":"g9","state":"completed","receivedBytes":1,"totalBytes":1,"filePath":"/elsewhere/g9"}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := browser.WaitForDownload(ctx, DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got.Path) != "download" || got.GUIDPath != filepath.Join(dir, "g9") {
		t.Fatalf("got %+v", got)
	}
}

func TestExceptionText(t *testing.T) {
	t.Parallel()

	got := exceptionText(&cdpruntime.ExceptionDetails{Text: "Uncaught", Exception: &cdpruntime.RemoteObject{Description: "TypeError: x is null\n    at <anonymous>:1:1"}})
	if !strings.Contains(got, "Uncaught") || !strings.Contains(got, "TypeError: x is null") {
		t.Fatalf("exceptionText = %q", got)
	}
	if got := exceptionText(&cdpruntime.ExceptionDetails{Text: "Uncaught"}); got != "Uncaught" {
		t.Fatalf("exceptionText = %q", got)
	}
}
