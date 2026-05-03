package browser

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

	res := result.(*GetVersionResult)
	res.ProtocolVersion = "1.3"
	res.Product = "Chrome/136.0.0.0"
	res.UserAgent = "Mozilla/5.0"
	res.JavaScriptVersion = "12.3.4"

	return nil
}

func TestGetVersionDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	result, err := GetVersion().Do(context.Background(), caller)
	if err != nil {
		t.Fatalf("GetVersion().Do() error = %v", err)
	}

	if got, want := caller.method, CommandGetVersion; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}

	if _, ok := caller.params.(*GetVersionParams); !ok {
		t.Fatalf("caller.params type = %T, want *GetVersionParams", caller.params)
	}

	if got, want := result.Product, "Chrome/136.0.0.0"; got != want {
		t.Fatalf("result.Product = %q, want %q", got, want)
	}
}
