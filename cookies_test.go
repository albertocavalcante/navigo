package navigo

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdpnetwork "github.com/albertocavalcante/navigo/cdp/proto/network"
	cdpstorage "github.com/albertocavalcante/navigo/cdp/proto/storage"
	"github.com/albertocavalcante/navigo/launcher"
)

func TestBrowserCookies(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	browser := &Browser{
		conn:    conn,
		version: launcher.Version{Browser: "Chrome"},
		cancel:  func() {},
		done:    make(chan error, 1),
	}

	go func() {
		browser.done <- conn.Run(context.Background())
	}()

	resultCh := make(chan []Cookie, 1)
	errCh := make(chan error, 1)
	go func() {
		cookies, err := browser.Cookies(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- cookies
	}()

	expectMethodAndReply(t, transport, 1, cdpstorage.CommandGetCookies, `{"cookies":[{"name":"session","value":"abc","domain":"example.com","path":"/","expires":1714766400,"size":10,"httpOnly":true,"secure":true,"session":false,"sameSite":"Lax","priority":"Medium","sourceScheme":"Secure","sourcePort":443}]}`)

	select {
	case err := <-errCh:
		t.Fatalf("browser.Cookies() error = %v", err)
	case cookies := <-resultCh:
		if len(cookies) != 1 {
			t.Fatalf("len(cookies) = %d, want 1", len(cookies))
		}
		if got, want := cookies[0].Domain, "example.com"; got != want {
			t.Fatalf("cookie.Domain = %q, want %q", got, want)
		}
		if cookies[0].Expires == nil {
			t.Fatal("cookie.Expires = nil, want value")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser.Cookies()")
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}

func TestBrowserSetAndClearCookies(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	browser := &Browser{
		conn:    conn,
		version: launcher.Version{Browser: "Chrome"},
		cancel:  func() {},
		done:    make(chan error, 1),
	}

	go func() {
		browser.done <- conn.Run(context.Background())
	}()

	expires := time.Unix(1714766400, 0).UTC()
	errCh := make(chan error, 2)
	go func() {
		errCh <- browser.SetCookies(context.Background(), []CookieInput{{
			Name:         "session",
			Value:        "abc",
			URL:          "https://example.com",
			Domain:       "example.com",
			Path:         "/",
			Secure:       true,
			HTTPOnly:     true,
			SameSite:     "Lax",
			Priority:     "Medium",
			SourceScheme: "Secure",
			SourcePort:   443,
			Expires:      &expires,
		}})
	}()

	expectStorageSetCookies(t, transport, 1)

	go func() {
		errCh <- browser.ClearCookies(context.Background())
	}()
	expectMethodAndReply(t, transport, 2, cdpstorage.CommandClearCookies, `{}`)

	for range 2 {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("browser cookie mutation error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for browser cookie mutation")
		}
	}

	if err := browser.Close(); err != nil {
		t.Fatalf("browser.Close() error = %v", err)
	}
}

func TestPageCookies(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	page := &Page{session: conn.RootSession()}

	done := make(chan error, 1)
	go func() {
		done <- conn.Run(context.Background())
	}()

	resultCh := make(chan []Cookie, 1)
	errCh := make(chan error, 1)
	go func() {
		cookies, err := page.Cookies(context.Background(), "https://example.com", "https://api.example.com")
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- cookies
	}()

	expectNetworkGetCookies(t, transport, 1, []string{"https://example.com", "https://api.example.com"})

	select {
	case err := <-errCh:
		t.Fatalf("page.Cookies() error = %v", err)
	case cookies := <-resultCh:
		if len(cookies) != 1 {
			t.Fatalf("len(cookies) = %d, want 1", len(cookies))
		}
		if got, want := cookies[0].Name, "api_token"; got != want {
			t.Fatalf("cookie.Name = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for page.Cookies()")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
	<-done
}

func expectStorageSetCookies(t *testing.T, transport *fakeTransport, id int) {
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
		if got, want := msg["method"], cdpstorage.CommandSetCookies; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}
		params, _ := msg["params"].(map[string]any)
		rawCookies, _ := params["cookies"].([]any)
		if len(rawCookies) != 1 {
			t.Fatalf("len(params[cookies]) = %d, want 1", len(rawCookies))
		}
		cookie, _ := rawCookies[0].(map[string]any)
		if got, want := cookie["name"], "session"; got != want {
			t.Fatalf("cookie[name] = %v, want %v", got, want)
		}
		if got, want := cookie["url"], "https://example.com"; got != want {
			t.Fatalf("cookie[url] = %v, want %v", got, want)
		}
		if got, want := cookie["domain"], "example.com"; got != want {
			t.Fatalf("cookie[domain] = %v, want %v", got, want)
		}
		if got, want := cookie["expires"], float64(1714766400); got != want {
			t.Fatalf("cookie[expires] = %v, want %v", got, want)
		}
		transport.reads <- []byte(`{"id":` + strconv.Itoa(id) + `,"result":{}}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", cdpstorage.CommandSetCookies)
	}
}

func expectNetworkGetCookies(t *testing.T, transport *fakeTransport, id int, urls []string) {
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
		if got, want := msg["method"], cdpnetwork.CommandGetCookies; got != want {
			t.Fatalf("msg[method] = %v, want %v", got, want)
		}
		params, _ := msg["params"].(map[string]any)
		rawURLs, _ := params["urls"].([]any)
		if len(rawURLs) != len(urls) {
			t.Fatalf("len(params[urls]) = %d, want %d", len(rawURLs), len(urls))
		}
		for idx, want := range urls {
			if got := rawURLs[idx]; got != want {
				t.Fatalf("params[urls][%d] = %v, want %v", idx, got, want)
			}
		}
		transport.reads <- []byte(`{"id":` + strconv.Itoa(id) + `,"result":{"cookies":[{"name":"api_token","value":"secret","domain":"api.example.com","path":"/","expires":1714766400,"size":12,"httpOnly":true,"secure":true,"session":false,"sameSite":"None","priority":"High","sourceScheme":"Secure","sourcePort":443}]}}`)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", cdpnetwork.CommandGetCookies)
	}
}
