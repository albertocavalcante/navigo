package runtime

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

	value := result.(*EvaluateResult)
	value.Result = RemoteObject{
		Type:  "number",
		Value: json.RawMessage(`2`),
	}

	return nil
}

func TestEvaluateDo(t *testing.T) {
	t.Parallel()

	caller := &fakeCaller{}
	result, err := Evaluate("1 + 1").WithReturnByValue(true).WithAwaitPromise(true).Do(context.Background(), caller)
	if err != nil {
		t.Fatalf("Evaluate().Do() error = %v", err)
	}

	if got, want := caller.method, CommandEvaluate; got != want {
		t.Fatalf("caller.method = %q, want %q", got, want)
	}

	params, ok := caller.params.(*EvaluateParams)
	if !ok {
		t.Fatalf("caller.params type = %T, want *EvaluateParams", caller.params)
	}

	if !params.ReturnByValue {
		t.Fatal("params.ReturnByValue = false, want true")
	}

	if !params.AwaitPromise {
		t.Fatal("params.AwaitPromise = false, want true")
	}

	got, ok := result.Result.Value.(float64)
	if !ok {
		raw, rawOK := result.Result.Value.(json.RawMessage)
		if !rawOK {
			t.Fatalf("result.Result.Value type = %T, want float64 or json.RawMessage", result.Result.Value)
		}
		if got, want := string(raw), "2"; got != want {
			t.Fatalf("string(result.Result.Value) = %q, want %q", got, want)
		}
		return
	}
	if want := 2.0; got != want {
		t.Fatalf("result.Result.Value = %v, want %v", got, want)
	}
}
