package navigo

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpemulation "github.com/albertocavalcante/navigo/cdp/proto/emulation"
)

func TestPageSetViewport(t *testing.T) {
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
		errCh <- page.SetViewport(context.Background(), Viewport{
			Width:             1280,
			Height:            720,
			DeviceScaleFactor: 2,
			Mobile:            true,
			Scale:             1.25,
		})
	}()

	expectEmulationCommand(t, transport, 1, cdpemulation.CommandSetDeviceMetricsOverride, func(params map[string]any) {
		if got, want := params["width"], float64(1280); got != want {
			t.Fatalf("params[width] = %v, want %v", got, want)
		}
		if got, want := params["height"], float64(720); got != want {
			t.Fatalf("params[height] = %v, want %v", got, want)
		}
		if got, want := params["deviceScaleFactor"], float64(2); got != want {
			t.Fatalf("params[deviceScaleFactor] = %v, want %v", got, want)
		}
		if got, want := params["mobile"], true; got != want {
			t.Fatalf("params[mobile] = %v, want %v", got, want)
		}
		if got, want := params["scale"], 1.25; got != want {
			t.Fatalf("params[scale] = %v, want %v", got, want)
		}
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("page.SetViewport() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.SetViewport()")
	}

	_ = conn.Close()
	<-done
}

func TestPageSetUserAgent(t *testing.T) {
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
		errCh <- page.SetUserAgent(
			context.Background(),
			"CustomAgent/1.0",
			WithAcceptLanguage("en-US"),
			WithPlatform("Linux"),
		)
	}()

	expectEmulationCommand(t, transport, 1, cdpemulation.CommandSetUserAgentOverride, func(params map[string]any) {
		if got, want := params["userAgent"], "CustomAgent/1.0"; got != want {
			t.Fatalf("params[userAgent] = %v, want %v", got, want)
		}
		if got, want := params["acceptLanguage"], "en-US"; got != want {
			t.Fatalf("params[acceptLanguage] = %v, want %v", got, want)
		}
		if got, want := params["platform"], "Linux"; got != want {
			t.Fatalf("params[platform] = %v, want %v", got, want)
		}
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("page.SetUserAgent() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.SetUserAgent()")
	}

	_ = conn.Close()
	<-done
}

func TestPageSetTimezoneAndGeolocationAndCPU(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	errCh := make(chan error, 3)
	go func() {
		errCh <- page.SetTimezone(context.Background(), "America/Los_Angeles")
	}()
	expectEmulationCommand(t, transport, 1, cdpemulation.CommandSetTimezoneOverride, func(params map[string]any) {
		if got, want := params["timezoneId"], "America/Los_Angeles"; got != want {
			t.Fatalf("params[timezoneId] = %v, want %v", got, want)
		}
	})

	go func() {
		errCh <- page.SetGeolocation(
			context.Background(),
			37.7749,
			-122.4194,
			WithGeolocationAccuracy(12),
			WithGeolocationHeading(90),
			WithGeolocationSpeed(3.5),
		)
	}()
	expectEmulationCommand(t, transport, 2, cdpemulation.CommandSetGeolocationOverride, func(params map[string]any) {
		if got, want := params["latitude"], 37.7749; got != want {
			t.Fatalf("params[latitude] = %v, want %v", got, want)
		}
		if got, want := params["longitude"], -122.4194; got != want {
			t.Fatalf("params[longitude] = %v, want %v", got, want)
		}
		if got, want := params["accuracy"], float64(12); got != want {
			t.Fatalf("params[accuracy] = %v, want %v", got, want)
		}
		if got, want := params["heading"], float64(90); got != want {
			t.Fatalf("params[heading] = %v, want %v", got, want)
		}
		if got, want := params["speed"], 3.5; got != want {
			t.Fatalf("params[speed] = %v, want %v", got, want)
		}
	})

	go func() {
		errCh <- page.SetCPUThrottling(context.Background(), 4)
	}()
	expectEmulationCommand(t, transport, 3, cdpemulation.CommandSetCPUThrottlingRate, func(params map[string]any) {
		if got, want := params["rate"], float64(4); got != want {
			t.Fatalf("params[rate] = %v, want %v", got, want)
		}
	})

	for range 3 {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("emulation call error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for emulation calls")
		}
	}

	_ = conn.Close()
	<-done
}

func TestPageSetViewportAndCPUValidation(t *testing.T) {
	t.Parallel()

	page := &Page{}

	if err := page.SetViewport(context.Background(), Viewport{Width: -1, Height: 10, DeviceScaleFactor: 1}); err == nil {
		t.Fatal("page.SetViewport() error = nil, want error")
	}
	if err := page.SetCPUThrottling(context.Background(), 0.5); err == nil {
		t.Fatal("page.SetCPUThrottling() error = nil, want error")
	}
}

func expectEmulationCommand(t *testing.T, transport *fakeTransport, id int, method string, check func(map[string]any)) {
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
		if got, want := msg["method"], method; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}
		params, _ := msg["params"].(map[string]any)
		check(params)
		transport.reads <- []byte(`{"id":` + strconv.Itoa(id) + `,"result":{}}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", method)
	}
}
