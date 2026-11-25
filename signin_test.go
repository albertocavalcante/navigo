package navigo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
)

func TestSignInDetectorURL(t *testing.T) {
	t.Parallel()

	d := NewSignInDetector()
	signIn := []string{
		"https://example.test/login",
		"https://example.test/Login/",
		"https://example.test/app/sign-in?next=/x",
		"https://example.test/signin",
		"https://example.test/signon/start",
		"https://example.test/auth",
		"https://example.test/a/sso/redirect",
		"https://example.test/session-timeout",
		"https://example.test/logoff",
	}
	other := []string{
		"https://example.test/",
		"https://example.test/login-history",
		"https://example.test/account/loginhelp",
		"https://example.test/authors",
		"https://example.test/docs?next=/login",
		"https://example.test/docs#/login",
		"https://example.test/blog/ssourcing",
		"not a url \x7f",
	}
	for _, u := range signIn {
		if !d.MatchURL(u) {
			t.Errorf("MatchURL(%q) = false, want true", u)
		}
	}
	for _, u := range other {
		if d.MatchURL(u) {
			t.Errorf("MatchURL(%q) = true, want false", u)
		}
	}
}

func TestSignInDetectorCustom(t *testing.T) {
	t.Parallel()

	d := NewSignInDetector(
		WithSignInPathSegments("entrar"),
		WithSignInHosts("idp.example.test"),
		WithSignInTitlePatterns(`(?i)^please sign in`),
	)
	if !d.MatchURL("https://example.test/entrar") {
		t.Error("custom segment should match")
	}
	if d.MatchURL("https://example.test/login") {
		t.Error("default segments should be replaced")
	}
	if !d.MatchURL("https://idp.example.test/anything") {
		t.Error("host should match")
	}
	if !d.MatchTitle("Please Sign In to continue") || d.MatchTitle("Dashboard") {
		t.Error("title patterns wrong")
	}
	if d.Classify("https://example.test/", "Dashboard") != nil {
		t.Error("Classify should be nil")
	}
	err := d.Classify("https://example.test/entrar", "x")
	var sie *SignInRequiredError
	if !errors.Is(err, ErrSignInRequired) || !errors.As(err, &sie) || sie.URL == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestSignInDetectorBadTitlePattern(t *testing.T) {
	t.Parallel()

	d := NewSignInDetector(WithSignInTitlePatterns(`(`))
	if d.MatchTitle("anything") {
		t.Error("invalid pattern must be ignored")
	}
}

func frameNavigatedEvent(id, parent, url string) []byte {
	p := ""
	if parent != "" {
		p = `"parentId":"` + parent + `",`
	}
	return []byte(`{"method":"Page.frameNavigated","sessionId":"s1","params":{"frame":{"id":"` + id + `",` + p + `"loaderId":"l","url":"` + url + `","domainAndRegistry":"","securityOrigin":"","mimeType":"text/html","secureContextType":"Secure","crossOriginIsolatedContextType":"NotIsolated","gatedAPIFeatures":[]}}}`)
}

func TestPageOnNavigationAndGuard(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() { _ = conn.Run(context.Background()) }()
	page := &Page{session: conn.Session("s1")}

	got := make(chan NavigationEvent, 4)
	stop := page.OnNavigation(func(e NavigationEvent) { got <- e })
	defer stop()

	transport.reads <- frameNavigatedEvent("c", "m", "https://example.test/login") // subframe: ignored
	transport.reads <- frameNavigatedEvent("m", "", "https://example.test/home")
	select {
	case e := <-got:
		if e.URL != "https://example.test/home" {
			t.Fatalf("URL = %q", e.URL)
		}
	case <-time.After(time.Second):
		t.Fatal("no navigation event")
	}

	ctx, cancelGuard := page.GuardSignIn(context.Background(), NewSignInDetector())
	defer cancelGuard()
	transport.reads <- frameNavigatedEvent("m", "", "https://example.test/sso/start")
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrSignInRequired) {
			t.Fatalf("cause = %v", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("guard context not canceled")
	}
}
