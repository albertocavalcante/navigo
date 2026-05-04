package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSpecAndDomainNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "protocol.json")

	content := `{
		"version": {"major": "1", "minor": "3"},
		"domains": [
			{"domain": "Page"},
			{"domain": "Runtime"},
			{"domain": "Accessibility"}
		]
	}`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	spec, err := loadSpec(path)
	if err != nil {
		t.Fatalf("loadSpec() error = %v", err)
	}

	names := domainNames(spec)
	want := []string{"Accessibility", "Page", "Runtime"}
	if len(names) != len(want) {
		t.Fatalf("len(names) = %d, want %d", len(names), len(want))
	}

	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("names[%d] = %q, want %q", index, names[index], want[index])
		}
	}
}

func TestLoadSpecs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "browser_protocol.json")
	secondPath := filepath.Join(dir, "js_protocol.json")

	first := `{
		"version": {"major": "1", "minor": "3"},
		"domains": [{"domain": "Page"}]
	}`
	second := `{
		"version": {"major": "1", "minor": "3"},
		"domains": [{"domain": "Runtime"}]
	}`

	for _, item := range []struct {
		path    string
		content string
	}{
		{path: firstPath, content: first},
		{path: secondPath, content: second},
	} {
		if err := os.WriteFile(item.path, []byte(item.content), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", item.path, err)
		}
	}

	spec, err := loadSpecs([]string{firstPath, secondPath})
	if err != nil {
		t.Fatalf("loadSpecs() error = %v", err)
	}

	names := domainNames(spec)
	want := []string{"Page", "Runtime"}
	if len(names) != len(want) {
		t.Fatalf("len(names) = %d, want %d", len(names), len(want))
	}

	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("names[%d] = %q, want %q", index, names[index], want[index])
		}
	}
}

func TestFindDomain(t *testing.T) {
	t.Parallel()

	spec := protocolSpec{
		Domains: []domainSpec{
			{Domain: "Page"},
			{Domain: "Runtime"},
		},
	}

	domain, err := findDomain(spec, "Runtime")
	if err != nil {
		t.Fatalf("findDomain() error = %v", err)
	}

	if got, want := domain.Domain, "Runtime"; got != want {
		t.Fatalf("domain.Domain = %q, want %q", got, want)
	}
}

func TestLowerFirst(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"URL":                "url",
		"Navigate":           "navigate",
		"FrameID":            "frameID",
		"DOMContentLoaded":   "domContentLoaded",
		"SetLifecycleEvents": "setLifecycleEvents",
		"Type":               "type_",
		"Map":                "map_",
	}

	for input, want := range cases {
		if got := lowerFirst(input); got != want {
			t.Fatalf("lowerFirst(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPackageNameForDomain(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"DOM":         "dom",
		"DOMSnapshot": "domsnapshot",
		"IO":          "io",
		"CSS":         "css",
		"Type":        "type_",
	}

	for input, want := range cases {
		if got := packageNameForDomain(input); got != want {
			t.Fatalf("packageNameForDomain(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	t.Parallel()

	got := splitCSV(" browser.json, js.json , ,page ")
	want := []string{"browser.json", "js.json", "page"}
	if len(got) != len(want) {
		t.Fatalf("len(splitCSV()) = %d, want %d", len(got), len(want))
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("splitCSV()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}
