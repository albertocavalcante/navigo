package navigo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/albertocavalcante/navigo/cdp"
	cdpaccessibility "github.com/albertocavalcante/navigo/cdp/proto/accessibility"
	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
	cdptarget "github.com/albertocavalcante/navigo/cdp/proto/target"
)

type Page struct {
	browser   *Browser
	targetID  cdptarget.TargetID
	sessionID cdptarget.SessionID
	session   *cdp.Session
	keyboard  Keyboard

	closeOnce sync.Once
	closeErr  error

	mu                    sync.Mutex
	closed                bool
	eventSeq              uint64
	lastNavigation        navigationRecord
	lifecycleByLoader     map[string]map[cdppage.LifecycleEventName]uint64
	lifecycleByFrame      map[string]map[cdppage.LifecycleEventName]uint64
	frameStates           map[string]*frameState
	contextsByID          map[cdpruntime.ExecutionContextID]executionContextState
	defaultContextByFrame map[string]cdpruntime.ExecutionContextID
	mainFrameID           string
	waiters               map[int]*navigationWaiter
	nextWaiterID          int
	axWatchers            map[int]*axWatcher
	nextAXWatcherID       int
	logObserverCount      int
	routes                map[int]*routeEntry
	nextRouteID           int
	routeCancel           func()
	routeDone             chan struct{}
	eventCancel           func()
	eventDone             chan struct{}
}

type axWatcher struct {
	ch   chan struct{}
	done chan struct{}
}

type WaitUntil string

const (
	WaitUntilCommit           WaitUntil = "commit"
	WaitUntilLoad             WaitUntil = "load"
	WaitUntilDOMContentLoaded WaitUntil = "domcontentloaded"
	WaitUntilNetworkIdle      WaitUntil = "networkidle"
)

type navigationRecord struct {
	frameID  string
	loaderID string
	startSeq uint64
	sameDoc  bool
}

type navigationWaiter struct {
	record navigationRecord
	target cdppage.LifecycleEventName
	commit bool
	ch     chan error
}

type GotoOption func(*gotoConfig)

type gotoConfig struct {
	waitUntil WaitUntil
}

func defaultGotoConfig() gotoConfig {
	return gotoConfig{waitUntil: WaitUntilLoad}
}

func WithWaitUntil(until WaitUntil) GotoOption {
	return func(config *gotoConfig) {
		config.waitUntil = until
	}
}

type frameState struct {
	lastNavigatedSeq  uint64
	sameDocumentSeq   uint64
	startedLoadingSeq uint64
	stoppedLoadingSeq uint64
}

type executionContextState struct {
	frameID      string
	defaultWorld bool
}

func (b *Browser) NewPage(ctx context.Context) (*Page, error) {
	root := b.conn.RootSession()

	targetResult, err := cdptarget.CreateTarget("about:blank").Do(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("create target: %w", err)
	}

	attachResult, err := cdptarget.AttachToTarget(targetResult.TargetID).WithFlatten(true).Do(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("attach to target: %w", err)
	}

	session := b.conn.Session(string(attachResult.SessionID))
	page := &Page{
		browser:               b,
		targetID:              targetResult.TargetID,
		sessionID:             attachResult.SessionID,
		session:               session,
		waiters:               make(map[int]*navigationWaiter),
		lifecycleByLoader:     make(map[string]map[cdppage.LifecycleEventName]uint64),
		lifecycleByFrame:      make(map[string]map[cdppage.LifecycleEventName]uint64),
		frameStates:           make(map[string]*frameState),
		contextsByID:          make(map[cdpruntime.ExecutionContextID]executionContextState),
		defaultContextByFrame: make(map[string]cdpruntime.ExecutionContextID),
		axWatchers:            make(map[int]*axWatcher),
		eventDone:             make(chan struct{}),
	}
	page.keyboard = Keyboard{session: session}

	page.startEventLoop()

	if err := cdppage.Enable().Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable page domain: %w", err)
	}

	if err := cdpruntime.Enable().Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable runtime domain: %w", err)
	}

	if err := cdpdom.Enable().Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable DOM domain: %w", err)
	}

	if err := cdpaccessibility.Enable().Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable accessibility domain: %w", err)
	}

	if err := cdppage.SetLifecycleEventsEnabled(true).Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable page lifecycle events: %w", err)
	}

	return page, nil
}

