package navigo

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
	cdptarget "github.com/albertocavalcante/navigo/cdp/proto/target"
	"github.com/albertocavalcante/navigo/launcher"
)

func TestBrowserNewPageAndNavigate(t *testing.T) {
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

	pageCh := make(chan *Page, 1)
	errCh := make(chan error, 1)
	go func() {
		page, err := browser.NewPage(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		pageCh <- page
	}()

	expectMethodAndReply(t, transport, 1, cdptarget.CommandCreateTarget, `{"targetId":"target-1"}`)
	expectMethodAndReply(t, transport, 2, cdptarget.CommandAttachToTarget, `{"sessionId":"session-1"}`)
	expectMethodAndReplyWithSession(t, transport, 3, "session-1", cdppage.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 4, "session-1", cdppage.CommandSetLifecycleEventsEnabled, `{}`)

	var page *Page
	select {
	case err := <-errCh:
		t.Fatalf("browser.NewPage() error = %v", err)
	case page = <-pageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser.NewPage()")
	}

	if got, want := page.TargetID(), "target-1"; got != want {
		t.Fatalf("page.TargetID() = %q, want %q", got, want)
	}

	navResultCh := make(chan *cdppage.NavigateResult, 1)
	go func() {
		result, err := page.Navigate(context.Background(), "https://example.com")
		if err != nil {
			errCh <- err
			return
		}
		navResultCh <- result
	}()

	expectMethodAndReplyWithSession(t, transport, 5, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1","loaderId":"loader-1"}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Navigate() error = %v", err)
	case result := <-navResultCh:
		if got, want := result.FrameID, "frame-1"; got != want {
			t.Fatalf("result.FrameID = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Navigate()")
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- page.WaitForNavigation(context.Background())
	}()

	transport.reads <- []byte(`{"method":"Page.loadEventFired","sessionId":"session-1","params":{"timestamp":123.45}}`)

	select {
	case err := <-waitErrCh:
		if err != nil {
			t.Fatalf("page.WaitForNavigation() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.WaitForNavigation()")
	}

	domWaitErrCh := make(chan error, 1)
	go func() {
		domWaitErrCh <- page.WaitForNavigationUntil(context.Background(), WaitUntilDOMContentLoaded)
	}()

	transport.reads <- []byte(`{"method":"Page.lifecycleEvent","sessionId":"session-1","params":{"frameId":"frame-1","loaderId":"loader-1","name":"DOMContentLoaded","timestamp":124.45}}`)

	select {
	case err := <-domWaitErrCh:
		if err != nil {
			t.Fatalf("page.WaitForNavigationUntil(DOMContentLoaded) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.WaitForNavigationUntil(DOMContentLoaded)")
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}

func expectMethodAndReply(t *testing.T, transport *fakeTransport, id int, method string, result string) {
	t.Helper()
	expectMethodAndReplyWithSession(t, transport, id, "", method, result)
}

func expectMethodAndReplyWithSession(t *testing.T, transport *fakeTransport, id int, sessionID string, method string, result string) {
	t.Helper()

	select {
	case sent := <-transport.sends:
		var msg map[string]any
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if got, want := int(msg["id"].(float64)), id; got != want {
			t.Fatalf("msg[id] = %d, want %d", got, want)
		}

		if got, want := msg["method"], method; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}

		if sessionID != "" {
			if got, want := msg["sessionId"], sessionID; got != want {
				t.Fatalf("msg[sessionId] = %v, want %v", got, want)
			}
			transport.reads <- []byte(`{"id":` + strconv.Itoa(id) + `,"sessionId":"` + sessionID + `","result":` + result + `}`)
			return
		}

		transport.reads <- []byte(`{"id":` + strconv.Itoa(id) + `,"result":` + result + `}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s request", method)
	}
}
