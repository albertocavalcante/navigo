package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6455 mandates SHA-1 for handshake validation.
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func dialWebSocket(ctx context.Context, rawURL string) (net.Conn, *bufio.Reader, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse websocket URL: %w", err)
	}

	if parsedURL.Scheme != "ws" && parsedURL.Scheme != "wss" {
		return nil, nil, fmt.Errorf("parse websocket URL: unsupported scheme %q", parsedURL.Scheme)
	}

	address := parsedURL.Host
	if parsedURL.Port() == "" {
		switch parsedURL.Scheme {
		case "ws":
			address = net.JoinHostPort(parsedURL.Hostname(), "80")
		case "wss":
			address = net.JoinHostPort(parsedURL.Hostname(), "443")
		}
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("dial websocket: %w", err)
	}

	if parsedURL.Scheme == "wss" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: parsedURL.Hostname(), MinVersion: tls.VersionTLS12})
		if handshakeErr := tlsConn.HandshakeContext(ctx); handshakeErr != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("tls handshake: %w", handshakeErr)
		}

		conn = tlsConn
	}

	nonce, err := generateNonce()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("set websocket deadline: %w", deadlineErr)
		}
	}

	request := buildHandshakeRequest(parsedURL, nonce)
	if _, writeErr := io.WriteString(conn, request); writeErr != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("write websocket handshake: %w", writeErr)
	}

	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet, URL: parsedURL})
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("read websocket handshake: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("websocket handshake failed: unexpected status %s", response.Status)
	}

	if !headerContainsToken(response.Header, "Connection", "Upgrade") {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("websocket handshake failed: missing Connection upgrade token")
	}

	if !headerContainsToken(response.Header, "Upgrade", "websocket") {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("websocket handshake failed: missing Upgrade websocket token")
	}

	if got, want := response.Header.Get("Sec-WebSocket-Accept"), acceptKey(nonce); got != want {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("websocket handshake failed: invalid accept key")
	}

	if hasDeadline {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("clear websocket deadline: %w", err)
		}
	}

	return conn, reader, nil
}

func generateNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate websocket nonce: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf), nil
}

func buildHandshakeRequest(parsedURL *url.URL, nonce string) string {
	requestURI := parsedURL.RequestURI()
	if requestURI == "" {
		requestURI = "/"
	}

	host := parsedURL.Host

	var builder strings.Builder
	builder.WriteString("GET ")
	builder.WriteString(requestURI)
	builder.WriteString(" HTTP/1.1\r\n")
	builder.WriteString("Host: ")
	builder.WriteString(host)
	builder.WriteString("\r\n")
	builder.WriteString("Upgrade: websocket\r\n")
	builder.WriteString("Connection: Upgrade\r\n")
	builder.WriteString("Sec-WebSocket-Version: 13\r\n")
	builder.WriteString("Sec-WebSocket-Key: ")
	builder.WriteString(nonce)
	builder.WriteString("\r\n\r\n")

	return builder.String()
}

func acceptKey(nonce string) string {
	sum := sha1.Sum([]byte(nonce + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContainsToken(header http.Header, key string, token string) bool {
	values := header.Values(key)
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}

	return false
}
