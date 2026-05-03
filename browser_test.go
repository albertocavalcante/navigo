package navigo

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpbrowser "github.com/albertocavalcante/navigo/cdp/proto/browser"
	"github.com/albertocavalcante/navigo/launcher"
)

type fakeTransport struct {
	reads chan []byte
	sends chan []byte
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		reads: make(chan []byte, 8),
		sends: make(chan []byte, 8),
	}
}

func (f *fakeTransport) Send(_ context.Context, data []byte) error {
	f.sends <- append([]byte(nil), data...)
	return nil
}

func (f *fakeTransport) Read(_ context.Context) ([]byte, error) {
	payload, ok := <-f.reads
	if !ok {
		return nil, io.EOF
	}

	return payload, nil
}

func (f *fakeTransport) Close() error {
	close(f.reads)
	return nil
}

func TestBrowserGetVersion(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	browser := &Browser{
		conn:    conn,
		version: launcher.Version{Browser: "Chrome"},
		cancel:  func() {},
		done:    make(chan error, 1),
	}

	go func() {
		browser.done <- conn.Run(context.Background())
	}()

	callErr := make(chan error, 1)
	resultCh := make(chan *cdpbrowser.GetVersionResult, 1)

	go func() {
		result, err := browser.GetVersion(context.Background())
		if err != nil {
			callErr <- err
			return
		}

		resultCh <- result
	}()

	select {
	case sent := <-transport.sends:
		var msg map[string]any
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if got, want := msg["method"], cdpbrowser.CommandGetVersion; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}

		transport.reads <- []byte(`{"id":1,"result":{"protocolVersion":"1.3","product":"Chrome/136.0.0.0","revision":"abc","userAgent":"Mozilla/5.0","jsVersion":"12.3.4"}}`)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Browser.getVersion request")
	}

	select {
	case err := <-callErr:
		t.Fatalf("browser.GetVersion() error = %v", err)
	case result := <-resultCh:
		if got, want := result.Product, "Chrome/136.0.0.0"; got != want {
			t.Fatalf("result.Product = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Browser.getVersion result")
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}
