package navigo

import (
	"context"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
)

func TestPageWaitForSelectorVisible(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{
		session:               conn.RootSession(),
		defaultContextByFrame: map[string]cdpruntime.ExecutionContextID{"frame-1": 7},
		mainFrameID:           "frame-1",
	}

	resultCh := make(chan *Element, 1)
	errCh := make(chan error, 1)
	go func() {
		element, err := page.WaitForSelector(context.Background(), "#app", WithWaitState(WaitStateVisible), WithPollInterval(time.Millisecond))
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- element
	}()

	expectMethodAndReply(t, transport, 1, cdpdom.CommandGetDocument, `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 2, cdpdom.CommandQuerySelector, `{"nodeId":0}`)
	expectMethodAndReply(t, transport, 3, cdpdom.CommandGetDocument, `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 4, cdpdom.CommandQuerySelector, `{"nodeId":2}`)
	expectMethodAndReply(t, transport, 5, cdpdom.CommandDescribeNode, `{"node":{"nodeId":2,"backendNodeId":22,"nodeType":1,"nodeName":"DIV","localName":"div","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 6, cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLDivElement","objectId":"obj-node-1"}}`)
	expectMethodAndReply(t, transport, 7, cdpruntime.CommandCallFunctionOn, `{"result":{"type":"boolean","value":true}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.WaitForSelector() error = %v", err)
	case element := <-resultCh:
		if element == nil {
			t.Fatal("page.WaitForSelector() = nil, want element")
		}
		if got, want := element.NodeID(), 2; got != want {
			t.Fatalf("element.NodeID() = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.WaitForSelector()")
	}
}

func TestPageWaitForSelectorHidden(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{
		session:               conn.RootSession(),
		defaultContextByFrame: map[string]cdpruntime.ExecutionContextID{"frame-1": 7},
		mainFrameID:           "frame-1",
	}

	resultCh := make(chan *Element, 1)
	errCh := make(chan error, 1)
	go func() {
		element, err := page.WaitForSelector(context.Background(), "#missing", WithWaitState(WaitStateHidden), WithPollInterval(time.Millisecond))
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- element
	}()

	expectMethodAndReply(t, transport, 1, cdpdom.CommandGetDocument, `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 2, cdpdom.CommandQuerySelector, `{"nodeId":0}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.WaitForSelector(hidden) error = %v", err)
	case element := <-resultCh:
		if element != nil {
			t.Fatalf("page.WaitForSelector(hidden) = %#v, want nil", element)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.WaitForSelector(hidden)")
	}
}

func TestElementStateHelpers(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}
	element := &Element{
		page: page,
		handle: &Handle{
			page: page,
			object: cdpruntime.RemoteObject{
				ObjectID: "obj-node-1",
			},
		},
		nodeID:        2,
		backendNodeID: 22,
	}

	visibleCh := make(chan bool, 1)
	errCh := make(chan error, 1)
	go func() {
		visible, err := element.IsVisible(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		visibleCh <- visible
	}()

	expectMethodAndReply(t, transport, 1, cdpruntime.CommandCallFunctionOn, `{"result":{"type":"boolean","value":true}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.IsVisible() error = %v", err)
	case visible := <-visibleCh:
		if !visible {
			t.Fatal("element.IsVisible() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.IsVisible()")
	}

	enabledCh := make(chan bool, 1)
	go func() {
		enabled, err := element.IsEnabled(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		enabledCh <- enabled
	}()

	expectMethodAndReply(t, transport, 2, cdpruntime.CommandCallFunctionOn, `{"result":{"type":"boolean","value":true}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.IsEnabled() error = %v", err)
	case enabled := <-enabledCh:
		if !enabled {
			t.Fatal("element.IsEnabled() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.IsEnabled()")
	}

	editableCh := make(chan bool, 1)
	go func() {
		editable, err := element.IsEditable(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		editableCh <- editable
	}()

	expectMethodAndReply(t, transport, 3, cdpruntime.CommandCallFunctionOn, `{"result":{"type":"boolean","value":true}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.IsEditable() error = %v", err)
	case editable := <-editableCh:
		if !editable {
			t.Fatal("element.IsEditable() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.IsEditable()")
	}
}
