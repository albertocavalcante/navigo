package navigo

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
)

func TestSignInDetectorURL(t *testing.T) {
	t.Parallel()

	d := MustNewSignInDetector()
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
		"https://example.test/logon",
		"https://example.test/%6Cogin",
		"https://example.test/lo%67in;x=1",
		"https://example.test/login.aspx",
		"https://example.test/login;jsessionid=1",
		"https://example.test/a/Login.jsp;x=y/b",
	}
	other := []string{
		"https://example.test/",
		"https://example.test/login-history",
		"https://example.test/account/loginhelp",
		"https://example.test/authors",
		"https://example.test/docs?next=/login",
		"https://example.test/docs#/login",
		"https://example.test/blog/ssourcing",
		"https://example.test/login-history.aspx",
		"https://example.test/%6Cogin-history",
		"https://example.test/a%3Blogin",
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

	d, err := NewSignInDetector(
		WithSignInPathSegments("entrar"),
		WithSignInHosts("idp.example.test"),
		WithSignInTitlePatterns(`(?i)^please sign in`),
	)
	if err != nil {
		t.Fatal(err)
	}
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
	err = d.Classify("https://example.test/entrar", "x")
	var sie *SignInRequiredError
	if !errors.Is(err, ErrSignInRequired) || !errors.As(err, &sie) || sie.URL == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestSignInDetectorBadTitlePattern(t *testing.T) {
	t.Parallel()

	if _, err := NewSignInDetector(WithSignInTitlePatterns(`(`)); err == nil {
		t.Fatal("invalid pattern: want error")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustNewSignInDetector should panic on invalid pattern")
		}
	}()
	MustNewSignInDetector(WithSignInTitlePatterns(`(`))
}

func TestSignInDetectorHostTrailingDot(t *testing.T) {
	t.Parallel()

	d := MustNewSignInDetector(WithSignInHosts("idp.example.test."))
	if !d.MatchURL("https://IDP.example.test./x") || !d.MatchURL("https://idp.example.test/x") {
		t.Error("trailing dot should be ignored on both sides")
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

	evalReply := func(id int, value string) {
		t.Helper()
		select {
		case <-transport.sends:
			transport.reads <- []byte(`{"id":` + strconv.Itoa(id) + `,"sessionId":"s1","result":{"result":{"type":"string","value":"` + value + `"}}}`)
		case <-time.After(time.Second):
			t.Fatal("no evaluate request")
		}
	}

	type guardResult struct {
		ctx    context.Context
		cancel context.CancelFunc
		err    error
	}
	resCh := make(chan guardResult, 1)
	go func() {
		c, cn, err := page.GuardSignIn(context.Background(), MustNewSignInDetector())
		resCh <- guardResult{c, cn, err}
	}()
	evalReply(1, "https://example.test/home")
	evalReply(2, "Home")
	res := <-resCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	defer res.cancel()
	transport.reads <- frameNavigatedEvent("m", "", "https://example.test/sso/start")
	select {
	case <-res.ctx.Done():
		if !errors.Is(context.Cause(res.ctx), ErrSignInRequired) {
			t.Fatalf("cause = %v", context.Cause(res.ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("guard context not canceled")
	}
}

func TestGuardSignInAlreadyOnSignInPage(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	conn := cdp.NewConn(transport)
	go func() { _ = conn.Run(context.Background()) }()
	page := &Page{session: conn.Session("s1")}

	type guardResult struct {
		ctx    context.Context
		cancel context.CancelFunc
		err    error
	}
	resCh := make(chan guardResult, 1)
	go func() {
		c, cn, err := page.GuardSignIn(context.Background(), MustNewSignInDetector())
		resCh <- guardResult{c, cn, err}
	}()
	for id, v := range []string{"https://example.test/login", "Sign in"} {
		select {
		case <-transport.sends:
			transport.reads <- []byte(`{"id":` + strconv.Itoa(id+1) + `,"sessionId":"s1","result":{"result":{"type":"string","value":"` + v + `"}}}`)
		case <-time.After(time.Second):
			t.Fatal("no evaluate request")
		}
	}
	res := <-resCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	defer res.cancel()
	if !errors.Is(context.Cause(res.ctx), ErrSignInRequired) {
		t.Fatalf("cause = %v, want ErrSignInRequired", context.Cause(res.ctx))
	}
}

func TestGuardSignInNilDetector(t *testing.T) {
	t.Parallel()

	page := &Page{}
	if _, _, err := page.GuardSignIn(context.Background(), nil); err == nil {
		t.Fatal("nil detector: want error")
	}
	if err := page.CheckSignIn(context.Background(), nil); err == nil {
		t.Fatal("nil detector: want error from CheckSignIn")
	}
}
