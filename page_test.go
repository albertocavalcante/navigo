package navigo

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
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
	expectMethodAndReplyWithSession(t, transport, 4, "session-1", cdpruntime.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 5, "session-1", cdppage.CommandSetLifecycleEventsEnabled, `{}`)

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

	expectMethodAndReplyWithSession(t, transport, 6, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1","loaderId":"loader-1"}`)
	transport.reads <- []byte(`{"method":"Page.frameNavigated","sessionId":"session-1","params":{"frame":{"id":"frame-1","loaderId":"loader-1","url":"https://example.com"}}}`)
	transport.reads <- []byte(`{"method":"Runtime.executionContextCreated","sessionId":"session-1","params":{"context":{"id":7,"origin":"https://example.com","name":"","uniqueId":"ctx-1","auxData":{"frameId":"frame-1","isDefault":true}}}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Navigate() error = %v", err)
	case result := <-navResultCh:
		if got, want := string(result.FrameID), "frame-1"; got != want {
			t.Fatalf("result.FrameID = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Navigate()")
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- page.WaitForNavigation(context.Background())
	}()
	transport.reads <- []byte(`{"method":"Page.lifecycleEvent","sessionId":"session-1","params":{"frameId":"frame-1","loaderId":"loader-1","name":"load","timestamp":123.45}}`)

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

	evalValueCh := make(chan any, 1)
	go func() {
		value, err := page.Evaluate(context.Background(), "1 + 1")
		if err != nil {
			errCh <- err
			return
		}
		evalValueCh <- value
	}()

	expectMethodAndReplyWithSession(t, transport, 7, "session-1", cdpruntime.CommandEvaluate, `{"result":{"type":"number","value":2}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Evaluate() error = %v", err)
	case value := <-evalValueCh:
		number, ok := value.(float64)
		if !ok {
			t.Fatalf("value type = %T, want float64", value)
		}
		if got, want := number, 2.0; got != want {
			t.Fatalf("value = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Evaluate()")
	}

	evalSpecialCh := make(chan any, 1)
	go func() {
		value, err := page.Evaluate(context.Background(), "NaN")
		if err != nil {
			errCh <- err
			return
		}
		evalSpecialCh <- value
	}()

	expectMethodAndReplyWithSession(t, transport, 8, "session-1", cdpruntime.CommandEvaluate, `{"result":{"type":"number","unserializableValue":"NaN"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Evaluate(NaN) error = %v", err)
	case value := <-evalSpecialCh:
		number, ok := value.(float64)
		if !ok {
			t.Fatalf("value type = %T, want float64", value)
		}
		if !isNaN(number) {
			t.Fatalf("value = %v, want NaN", number)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Evaluate(NaN)")
	}

	callValueCh := make(chan any, 1)
	go func() {
		value, err := page.CallFunction(context.Background(), "function(a, b) { return a + b; }", 2, 3)
		if err != nil {
			errCh <- err
			return
		}
		callValueCh <- value
	}()

	expectMethodAndReplyWithSession(t, transport, 9, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"number","value":5}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.CallFunction() error = %v", err)
	case value := <-callValueCh:
		number, ok := value.(float64)
		if !ok {
			t.Fatalf("value type = %T, want float64", value)
		}
		if got, want := number, 5.0; got != want {
			t.Fatalf("value = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.CallFunction()")
	}

	frameTreeCh := make(chan *cdppage.FrameTree, 1)
	go func() {
		tree, err := page.GetFrameTree(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		frameTreeCh <- tree
	}()

	expectMethodAndReplyWithSession(t, transport, 10, "session-1", cdppage.CommandGetFrameTree, `{"frameTree":{"frame":{"id":"frame-1","loaderId":"loader-1","url":"https://example.com","domainAndRegistry":"example.com","securityOrigin":"https://example.com","mimeType":"text/html","secureContextType":"Secure","crossOriginIsolatedContextType":"NotIsolated","gatedAPIFeatures":[]}}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.GetFrameTree() error = %v", err)
	case tree := <-frameTreeCh:
		if got, want := string(tree.Frame.ID), "frame-1"; got != want {
			t.Fatalf("tree.Frame.ID = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.GetFrameTree()")
	}

	sameDocResultCh := make(chan *cdppage.NavigateResult, 1)
	go func() {
		result, err := page.Navigate(context.Background(), "https://example.com/#hash")
		if err != nil {
			errCh <- err
			return
		}
		sameDocResultCh <- result
	}()

	expectMethodAndReplyWithSession(t, transport, 11, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1"}`)

	select {
	case err := <-errCh:
		t.Fatalf("same-document page.Navigate() error = %v", err)
	case <-sameDocResultCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for same-document page.Navigate()")
	}

	commitErrCh := make(chan error, 1)
	go func() {
		commitErrCh <- page.WaitForNavigationUntil(context.Background(), WaitUntilCommit)
	}()
	transport.reads <- []byte(`{"method":"Page.navigatedWithinDocument","sessionId":"session-1","params":{"frameId":"frame-1","url":"https://example.com/#hash"}}`)

	select {
	case err := <-commitErrCh:
		if err != nil {
			t.Fatalf("page.WaitForNavigationUntil(commit) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.WaitForNavigationUntil(commit)")
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}

func TestPageGotoWaitsAtomically(t *testing.T) {
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
	expectMethodAndReplyWithSession(t, transport, 4, "session-1", cdpruntime.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 5, "session-1", cdppage.CommandSetLifecycleEventsEnabled, `{}`)

	var page *Page
	select {
	case err := <-errCh:
		t.Fatalf("browser.NewPage() error = %v", err)
	case page = <-pageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser.NewPage()")
	}

	gotoResultCh := make(chan *cdppage.NavigateResult, 1)
	go func() {
		result, err := page.Goto(context.Background(), "https://example.com")
		if err != nil {
			errCh <- err
			return
		}
		gotoResultCh <- result
	}()

	expectMethodAndReplyWithSession(t, transport, 6, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1","loaderId":"loader-1"}`)
	transport.reads <- []byte(`{"method":"Page.frameNavigated","sessionId":"session-1","params":{"frame":{"id":"frame-1","loaderId":"loader-1","url":"https://example.com"}}}`)
	transport.reads <- []byte(`{"method":"Page.lifecycleEvent","sessionId":"session-1","params":{"frameId":"frame-1","loaderId":"loader-1","name":"load","timestamp":123.45}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Goto() error = %v", err)
	case result := <-gotoResultCh:
		if got, want := result.LoaderID, "loader-1"; got != want {
			t.Fatalf("result.LoaderID = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Goto()")
	}

	sameDocResultCh := make(chan *cdppage.NavigateResult, 1)
	go func() {
		result, err := page.Goto(context.Background(), "https://example.com/#hash", WithWaitUntil(WaitUntilCommit))
		if err != nil {
			errCh <- err
			return
		}
		sameDocResultCh <- result
	}()

	expectMethodAndReplyWithSession(t, transport, 7, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1"}`)
	transport.reads <- []byte(`{"method":"Page.navigatedWithinDocument","sessionId":"session-1","params":{"frameId":"frame-1","url":"https://example.com/#hash"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("same-document page.Goto() error = %v", err)
	case result := <-sameDocResultCh:
		if got, want := string(result.FrameID), "frame-1"; got != want {
			t.Fatalf("result.FrameID = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for same-document page.Goto()")
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}

func isNaN(value float64) bool {
	return math.IsNaN(value)
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
