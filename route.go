package navigo

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	cdpfetch "github.com/albertocavalcante/navigo/cdp/proto/fetch"
)

var errInterceptedRequestResolved = errors.New("intercepted request already resolved")

type RouteHandler func(context.Context, *InterceptedRequest)

type RouteOption func(*routeConfig)

type InterceptStage string

const (
	InterceptStageRequest  InterceptStage = "request"
	InterceptStageResponse InterceptStage = "response"
)

type routeConfig struct {
	stage InterceptStage
}

type routeEntry struct {
	pattern string
	stage   InterceptStage
	handler RouteHandler
}

type Header struct {
	Name  string
	Value string
}

type ContinueOption func(*continueConfig)

type continueConfig struct {
	url               string
	method            string
	postData          []byte
	headers           []Header
	interceptResponse bool
}

type FulfillOption func(*fulfillConfig)

type fulfillConfig struct {
	headers        []Header
	responsePhrase string
}

type InterceptedRequest struct {
	page    *Page
	request cdpfetch.RequestPaused
	stage   InterceptStage

	mu       sync.Mutex
	resolved bool
	cancel   context.CancelFunc
}

func defaultRouteConfig() routeConfig {
	return routeConfig{stage: InterceptStageRequest}
}

func WithRouteStage(stage InterceptStage) RouteOption {
	return func(config *routeConfig) {
		config.stage = stage
	}
}

func WithContinueURL(url string) ContinueOption {
	return func(config *continueConfig) {
		config.url = url
	}
}

func WithContinueMethod(method string) ContinueOption {
	return func(config *continueConfig) {
		config.method = method
	}
}

func WithContinuePostData(postData []byte) ContinueOption {
	return func(config *continueConfig) {
		config.postData = append([]byte(nil), postData...)
	}
}

func WithContinueHeaders(headers []Header) ContinueOption {
	return func(config *continueConfig) {
		config.headers = append([]Header(nil), headers...)
	}
}

func WithContinueHeader(name, value string) ContinueOption {
	return func(config *continueConfig) {
		config.headers = append(config.headers, Header{Name: name, Value: value})
	}
}

func WithInterceptResponse() ContinueOption {
	return func(config *continueConfig) {
		config.interceptResponse = true
	}
}

func WithFulfillHeaders(headers []Header) FulfillOption {
	return func(config *fulfillConfig) {
		config.headers = append([]Header(nil), headers...)
	}
}

func WithFulfillHeader(name, value string) FulfillOption {
	return func(config *fulfillConfig) {
		config.headers = append(config.headers, Header{Name: name, Value: value})
	}
}

func WithResponsePhrase(phrase string) FulfillOption {
	return func(config *fulfillConfig) {
		config.responsePhrase = phrase
	}
}

func (p *Page) Route(ctx context.Context, pattern string, handler RouteHandler, opts ...RouteOption) (func(), error) {
	if handler == nil {
		return nil, errors.New("route request: nil handler")
	}

	config := defaultRouteConfig()
	for _, opt := range opts {
		opt(&config)
	}
	if config.stage != InterceptStageRequest && config.stage != InterceptStageResponse {
		return nil, fmt.Errorf("route request: unsupported intercept stage %q", config.stage)
	}
	if pattern == "" {
		pattern = "*"
	}

	entry := &routeEntry{
		pattern: pattern,
		stage:   config.stage,
		handler: handler,
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("route request: page closed")
	}
	id := p.nextRouteID
	p.nextRouteID++
	if p.routes == nil {
		p.routes = make(map[int]*routeEntry)
	}
	p.routes[id] = entry
	p.mu.Unlock()

	if err := p.ensureRouteLoop(); err != nil {
		p.removeRoute(id)
		return nil, err
	}
	if err := p.syncRoutes(ctx); err != nil {
		p.removeRoute(id)
		_ = p.stopRoutes(context.Background())
		return nil, err
	}

	cancel := func() {
		p.removeRoute(id)

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()

		if p.routeCount() == 0 {
			_ = p.stopRoutes(stopCtx)
			return
		}

		_ = p.syncRoutes(stopCtx)
	}

	return cancel, nil
}

