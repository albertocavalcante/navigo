package navigo

import "testing"

func TestKeyEventSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key      string
		wantKey  string
		wantCode string
		wantText string
		wantVK   int
	}{
		{key: "Enter", wantKey: "Enter", wantCode: "Enter", wantText: "\r", wantVK: 13},
		{key: "Escape", wantKey: "Escape", wantCode: "Escape", wantText: "", wantVK: 27},
		{key: "ArrowLeft", wantKey: "ArrowLeft", wantCode: "ArrowLeft", wantText: "", wantVK: 37},
		{key: "a", wantKey: "a", wantCode: "a", wantText: "a", wantVK: int('a')},
	}

	for _, test := range tests {
		spec, err := keyEventSpec(test.key)
		if err != nil {
			t.Fatalf("keyEventSpec(%q) error = %v", test.key, err)
		}
		if spec.key != test.wantKey || spec.code != test.wantCode || spec.text != test.wantText || spec.windowsVirtualKeyCode != test.wantVK {
			t.Fatalf("keyEventSpec(%q) = %+v", test.key, spec)
		}
	}
}
