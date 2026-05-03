package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteDiscover(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/json/version"; got != want {
			t.Fatalf("r.URL.Path = %q, want %q", got, want)
		}

		_, _ = w.Write([]byte(`{"Browser":"Chrome","webSocketDebuggerUrl":"ws://127.0.0.1/devtools/browser/123"}`))
	}))
	defer server.Close()

	version, err := (Remote{}).Discover(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if got, want := version.Browser, "Chrome"; got != want {
		t.Fatalf("version.Browser = %q, want %q", got, want)
	}

	if got, want := version.WebSocketDebuggerURL, "ws://127.0.0.1/devtools/browser/123"; got != want {
		t.Fatalf("version.WebSocketDebuggerURL = %q, want %q", got, want)
	}
}