func (p *Page) ensureRouteLoop() error {
	p.mu.Lock()
	if p.routeDone != nil {
		p.mu.Unlock()
		return nil
	}
	p.routeDone = make(chan struct{})
	done := p.routeDone
	p.mu.Unlock()

	events, cancel := p.session.Subscribe(32)
	p.mu.Lock()
	p.routeCancel = cancel
	p.mu.Unlock()

	go func() {
		defer close(done)
		defer cancel()

		routeCtx, routeCancel := context.WithCancel(context.Background())
		defer routeCancel()

		for event := range events {
			if event.Method != cdpfetch.EventRequestPaused {
				continue
			}

			paused, err := cdpfetch.DecodeRequestPaused(event.Params)
			if err != nil {
				continue
			}

			entry := p.matchRoute(*paused)
			if entry == nil {
				req := newInterceptedRequest(p, *paused)
				_ = req.continueUnresolved(routeCtx)
				continue
			}

			p.dispatchInterceptedRequest(routeCtx, entry, *paused)
		}
	}()

	return nil
}

func (p *Page) stopRoutes(ctx context.Context) error {
	p.mu.Lock()
	cancel := p.routeCancel
	done := p.routeDone
	p.routeCancel = nil
	p.routeDone = nil
	clear(p.routes)
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if cancel == nil && done == nil {
		return nil
	}

	if p.session == nil {
		return nil
	}
	if err := cdpfetch.Disable().Do(ctx, p.session); err != nil {
		return fmt.Errorf("route request: disable fetch domain: %w", err)
	}

	return nil
}

func (p *Page) syncRoutes(ctx context.Context) error {
	p.mu.Lock()
	patterns := p.fetchPatternsLocked()
	p.mu.Unlock()

	if len(patterns) == 0 {
		if err := cdpfetch.Disable().Do(ctx, p.session); err != nil {
			return fmt.Errorf("route request: disable fetch domain: %w", err)
		}
		return nil
	}

	params := cdpfetch.Enable().WithPatterns(patterns)
	if err := params.Do(ctx, p.session); err != nil {
		return fmt.Errorf("route request: enable fetch routes: %w", err)
	}

	return nil
}

func (p *Page) fetchPatternsLocked() []cdpfetch.RequestPattern {
	if len(p.routes) == 0 {
		return nil
	}

	patterns := make([]cdpfetch.RequestPattern, 0, len(p.routes))
	for _, route := range p.routes {
		patterns = append(patterns, cdpfetch.RequestPattern{
			URLPattern:   route.pattern,
			RequestStage: fetchRequestStage(route.stage),
		})
	}

	return patterns
}

func (p *Page) routeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.routes)
}

func (p *Page) removeRoute(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.routes, id)
}

func (p *Page) matchRoute(paused cdpfetch.RequestPaused) *routeEntry {
	stage := interceptedRequestStage(paused)

	p.mu.Lock()
	defer p.mu.Unlock()

	var (
		bestID int
		best   *routeEntry
	)
	for id, route := range p.routes {
		if route.stage != stage {
			continue
		}
		if !globMatch(route.pattern, paused.Request.URL) {
			continue
		}
		if best == nil || id < bestID {
			bestID = id
			best = route
		}
	}

	return best
}

func (p *Page) dispatchInterceptedRequest(parent context.Context, entry *routeEntry, paused cdpfetch.RequestPaused) {
	reqCtx, cancel := context.WithCancel(parent)
	req := newInterceptedRequest(p, paused)
	req.cancel = cancel

	go func() {
		defer cancel()
		defer func() {
			_ = req.continueUnresolved(parent)
		}()
		defer func() {
			_ = recover()
		}()

		entry.handler(reqCtx, req)
	}()
}

func newInterceptedRequest(page *Page, paused cdpfetch.RequestPaused) *InterceptedRequest {
	return &InterceptedRequest{
		page:    page,
		request: paused,
		stage:   interceptedRequestStage(paused),
	}
}

func (r *InterceptedRequest) URL() string {
	return r.request.Request.URL
}

func (r *InterceptedRequest) Method() string {
	return r.request.Request.Method
}

func (r *InterceptedRequest) ResourceType() string {
	return string(r.request.ResourceType)
}

func (r *InterceptedRequest) FrameID() string {
	return string(r.request.FrameID)
}

func (r *InterceptedRequest) NetworkRequestID() string {
	return string(r.request.NetworkID)
}

func (r *InterceptedRequest) Stage() InterceptStage {
	return r.stage
}

func (r *InterceptedRequest) Continue(ctx context.Context, opts ...ContinueOption) error {
	config := continueConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	return r.resolve(ctx, func(callCtx context.Context) error {
		params := cdpfetch.ContinueRequest(r.request.RequestID)
		if config.url != "" {
			params = params.WithURL(config.url)
		}
		if config.method != "" {
			params = params.WithMethod(config.method)
		}
		if len(config.postData) > 0 {
			params = params.WithPostData(base64.StdEncoding.EncodeToString(config.postData))
		}
		if len(config.headers) > 0 {
			params = params.WithHeaders(fetchHeaders(config.headers))
		}
		if config.interceptResponse {
			params = params.WithInterceptResponse(true)
		}

		if err := params.Do(callCtx, r.page.session); err != nil {
			return fmt.Errorf("continue intercepted request: %w", err)
		}
		return nil
	})
}

