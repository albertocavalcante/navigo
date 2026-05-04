package navigo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
)

func TestPageScreenshot(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}

	resultCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := page.Screenshot(context.Background(), WithScreenshotFormat(ScreenshotFormatJPEG), WithScreenshotQuality(80))
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- data
	}()

	expectCaptureScreenshot(t, transport, 1, "", func(msg map[string]any) {
		params := msg["params"].(map[string]any)
		if got, want := params["format"], string(cdppage.CaptureScreenshotParamsFormatJpeg); got != want {
			t.Fatalf("params[format] = %v, want %v", got, want)
		}
		if got, want := int(params["quality"].(float64)), 80; got != want {
			t.Fatalf("params[quality] = %d, want %d", got, want)
		}
	})

	transport.reads <- []byte(`{"id":1,"result":{"data":"` + base64.StdEncoding.EncodeToString([]byte("page-shot")) + `"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("page.Screenshot() error = %v", err)
	case data := <-resultCh:
		if got, want := string(data), "page-shot"; got != want {
			t.Fatalf("string(data) = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Screenshot()")
	}
}

func TestElementScreenshot(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() {
		_ = conn.Run(context.Background())
	}()

	page := &Page{session: conn.RootSession()}
	element := &Element{
		page:   page,
		nodeID: 22,
	}

	resultCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := element.Screenshot(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- data
	}()

	expectMethodAndReply(t, transport, 1, cdpdom.CommandScrollIntoViewIfNeeded, `{}`)
	expectMethodAndReply(t, transport, 2, cdpdom.CommandGetBoxModel, `{"model":{"border":[10,20,110,20,110,60,10,60],"content":[12,22,108,22,108,58,12,58],"width":100,"height":40}}`)
	expectCaptureScreenshot(t, transport, 3, "", func(msg map[string]any) {
		params := msg["params"].(map[string]any)
		clip := params["clip"].(map[string]any)
		if got, want := clip["x"], 10.0; got != want {
			t.Fatalf("clip[x] = %v, want %v", got, want)
		}
		if got, want := clip["y"], 20.0; got != want {
			t.Fatalf("clip[y] = %v, want %v", got, want)
		}
		if got, want := clip["width"], 100.0; got != want {
			t.Fatalf("clip[width] = %v, want %v", got, want)
		}
		if got, want := clip["height"], 40.0; got != want {
			t.Fatalf("clip[height] = %v, want %v", got, want)
		}
		if got, want := clip["scale"], 1.0; got != want {
			t.Fatalf("clip[scale] = %v, want %v", got, want)
		}
	})

	transport.reads <- []byte(`{"id":3,"result":{"data":"` + base64.StdEncoding.EncodeToString([]byte("element-shot")) + `"}}`)

	select {
	case err := <-errCh:
		t.Fatalf("element.Screenshot() error = %v", err)
	case data := <-resultCh:
		if got, want := string(data), "element-shot"; got != want {
			t.Fatalf("string(data) = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for element.Screenshot()")
	}
}

func TestViewportFromBoxModel(t *testing.T) {
	t.Parallel()

	clip, ok := viewportFromBoxModel(cdpdom.BoxModel{
		Border: cdpdom.Quad{10.0, 20.0, 110.0, 20.0, 110.0, 60.0, 10.0, 60.0},
	})
	if !ok {
		t.Fatal("viewportFromBoxModel() ok = false, want true")
	}
	if got, want := clip.Width, 100.0; got != want {
		t.Fatalf("clip.Width = %v, want %v", got, want)
	}
	if got, want := clip.Height, 40.0; got != want {
		t.Fatalf("clip.Height = %v, want %v", got, want)
	}
}

func expectCaptureScreenshot(t *testing.T, transport *fakeTransport, id int, sessionID string, check func(map[string]any)) {
	t.Helper()

	select {
	case sent := <-transport.sends:
		var msg map[string]any
		if err := json.Unmarshal(sent, &msg); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if got, want := int(msg["id"].(float64)), id; got != want {
			t.Fatalf("msg[id] = %d, want %d", got, want)
		}
		if got, want := msg["method"], cdppage.CommandCaptureScreenshot; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}
		if sessionID != "" {
			if got, want := msg["sessionId"], sessionID; got != want {
				t.Fatalf("msg[sessionId] = %v, want %v", got, want)
			}
		}

		check(msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Page.captureScreenshot request")
	}
}
