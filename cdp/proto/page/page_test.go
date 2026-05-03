package page

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeCaller struct {
	method string
	params any
}

func (f *fakeCaller) Call(_ context.Context, method string, params any, result any) error {
	f.method = method
	f.params = params

	if value, ok := result.(*NavigateResult); ok {
		value.FrameID = "frame-1"
		value.LoaderID = "loader-1"
	}

	return nil
}

func TestEnableDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	if err := Enable().Do(context.Background(), caller); err != nil {
		t.Fatalf("Enable().Do() error = %v", err)
	}

	if got, want := caller.method, CommandEnable; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}
}

func TestNavigateDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	result, err := Navigate("https://example.com").Do(context.Background(), caller)
	if err != nil {
		t.Fatalf("Navigate().Do() error = %v", err)
	}

	if got, want := caller.method, CommandNavigate; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}

	if got, want := result.FrameID, "frame-1"; got != want {
		t.Fatalf("result.FrameID = %q, want %q", got, want)
	}
}

func TestSetLifecycleEventsEnabledDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	if err := SetLifecycleEventsEnabled(true).Do(context.Background(), caller); err != nil {
		t.Fatalf("SetLifecycleEventsEnabled().Do() error = %v", err)
	}

	if got, want := caller.method, CommandSetLifecycleEventsEnabled; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}
}

func TestDecodeLoadEventFired(t *testing.T) {
	t.Parallel()

	event, err := DecodeLoadEventFired(json.RawMessage(`{"timestamp":123.45}`))
	if err != nil {
		t.Fatalf("DecodeLoadEventFired() error = %v", err)
	}

	if got, want := event.Timestamp, 123.45; got != want {
		t.Fatalf("event.Timestamp = %v, want %v", got, want)
	}
}

func TestDecodeLifecycleEvent(t *testing.T) {
	t.Parallel()

	event, err := DecodeLifecycleEvent(json.RawMessage(`{"frameId":"frame-1","loaderId":"loader-1","name":"DOMContentLoaded","timestamp":123.45}`))
	if err != nil {
		t.Fatalf("DecodeLifecycleEvent() error = %v", err)
	}

	if got, want := event.Name, LifecycleEventDOMContentLoaded; got != want {
		t.Fatalf("event.Name = %q, want %q", got, want)
	}
}
