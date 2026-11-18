package navigo

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // RFC 6455 mandates SHA-1 for the test server handshake.
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/internal/ws"
	"github.com/albertocavalcante/navigo/launcher"
)

func TestConnectOptionsReachRemote(t *testing.T) {
	t.Parallel()

	client := &http.Client{Timeout: time.Minute}

	tests := []struct {
		name  string
		opts  []ConnectOption
		check func(launcher.Remote) bool
	}{
		{"no options", nil, func(r launcher.Remote) bool {
			return !r.StrictWebSocketURL && r.MaxMessageSize == 0 && r.MaxVersionResponseBytes == 0 && r.HTTPClient == nil && r.Dial == nil
		}},
		{"nil option ignored", []ConnectOption{nil}, func(r launcher.Remote) bool { return !r.StrictWebSocketURL }},
		{"strict", []ConnectOption{WithStrictWebSocketURL()}, func(r launcher.Remote) bool { return r.StrictWebSocketURL }},
		{"message size", []ConnectOption{WithMaxMessageSize(123)}, func(r launcher.Remote) bool { return r.MaxMessageSize == 123 }},
		{"version bytes", []ConnectOption{WithMaxVersionResponseBytes(456)}, func(r launcher.Remote) bool { return r.MaxVersionResponseBytes == 456 }},
		{"http client", []ConnectOption{WithHTTPClient(client)}, func(r launcher.Remote) bool { return r.HTTPClient == client }},
		{"all, later wins", []ConnectOption{WithMaxMessageSize(1), WithMaxMessageSize(2), WithStrictWebSocketURL()}, func(r launcher.Remote) bool {
			return r.MaxMessageSize == 2 && r.StrictWebSocketURL
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if remote := remoteFromOptions(tt.opts); !tt.check(remote) {
				t.Fatalf("remote = %+v does not reflect options", remote)
			}
		})
	}
}

// versionServer answers /json/version advertising wsURL.
func versionServer(t *testing.T, wsURL func(host string) string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"Browser":"Fake","webSocketDebuggerUrl":%q}`, wsURL(r.Host))
	}))
	t.Cleanup(server.Close)

	return server
}

func TestConnectStrictRefusesMismatchedHost(t *testing.T) {
	t.Parallel()

	// Both hosts are loopback, so only strict mode notices the difference.
	server := versionServer(t, func(host string) string {
		_, port, _ := net.SplitHostPort(host)
		return "ws://localhost:" + port + "/devtools/browser/x"
	})

	_, err := Connect(t.Context(), server.URL, WithStrictWebSocketURL())
	if !errors.Is(err, launcher.ErrUntrustedWebSocketURL) {
		t.Fatalf("strict Connect() error = %v, want ErrUntrustedWebSocketURL", err)
	}

	// Without the option the same reply is accepted by discovery; the dial then
	// fails because the fake endpoint is not a WebSocket server.
	_, err = Connect(t.Context(), server.URL)
	if err == nil || errors.Is(err, launcher.ErrUntrustedWebSocketURL) {
		t.Fatalf("default Connect() error = %v, want a dial error", err)
	}
}

type countingTripper struct{ calls atomic.Int32 }

func (c *countingTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func TestConnectVersionLimitAndHTTPClient(t *testing.T) {
	t.Parallel()

	server := versionServer(t, func(string) string { return "ws://127.0.0.1:1/x" })

	_, err := Connect(t.Context(), server.URL, WithMaxVersionResponseBytes(8))
	if !errors.Is(err, launcher.ErrVersionResponseTooLarge) {
		t.Fatalf("Connect() error = %v, want ErrVersionResponseTooLarge", err)
	}

	tripper := &countingTripper{}
	_, _ = Connect(t.Context(), server.URL, WithHTTPClient(&http.Client{Transport: tripper}))

	if tripper.calls.Load() == 0 {
		t.Fatal("custom HTTP client was not used for the version probe")
	}
}

// wsServer accepts one WebSocket client and sends a single text frame of the
// given size, then waits until the client goes away.
func wsServer(t *testing.T, size int) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		reader := bufio.NewReader(conn)
		var key string
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			if value, ok := strings.CutPrefix(line, "Sec-WebSocket-Key: "); ok {
				key = strings.TrimSpace(value)
			}
			if line == "\r\n" {
				break
			}
		}

		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) //nolint:gosec // see import.
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))

		body := `{"method":"X.y","params":{"pad":"` + strings.Repeat("a", size) + `"}}`
		frame := []byte{0x81, 127}
		for shift := 56; shift >= 0; shift -= 8 {
			frame = append(frame, byte(len(body)>>shift))
		}
		_, _ = conn.Write(append(frame, body...))

		buf := make([]byte, 512)
		for {
			if _, readErr := conn.Read(buf); readErr != nil {
				return
			}
		}
	}()

	return "ws://" + listener.Addr().String() + "/devtools/browser/x"
}

func TestConnectMaxMessageSizeReachesDialer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	capped, err := Connect(ctx, wsServer(t, 70000), WithMaxMessageSize(4096))
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() { _ = capped.Close() }()

	if waitErr := capped.Wait(); !errors.Is(waitErr, ws.ErrMessageTooLarge) {
		t.Fatalf("capped Wait() error = %v, want ErrMessageTooLarge", waitErr)
	}

	// No options: the same message is well under the default cap.
	plain, err := Connect(ctx, wsServer(t, 70000))
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if closeErr := plain.Close(); closeErr != nil {
		t.Fatalf("zero-option Close() error = %v, want nil", closeErr)
	}
}
