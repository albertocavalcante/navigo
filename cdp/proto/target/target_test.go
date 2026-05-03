package target

import (
	"context"
	"testing"
)

type fakeCaller struct {
	method string
	params any
}

func (f *fakeCaller) Call(_ context.Context, method string, params any, result any) error {
	f.method = method
	f.params = params

	switch value := result.(type) {
	case *CreateTargetResult:
		value.TargetID = "target-1"
	case *AttachToTargetResult:
		value.SessionID = "session-1"
	}

	return nil
}

func TestCreateTargetDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	result, err := CreateTarget("about:blank").Do(context.Background(), caller)
	if err != nil {
		t.Fatalf("CreateTarget().Do() error = %v", err)
	}

	if got, want := caller.method, CommandCreateTarget; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}

	if got, want := result.TargetID, TargetID("target-1"); got != want {
		t.Fatalf("result.TargetID = %q, want %q", got, want)
	}
}

func TestAttachToTargetDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	result, err := AttachToTarget("target-1").WithFlatten(true).Do(context.Background(), caller)
	if err != nil {
		t.Fatalf("AttachToTarget().Do() error = %v", err)
	}

	if got, want := caller.method, CommandAttachToTarget; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}

	params, ok := caller.params.(*AttachToTargetParams)
	if !ok {
		t.Fatalf("caller.params type = %T, want *AttachToTargetParams", caller.params)
	}

	if !params.Flatten {
		t.Fatal("params.Flatten = false, want true")
	}

	if got, want := result.SessionID, SessionID("session-1"); got != want {
		t.Fatalf("result.SessionID = %q, want %q", got, want)
	}
}
