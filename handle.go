package navigo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
)

type Handle struct {
	page   *Page
	object cdpruntime.RemoteObject

	mu       sync.Mutex
	released bool
}

func (h *Handle) ObjectID() string {
	return string(h.object.ObjectID)
}

func (h *Handle) Type() cdpruntime.RemoteObjectType {
	return h.object.Type
}

func (h *Handle) Subtype() cdpruntime.RemoteObjectSubtype {
	return h.object.Subtype
}

func (h *Handle) Release(ctx context.Context) error {
	h.mu.Lock()
	if h.released {
		h.mu.Unlock()
		return nil
	}
	h.released = true
	objectID := h.object.ObjectID
	h.mu.Unlock()

	if err := cdpruntime.ReleaseObject(objectID).Do(ctx, h.page.session); err != nil {
		return fmt.Errorf("release handle: %w", err)
	}

	return nil
}

func (h *Handle) JSONValue(ctx context.Context) (any, error) {
	return h.callFunction(ctx, "function() { return this; }", false)
}

func (h *Handle) CallFunction(ctx context.Context, functionDeclaration string, args ...any) (any, error) {
	return h.callFunction(ctx, functionDeclaration, false, args...)
}

func (h *Handle) CallFunctionHandle(ctx context.Context, functionDeclaration string, args ...any) (*Handle, error) {
	value, err := h.callFunction(ctx, functionDeclaration, true, args...)
	if err != nil {
		return nil, err
	}

	handle, ok := value.(*Handle)
	if !ok {
		return nil, fmt.Errorf("call handle function: unexpected result type %T", value)
	}

	return handle, nil
}

func (h *Handle) callFunction(ctx context.Context, functionDeclaration string, wantHandle bool, args ...any) (any, error) {
	objectID, err := h.ensureUsable()
	if err != nil {
		return nil, err
	}

	callArguments := make([]cdpruntime.CallArgument, 0, len(args))
	for _, arg := range args {
		callArgument, callErr := encodeCallArgument(arg)
		if callErr != nil {
			return nil, callErr
		}
		callArguments = append(callArguments, callArgument)
	}

	call := cdpruntime.CallFunctionOn(functionDeclaration).
		WithObjectID(objectID).
		WithArguments(callArguments).
		WithAwaitPromise(true)
	if !wantHandle {
		call = call.WithReturnByValue(true)
	}

	result, err := call.Do(ctx, h.page.session)
	if err != nil {
		return nil, fmt.Errorf("call handle function: %w", err)
	}

	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("call handle function: %s", result.ExceptionDetails.Text)
	}

	if wantHandle {
		return newHandle(h.page, result.Result, "call handle function")
	}

	return decodeRemoteValue(result.Result)
}

func (h *Handle) ensureUsable() (cdpruntime.RemoteObjectID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.released {
		return "", errors.New("handle already released")
	}

	if h.object.ObjectID == "" {
		return "", errors.New("handle has no remote object ID")
	}

	return h.object.ObjectID, nil
}

func newHandle(page *Page, object cdpruntime.RemoteObject, operation string) (*Handle, error) {
	if object.ObjectID == "" {
		return nil, fmt.Errorf("%s: result did not include a remote object ID", operation)
	}

	return &Handle{
		page:   page,
		object: object,
	}, nil
}
