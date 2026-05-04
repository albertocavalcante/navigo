package navigo

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpaccessibility "github.com/albertocavalcante/navigo/cdp/proto/accessibility"
	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdpinput "github.com/albertocavalcante/navigo/cdp/proto/input"
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
	expectMethodAndReplyWithSession(t, transport, 5, "session-1", cdpdom.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 6, "session-1", cdpaccessibility.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 7, "session-1", cdppage.CommandSetLifecycleEventsEnabled, `{}`)

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

	expectMethodAndReplyWithSession(t, transport, 8, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1","loaderId":"loader-1"}`)
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

	expectMethodAndReplyWithSession(t, transport, 9, "session-1", cdpruntime.CommandEvaluate, `{"result":{"type":"number","value":2}}`)

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

	expectMethodAndReplyWithSession(t, transport, 10, "session-1", cdpruntime.CommandEvaluate, `{"result":{"type":"number","unserializableValue":"NaN"}}`)

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

	expectMethodAndReplyWithSession(t, transport, 11, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"number","value":5}}`)

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

	expectMethodAndReplyWithSession(t, transport, 12, "session-1", cdppage.CommandGetFrameTree, `{"frameTree":{"frame":{"id":"frame-1","loaderId":"loader-1","url":"https://example.com","domainAndRegistry":"example.com","securityOrigin":"https://example.com","mimeType":"text/html","secureContextType":"Secure","crossOriginIsolatedContextType":"NotIsolated","gatedAPIFeatures":[]}}}`)

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

	elementCh := make(chan *Element, 1)
	go func() {
		element, err := page.QuerySelector(context.Background(), "#app")
		if err != nil {
			errCh <- err
			return
		}
		elementCh <- element
	}()

	expectMethodAndReplyWithSession(t, transport, 13, "session-1", cdpdom.CommandGetDocument, `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":"","childNodeCount":1}}`)
	expectMethodAndReplyWithSession(t, transport, 14, "session-1", cdpdom.CommandQuerySelector, `{"nodeId":2}`)
	expectMethodAndReplyWithSession(t, transport, 15, "session-1", cdpdom.CommandDescribeNode, `{"node":{"nodeId":2,"backendNodeId":22,"nodeType":1,"nodeName":"DIV","localName":"div","nodeValue":"","childNodeCount":1,"attributes":["id","app"]}}`)
	expectMethodAndReplyWithSession(t, transport, 16, "session-1", cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLDivElement","objectId":"obj-node-1","description":"div#app"}}`)

	var element *Element
	select {
	case err := <-errCh:
		t.Fatalf("page.QuerySelector() error = %v", err)
	case element = <-elementCh:
		if element == nil {
			t.Fatal("page.QuerySelector() = nil, want element")
		}
		if got, want := element.NodeID(), 2; got != want {
			t.Fatalf("element.NodeID() = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.QuerySelector()")
	}

	outerHTMLCh := make(chan string, 1)
	go func() {
		outerHTML, err := element.OuterHTML(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		outerHTMLCh <- outerHTML
	}()

	expectMethodAndReplyWithSession(t, transport, 17, "session-1", cdpdom.CommandGetOuterHTML, `{"outerHTML":"<div id=\"app\"><span class=\"child\"></span></div>"}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.OuterHTML() error = %v", err)
	case outerHTML := <-outerHTMLCh:
		if got, want := outerHTML, `<div id="app"><span class="child"></span></div>`; got != want {
			t.Fatalf("element.OuterHTML() = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.OuterHTML()")
	}

	childElementCh := make(chan *Element, 1)
	go func() {
		child, err := element.QuerySelector(context.Background(), ".child")
		if err != nil {
			errCh <- err
			return
		}
		childElementCh <- child
	}()

	expectMethodAndReplyWithSession(t, transport, 18, "session-1", cdpdom.CommandQuerySelector, `{"nodeId":3}`)
	expectMethodAndReplyWithSession(t, transport, 19, "session-1", cdpdom.CommandDescribeNode, `{"node":{"nodeId":3,"backendNodeId":33,"nodeType":1,"nodeName":"SPAN","localName":"span","nodeValue":"","attributes":["class","child"]}}`)
	expectMethodAndReplyWithSession(t, transport, 20, "session-1", cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLSpanElement","objectId":"obj-node-2","description":"span.child"}}`)

	var child *Element
	select {
	case err := <-errCh:
		t.Fatalf("element.QuerySelector() error = %v", err)
	case child = <-childElementCh:
		if child == nil {
			t.Fatal("element.QuerySelector() = nil, want child element")
		}
		if got, want := child.BackendNodeID(), 33; got != want {
			t.Fatalf("child.BackendNodeID() = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.QuerySelector()")
	}

	attributesCh := make(chan map[string]string, 1)
	go func() {
		attributes, err := element.Attributes(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		attributesCh <- attributes
	}()

	expectMethodAndReplyWithSession(t, transport, 21, "session-1", cdpdom.CommandGetAttributes, `{"attributes":["id","app","data-role","main"]}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.Attributes() error = %v", err)
	case attributes := <-attributesCh:
		if got, want := attributes["id"], "app"; got != want {
			t.Fatalf("attributes[id] = %q, want %q", got, want)
		}
		if got, want := attributes["data-role"], "main"; got != want {
			t.Fatalf("attributes[data-role] = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Attributes()")
	}

	type attributeResult struct {
		value string
		ok    bool
	}
	attributeCh := make(chan attributeResult, 1)
	go func() {
		value, ok, err := element.Attribute(context.Background(), "id")
		if err != nil {
			errCh <- err
			return
		}
		attributeCh <- attributeResult{value: value, ok: ok}
	}()

	expectMethodAndReplyWithSession(t, transport, 22, "session-1", cdpdom.CommandGetAttributes, `{"attributes":["id","app","data-role","main"]}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.Attribute() error = %v", err)
	case result := <-attributeCh:
		if !result.ok {
			t.Fatal("element.Attribute(id) ok = false, want true")
		}
		if got, want := result.value, "app"; got != want {
			t.Fatalf("element.Attribute(id) = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Attribute()")
	}

	innerHTMLCh := make(chan string, 1)
	go func() {
		innerHTML, err := element.InnerHTML(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		innerHTMLCh <- innerHTML
	}()

	expectMethodAndReplyWithSession(t, transport, 23, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"string","value":"<span class=\"child\"></span>"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.InnerHTML() error = %v", err)
	case innerHTML := <-innerHTMLCh:
		if got, want := innerHTML, `<span class="child"></span>`; got != want {
			t.Fatalf("element.InnerHTML() = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.InnerHTML()")
	}

	textContentCh := make(chan string, 1)
	go func() {
		textContent, err := element.TextContent(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		textContentCh <- textContent
	}()

	expectMethodAndReplyWithSession(t, transport, 24, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"string","value":"child text"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.TextContent() error = %v", err)
	case textContent := <-textContentCh:
		if got, want := textContent, "child text"; got != want {
			t.Fatalf("element.TextContent() = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.TextContent()")
	}

	innerTextCh := make(chan string, 1)
	go func() {
		innerText, err := element.InnerText(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		innerTextCh <- innerText
	}()

	expectMethodAndReplyWithSession(t, transport, 25, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"string","value":"child text"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.InnerText() error = %v", err)
	case innerText := <-innerTextCh:
		if got, want := innerText, "child text"; got != want {
			t.Fatalf("element.InnerText() = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.InnerText()")
	}

	focusErrCh := make(chan error, 1)
	go func() {
		focusErrCh <- element.Focus(context.Background())
	}()

	expectMethodAndReplyWithSession(t, transport, 26, "session-1", cdpdom.CommandFocus, `{}`)

	select {
	case err := <-focusErrCh:
		if err != nil {
			t.Fatalf("element.Focus() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Focus()")
	}

	scrollErrCh := make(chan error, 1)
	go func() {
		scrollErrCh <- element.ScrollIntoView(context.Background())
	}()

	expectMethodAndReplyWithSession(t, transport, 27, "session-1", cdpdom.CommandScrollIntoViewIfNeeded, `{}`)

	select {
	case err := <-scrollErrCh:
		if err != nil {
			t.Fatalf("element.ScrollIntoView() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.ScrollIntoView()")
	}

	setAttributeErrCh := make(chan error, 1)
	go func() {
		setAttributeErrCh <- element.SetAttribute(context.Background(), "data-state", "ready")
	}()

	expectMethodAndReplyWithSession(t, transport, 28, "session-1", cdpdom.CommandSetAttributeValue, `{}`)

	select {
	case err := <-setAttributeErrCh:
		if err != nil {
			t.Fatalf("element.SetAttribute() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.SetAttribute()")
	}

	clickErrCh := make(chan error, 1)
	go func() {
		clickErrCh <- element.Click(context.Background())
	}()

	expectMethodAndReplyWithSession(t, transport, 29, "session-1", cdpdom.CommandScrollIntoViewIfNeeded, `{}`)
	expectMethodAndReplyWithSession(t, transport, 30, "session-1", cdpdom.CommandGetContentQuads, `{"quads":[[10,20,110,20,110,60,10,60]]}`)
	expectMethodAndReplyWithSession(t, transport, 31, "session-1", cdpinput.CommandDispatchMouseEvent, `{}`)
	expectMethodAndReplyWithSession(t, transport, 32, "session-1", cdpinput.CommandDispatchMouseEvent, `{}`)
	expectMethodAndReplyWithSession(t, transport, 33, "session-1", cdpinput.CommandDispatchMouseEvent, `{}`)

	select {
	case err := <-clickErrCh:
		if err != nil {
			t.Fatalf("element.Click() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Click()")
	}

	typeErrCh := make(chan error, 1)
	go func() {
		typeErrCh <- element.Type(context.Background(), "hello")
	}()

	expectMethodAndReplyWithSession(t, transport, 34, "session-1", cdpdom.CommandFocus, `{}`)
	expectMethodAndReplyWithSession(t, transport, 35, "session-1", cdpinput.CommandInsertText, `{}`)

	select {
	case err := <-typeErrCh:
		if err != nil {
			t.Fatalf("element.Type() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Type()")
	}

	pressErrCh := make(chan error, 1)
	go func() {
		pressErrCh <- element.Press(context.Background(), "Enter")
	}()

	expectMethodAndReplyWithSession(t, transport, 36, "session-1", cdpdom.CommandFocus, `{}`)
	expectMethodAndReplyWithSession(t, transport, 37, "session-1", cdpinput.CommandDispatchKeyEvent, `{}`)
	expectMethodAndReplyWithSession(t, transport, 38, "session-1", cdpinput.CommandDispatchKeyEvent, `{}`)

	select {
	case err := <-pressErrCh:
		if err != nil {
			t.Fatalf("element.Press() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Press()")
	}

	fillErrCh := make(chan error, 1)
	go func() {
		fillErrCh <- element.Fill(context.Background(), "updated")
	}()

	expectMethodAndReplyWithSession(t, transport, 39, "session-1", cdpdom.CommandFocus, `{}`)
	expectMethodAndReplyWithSession(t, transport, 40, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"boolean","value":true}}`)
	expectMethodAndReplyWithSession(t, transport, 41, "session-1", cdpinput.CommandDispatchKeyEvent, `{}`)
	expectMethodAndReplyWithSession(t, transport, 42, "session-1", cdpinput.CommandDispatchKeyEvent, `{}`)
	expectMethodAndReplyWithSession(t, transport, 43, "session-1", cdpinput.CommandInsertText, `{}`)

	select {
	case err := <-fillErrCh:
		if err != nil {
			t.Fatalf("element.Fill() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Fill()")
	}

	handleCh := make(chan *Handle, 1)
	go func() {
		handle, err := page.EvaluateHandle(context.Background(), "({answer: 42})")
		if err != nil {
			errCh <- err
			return
		}
		handleCh <- handle
	}()

	expectMethodAndReplyWithSession(t, transport, 44, "session-1", cdpruntime.CommandEvaluate, `{"result":{"type":"object","className":"Object","objectId":"obj-1"}}`)

	var handle *Handle
	select {
	case err := <-errCh:
		t.Fatalf("page.EvaluateHandle() error = %v", err)
	case handle = <-handleCh:
		if got, want := handle.ObjectID(), "obj-1"; got != want {
			t.Fatalf("handle.ObjectID() = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.EvaluateHandle()")
	}

	jsonValueCh := make(chan any, 1)
	go func() {
		value, err := handle.JSONValue(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		jsonValueCh <- value
	}()

	expectMethodAndReplyWithSession(t, transport, 45, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"object","value":{"answer":42}}}`)

	select {
	case err := <-errCh:
		t.Fatalf("handle.JSONValue() error = %v", err)
	case value := <-jsonValueCh:
		result, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("value type = %T, want map[string]any", value)
		}
		if got, want := result["answer"], float64(42); got != want {
			t.Fatalf("value[answer] = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handle.JSONValue()")
	}

	callWithHandleCh := make(chan any, 1)
	go func() {
		value, err := page.CallFunction(context.Background(), "function(obj) { return obj.answer; }", handle)
		if err != nil {
			errCh <- err
			return
		}
		callWithHandleCh <- value
	}()

	expectMethodAndReplyWithSession(t, transport, 46, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"number","value":42}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.CallFunction(handle) error = %v", err)
	case value := <-callWithHandleCh:
		number, ok := value.(float64)
		if !ok {
			t.Fatalf("value type = %T, want float64", value)
		}
		if got, want := number, 42.0; got != want {
			t.Fatalf("value = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.CallFunction(handle)")
	}

	elementFromHandleCh := make(chan *Element, 1)
	go func() {
		elementFromHandle, asErr := child.Handle().AsElement(context.Background())
		if asErr != nil {
			errCh <- asErr
			return
		}
		elementFromHandleCh <- elementFromHandle
	}()

	expectMethodAndReplyWithSession(t, transport, 47, "session-1", cdpdom.CommandRequestNode, `{"nodeId":3}`)
	expectMethodAndReplyWithSession(t, transport, 48, "session-1", cdpdom.CommandDescribeNode, `{"node":{"nodeId":3,"backendNodeId":33,"nodeType":1,"nodeName":"SPAN","localName":"span","nodeValue":"","attributes":["class","child"]}}`)
	expectMethodAndReplyWithSession(t, transport, 49, "session-1", cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLSpanElement","objectId":"obj-node-2b","description":"span.child"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("handle.AsElement() error = %v", err)
	case elementFromHandle := <-elementFromHandleCh:
		if got, want := elementFromHandle.NodeID(), 3; got != want {
			t.Fatalf("elementFromHandle.NodeID() = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handle.AsElement()")
	}

	secondHandleCh := make(chan *Handle, 1)
	go func() {
		nextHandle, err := page.CallFunctionHandle(context.Background(), "function() { return ({count: 2}); }")
		if err != nil {
			errCh <- err
			return
		}
		secondHandleCh <- nextHandle
	}()

	expectMethodAndReplyWithSession(t, transport, 50, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"object","className":"Object","objectId":"obj-2"}}`)

	var secondHandle *Handle
	select {
	case err := <-errCh:
		t.Fatalf("page.CallFunctionHandle() error = %v", err)
	case secondHandle = <-secondHandleCh:
		if got, want := secondHandle.ObjectID(), "obj-2"; got != want {
			t.Fatalf("secondHandle.ObjectID() = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.CallFunctionHandle()")
	}

	handleCallCh := make(chan any, 1)
	go func() {
		value, err := secondHandle.CallFunction(context.Background(), "function(x) { return this.count + x; }", 3)
		if err != nil {
			errCh <- err
			return
		}
		handleCallCh <- value
	}()

	expectMethodAndReplyWithSession(t, transport, 51, "session-1", cdpruntime.CommandCallFunctionOn, `{"result":{"type":"number","value":5}}`)

	select {
	case err := <-errCh:
		t.Fatalf("handle.CallFunction() error = %v", err)
	case value := <-handleCallCh:
		number, ok := value.(float64)
		if !ok {
			t.Fatalf("value type = %T, want float64", value)
		}
		if got, want := number, 5.0; got != want {
			t.Fatalf("value = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handle.CallFunction()")
	}

	releaseErrCh := make(chan error, 1)
	go func() {
		releaseErrCh <- handle.Release(context.Background())
	}()

	expectMethodAndReplyWithSession(t, transport, 52, "session-1", cdpruntime.CommandReleaseObject, `{}`)

	select {
	case err := <-releaseErrCh:
		if err != nil {
			t.Fatalf("handle.Release() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handle.Release()")
	}

	if _, err := handle.JSONValue(context.Background()); err == nil {
		t.Fatal("handle.JSONValue() after Release() error = nil, want error")
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

	expectMethodAndReplyWithSession(t, transport, 53, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1"}`)

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
	expectMethodAndReplyWithSession(t, transport, 5, "session-1", cdpdom.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 6, "session-1", cdpaccessibility.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 7, "session-1", cdppage.CommandSetLifecycleEventsEnabled, `{}`)

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

	expectMethodAndReplyWithSession(t, transport, 8, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1","loaderId":"loader-1"}`)
	transport.reads <- []byte(`{"method":"Page.frameNavigated","sessionId":"session-1","params":{"frame":{"id":"frame-1","loaderId":"loader-1","url":"https://example.com"}}}`)
	transport.reads <- []byte(`{"method":"Page.lifecycleEvent","sessionId":"session-1","params":{"frameId":"frame-1","loaderId":"loader-1","name":"load","timestamp":123.45}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Goto() error = %v", err)
	case result := <-gotoResultCh:
		if got, want := string(result.LoaderID), "loader-1"; got != want {
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

	expectMethodAndReplyWithSession(t, transport, 9, "session-1", cdppage.CommandNavigate, `{"frameId":"frame-1"}`)
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

func TestPageCloseDetachesAndClosesTarget(t *testing.T) {
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
	expectMethodAndReplyWithSession(t, transport, 5, "session-1", cdpdom.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 6, "session-1", cdpaccessibility.CommandEnable, `{}`)
	expectMethodAndReplyWithSession(t, transport, 7, "session-1", cdppage.CommandSetLifecycleEventsEnabled, `{}`)

	var page *Page
	select {
	case err := <-errCh:
		t.Fatalf("browser.NewPage() error = %v", err)
	case page = <-pageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser.NewPage()")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- page.Close(context.Background())
	}()

	expectMethodAndReply(t, transport, 8, cdptarget.CommandDetachFromTarget, `{}`)
	expectMethodAndReply(t, transport, 9, cdptarget.CommandCloseTarget, `{"success":true}`)

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("page.Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Close()")
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}

func TestPageCallFunctionRejectsHandleFromDifferentSession(t *testing.T) {
	t.Parallel()

	pageA := &Page{session: &cdp.Session{}}
	pageB := &Page{session: &cdp.Session{}}
	handle := &Handle{
		page: pageB,
		object: cdpruntime.RemoteObject{
			ObjectID: "obj-1",
		},
	}

	if _, err := pageA.CallFunction(context.Background(), "function(obj) { return obj; }", handle); err == nil {
		t.Fatal("page.CallFunction(handle from different session) error = nil, want error")
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
