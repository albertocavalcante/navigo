package navigo

import (
	"context"
	"strings"
	"testing"

	"github.com/albertocavalcante/navigo/cdp"
	cdpaccessibility "github.com/albertocavalcante/navigo/cdp/proto/accessibility"
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
		{"nodeId":"ax-button","ignored":false,"role":{"type":"role","value":"button"},"name":{"type":"string","value":"Submit"},"backendDOMNodeId":22}
	]}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Snapshot() error = %v", err)
	case snapshot := <-resultCh:
		if got, want := len(snapshot.Nodes()), 2; got != want {
			t.Fatalf("len(snapshot.Nodes()) = %d, want %d", got, want)
		}

		buttons := snapshot.FindByRole("button")
		if got, want := len(buttons), 1; got != want {
			t.Fatalf("len(snapshot.FindByRole(button)) = %d, want %d", got, want)
		}
		if got, want := buttons[0].Name, "Submit"; got != want {
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

		markdown := snapshot.Markdown()
		for _, snippet := range []string{
			`- RootWebArea "Example"`,
			`- button "Submit" [r22]`,
		} {
			if !strings.Contains(markdown, snippet) {
				t.Fatalf("snapshot.Markdown() missing %q\n%s", snippet, markdown)
			}
		}
	}
}
