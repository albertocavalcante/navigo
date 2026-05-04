package navigo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdplog "github.com/albertocavalcante/navigo/cdp/proto/log"
)

func TestPageObserveLog(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	observerCh := make(chan *LogObserver, 1)
	errCh := make(chan error, 1)
	go func() {
		observer, err := page.ObserveLog(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		observerCh <- observer
	}()

	expectMethodAndReply(t, transport, 1, cdplog.CommandEnable, `{}`)

	var observer *LogObserver
	select {
	case err := <-errCh:
		t.Fatalf("page.ObserveLog() error = %v", err)
	case observer = <-observerCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.ObserveLog()")
	}

	transport.reads <- []byte(`{"method":"Log.entryAdded","params":{"entry":{"source":"javascript","level":"error","text":"boom","category":"cors","timestamp":123.45,"url":"https://example.com/app.js","lineNumber":7,"networkRequestId":"req-1","workerId":"worker-1"}}}`)

	select {
	case event := <-observer.Events():
		if got, want := event.Text, "boom"; got != want {
			t.Fatalf("event.Text = %q, want %q", got, want)
		}
		if got, want := event.Level, "error"; got != want {
			t.Fatalf("event.Level = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log event")
	}

	observer.Close()
	expectMethodAndReply(t, transport, 2, cdplog.CommandDisable, `{}`)

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	<-done
}

func TestPageObserveLogSharesEnable(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	firstCh := make(chan *LogObserver, 1)
	secondCh := make(chan *LogObserver, 1)
	errCh := make(chan error, 2)

	go func() {
		observer, err := page.ObserveLog(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		firstCh <- observer
	}()
	expectMethodAndReply(t, transport, 1, cdplog.CommandEnable, `{}`)

	go func() {
		observer, err := page.ObserveLog(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		secondCh <- observer
	}()

	var first, second *LogObserver
	select {
	case err := <-errCh:
		t.Fatalf("first page.ObserveLog() error = %v", err)
	case first = <-firstCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first observer")
	}

	select {
	case err := <-errCh:
		t.Fatalf("second page.ObserveLog() error = %v", err)
	case second = <-secondCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second observer")
	}

	first.Close()

	select {
	case sent := <-transport.sends:
		var msg map[string]any
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		t.Fatalf("unexpected command after first observer close: %v", msg["method"])
	case <-time.After(50 * time.Millisecond):
	}

	second.Close()
	expectMethodAndReply(t, transport, 2, cdplog.CommandDisable, `{}`)

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	<-done
}

func TestPageClearLog(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- page.ClearLog(context.Background())
	}()

	expectMethodAndReply(t, transport, 1, cdplog.CommandClear, `{}`)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("page.ClearLog() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.ClearLog()")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	<-done
}