func (p *Page) TargetID() string {
	return string(p.targetID)
}

func (p *Page) SessionID() string {
	return string(p.sessionID)
}

func (p *Page) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		cancel := p.eventCancel
		done := p.eventDone
		p.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}

		p.failWaiters(errors.New("page closed"))
		p.closeAXWatchers()
		if err := p.stopRoutes(ctx); err != nil && p.closeErr == nil {
			p.closeErr = err
			return
		}

		if p.browser == nil || p.browser.conn == nil {
			return
		}

		root := p.browser.conn.RootSession()
		if p.sessionID != "" {
			if err := cdptarget.DetachFromTarget().WithSessionID(p.sessionID).Do(ctx, root); err != nil {
				p.closeErr = fmt.Errorf("close page: detach target session: %w", err)
				return
			}
		}

		if p.targetID != "" {
			result, err := cdptarget.CloseTarget(p.targetID).Do(ctx, root)
			if err != nil {
				p.closeErr = fmt.Errorf("close page: close target: %w", err)
				return
			}
			if result != nil && !result.Success {
				p.closeErr = errors.New("close page: target close unsuccessful")
			}
		}
	})

	return p.closeErr
}

func (p *Page) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.closed
}

func (p *Page) Navigate(ctx context.Context, url string) (*cdppage.NavigateResult, error) {
	p.mu.Lock()
	startSeq := p.eventSeq
	p.mu.Unlock()

	result, err := cdppage.Navigate(url).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("navigate page: %w", err)
	}

	if result.ErrorText != "" {
		return nil, fmt.Errorf("navigate page: %s", result.ErrorText)
	}

	p.mu.Lock()
	p.lastNavigation = navigationRecord{
		frameID:  string(result.FrameID),
		loaderID: string(result.LoaderID),
		startSeq: startSeq,
		sameDoc:  result.LoaderID == "",
	}
	p.mu.Unlock()

	return result, nil
}

func (p *Page) Goto(ctx context.Context, url string, opts ...GotoOption) (*cdppage.NavigateResult, error) {
	config := defaultGotoConfig()
	for _, opt := range opts {
		opt(&config)
	}

	waiter, waiterID, err := p.prepareNavigationWait(config.waitUntil)
	if err != nil {
		return nil, err
	}
	defer p.removeWaiter(waiterID)

	result, err := p.Navigate(ctx, url)
	if err != nil {
		return nil, err
	}

	p.attachWaiterToNavigation(waiterID, waiter, p.lastNavigation, config.waitUntil)
	if err := p.awaitNavigationWait(ctx, waiter); err != nil {
		return nil, err
	}

	return result, nil
}

func (p *Page) Evaluate(ctx context.Context, expression string) (any, error) {
	result, err := cdpruntime.Evaluate(expression).WithReturnByValue(true).WithAwaitPromise(true).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("evaluate page expression: %w", err)
	}

	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("evaluate page expression: %s", result.ExceptionDetails.Text)
	}

	return decodeRemoteValue(result.Result)
}

func (p *Page) EvaluateHandle(ctx context.Context, expression string) (*Handle, error) {
	result, err := cdpruntime.Evaluate(expression).WithAwaitPromise(true).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("evaluate page expression: %w", err)
	}

	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("evaluate page expression: %s", result.ExceptionDetails.Text)
	}

	return newHandle(p, result.Result, "evaluate page expression")
}

func (p *Page) CallFunction(ctx context.Context, functionDeclaration string, args ...any) (any, error) {
	executionContextID, err := p.mainWorldExecutionContextID()
	if err != nil {
		return nil, err
	}

	callArguments := make([]cdpruntime.CallArgument, 0, len(args))
	for _, arg := range args {
		callArgument, callErr := encodeCallArgument(p, arg)
		if callErr != nil {
			return nil, callErr
		}
		callArguments = append(callArguments, callArgument)
	}

	result, err := cdpruntime.CallFunctionOn(functionDeclaration).
		WithExecutionContextID(executionContextID).
		WithArguments(callArguments).
		WithReturnByValue(true).
		WithAwaitPromise(true).
		Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("call page function: %w", err)
	}

	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("call page function: %s", result.ExceptionDetails.Text)
	}

	return decodeRemoteValue(result.Result)
}