func (r *InterceptedRequest) Abort(ctx context.Context, reason cdpfetch.ErrorReason) error {
	return r.resolve(ctx, func(callCtx context.Context) error {
		if err := cdpfetch.FailRequest(r.request.RequestID, reason).Do(callCtx, r.page.session); err != nil {
			return fmt.Errorf("abort intercepted request: %w", err)
		}
		return nil
	})
}

func (r *InterceptedRequest) Fulfill(ctx context.Context, status int, body []byte, opts ...FulfillOption) error {
	config := fulfillConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	return r.resolve(ctx, func(callCtx context.Context) error {
		params := cdpfetch.FulfillRequest(r.request.RequestID, status)
		if len(config.headers) > 0 {
			params = params.WithResponseHeaders(fetchHeaders(config.headers))
		}
		if len(body) > 0 {
			params = params.WithBody(base64.StdEncoding.EncodeToString(body))
		}
		if config.responsePhrase != "" {
			params = params.WithResponsePhrase(config.responsePhrase)
		}
		if err := params.Do(callCtx, r.page.session); err != nil {
			return fmt.Errorf("fulfill intercepted request: %w", err)
		}
		return nil
	})
}

func (r *InterceptedRequest) ResponseBody(ctx context.Context) ([]byte, error) {
	if r.stage != InterceptStageResponse {
		return nil, errors.New("get intercepted response body: request is not paused at response stage")
	}

	result, err := cdpfetch.GetResponseBody(r.request.RequestID).Do(ctx, r.page.session)
	if err != nil {
		return nil, fmt.Errorf("get intercepted response body: %w", err)
	}
	if result.Base64Encoded {
		data, err := base64.StdEncoding.DecodeString(result.Body)
		if err != nil {
			return nil, fmt.Errorf("get intercepted response body: decode body: %w", err)
		}
		return data, nil
	}

	return []byte(result.Body), nil
}

func (r *InterceptedRequest) continueUnresolved(ctx context.Context) error {
	return r.tryResolve(ctx, func(callCtx context.Context) error {
		return cdpfetch.ContinueRequest(r.request.RequestID).Do(callCtx, r.page.session)
	})
}

func (r *InterceptedRequest) resolve(ctx context.Context, call func(context.Context) error) error {
	err := r.tryResolve(ctx, call)
	if errors.Is(err, errInterceptedRequestResolved) {
		return err
	}
	return err
}

func (r *InterceptedRequest) tryResolve(ctx context.Context, call func(context.Context) error) error {
	r.mu.Lock()
	if r.resolved {
		r.mu.Unlock()
		return errInterceptedRequestResolved
	}
	r.resolved = true
	cancel := r.cancel
	r.mu.Unlock()

	if err := call(ctx); err != nil {
		r.mu.Lock()
		r.resolved = false
		r.mu.Unlock()
		return err
	}
	if cancel != nil {
		cancel()
	}

	return nil
}

func fetchHeaders(headers []Header) []cdpfetch.HeaderEntry {
	result := make([]cdpfetch.HeaderEntry, 0, len(headers))
	for _, header := range headers {
		result = append(result, cdpfetch.HeaderEntry{Name: header.Name, Value: header.Value})
	}
	return result
}

func fetchRequestStage(stage InterceptStage) cdpfetch.RequestStage {
	if stage == InterceptStageResponse {
		return cdpfetch.RequestStageResponse
	}
	return cdpfetch.RequestStageRequest
}

func interceptedRequestStage(paused cdpfetch.RequestPaused) InterceptStage {
	if paused.ResponseStatusCode != 0 || paused.ResponseErrorReason != "" {
		return InterceptStageResponse
	}
	return InterceptStageRequest
}

func globMatch(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	valueRunes := []rune(value)
	dp := make([]bool, len(valueRunes)+1)
	dp[0] = true

	for _, token := range pattern {
		next := make([]bool, len(valueRunes)+1)
		switch token {
		case '*':
			for idx := range valueRunes {
				next[idx+1] = next[idx] || dp[idx+1] || dp[idx]
			}
			next[0] = dp[0]
		case '?':
			for idx := range valueRunes {
				next[idx+1] = dp[idx]
			}
		default:
			for idx, char := range valueRunes {
				next[idx+1] = dp[idx] && char == token
			}
		}
		dp = next
	}

	return dp[len(valueRunes)]
}
