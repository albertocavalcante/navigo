package navigo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/albertocavalcante/navigo/cdp"
	cdpbrowser "github.com/albertocavalcante/navigo/cdp/proto/browser"
	"github.com/albertocavalcante/navigo/launcher"
)

type Browser struct {
	conn    *cdp.Conn
	version launcher.Version
	cancel  context.CancelFunc
	done    chan error
	waitMu  sync.Mutex
	waited  bool

	downloadsMu sync.Mutex
	downloads   *downloadTracker
}

// ConnectOption customizes [Connect]. With no options, Connect applies the
// launcher.Remote defaults: a 1 MiB cap on the /json/version reply, a 64 MiB
// cap on one WebSocket message, the system HTTP client, and a WebSocket URL
// check that only refuses a non-WebSocket scheme or a non-loopback host
// advertised by a loopback endpoint.
type ConnectOption func(*launcher.Remote)

// WithStrictWebSocketURL requires the advertised WebSocket URL to use the
// scheme matching the endpoint (ws for http, wss for https) and to name exactly
// the host and port that were probed. It can fail behind a reverse proxy or
// port mapping that rewrites the host, so it is off by default.
func WithStrictWebSocketURL() ConnectOption {
	return func(r *launcher.Remote) { r.StrictWebSocketURL = true }
}

// WithMaxMessageSize caps one reassembled CDP WebSocket message at n bytes.
// A non-positive n keeps the default of 64 MiB.
func WithMaxMessageSize(n int64) ConnectOption {
	return func(r *launcher.Remote) { r.MaxMessageSize = n }
}

// WithMaxVersionResponseBytes caps the /json/version reply at n bytes. A
// non-positive n keeps the default of 1 MiB.
func WithMaxVersionResponseBytes(n int64) ConnectOption {
	return func(r *launcher.Remote) { r.MaxVersionResponseBytes = n }
}

// WithHTTPClient sets the client used for the /json/version probe. Redirects
// are never followed, whatever the client's own policy. A nil client keeps the
// default.
func WithHTTPClient(client *http.Client) ConnectOption {
	return func(r *launcher.Remote) { r.HTTPClient = client }
}

func remoteFromOptions(opts []ConnectOption) launcher.Remote {
	var remote launcher.Remote
	for _, opt := range opts {
		if opt != nil {
			opt(&remote)
		}
	}

	return remote
}

// Connect attaches to an existing browser by its DevTools HTTP endpoint or
// ws/wss URL. The connection lives until ctx ends or the browser is closed.
func Connect(ctx context.Context, rawURL string, opts ...ConnectOption) (*Browser, error) {
	conn, version, err := remoteFromOptions(opts).Connect(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("connect browser: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	browser := &Browser{
		conn:    conn,
		version: version,
		cancel:  cancel,
		done:    make(chan error, 1),
	}

	go func() {
		browser.done <- conn.Run(runCtx)
	}()

	return browser, nil
}

func (b *Browser) CDP() *cdp.Conn {
	return b.conn
}

func (b *Browser) Version() launcher.Version {
	return b.version
}

func (b *Browser) GetVersion(ctx context.Context) (*cdpbrowser.GetVersionResult, error) {
	result, err := cdpbrowser.GetVersion().Do(ctx, b.conn.RootSession())
	if err != nil {
		return nil, fmt.Errorf("get browser version: %w", err)
	}

	return result, nil
}

func (b *Browser) Close() error {
	b.cancel()
	if err := b.conn.Close(); err != nil {
		return fmt.Errorf("close browser connection: %w", err)
	}

	return b.wait()
}

func (b *Browser) Wait() error {
	return b.wait()
}

func (b *Browser) wait() error {
	b.waitMu.Lock()
	defer b.waitMu.Unlock()

	if b.waited {
		return nil
	}

	err := <-b.done
	b.waited = true
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}

	return err
}
