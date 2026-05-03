package cdp

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeTransport struct {
	reads chan []byte
	sends chan []byte
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		reads: make(chan []byte, 8),
		sends: make(chan []byte, 8),
	}
}

func (f *fakeTransport) Send(_ context.Context, data []byte) error {
	f.sends <- append([]byte(nil), data...)
	return nil
}

func (f *fakeTransport) Read(_ context.Context) ([]byte, error) {
	payload, ok := <-f.reads
	if !ok {
		return nil, io.EOF
	}

	return payload, nil
}

func (f *fakeTransport) Close() error {
	close(f.reads)
	return nil
}

func TestSessionCallRoutesBySessionID(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := NewConn(transport)
	session := conn.Session("session-1")

	runErr := make(chan error, 1)
	go func() {
		runErr <- conn.Run(context.Background())
	}()

	type result struct {
		Value string `json:"value"`
	}

	callErr := make(chan error, 1)
	var got result
	go func() {
		callErr <- session.Call(context.Background(), "Runtime.evaluate", map[string]string{"expression": "1+1"}, &got)
	}()

	select {
	case sent := <-transport.sends:
		var msg message
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if gotSessionID, want := msg.SessionID, "session-1"; gotSessionID != want {
			t.Fatalf("msg.SessionID = %q, want %q", gotSessionID, want)
		}

		responsePayload := []byte(`{"id":1,"sessionId":"session-1","result":{"value":"ok"}}`)
		transport.reads <- responsePayload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CDP send")
	}

	select {
	case err := <-callErr:
		if err != nil {
			t.Fatalf("session.Call() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session.Call")
	}

	if got.Value != "ok" {
		t.Fatalf("got.Value = %q, want %q", got.Value, "ok")
	}

	_ = conn.Close()
	<-runErr
}

func TestSessionSubscribeReceivesEvents(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := NewConn(transport)

	runErr := make(chan error, 1)
	go func() {
		runErr <- conn.Run(context.Background())
	}()

	events, cancel := conn.Session("session-2").Subscribe(1)
	defer cancel()

	transport.reads <- []byte(`{"method":"Page.loadEventFired","sessionId":"session-2","params":{"value":1}}`)

	select {
	case event := <-events:
		if got, want := event.Method, "Page.loadEventFired"; got != want {
			t.Fatalf("event.Method = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	_ = conn.Close()
	<-runErr
}

func TestSessionUnsubscribeIsSafeDuringPublish(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := NewConn(transport)
	session := conn.Session("session-3")

	for range 1000 {
		events, cancel := session.Subscribe(1)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			session.publish(Event{Method: "Runtime.consoleAPICalled"})
		}()

		go func() {
			defer wg.Done()
			cancel()
		}()

		wg.Wait()

		select {
		case <-events:
		default:
		}
	}
}
