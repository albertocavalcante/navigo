package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"

	"github.com/albertocavalcante/navigo/cdp"
	"github.com/albertocavalcante/navigo/internal/ws"
)

type Version struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	V8Version            string `json:"V8-Version"`
	WebKitVersion        string `json:"WebKit-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type DialFunc func(ctx context.Context, rawURL string) (cdp.Transport, error)

// DefaultMaxVersionResponseBytes caps the /json/version reply. A real reply is
// well under a kilobyte.
const DefaultMaxVersionResponseBytes = 1 << 20

var (
	// ErrVersionResponseTooLarge reports a /json/version reply above the cap.
	ErrVersionResponseTooLarge = errors.New("CDP version response exceeds size limit")
	// ErrDiscoveryRedirect reports that the version endpoint answered with a
	// redirect, which is never followed.
	ErrDiscoveryRedirect = errors.New("CDP version endpoint returned a redirect")
	// ErrUntrustedWebSocketURL reports a webSocketDebuggerUrl that failed
	// validation.
	ErrUntrustedWebSocketURL = errors.New("untrusted CDP webSocketDebuggerUrl")
)

// Remote discovers and connects to an existing DevTools endpoint. The endpoint
// is an untrusted peer: replies are size-bounded, redirects are not followed
// and the advertised WebSocket URL is validated.
type Remote struct {
	// HTTPClient performs the version probe. Redirect following is always
	// disabled on a copy of it; the client passed in is not modified.
	HTTPClient *http.Client
	Dial       DialFunc

	// MaxVersionResponseBytes caps the version reply. Zero means
	// DefaultMaxVersionResponseBytes.
	MaxVersionResponseBytes int64

	// MaxMessageSize caps one CDP WebSocket message for the default dialer.
	// Zero means the ws package default (64 MiB). A custom Dial applies its own.
	MaxMessageSize int64

	// StrictWebSocketURL additionally requires the advertised WebSocket URL
	// to use ws for an http endpoint and wss for https, and to name exactly
	// the host and port that were probed. Browsers answer with the Host the
	// request used, so this holds for direct connections but can fail behind a
	// reverse proxy or port mapping that rewrites it, hence it is opt-in.
	//
	// Independent of this option, the URL must be ws or wss with a host, and a
	// loopback endpoint may only advertise a loopback WebSocket host.
	StrictWebSocketURL bool
}

func (r Remote) Discover(ctx context.Context, rawURL string) (Version, error) {
	if strings.HasPrefix(rawURL, "ws://") || strings.HasPrefix(rawURL, "wss://") {
		return Version{WebSocketDebuggerURL: rawURL}, nil
	}

	versionURL, err := versionEndpoint(rawURL)
	if err != nil {
		return Version{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return Version{}, fmt.Errorf("build CDP discovery request: %w", err)
	}

	client := http.DefaultClient
	if r.HTTPClient != nil {
		client = r.HTTPClient
	}

	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	response, err := noRedirect.Do(request)
	if err != nil {
		return Version{}, fmt.Errorf("request CDP version endpoint: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return Version{}, fmt.Errorf("request CDP version endpoint: %w (%s)", ErrDiscoveryRedirect, response.Status)
	}

	if response.StatusCode != http.StatusOK {
		return Version{}, fmt.Errorf("request CDP version endpoint: unexpected status %s", response.Status)
	}

	limit := r.MaxVersionResponseBytes
	if limit <= 0 {
		limit = DefaultMaxVersionResponseBytes
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return Version{}, fmt.Errorf("read CDP version response: %w", err)
	}

	if int64(len(body)) > limit {
		return Version{}, fmt.Errorf("read CDP version response: %w (limit %d bytes)", ErrVersionResponseTooLarge, limit)
	}

	var version Version
	if err := json.Unmarshal(body, &version); err != nil {
		return Version{}, fmt.Errorf("decode CDP version response: %w", err)
	}

	if version.WebSocketDebuggerURL == "" {
		return Version{}, fmt.Errorf("decode CDP version response: missing webSocketDebuggerUrl")
	}

	if err := validateWebSocketURL(rawURL, version.WebSocketDebuggerURL, r.StrictWebSocketURL); err != nil {
		return Version{}, err
	}

	return version, nil
}

func (r Remote) Connect(ctx context.Context, rawURL string) (*cdp.Conn, Version, error) {
	version, err := r.Discover(ctx, rawURL)
	if err != nil {
		return nil, Version{}, err
	}

	dial := r.Dial
	if dial == nil {
		dial = func(ctx context.Context, rawURL string) (cdp.Transport, error) {
			return ws.Dial(ctx, rawURL, ws.WithMaxMessageSize(r.MaxMessageSize))
		}
	}

	transport, err := dial(ctx, version.WebSocketDebuggerURL)
	if err != nil {
		return nil, Version{}, fmt.Errorf("dial CDP websocket: %w", err)
	}

	return cdp.NewConn(transport), version, nil
}

func versionEndpoint(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse CDP endpoint: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("parse CDP endpoint: unsupported scheme %q", parsedURL.Scheme)
	}

	parsedURL.Path = path.Join(parsedURL.Path, "/json/version")

	return parsedURL.String(), nil
}

func validateWebSocketURL(endpoint, advertised string, strict bool) error {
	wsURL, err := url.Parse(advertised)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUntrustedWebSocketURL, err)
	}

	if wsURL.Scheme != "ws" && wsURL.Scheme != "wss" {
		return fmt.Errorf("%w: unsupported scheme %q", ErrUntrustedWebSocketURL, wsURL.Scheme)
	}

	if wsURL.Hostname() == "" {
		return fmt.Errorf("%w: missing host", ErrUntrustedWebSocketURL)
	}

	base, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse CDP endpoint: %w", err)
	}

	if isLoopbackHost(base.Hostname()) && !isLoopbackHost(wsURL.Hostname()) {
		return fmt.Errorf("%w: loopback endpoint advertised non-loopback host %q", ErrUntrustedWebSocketURL, wsURL.Hostname())
	}

	if !strict {
		return nil
	}

	wantScheme := map[string]string{"http": "ws", "https": "wss"}[base.Scheme]
	if wsURL.Scheme != wantScheme {
		return fmt.Errorf("%w: scheme %q does not match endpoint scheme %q", ErrUntrustedWebSocketURL, wsURL.Scheme, base.Scheme)
	}

	if !strings.EqualFold(wsURL.Hostname(), base.Hostname()) || effectivePort(wsURL) != effectivePort(base) {
		return fmt.Errorf("%w: %q does not match probed endpoint %q", ErrUntrustedWebSocketURL, wsURL.Host, base.Host)
	}

	return nil
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}

	switch u.Scheme {
	case "https", "wss":
		return "443"
	default:
		return "80"
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}

	addr, err := netip.ParseAddr(host)

	return err == nil && addr.IsLoopback()
}
