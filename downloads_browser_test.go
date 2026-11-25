package navigo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/launcher"
)

// TestDownloadWithLocalChrome drives a locally installed Chrome/Chromium in
// headless mode against a local httptest server. It is skipped when no
// browser is installed.
func TestDownloadWithLocalChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="report.txt"`)
		_, _ = w.Write([]byte("local download body"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>home</title><a id="dl" href="/file">get</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	proc, err := (launcher.Local{}).Launch(ctx, launcher.Options{Headless: true, Args: []string{"--no-sandbox"}})
	if err != nil {
		t.Skipf("local browser unavailable: %v", err)
	}
	defer func() { _ = proc.Close() }()

	browser, err := Connect(ctx, proc.RemoteURL())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = browser.Close() }()

	dir := t.TempDir()
	if err := browser.EnableDownloads(ctx, dir); err != nil {
		t.Fatalf("EnableDownloads() error = %v", err)
	}
	page, err := browser.NewPage(ctx)
	if err != nil {
		t.Fatalf("NewPage() error = %v", err)
	}
	if _, err := page.Goto(ctx, srv.URL+"/"); err != nil {
		t.Fatalf("Goto() error = %v", err)
	}
	if _, err := page.Evaluate(ctx, `document.getElementById("dl").click()`); err != nil {
		t.Fatalf("click error = %v", err)
	}

	got, err := browser.WaitForDownload(ctx, DownloadOptions{ComputeSHA256: true, IdleTimeout: 20 * time.Second})
	if err != nil {
		t.Fatalf("WaitForDownload() error = %v", err)
	}
	if got.Path != filepath.Join(dir, "report.txt") || got.Size != int64(len("local download body")) || got.SHA256 == "" {
		t.Fatalf("unexpected download: %+v", got)
	}
	if data, _ := os.ReadFile(got.Path); string(data) != "local download body" {
		t.Fatalf("content = %q", data)
	}
}
