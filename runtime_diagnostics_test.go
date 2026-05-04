package navigo

import (
	"context"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
)

func TestPageObserveConsole(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	observer, err := page.ObserveConsole(context.Background())
	if err != nil {
		t.Fatalf("page.ObserveConsole() error = %v", err)
	}

	transport.reads <- []byte(`{"method":"Runtime.consoleAPICalled","params":{"type":"log","args":[{"type":"string","value":"hello"},{"type":"number","value":42}],"executionContextId":7,"timestamp":123.45,"context":"default"}}`)

	select {
	case event := <-observer.Events():
		if got, want := event.Type, string(cdpruntime.ConsoleAPICalledTypeLog); got != want {
			t.Fatalf("event.Type = %q, want %q", got, want)
		}
		if got, want := len(event.Args), 2; got != want {
			t.Fatalf("len(event.Args) = %d, want %d", got, want)
		}
		if got, want := event.Args[0], "hello"; got != want {
			t.Fatalf("event.Args[0] = %v, want %v", got, want)
		}
		if got, want := event.Args[1], float64(42); got != want {
			t.Fatalf("event.Args[1] = %v, want %v", got, want)
		}
		if got, want := event.ExecutionContextID, 7; got != want {
			t.Fatalf("event.ExecutionContextID = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for console event")
	}

	observer.Close()

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	<-done
}

func TestPageObserveExceptions(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	observer, err := page.ObserveExceptions(context.Background())
	if err != nil {
		t.Fatalf("page.ObserveExceptions() error = %v", err)
	}

	transport.reads <- []byte(`{"method":"Runtime.exceptionThrown","params":{"timestamp":124.45,"exceptionDetails":{"exceptionId":9,"text":"Uncaught","lineNumber":12,"columnNumber":3,"scriptId":"42","url":"https://example.com/app.js","executionContextId":7}}}`)

	select {
	case event := <-observer.Events():
		if got, want := event.ExceptionID, 9; got != want {
			t.Fatalf("event.ExceptionID = %d, want %d", got, want)
		}
		if got, want := event.Text, "Uncaught"; got != want {
			t.Fatalf("event.Text = %q, want %q", got, want)
		}
		if got, want := event.URL, "https://example.com/app.js"; got != want {
			t.Fatalf("event.URL = %q, want %q", got, want)
		}
		if got, want := event.ExecutionContextID, 7; got != want {
			t.Fatalf("event.ExecutionContextID = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for exception event")
	}

	observer.Close()

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	<-done
}
