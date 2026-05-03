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
