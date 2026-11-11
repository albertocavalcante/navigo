package fetch_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/albertocavalcante/navigo/cdp/proto/fetch"
)

func TestReferencedNetworkHeadersPreserveDynamicMembers(t *testing.T) {
	event, err := fetch.DecodeRequestPaused(json.RawMessage(`{"requestId":"synthetic","request":{"url":"https://example.invalid/","method":"GET","headers":{"Content-Type":"application/json","X-Fixture":"synthetic"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event.Request.Headers)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"Content-Type":"application/json"`)) || !bytes.Contains(encoded, []byte(`"X-Fixture":"synthetic"`)) {
		t.Fatal("referenced dictionary fields discarded")
	}
}
