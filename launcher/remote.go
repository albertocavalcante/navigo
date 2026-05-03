package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

type Remote struct {
	HTTPClient *http.Client
	Dial       DialFunc
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

	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	response, err := client.Do(request)
	if err != nil {
		return Version{}, fmt.Errorf("request CDP version endpoint: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return Version{}, fmt.Errorf("request CDP version endpoint: unexpected status %s", response.Status)
	}

	var version Version
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		return Version{}, fmt.Errorf("decode CDP version response: %w", err)
	}

	if version.WebSocketDebuggerURL == "" {
		return Version{}, fmt.Errorf("decode CDP version response: missing webSocketDebuggerUrl")
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
			return ws.Dial(ctx, rawURL)
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