func (p *Page) CallFunctionHandle(ctx context.Context, functionDeclaration string, args ...any) (*Handle, error) {
	executionContextID, err := p.mainWorldExecutionContextID()
	if err != nil {
		return nil, err
	}

	callArguments := make([]cdpruntime.CallArgument, 0, len(args))
	for _, arg := range args {
		callArgument, callErr := encodeCallArgument(p, arg)
		if callErr != nil {
			return nil, callErr
		}
		callArguments = append(callArguments, callArgument)
	}

	result, err := cdpruntime.CallFunctionOn(functionDeclaration).
		WithExecutionContextID(executionContextID).
		WithArguments(callArguments).
		WithAwaitPromise(true).
		Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("call page function: %w", err)
	}

	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("call page function: %s", result.ExceptionDetails.Text)
	}

	return newHandle(p, result.Result, "call page function")
}

func (p *Page) GetFrameTree(ctx context.Context) (*cdppage.FrameTree, error) {
	result, err := cdppage.GetFrameTree().Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("get frame tree: %w", err)
	}

	p.mu.Lock()
	p.mainFrameID = string(result.FrameTree.Frame.ID)
	p.mu.Unlock()

	return &result.FrameTree, nil
}

func (p *Page) QuerySelector(ctx context.Context, selector string) (*Element, error) {
	documentNodeID, err := p.documentNodeID(ctx)
	if err != nil {
		return nil, err
	}

	result, err := cdpdom.QuerySelector(documentNodeID, selector).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("query selector: %w", err)
	}

	if result.NodeID == 0 {
		return nil, nil
	}

	return p.resolveNodeIDToElement(ctx, result.NodeID)
}

func (p *Page) QuerySelectorAll(ctx context.Context, selector string) ([]*Element, error) {
	documentNodeID, err := p.documentNodeID(ctx)
	if err != nil {
		return nil, err
	}

	result, err := cdpdom.QuerySelectorAll(documentNodeID, selector).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("query selector all: %w", err)
	}

	elements := make([]*Element, 0, len(result.NodeIDs))
	for _, nodeID := range result.NodeIDs {
		element, resolveErr := p.resolveNodeIDToElement(ctx, nodeID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if element != nil {
			elements = append(elements, element)
		}
	}

	return elements, nil
}

func (p *Page) WaitForNavigation(ctx context.Context) error {
	return p.WaitForNavigationUntil(ctx, WaitUntilLoad)
}

func (p *Page) WaitForNavigationUntil(ctx context.Context, until WaitUntil) error {
	waiter, waiterID, err := p.prepareWaitForLastNavigation(until)
	if err != nil {
		return err
	}
	defer p.removeWaiter(waiterID)

	return p.awaitNavigationWait(ctx, waiter)
}

