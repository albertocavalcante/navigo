package navigo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
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

var defaultSignInSegments = []string{"login", "signin", "sign-in", "signon", "auth", "sso", "logon", "session-timeout", "logoff"}

// SignInDetector classifies a URL or title as a sign-in page. It is immutable
// after construction and safe for concurrent use.
type SignInDetector struct {
	segments map[string]struct{}
	hosts    map[string]struct{}
	titles   []*regexp.Regexp
	rawTitle []string
}

// SignInOption customizes [NewSignInDetector].
type SignInOption func(*SignInDetector)

// Segment matching drops ";params" and a file extension from each segment, so
// "/login.aspx" and "/login;jsessionid=1" match "login". A generic "auth"
// segment also matches paths like "/docs/auth"; narrow the list with
// [WithSignInPathSegments] when that is a problem.

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
// An invalid pattern makes [NewSignInDetector] return an error. Titles are
// checked by [SignInDetector.Classify] and [Page.CheckSignIn]; navigation
// events carry no title, so [Page.GuardSignIn] only checks titles once, at start.
func WithSignInTitlePatterns(patterns ...string) SignInOption {
	return func(d *SignInDetector) {
		d.rawTitle = append(d.rawTitle, patterns...)
	}
}

func lowerSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(v)), "."); v != "" {
			set[v] = struct{}{}
		}
	}

	return set
}

// NewSignInDetector builds a detector. Without options it uses generic path
// segments: login, logon, signin, sign-in, signon, auth, sso, session-timeout,
// logoff. It fails when a title pattern does not compile.
func NewSignInDetector(opts ...SignInOption) (*SignInDetector, error) {
	d := &SignInDetector{segments: lowerSet(defaultSignInSegments), hosts: map[string]struct{}{}}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	for _, p := range d.rawTitle {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("sign-in title pattern %q: %w", p, err)
		}
		d.titles = append(d.titles, re)
	}

	return d, nil
}

// MustNewSignInDetector is [NewSignInDetector] that panics on an invalid
// option, for package-level detectors built from constant patterns.
func MustNewSignInDetector(opts ...SignInOption) *SignInDetector {
	d, err := NewSignInDetector(opts...)
	if err != nil {
		panic(err)
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
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if _, ok := d.hosts[host]; ok && host != "" {
		return true
	}
	// Use the raw path: ";params" must be cut before escapes are decoded.
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		seg, _, _ = strings.Cut(seg, ";")
		if unescaped, err := url.PathUnescape(seg); err == nil {
			seg = unescaped
		}
		seg = strings.ToLower(seg)
		if _, ok := d.segments[seg]; ok {
			return true
		}
		if ext := filepath.Ext(seg); ext != "" {
			if _, ok := d.segments[strings.TrimSuffix(seg, ext)]; ok {
				return true
			}
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
// ErrSignInRequired (see [context.Cause]) when the page is already on, or
// navigates to, a sign-in page. Pass it to waits so they stop instead of
// hanging. Call the returned function to release the guard. Titles are only
// checked for the page's current state; navigation events carry no title.
func (p *Page) GuardSignIn(ctx context.Context, detector *SignInDetector) (context.Context, context.CancelFunc, error) {
	if detector == nil {
		return nil, nil, errors.New("guard sign-in: nil detector")
	}

	guarded, cancelCause := context.WithCancelCause(ctx)
	// Subscribe first so a navigation during the initial check is not missed.
	stop := p.OnNavigation(func(e NavigationEvent) {
		if err := detector.Classify(e.URL, ""); err != nil {
			cancelCause(err)
		}
	})
	release := func() {
		stop()
		cancelCause(context.Canceled)
	}

	if err := p.CheckSignIn(ctx, detector); err != nil {
		if errors.Is(err, ErrSignInRequired) {
			cancelCause(err)
			return guarded, release, nil
		}
		release()

		return nil, nil, fmt.Errorf("guard sign-in: %w", err)
	}

	return guarded, release, nil
}

// CheckSignIn inspects the page's current URL and title once.
func (p *Page) CheckSignIn(ctx context.Context, detector *SignInDetector) error {
	if detector == nil {
		return errors.New("check sign-in: nil detector")
	}
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
