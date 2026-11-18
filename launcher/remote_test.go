package launcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRemoteDiscoverHostileReplies(t *testing.T) {
	t.Parallel()

	big := `{"Browser":"` + strings.Repeat("a", 4096) + `","webSocketDebuggerUrl":"ws://127.0.0.1/x"}`

	// reply serves body with __HOST__ replaced by the address that was probed.
	reply := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.ReplaceAll(body, "__HOST__", r.Host)))
		}
	}
	strict := Remote{StrictWebSocketURL: true}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		remote  Remote
		want    error
	}{
		{"oversize reply", reply(big), Remote{MaxVersionResponseBytes: 1024}, ErrVersionResponseTooLarge},
		{"endless reply", func(w http.ResponseWriter, r *http.Request) {
			chunk := []byte(strings.Repeat(" ", 4096))
			for r.Context().Err() == nil {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}, Remote{MaxVersionResponseBytes: 8192}, ErrVersionResponseTooLarge},
		{"redirect not followed", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/elsewhere" {
				t.Error("redirect target was requested")
			}
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}, Remote{}, ErrDiscoveryRedirect},
		{"redirect not followed with permissive custom client", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/elsewhere" {
				t.Error("redirect target was requested")
			}
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
		}, Remote{HTTPClient: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}}, ErrDiscoveryRedirect},
		{"non-websocket scheme", reply(`{"webSocketDebuggerUrl":"http://127.0.0.1/x"}`), Remote{}, ErrUntrustedWebSocketURL},
		{"file scheme", reply(`{"webSocketDebuggerUrl":"file:///etc/passwd"}`), Remote{}, ErrUntrustedWebSocketURL},
		{"missing host", reply(`{"webSocketDebuggerUrl":"ws:///x"}`), Remote{}, ErrUntrustedWebSocketURL},
		{"loopback advertising remote host", reply(`{"webSocketDebuggerUrl":"ws://203.0.113.9:9222/x"}`), Remote{}, ErrUntrustedWebSocketURL},
		{"strict: other loopback port", reply(`{"webSocketDebuggerUrl":"ws://127.0.0.1:1/x"}`), strict, ErrUntrustedWebSocketURL},
		{"strict: other host spelling", reply(`{"webSocketDebuggerUrl":"ws://localhost:1/x"}`), strict, ErrUntrustedWebSocketURL},
		{"strict: wss for http endpoint", reply(`{"webSocketDebuggerUrl":"wss://__HOST__/x"}`), strict, ErrUntrustedWebSocketURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(tt.handler)
			defer server.Close()

			_, err := tt.remote.Discover(context.Background(), server.URL)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Discover() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRemoteDiscoverAcceptsMatchingURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remote Remote
	}{
		{"default", Remote{}},
		{"strict", Remote{StrictWebSocketURL: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://` + r.Host + `/devtools/browser/1"}`))
			}))
			defer server.Close()

			if _, err := tt.remote.Discover(context.Background(), server.URL); err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
		})
	}
}

func TestValidateWebSocketURLNonLoopbackEndpoint(t *testing.T) {
	t.Parallel()

	// A remote endpoint may advertise any host by default (reverse proxies,
	// port mappings); only strict mode pins it.
	if err := validateWebSocketURL("http://203.0.113.5:9222", "ws://198.51.100.7:1/x", false); err != nil {
		t.Fatalf("default validation error = %v", err)
	}

	if err := validateWebSocketURL("https://203.0.113.5", "wss://203.0.113.5:443/x", true); err != nil {
		t.Fatalf("strict validation error = %v", err)
	}

	if err := validateWebSocketURL("http://203.0.113.5:9222", "ws://198.51.100.7:1/x", true); !errors.Is(err, ErrUntrustedWebSocketURL) {
		t.Fatalf("strict validation error = %v, want ErrUntrustedWebSocketURL", err)
	}
}