func (p *Page) startEventLoop() {
	events, cancel := p.session.Subscribe(32)
	p.mu.Lock()
	if p.eventDone == nil {
		p.eventDone = make(chan struct{})
	}
	p.eventCancel = cancel
	done := p.eventDone
	p.mu.Unlock()

	go func() {
		defer close(done)
		for event := range events {
			switch event.Method {
			case cdppage.EventLifecycleEvent:
				lifecycleEvent, err := cdppage.DecodeLifecycleEvent(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordLifecycleEvent(*lifecycleEvent)
			case cdppage.EventFrameNavigated:
				frameNavigated, err := cdppage.DecodeFrameNavigated(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordFrameNavigated(*frameNavigated)
			case cdppage.EventNavigatedWithinDocument:
				sameDocument, err := cdppage.DecodeNavigatedWithinDocument(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordNavigatedWithinDocument(*sameDocument)
			case cdppage.EventFrameStartedLoading:
				startedLoading, err := cdppage.DecodeFrameStartedLoading(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordFrameStartedLoading(*startedLoading)
			case cdppage.EventFrameStoppedLoading:
				stoppedLoading, err := cdppage.DecodeFrameStoppedLoading(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordFrameStoppedLoading(*stoppedLoading)
			case cdpruntime.EventExecutionContextCreated:
				created, err := cdpruntime.DecodeExecutionContextCreated(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordExecutionContextCreated(*created)
			case cdpruntime.EventExecutionContextDestroyed:
				destroyed, err := cdpruntime.DecodeExecutionContextDestroyed(event.Params)
				if err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordExecutionContextDestroyed(*destroyed)
			case cdpruntime.EventExecutionContextsCleared:
				if _, err := cdpruntime.DecodeExecutionContextsCleared(event.Params); err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.recordExecutionContextsCleared()
			case cdpaccessibility.EventLoadComplete:
				if _, err := cdpaccessibility.DecodeLoadComplete(event.Params); err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.notifyAXWatchers()
			case cdpaccessibility.EventNodesUpdated:
				if _, err := cdpaccessibility.DecodeNodesUpdated(event.Params); err != nil {
					p.failWaiters(fmt.Errorf("wait for navigation: %w", err))
					return
				}

				p.notifyAXWatchers()
			}
		}

		p.failWaiters(errors.New("wait for navigation: session closed"))
	}()
}

func (p *Page) recordLifecycleEvent(event cdppage.LifecycleEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eventSeq++
	seq := p.eventSeq

	if event.LoaderID != "" {
		loaderID := string(event.LoaderID)
		if p.lifecycleByLoader[loaderID] == nil {
			p.lifecycleByLoader[loaderID] = make(map[cdppage.LifecycleEventName]uint64)
		}
		p.lifecycleByLoader[loaderID][event.Name] = seq
	}

	if event.FrameID != "" {
		frameID := string(event.FrameID)
		if p.lifecycleByFrame[frameID] == nil {
			p.lifecycleByFrame[frameID] = make(map[cdppage.LifecycleEventName]uint64)
		}
		p.lifecycleByFrame[frameID][event.Name] = seq
	}

	p.resolveWaitersLocked()
}

func (p *Page) navigationReadyLocked(record navigationRecord, target cdppage.LifecycleEventName, commit bool) bool {
	if commit {
		return p.navigationCommittedLocked(record)
	}

	if record.loaderID != "" {
		if seq := p.lifecycleByLoader[record.loaderID][target]; seq > record.startSeq {
			return true
		}
	}

	if record.frameID != "" {
		if seq := p.lifecycleByFrame[record.frameID][target]; seq > record.startSeq {
			return true
		}

		if record.sameDoc && target != cdppage.LifecycleEventNetworkIdle {
			if state := p.frameStates[record.frameID]; state != nil && state.sameDocumentSeq > record.startSeq {
				return true
			}
		}
	}

	return false
}

func (p *Page) removeWaiter(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.waiters, id)
}

func (p *Page) failWaiters(err error) {
	p.mu.Lock()
	waiters := make([]*navigationWaiter, 0, len(p.waiters))
	for id, waiter := range p.waiters {
		delete(p.waiters, id)
		waiters = append(waiters, waiter)
	}
	p.mu.Unlock()

	for _, waiter := range waiters {
		waiter.ch <- err
		close(waiter.ch)
	}
}

func waitUntilLifecycle(until WaitUntil) (cdppage.LifecycleEventName, error) {
	switch until {
	case WaitUntilCommit:
		return "", nil
	case WaitUntilDOMContentLoaded:
		return cdppage.LifecycleEventDOMContentLoaded, nil
	case WaitUntilLoad:
		return cdppage.LifecycleEventLoad, nil
	case WaitUntilNetworkIdle:
		return cdppage.LifecycleEventNetworkIdle, nil
	default:
		return "", fmt.Errorf("wait for navigation: unsupported wait mode %q", until)
	}
}

func (p *Page) recordFrameNavigated(event cdppage.FrameNavigated) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eventSeq++
	seq := p.eventSeq
	frameID := string(event.Frame.ID)
	state := p.ensureFrameStateLocked(frameID)
	state.lastNavigatedSeq = seq

	if event.Frame.ParentID == "" {
		p.mainFrameID = frameID
	}

	if event.Frame.LoaderID != "" && p.mainFrameID == frameID {
		p.lastNavigation.frameID = frameID
		p.lastNavigation.loaderID = string(event.Frame.LoaderID)
		p.lastNavigation.sameDoc = false
	}

	p.resolveWaitersLocked()
}

func (p *Page) recordNavigatedWithinDocument(event cdppage.NavigatedWithinDocument) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eventSeq++
	seq := p.eventSeq
	state := p.ensureFrameStateLocked(string(event.FrameID))
	state.sameDocumentSeq = seq

	frameID := string(event.FrameID)
	if p.mainFrameID == "" || p.mainFrameID == frameID {
		p.mainFrameID = frameID
		if p.lastNavigation.frameID == frameID {
			p.lastNavigation.sameDoc = true
		}
	}

	p.resolveWaitersLocked()
}

func (p *Page) recordFrameStartedLoading(event cdppage.FrameStartedLoading) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eventSeq++
	state := p.ensureFrameStateLocked(string(event.FrameID))
	state.startedLoadingSeq = p.eventSeq
	p.resolveWaitersLocked()
}

func (p *Page) recordFrameStoppedLoading(event cdppage.FrameStoppedLoading) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eventSeq++
	state := p.ensureFrameStateLocked(string(event.FrameID))
	state.stoppedLoadingSeq = p.eventSeq
	p.resolveWaitersLocked()
}

func (p *Page) recordExecutionContextCreated(event cdpruntime.ExecutionContextCreated) {
	var aux struct {
		FrameID   string `json:"frameId"`
		IsDefault bool   `json:"isDefault"`
	}

	_ = json.Unmarshal(event.Context.AuxData, &aux)

	p.mu.Lock()
	defer p.mu.Unlock()

	p.contextsByID[event.Context.ID] = executionContextState{
		frameID:      aux.FrameID,
		defaultWorld: aux.IsDefault,
	}
	if aux.FrameID != "" && aux.IsDefault {
		p.defaultContextByFrame[aux.FrameID] = event.Context.ID
	}
}

func (p *Page) recordExecutionContextDestroyed(event cdpruntime.ExecutionContextDestroyed) {
	p.mu.Lock()
	defer p.mu.Unlock()

	contextState, ok := p.contextsByID[event.ExecutionContextID]
	if ok && contextState.defaultWorld {
		if existing := p.defaultContextByFrame[contextState.frameID]; existing == event.ExecutionContextID {
			delete(p.defaultContextByFrame, contextState.frameID)
		}
	}
	delete(p.contextsByID, event.ExecutionContextID)
}

func (p *Page) recordExecutionContextsCleared() {
	p.mu.Lock()
	defer p.mu.Unlock()

	clear(p.contextsByID)
	clear(p.defaultContextByFrame)
}

func (p *Page) subscribeAXUpdates() (<-chan struct{}, func()) {
	watcher := &axWatcher{
		ch:   make(chan struct{}, 1),
		done: make(chan struct{}),
	}

	p.mu.Lock()
	if p.axWatchers == nil {
		p.axWatchers = make(map[int]*axWatcher)
	}
	id := p.nextAXWatcherID
	p.nextAXWatcherID++
	p.axWatchers[id] = watcher
	p.mu.Unlock()

	cancel := func() {
		p.mu.Lock()
		if existing, ok := p.axWatchers[id]; ok {
			delete(p.axWatchers, id)
			close(existing.done)
		}
		p.mu.Unlock()
	}

	return watcher.ch, cancel
}

func (p *Page) notifyAXWatchers() {
	p.mu.Lock()
	watchers := make([]*axWatcher, 0, len(p.axWatchers))
	for _, watcher := range p.axWatchers {
		watchers = append(watchers, watcher)
	}
	p.mu.Unlock()

	for _, watcher := range watchers {
		select {
		case <-watcher.done:
		case watcher.ch <- struct{}{}:
		default:
		}
	}
}

func (p *Page) closeAXWatchers() {
	p.mu.Lock()
	watchers := make([]*axWatcher, 0, len(p.axWatchers))
	for id, watcher := range p.axWatchers {
		delete(p.axWatchers, id)
		watchers = append(watchers, watcher)
	}
	p.mu.Unlock()

	for _, watcher := range watchers {
		close(watcher.done)
		close(watcher.ch)
	}
}

func (p *Page) ensureFrameStateLocked(frameID string) *frameState {
	if p.frameStates[frameID] == nil {
		p.frameStates[frameID] = &frameState{}
	}

	return p.frameStates[frameID]
}

func (p *Page) resolveWaitersLocked() {
	for id, waiter := range p.waiters {
		if p.navigationReadyLocked(waiter.record, waiter.target, waiter.commit) {
			delete(p.waiters, id)
			waiter.ch <- nil
			close(waiter.ch)
		}
	}
}

func (p *Page) navigationCommittedLocked(record navigationRecord) bool {
	if record.frameID == "" {
		return false
	}

	state := p.frameStates[record.frameID]
	if state == nil {
		return false
	}

	if state.lastNavigatedSeq > record.startSeq {
		return true
	}

	if state.sameDocumentSeq > record.startSeq {
		return true
	}

	return false
}

func (p *Page) prepareNavigationWait(until WaitUntil) (*navigationWaiter, int, error) {
	target, err := waitUntilLifecycle(until)
	if err != nil {
		return nil, 0, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	waiterID := p.nextWaiterID
	p.nextWaiterID++
	waiter := &navigationWaiter{
		target: target,
		commit: until == WaitUntilCommit,
		ch:     make(chan error, 1),
	}
	p.waiters[waiterID] = waiter

	return waiter, waiterID, nil
}

func (p *Page) prepareWaitForLastNavigation(until WaitUntil) (*navigationWaiter, int, error) {
	target, err := waitUntilLifecycle(until)
	if err != nil {
		return nil, 0, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	record := p.lastNavigation
	if record.frameID == "" && record.loaderID == "" {
		return nil, 0, errors.New("wait for navigation: no navigation recorded")
	}

	if p.navigationReadyLocked(record, target, until == WaitUntilCommit) {
		ch := make(chan error, 1)
		ch <- nil
		close(ch)
		return &navigationWaiter{
			record: record,
			target: target,
			commit: until == WaitUntilCommit,
			ch:     ch,
		}, -1, nil
	}

	waiterID := p.nextWaiterID
	p.nextWaiterID++
	waiter := &navigationWaiter{
		record: record,
		target: target,
		commit: until == WaitUntilCommit,
		ch:     make(chan error, 1),
	}
	p.waiters[waiterID] = waiter

	return waiter, waiterID, nil
}

func (p *Page) attachWaiterToNavigation(waiterID int, waiter *navigationWaiter, record navigationRecord, until WaitUntil) {
	target, err := waitUntilLifecycle(until)
	if err != nil {
		waiter.ch <- err
		close(waiter.ch)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	waiter.record = record
	waiter.target = target
	waiter.commit = until == WaitUntilCommit

	if p.navigationReadyLocked(waiter.record, waiter.target, waiter.commit) {
		delete(p.waiters, waiterID)
		waiter.ch <- nil
		close(waiter.ch)
	}
}

func (p *Page) awaitNavigationWait(ctx context.Context, waiter *navigationWaiter) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for navigation: %w", ctx.Err())
	case err, ok := <-waiter.ch:
		if !ok {
			return nil
		}
		return err
	}
}

func (p *Page) mainWorldExecutionContextID() (cdpruntime.ExecutionContextID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mainFrameID != "" {
		if executionContextID, ok := p.defaultContextByFrame[p.mainFrameID]; ok {
			return executionContextID, nil
		}
	}

	if len(p.defaultContextByFrame) == 1 {
		for _, executionContextID := range p.defaultContextByFrame {
			return executionContextID, nil
		}
	}

	return 0, errors.New("call page function: no main-world execution context recorded")
}

func (p *Page) documentNodeID(ctx context.Context) (cdpdom.NodeID, error) {
	result, err := cdpdom.GetDocument().Do(ctx, p.session)
	if err != nil {
		return 0, fmt.Errorf("get document: %w", err)
	}

	return result.Root.NodeID, nil
}

func (p *Page) resolveNodeIDToElement(ctx context.Context, nodeID cdpdom.NodeID) (*Element, error) {
	nodeResult, err := cdpdom.DescribeNode().WithNodeID(nodeID).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("describe node: %w", err)
	}

	executionContextID, err := p.mainWorldExecutionContextID()
	if err != nil {
		return nil, err
	}

	resolveResult, err := cdpdom.ResolveNode().
		WithNodeID(nodeID).
		WithExecutionContextID(cdpdom.ExecutionContextID(executionContextID)).
		Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("resolve node: %w", err)
	}

	handle, err := newHandle(p, runtimeObjectFromDOM(resolveResult.Object), "resolve node")
	if err != nil {
		return nil, err
	}

	return &Element{
		page:          p,
		handle:        handle,
		nodeID:        nodeResult.Node.NodeID,
		backendNodeID: nodeResult.Node.BackendNodeID,
	}, nil
}

