package navigo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpaccessibility "github.com/albertocavalcante/navigo/cdp/proto/accessibility"
	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdpinput "github.com/albertocavalcante/navigo/cdp/proto/input"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
)

func TestPageSnapshot(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}

	resultCh := make(chan *Snapshot, 1)
	errCh := make(chan error, 1)
	go func() {
		snapshot, err := page.Snapshot(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- snapshot
	}()

	expectMethodAndReply(t, transport, 1, cdpaccessibility.CommandGetFullAXTree, `{"nodes":[
		{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"Example"},"childIds":["ax-button"]},
		{"nodeId":"ax-button","ignored":false,"role":{"type":"role","value":"button"},"name":{"type":"string","value":"Submit form"},"backendDOMNodeId":22,"properties":[{"name":"disabled","value":{"type":"boolean","value":true}}]}
	]}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Snapshot() error = %v", err)
	case snapshot := <-resultCh:
		if got, want := len(page.axWatchers), 0; got != want {
			t.Fatalf("len(page.axWatchers) after Snapshot() = %d, want %d", got, want)
		}
		if got, want := len(snapshot.Nodes()), 2; got != want {
			t.Fatalf("len(snapshot.Nodes()) = %d, want %d", got, want)
		}

		buttons := snapshot.FindByRole("butt")
		if got, want := len(buttons), 1; got != want {
			t.Fatalf("len(snapshot.FindByRole(button)) = %d, want %d", got, want)
		}
		if got, want := buttons[0].Name, "Submit form"; got != want {
			t.Fatalf("buttons[0].Name = %q, want %q", got, want)
		}
		if got, want := buttons[0].Ref, "r22"; got != want {
			t.Fatalf("buttons[0].Ref = %q, want %q", got, want)
		}

		node, ok := snapshot.FindByRef("r22")
		if !ok {
			t.Fatal("snapshot.FindByRef(r22) ok = false, want true")
		}
		if got, want := node.Role, "button"; got != want {
			t.Fatalf("node.Role = %q, want %q", got, want)
		}

		interactive := snapshot.InteractiveNodes()
		if got, want := len(interactive), 1; got != want {
			t.Fatalf("len(snapshot.InteractiveNodes()) = %d, want %d", got, want)
		}

		byName := snapshot.FindByName("submit")
		if got, want := len(byName), 1; got != want {
			t.Fatalf("len(snapshot.FindByName(submit)) = %d, want %d", got, want)
		}

		markdown := snapshot.Markdown()
		for _, snippet := range []string{
			`- RootWebArea "Example"`,
			`- button "Submit form" [r22] [disabled]`,
		} {
			if !strings.Contains(markdown, snippet) {
				t.Fatalf("snapshot.Markdown() missing %q\n%s", snippet, markdown)
			}
		}
	}
}

func TestSnapshotActions(t *testing.T) {
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

	resultCh := make(chan *Snapshot, 1)
	errCh := make(chan error, 1)
	go func() {
		snapshot, err := page.Snapshot(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- snapshot
	}()

	expectMethodAndReply(t, transport, 1, cdpaccessibility.CommandGetFullAXTree, `{"nodes":[
		{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"Example"},"childIds":["ax-input"]},
		{"nodeId":"ax-input","ignored":false,"role":{"type":"role","value":"textbox"},"name":{"type":"string","value":"Search"},"backendDOMNodeId":22}
	]}`)

	var snapshot *Snapshot
	select {
	case err := <-errCh:
		t.Fatalf("page.Snapshot() error = %v", err)
	case snapshot = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}
	if snapshot == nil {
		t.Fatal("snapshot = nil, want snapshot")
	}

	clickErrCh := make(chan error, 1)
	go func() {
		clickErrCh <- snapshot.Click(context.Background(), "r22")
	}()

	expectMethodAndReply(t, transport, 2, cdpdom.CommandDescribeNode, `{"node":{"nodeId":2,"backendNodeId":22,"nodeType":1,"nodeName":"INPUT","localName":"input","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 3, cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLInputElement","objectId":"obj-node-1"}}`)
	expectMethodAndReply(t, transport, 4, cdpdom.CommandScrollIntoViewIfNeeded, `{}`)
	expectMethodAndReply(t, transport, 5, cdpdom.CommandGetContentQuads, `{"quads":[[10,20,110,20,110,60,10,60]]}`)
	expectMethodAndReply(t, transport, 6, cdpinput.CommandDispatchMouseEvent, `{}`)
	expectMethodAndReply(t, transport, 7, cdpinput.CommandDispatchMouseEvent, `{}`)
	expectMethodAndReply(t, transport, 8, cdpinput.CommandDispatchMouseEvent, `{}`)

	select {
	case err := <-clickErrCh:
		if err != nil {
			t.Fatalf("snapshot.Click() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot.Click()")
	}

	typeErrCh := make(chan error, 1)
	go func() {
		typeErrCh <- snapshot.Type(context.Background(), "r22", "hello")
	}()

	expectMethodAndReply(t, transport, 9, cdpdom.CommandDescribeNode, `{"node":{"nodeId":2,"backendNodeId":22,"nodeType":1,"nodeName":"INPUT","localName":"input","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 10, cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLInputElement","objectId":"obj-node-2"}}`)
	expectMethodAndReply(t, transport, 11, cdpdom.CommandFocus, `{}`)
	expectMethodAndReply(t, transport, 12, cdpinput.CommandInsertText, `{}`)

	select {
	case err := <-typeErrCh:
		if err != nil {
			t.Fatalf("snapshot.Type() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot.Type()")
	}

	fillErrCh := make(chan error, 1)
	go func() {
		fillErrCh <- snapshot.Fill(context.Background(), "r22", "updated")
	}()

	expectMethodAndReply(t, transport, 13, cdpdom.CommandDescribeNode, `{"node":{"nodeId":2,"backendNodeId":22,"nodeType":1,"nodeName":"INPUT","localName":"input","nodeValue":""}}`)
	expectMethodAndReply(t, transport, 14, cdpdom.CommandResolveNode, `{"object":{"type":"object","subtype":"node","className":"HTMLInputElement","objectId":"obj-node-3"}}`)
	expectMethodAndReply(t, transport, 15, cdpdom.CommandFocus, `{}`)
	expectMethodAndReply(t, transport, 16, cdpruntime.CommandCallFunctionOn, `{"result":{"type":"boolean","value":true}}`)
	expectMethodAndReply(t, transport, 17, cdpinput.CommandDispatchKeyEvent, `{}`)
	expectMethodAndReply(t, transport, 18, cdpinput.CommandDispatchKeyEvent, `{}`)
	expectMethodAndReply(t, transport, 19, cdpinput.CommandInsertText, `{}`)

	select {
	case err := <-fillErrCh:
		if err != nil {
			t.Fatalf("snapshot.Fill() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot.Fill()")
	}
}

func TestSnapshotRefresh(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}

	resultCh := make(chan *Snapshot, 1)
	errCh := make(chan error, 1)
	go func() {
		snapshot, err := page.Snapshot(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- snapshot
	}()

	expectMethodAndReply(t, transport, 1, cdpaccessibility.CommandGetFullAXTree, `{"nodes":[
		{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"Before"},"childIds":["ax-button"]},
		{"nodeId":"ax-button","ignored":false,"role":{"type":"role","value":"button"},"name":{"type":"string","value":"Old"},"backendDOMNodeId":22}
	]}`)

	var snapshot *Snapshot
	select {
	case err := <-errCh:
		t.Fatalf("page.Snapshot() error = %v", err)
	case snapshot = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}

	if got, want := snapshot.Markdown(), "- RootWebArea \"Before\"\n  - button \"Old\" [r22]"; got != want {
		t.Fatalf("snapshot.Markdown() before refresh = %q, want %q", got, want)
	}

	refreshErrCh := make(chan error, 1)
	go func() {
		refreshErrCh <- snapshot.Refresh(context.Background())
	}()

	expectMethodAndReply(t, transport, 2, cdpaccessibility.CommandGetFullAXTree, `{"nodes":[
		{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"After"},"childIds":["ax-textbox"]},
		{"nodeId":"ax-textbox","ignored":false,"role":{"type":"role","value":"textbox"},"name":{"type":"string","value":"Search"},"backendDOMNodeId":44,"properties":[{"name":"readonly","value":{"type":"boolean","value":true}}]}
	]}`)

	select {
	case err := <-refreshErrCh:
		if err != nil {
			t.Fatalf("snapshot.Refresh() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot.Refresh()")
	}

	if _, ok := snapshot.FindByRef("r22"); ok {
		t.Fatal("snapshot.FindByRef(r22) ok = true after refresh, want false")
	}

	textboxes := snapshot.FindByRole("textbox")
	if got, want := len(textboxes), 1; got != want {
		t.Fatalf("len(snapshot.FindByRole(textbox)) = %d, want %d", got, want)
	}
	if got, want := textboxes[0].Ref, "r44"; got != want {
		t.Fatalf("textboxes[0].Ref = %q, want %q", got, want)
	}

	for _, snippet := range []string{
		`- RootWebArea "After"`,
		`- textbox "Search" [r44] [readonly]`,
	} {
		if !strings.Contains(snapshot.Markdown(), snippet) {
			t.Fatalf("snapshot.Markdown() after refresh missing %q\n%s", snippet, snapshot.Markdown())
		}
	}
}

func TestSnapshotWaitForUpdate(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{
		session:    conn.RootSession(),
		axWatchers: make(map[int]*axWatcher),
	}
	page.startEventLoop()

	resultCh := make(chan *Snapshot, 1)
	errCh := make(chan error, 1)
	go func() {
		snapshot, err := page.Snapshot(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- snapshot
	}()

	expectMethodAndReply(t, transport, 1, cdpaccessibility.CommandGetFullAXTree, `{"nodes":[
		{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"Before"}}
	]}`)

	var snapshot *Snapshot
	select {
	case err := <-errCh:
		t.Fatalf("page.Snapshot() error = %v", err)
	case snapshot = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}

	if _, err := snapshot.ensureUpdateSubscription(); err != nil {
		t.Fatalf("snapshot.ensureUpdateSubscription() error = %v", err)
	}
	if got, want := len(page.axWatchers), 1; got != want {
		t.Fatalf("len(page.axWatchers) after ensureUpdateSubscription() = %d, want %d", got, want)
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- snapshot.WaitForUpdate(context.Background())
	}()

	transport.reads <- []byte(`{"method":"Accessibility.nodesUpdated","params":{"nodes":[{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"Changed"}}]}}`)
	expectMethodAndReply(t, transport, 2, cdpaccessibility.CommandGetFullAXTree, `{"nodes":[
		{"nodeId":"ax-root","ignored":false,"role":{"type":"role","value":"RootWebArea"},"name":{"type":"string","value":"After update"}}
	]}`)

	select {
	case err := <-waitErrCh:
		if err != nil {
			t.Fatalf("snapshot.WaitForUpdate() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot.WaitForUpdate()")
	}

	if got, want := snapshot.Markdown(), `- RootWebArea "After update"`; got != want {
		t.Fatalf("snapshot.Markdown() after update = %q, want %q", got, want)
	}

	snapshot.Close()
	if got, want := len(page.axWatchers), 0; got != want {
		t.Fatalf("len(page.axWatchers) after Snapshot.Close() = %d, want %d", got, want)
	}
}
