package navigo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpfetch "github.com/albertocavalcante/navigo/cdp/proto/fetch"
)

func TestPageRouteContinuesByDefault(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	cancelCh := make(chan func(), 1)
	errCh := make(chan error, 1)
	go func() {
		cancel, err := page.Route(context.Background(), "*example.com/api/*", func(_ context.Context, _ *InterceptedRequest) {})
		if err != nil {
			errCh <- err
			return
		}
		cancelCh <- cancel
	}()

	expectFetchEnable(t, transport, 1, "*example.com/api/*", cdpfetch.RequestStageRequest)

	var cancel func()
	select {
	case err := <-errCh:
		t.Fatalf("page.Route() error = %v", err)
	case cancel = <-cancelCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Route()")
	}

	transport.reads <- []byte(`{"method":"Fetch.requestPaused","params":{"requestId":"fetch-1","request":{"url":"https://example.com/api/user","method":"GET","headers":{},"initialPriority":"High","referrerPolicy":"strict-origin-when-cross-origin"},"frameId":"frame-1","resourceType":"XHR"}}`)
	expectFetchContinue(t, transport, 2, "fetch-1", nil)

	cancel()
	expectFetchDisable(t, transport, 3)

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	if err := <-done; err != nil && err.Error() != "EOF" {
		t.Fatalf("conn.Run() error = %v", err)
	}
}

func TestPageRouteFulfillAndResponseBody(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	bodyCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	cancelCh := make(chan func(), 1)
	go func() {
		cancel, err := page.Route(
			context.Background(),
			"*example.com/api/*",
			func(ctx context.Context, req *InterceptedRequest) {
				body, err := req.ResponseBody(ctx)
				if err != nil {
					errCh <- err
					return
				}
				bodyCh <- body
				if err := req.Fulfill(ctx, 201, []byte(`{"ok":true}`), WithFulfillHeader("content-type", "application/json")); err != nil {
					errCh <- err
				}
			},
			WithRouteStage(InterceptStageResponse),
		)
		if err != nil {
			errCh <- err
			return
		}
		cancelCh <- cancel
	}()

	expectFetchEnable(t, transport, 1, "*example.com/api/*", cdpfetch.RequestStageResponse)

	select {
	case err := <-errCh:
		t.Fatalf("page.Route() error = %v", err)
	case <-cancelCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response-stage route registration")
	}

	transport.reads <- []byte(`{"method":"Fetch.requestPaused","params":{"requestId":"fetch-2","request":{"url":"https://example.com/api/user","method":"GET","headers":{},"initialPriority":"High","referrerPolicy":"strict-origin-when-cross-origin"},"frameId":"frame-1","resourceType":"XHR","responseStatusCode":200,"responseHeaders":[{"name":"content-type","value":"application/json"}]}}`)
	expectMethodAndReply(t, transport, 2, cdpfetch.CommandGetResponseBody, `{"body":"`+base64.StdEncoding.EncodeToString([]byte(`{"from":"origin"}`))+`","base64Encoded":true}`)
	expectFetchFulfill(t, transport, 3, "fetch-2", 201, []byte(`{"ok":true}`), []Header{{Name: "content-type", Value: "application/json"}})

	select {
	case err := <-errCh:
		t.Fatalf("route handler error = %v", err)
	case body := <-bodyCh:
		if got, want := string(body), `{"from":"origin"}`; got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for route handler body")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	if err := <-done; err != nil && err.Error() != "EOF" {
		t.Fatalf("conn.Run() error = %v", err)
	}
}

func expectFetchEnable(t *testing.T, transport *fakeTransport, id int, pattern string, stage cdpfetch.RequestStage) {
	t.Helper()

	select {
	case sent := <-transport.sends:
		var msg struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Patterns []struct {
					URLPattern   string `json:"urlPattern"`
					RequestStage string `json:"requestStage"`
				} `json:"patterns"`
			} `json:"params"`
		}
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if got, want := msg.ID, id; got != want {
			t.Fatalf("msg.ID = %d, want %d", got, want)
		}
		if got, want := msg.Method, cdpfetch.CommandEnable; got != want {
			t.Fatalf("msg.Method = %q, want %q", got, want)
		}
		if len(msg.Params.Patterns) != 1 {
			t.Fatalf("len(msg.Params.Patterns) = %d, want 1", len(msg.Params.Patterns))
		}
		if got, want := msg.Params.Patterns[0].URLPattern, pattern; got != want {
			t.Fatalf("pattern = %q, want %q", got, want)
		}
		if got, want := msg.Params.Patterns[0].RequestStage, string(stage); got != want {
			t.Fatalf("stage = %q, want %q", got, want)
		}
		transport.reads <- []byte(`{"id":` + jsonNumber(id) + `,"result":{}}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", cdpfetch.CommandEnable)
	}
}

func expectFetchDisable(t *testing.T, transport *fakeTransport, id int) {
	t.Helper()
	expectMethodAndReply(t, transport, id, cdpfetch.CommandDisable, `{}`)
}

func expectFetchContinue(t *testing.T, transport *fakeTransport, id int, requestID string, check func(map[string]any)) {
	t.Helper()

	select {
	case sent := <-transport.sends:
		var msg map[string]any
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if got, want := msg["id"], float64(id); got != want {
			t.Fatalf("msg[id] = %v, want %v", got, want)
		}
		if got, want := msg["method"], cdpfetch.CommandContinueRequest; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}
		params, _ := msg["params"].(map[string]any)
		if got, want := params["requestId"], requestID; got != want {
			t.Fatalf("params[requestId] = %v, want %v", got, want)
		}
		if check != nil {
			check(params)
		}
		transport.reads <- []byte(`{"id":` + jsonNumber(id) + `,"result":{}}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", cdpfetch.CommandContinueRequest)
	}
}

func expectFetchFulfill(t *testing.T, transport *fakeTransport, id int, requestID string, status int, body []byte, headers []Header) {
	t.Helper()

	select {
	case sent := <-transport.sends:
		var msg map[string]any
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if got, want := msg["id"], float64(id); got != want {
			t.Fatalf("msg[id] = %v, want %v", got, want)
		}
		if got, want := msg["method"], cdpfetch.CommandFulfillRequest; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}
		params, _ := msg["params"].(map[string]any)
		if got, want := params["requestId"], requestID; got != want {
			t.Fatalf("params[requestId] = %v, want %v", got, want)
		}
		if got, want := params["responseCode"], float64(status); got != want {
			t.Fatalf("params[responseCode] = %v, want %v", got, want)
		}
		if got, want := params["body"], base64.StdEncoding.EncodeToString(body); got != want {
			t.Fatalf("params[body] = %v, want %v", got, want)
		}
		rawHeaders, _ := params["responseHeaders"].([]any)
		if len(rawHeaders) != len(headers) {
			t.Fatalf("len(params[responseHeaders]) = %d, want %d", len(rawHeaders), len(headers))
		}
		for idx, header := range headers {
			rawHeader, _ := rawHeaders[idx].(map[string]any)
			if got, want := rawHeader["name"], header.Name; got != want {
				t.Fatalf("header[%d].name = %v, want %v", idx, got, want)
			}
			if got, want := rawHeader["value"], header.Value; got != want {
				t.Fatalf("header[%d].value = %v, want %v", idx, got, want)
			}
		}
		transport.reads <- []byte(`{"id":` + jsonNumber(id) + `,"result":{}}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", cdpfetch.CommandFulfillRequest)
	}
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}
