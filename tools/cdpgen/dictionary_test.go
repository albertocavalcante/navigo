package main

import (
	"strings"
	"testing"
)

func TestNamedOpenObjectRendersDictionary(t *testing.T) {
	domain := domainSpec{Domain: "Network", Types: []typeSpec{{ID: "Headers", Type: "object"}}, Commands: []commandSpec{{Name: "setExtraHTTPHeaders", Parameters: []propertySpec{{Name: "headers", Ref: "Headers"}}}}}
	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range []string{"type Headers map[string]json.RawMessage", "func (", "MarshalJSON() ([]byte, error)"} {
		if !strings.Contains(string(rendered), snippet) {
			t.Fatalf("missing open-object contract %q", snippet)
		}
	}
	if strings.Contains(string(rendered), "type Headers struct") {
		t.Fatal("open object was rendered as closed empty struct")
	}
}

func TestReferencedOpenObjectRendersDictionary(t *testing.T) {
	network := domainSpec{Domain: "Network", Types: []typeSpec{{ID: "Headers", Type: "object"}}}
	fetch := domainSpec{Domain: "Fetch", Commands: []commandSpec{{Name: "continueRequest", Parameters: []propertySpec{{Name: "headers", Ref: "Network.Headers", Optional: true}}}}}
	rendered, err := renderDomainWithTypeIndex(fetch, buildTypeIndex(protocolSpec{Domains: []domainSpec{network, fetch}}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "type Headers map[string]json.RawMessage") {
		t.Fatal("cross-domain open object lost dictionary shape")
	}
	if strings.Count(string(rendered), "type Headers ") != 1 {
		t.Fatal("cross-domain dictionary declaration repeated")
	}
}

func TestEmptyCommandParametersRemainClosed(t *testing.T) {
	domain := domainSpec{Domain: "Network", Commands: []commandSpec{{Name: "enable"}}}
	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "type EnableParams struct") {
		t.Fatal("empty command parameters changed from a closed object")
	}
	if strings.Contains(string(rendered), "map[string]json.RawMessage") {
		t.Fatal("empty command parameters became an open dictionary")
	}
}
