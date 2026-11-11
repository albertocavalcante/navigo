package network_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/albertocavalcante/navigo/cdp/proto/network"
)

func TestHeadersPreserveDynamicMembers(t *testing.T) {
	body := []byte(`{"Accept":"application/json","Authorization":"SYNTHETIC_CANARY","X-Count":9007199254740993,"X-Future":{"enabled":false},"X-Null":null}`)
	var headers network.Headers
	if err := json.Unmarshal(body, &headers); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(headers)
	if err != nil {
		t.Fatal(err)
	}
	var original, restored map[string]json.RawMessage
	if json.Unmarshal(body, &original) != nil || json.Unmarshal(encoded, &restored) != nil {
		t.Fatal("invalid test JSON")
	}
	if len(restored) != len(original) {
		t.Fatalf("header entries lost: got %d want %d", len(restored), len(original))
	}
	for key, value := range original {
		if !bytes.Equal(restored[key], value) {
			t.Fatalf("header %s wire value changed", key)
		}
	}
}

func TestHeaderZeroValueRemainsAnObject(t *testing.T) {
	for name, body := range map[string][]byte{"zero": nil, "empty": []byte(`{}`), "legacy_null": []byte(`null`)} {
		t.Run(name, func(t *testing.T) {
			var headers network.Headers
			if body != nil {
				if err := json.Unmarshal(body, &headers); err != nil {
					t.Fatal(err)
				}
			}
			encoded, err := json.Marshal(headers)
			if err != nil || string(encoded) != "{}" {
				t.Fatalf("zero header shape changed: %s %v", encoded, err)
			}
		})
	}
}

type headerCaller struct {
	t      *testing.T
	params []byte
}

func (c *headerCaller) Call(_ context.Context, method string, params any, _ any) error {
	c.t.Helper()
	if method != "Network.setExtraHTTPHeaders" {
		c.t.Fatalf("unexpected command %s", method)
	}
	var err error
	c.params, err = json.Marshal(params)
	return err
}

func TestSetExtraHTTPHeadersEmitsDictionary(t *testing.T) {
	var headers network.Headers
	if err := json.Unmarshal([]byte(`{"X-Fixture":"synthetic value"}`), &headers); err != nil {
		t.Fatal(err)
	}
	caller := &headerCaller{t: t}
	if err := network.SetExtraHTTPHeaders(headers).Do(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(caller.params, []byte(`"X-Fixture":"synthetic value"`)) {
		t.Fatal("command discarded caller header dictionary")
	}
}

func TestHeadersRejectInvalidMemberEncoding(t *testing.T) {
	headers := network.Headers{"X-Fixture": json.RawMessage(`invalid`)}
	if _, err := json.Marshal(headers); err == nil {
		t.Fatal("invalid raw JSON member was silently encoded")
	}
}

func TestHeadersRejectNonObjects(t *testing.T) {
	for _, body := range []string{`[]`, `42`, `"text"`, `true`} {
		t.Run(body, func(t *testing.T) {
			var headers network.Headers
			if err := json.Unmarshal([]byte(body), &headers); err == nil {
				t.Fatal("non-object dictionary was accepted")
			}
		})
	}
}
