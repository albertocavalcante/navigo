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
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
	cdptarget "github.com/albertocavalcante/navigo/cdp/proto/target"
)

type Page struct {
	browser   *Browser
	targetID  cdptarget.TargetID
	sessionID cdptarget.SessionID
	session   *cdp.Session

	mu                sync.Mutex
	eventSeq          uint64
	lastNavigation    navigationRecord
	lifecycleByLoader map[string]map[cdppage.LifecycleEventName]uint64
	lifecycleByFrame  map[string]map[cdppage.LifecycleEventName]uint64
	frameStates       map[string]*frameState
	mainFrameID       string
	waiters           map[int]*navigationWaiter
	nextWaiterID      int
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
	if err := cdppage.Enable().Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable page domain: %w", err)
	}

	if err := cdppage.SetLifecycleEventsEnabled(true).Do(ctx, session); err != nil {
		return nil, fmt.Errorf("enable page lifecycle events: %w", err)
	}

	page := &Page{
		browser:           b,
		targetID:          targetResult.TargetID,
		sessionID:         attachResult.SessionID,
		session:           session,
		waiters:           make(map[int]*navigationWaiter),
		lifecycleByLoader: make(map[string]map[cdppage.LifecycleEventName]uint64),
		lifecycleByFrame:  make(map[string]map[cdppage.LifecycleEventName]uint64),
		frameStates:       make(map[string]*frameState),
	}

	page.startEventLoop()

	return page, nil
}

func (p *Page) TargetID() string {
	return string(p.targetID)
}

func (p *Page) SessionID() string {
	return string(p.sessionID)
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
		loaderID: result.LoaderID,
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
	_ = cancel

	go func() {
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
		if p.lifecycleByLoader[event.LoaderID] == nil {
			p.lifecycleByLoader[event.LoaderID] = make(map[cdppage.LifecycleEventName]uint64)
		}
		p.lifecycleByLoader[event.LoaderID][event.Name] = seq
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
		p.lastNavigation.loaderID = event.Frame.LoaderID
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