func (p *Page) resolveBackendNodeIDToElement(ctx context.Context, backendNodeID int) (*Element, error) {
	nodeResult, err := cdpdom.DescribeNode().WithBackendNodeID(cdpdom.BackendNodeID(backendNodeID)).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("describe backend node: %w", err)
	}

	executionContextID, err := p.mainWorldExecutionContextID()
	if err != nil {
		return nil, err
	}

	resolveResult, err := cdpdom.ResolveNode().
		WithBackendNodeID(cdpdom.BackendNodeID(backendNodeID)).
		WithExecutionContextID(cdpdom.ExecutionContextID(executionContextID)).
		Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("resolve backend node: %w", err)
	}

	handle, err := newHandle(p, runtimeObjectFromDOM(resolveResult.Object), "resolve backend node")
	if err != nil {
		return nil, err
	}

	return &Element{
		page:          p,
		handle:        handle,
		nodeID:        nodeResult.Node.NodeID,
		backendNodeID: nodeResult.Node.BackendNodeID,
	}, nil
}

func encodeCallArgument(page *Page, value any) (cdpruntime.CallArgument, error) {
	switch number := value.(type) {
	case *Handle:
		if page != nil && number.page != nil && number.page.session != page.session {
			return cdpruntime.CallArgument{}, errors.New("encode call argument: handle belongs to a different page session")
		}
		objectID, err := number.ensureUsable()
		if err != nil {
			return cdpruntime.CallArgument{}, fmt.Errorf("encode call argument: %w", err)
		}
		return cdpruntime.CallArgument{ObjectID: objectID}, nil
	case nil:
		return cdpruntime.CallArgument{Value: json.RawMessage("null")}, nil
	case float64:
		return encodeFloatCallArgument(number), nil
	case float32:
		return encodeFloatCallArgument(float64(number)), nil
	default:
		return cdpruntime.CallArgument{Value: value}, nil
	}
}

