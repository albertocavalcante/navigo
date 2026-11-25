package navigo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/albertocavalcante/navigo/cdp"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
)

// ErrSignInRequired is matched with errors.Is when navigation lands on a
// sign-in page. Callers should stop and ask for a fresh session instead of
// waiting for content that will never appear.
var ErrSignInRequired = errors.New("sign-in required")

// SignInRequiredError carries what triggered the detection.
type SignInRequiredError struct {
	URL    string
	Title  string
	Reason string
}

func (e *SignInRequiredError) Error() string {
	return fmt.Sprintf("sign-in required (%s): %s", e.Reason, e.URL)
}

func (e *SignInRequiredError) Unwrap() error { return ErrSignInRequired }

var defaultSignInSegments = []string{"login", "signin", "sign-in", "signon", "auth", "sso", "session-timeout", "logoff"}

// SignInDetector classifies a URL or title as a sign-in page. It is immutable
// after construction and safe for concurrent use.
type SignInDetector struct {
	segments map[string]struct{}
	hosts    map[string]struct{}
	titles   []*regexp.Regexp
}

// SignInOption customizes [NewSignInDetector].
type SignInOption func(*SignInDetector)

// WithSignInPathSegments replaces the default path segments. A URL matches
// when one whole path segment equals an entry, case-insensitively, so
// "login-history" never matches "login".
func WithSignInPathSegments(segments ...string) SignInOption {
	return func(d *SignInDetector) {
		d.segments = lowerSet(segments)
	}
}

// WithSignInHosts adds exact host names (without port) that always count as
// sign-in pages, such as an identity provider.
func WithSignInHosts(hosts ...string) SignInOption {
	return func(d *SignInDetector) {
		for h := range lowerSet(hosts) {
			d.hosts[h] = struct{}{}
		}
	}
}

// WithSignInTitlePatterns adds regular expressions tested against page titles.
// Patterns that fail to compile are ignored.
func WithSignInTitlePatterns(patterns ...string) SignInOption {
	return func(d *SignInDetector) {
		for _, p := range patterns {
			if re, err := regexp.Compile(p); err == nil {
				d.titles = append(d.titles, re)
			}
		}
	}
}

func lowerSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			set[v] = struct{}{}
		}
	}

	return set
}

// NewSignInDetector builds a detector. Without options it uses generic path
// segments: login, signin, sign-in, signon, auth, sso, session-timeout, logoff.
func NewSignInDetector(opts ...SignInOption) *SignInDetector {
	d := &SignInDetector{segments: lowerSet(defaultSignInSegments), hosts: map[string]struct{}{}}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}

	return d
}

// MatchURL reports whether rawURL's host or a whole path segment matches.
// Query strings and fragments are ignored.
func (d *SignInDetector) MatchURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if _, ok := d.hosts[strings.ToLower(u.Hostname())]; ok && u.Hostname() != "" {
		return true
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if _, ok := d.segments[strings.ToLower(seg)]; ok {
			return true
		}
	}

	return false
}

// MatchTitle reports whether title matches a configured title pattern.
func (d *SignInDetector) MatchTitle(title string) bool {
	for _, re := range d.titles {
		if re.MatchString(title) {
			return true
		}
	}

	return false
}

// Classify returns a *SignInRequiredError (matching ErrSignInRequired) when
// the URL or title looks like a sign-in page, and nil otherwise.
func (d *SignInDetector) Classify(rawURL, title string) error {
	switch {
	case d.MatchURL(rawURL):
		return &SignInRequiredError{URL: rawURL, Title: title, Reason: "url"}
	case title != "" && d.MatchTitle(title):
		return &SignInRequiredError{URL: rawURL, Title: title, Reason: "title"}
	}

	return nil
}

// NavigationEvent is a main-frame navigation.
type NavigationEvent struct {
	URL          string
	SameDocument bool
}

// OnNavigation calls handler for every main-frame navigation, including
// same-document ones, from a separate goroutine. The returned function stops
// delivery and is safe to call more than once.
func (p *Page) OnNavigation(handler func(NavigationEvent)) (stop func()) {
	events, cancel := p.session.Subscribe(32)
	go func() {
		for event := range events {
			if nav, ok := p.decodeNavigation(event); ok {
				handler(nav)
			}
		}
	}()

	return cancel
}

func (p *Page) decodeNavigation(event cdp.Event) (NavigationEvent, bool) {
	switch event.Method {
	case cdppage.EventFrameNavigated:
		decoded, err := cdppage.DecodeFrameNavigated(event.Params)
		if err != nil || decoded.Frame.ParentID != "" {
			return NavigationEvent{}, false
		}

		return NavigationEvent{URL: decoded.Frame.URL + decoded.Frame.URLFragment}, true
	case cdppage.EventNavigatedWithinDocument:
		decoded, err := cdppage.DecodeNavigatedWithinDocument(event.Params)
		if err != nil {
			return NavigationEvent{}, false
		}
		p.mu.Lock()
		main := p.mainFrameID
		p.mu.Unlock()
		if main != "" && string(decoded.FrameID) != main {
			return NavigationEvent{}, false
		}

		return NavigationEvent{URL: decoded.URL, SameDocument: true}, true
	}

	return NavigationEvent{}, false
}

// GuardSignIn returns a context derived from ctx that is canceled with cause
// ErrSignInRequired (see [context.Cause]) as soon as the page navigates to a
// URL the detector classifies as sign-in. Pass it to waits so they stop
// instead of hanging. Call the returned function to release the guard.
func (p *Page) GuardSignIn(ctx context.Context, detector *SignInDetector) (context.Context, context.CancelFunc) {
	guarded, cancelCause := context.WithCancelCause(ctx)
	stop := p.OnNavigation(func(e NavigationEvent) {
		if err := detector.Classify(e.URL, ""); err != nil {
			cancelCause(err)
		}
	})

	return guarded, func() {
		stop()
		cancelCause(context.Canceled)
	}
}

// CheckSignIn inspects the page's current URL and title once.
func (p *Page) CheckSignIn(ctx context.Context, detector *SignInDetector) error {
	href, err := p.Evaluate(ctx, "location.href")
	if err != nil {
		return fmt.Errorf("check sign-in: %w", err)
	}
	title, err := p.Evaluate(ctx, "document.title")
	if err != nil {
		return fmt.Errorf("check sign-in: %w", err)
	}
	h, _ := href.(string)
	t, _ := title.(string)

	return detector.Classify(h, t)
}
