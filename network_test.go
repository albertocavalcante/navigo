package navigo

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpnetwork "github.com/albertocavalcante/navigo/cdp/proto/network"
)

func TestPageObserveNetwork(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}

	observerCh := make(chan *NetworkObserver, 1)
	errCh := make(chan error, 1)
	go func() {
		observer, err := page.ObserveNetwork(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		observerCh <- observer
	}()

	expectMethodAndReply(t, transport, 1, cdpnetwork.CommandEnable, `{}`)

	var observer *NetworkObserver
	select {
	case err := <-errCh:
		t.Fatalf("page.ObserveNetwork() error = %v", err)
	case observer = <-observerCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for network observer")
	}

	transport.reads <- []byte(`{"method":"Network.requestWillBeSent","params":{"requestId":"req-1","loaderId":"loader-1","documentURL":"https://example.com","request":{"url":"https://example.com/api","method":"GET","headers":{},"initialPriority":"High","referrerPolicy":"strict-origin-when-cross-origin"},"timestamp":123.45,"wallTime":123.45,"initiator":{"type":"script"},"redirectHasExtraInfo":false,"type":"Fetch","frameId":"frame-1"}}`)
	transport.reads <- []byte(`{"method":"Network.responseReceived","params":{"requestId":"req-1","loaderId":"loader-1","timestamp":124.45,"type":"Fetch","response":{"url":"https://example.com/api","status":200,"statusText":"OK","headers":{},"mimeType":"application/json","connectionReused":true,"connectionID":1,"encodedDataLength":12,"securityState":"secure","headersText":"","protocol":"h2","alternateProtocolUsage":"unspecifiedReason"},"hasExtraInfo":false,"frameId":"frame-1"}}`)
	transport.reads <- []byte(`{"method":"Network.loadingFinished","params":{"requestId":"req-1","timestamp":125.45,"encodedDataLength":12}}`)
	transport.reads <- []byte(`{"method":"Network.loadingFailed","params":{"requestId":"req-2","timestamp":126.45,"type":"Fetch","errorText":"net::ERR_ABORTED","canceled":true}}`)

	wantKinds := []NetworkEventKind{
		NetworkEventRequestStarted,
		NetworkEventResponseReceived,
		NetworkEventLoadingFinished,
		NetworkEventLoadingFailed,
	}

	for _, want := range wantKinds {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("observer.Events() closed unexpectedly")
			}
			if got := event.Kind; got != want {
				t.Fatalf("event.Kind = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q event", want)
		}
	}

	observer.Close()
}

func TestPageResponseBody(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}

	bodyCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		body, err := page.ResponseBody(context.Background(), "req-1")
		if err != nil {
			errCh <- err
			return
		}
		bodyCh <- body
	}()

	expectMethodAndReply(t, transport, 1, cdpnetwork.CommandGetResponseBody, `{"body":"`+base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`))+`","base64Encoded":true}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.ResponseBody() error = %v", err)
	case body := <-bodyCh:
		if got, want := string(body), `{"ok":true}`; got != want {
			t.Fatalf("string(body) = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.ResponseBody()")
	}
}