func encodeFloatCallArgument(value float64) cdpruntime.CallArgument {
	switch {
	case math.IsNaN(value):
		return cdpruntime.CallArgument{UnserializableValue: "NaN"}
	case math.IsInf(value, 1):
		return cdpruntime.CallArgument{UnserializableValue: "Infinity"}
	case math.IsInf(value, -1):
		return cdpruntime.CallArgument{UnserializableValue: "-Infinity"}
	case value == 0 && math.Signbit(value):
		return cdpruntime.CallArgument{UnserializableValue: "-0"}
	default:
		return cdpruntime.CallArgument{Value: value}
	}
}

func decodeRemoteValue(object cdpruntime.RemoteObject) (any, error) {
	switch value := any(object.Value).(type) {
	case nil:
	case json.RawMessage:
		if len(value) == 0 {
			break
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, fmt.Errorf("evaluate page expression: decode value: %w", err)
		}

		return decoded, nil
	case []byte:
		if len(value) == 0 {
			break
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, fmt.Errorf("evaluate page expression: decode value: %w", err)
		}

		return decoded, nil
	default:
		return value, nil
	}

	switch object.UnserializableValue {
	case "":
		return nil, nil
	case "NaN":
		return math.NaN(), nil
	case "Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	case "-0":
		return math.Copysign(0, -1), nil
	default:
		value := string(object.UnserializableValue)
		if strings.HasSuffix(value, "n") {
			return value, nil
		}

		return value, nil
	}
}
