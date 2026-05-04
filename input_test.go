package navigo

import (
	"testing"

	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
)

func TestCenterPointFromQuads(t *testing.T) {
	t.Parallel()

	x, y, ok := centerPointFromQuads([]cdpdom.Quad{
		{10.0, 20.0, 110.0, 20.0, 110.0, 60.0, 10.0, 60.0},
	})
	if !ok {
		t.Fatal("centerPointFromQuads() ok = false, want true")
	}
	if got, want := x, 60.0; got != want {
		t.Fatalf("x = %v, want %v", got, want)
	}
	if got, want := y, 40.0; got != want {
		t.Fatalf("y = %v, want %v", got, want)
	}
}

func TestCenterPointFromBoxModel(t *testing.T) {
	t.Parallel()

	x, y, ok := centerPointFromBoxModel(cdpdom.BoxModel{
		Content: cdpdom.Quad{10.0, 20.0, 110.0, 20.0, 110.0, 60.0, 10.0, 60.0},
	})
	if !ok {
		t.Fatal("centerPointFromBoxModel() ok = false, want true")
	}
	if got, want := x, 60.0; got != want {
		t.Fatalf("x = %v, want %v", got, want)
	}
	if got, want := y, 40.0; got != want {
		t.Fatalf("y = %v, want %v", got, want)
	}
}
