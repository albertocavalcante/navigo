package navigo

import (
	"context"
	"errors"
	"fmt"

	"github.com/albertocavalcante/navigo/cdp"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
	cdptarget "github.com/albertocavalcante/navigo/cdp/proto/target"
)

type Page struct {
	browser   *Browser
	targetID  cdptarget.TargetID
	sessionID cdptarget.SessionID
	session   *cdp.Session
}

type WaitUntil string

const (
	WaitUntilLoad             WaitUntil = "load"
	WaitUntilDOMContentLoaded WaitUntil = "domcontentloaded"
	WaitUntilNetworkIdle      WaitUntil = "networkidle"
)

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

	return &Page{
		browser:   b,
		targetID:  targetResult.TargetID,
		sessionID: attachResult.SessionID,
		session:   session,
	}, nil
}

func (p *Page) TargetID() string {
	return string(p.targetID)
}

func (p *Page) SessionID() string {
	return string(p.sessionID)
}

func (p *Page) Navigate(ctx context.Context, url string) (*cdppage.NavigateResult, error) {
	result, err := cdppage.Navigate(url).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("navigate page: %w", err)
	}

	if result.ErrorText != "" {
		return nil, fmt.Errorf("navigate page: %s", result.ErrorText)
	}

	return result, nil
}

func (p *Page) WaitForNavigation(ctx context.Context) error {
	return p.WaitForNavigationUntil(ctx, WaitUntilLoad)
}

func (p *Page) WaitForNavigationUntil(ctx context.Context, until WaitUntil) error {
	events, cancel := p.session.Subscribe(1)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for navigation: %w", ctx.Err())
		case event, ok := <-events:
			if !ok {
				return errors.New("wait for navigation: session closed")
			}

			switch event.Method {
			case cdppage.EventLoadEventFired:
				if until != WaitUntilLoad {
					continue
				}

				if _, err := cdppage.DecodeLoadEventFired(event.Params); err != nil {
					return fmt.Errorf("wait for navigation: %w", err)
				}

				return nil
			case cdppage.EventLifecycleEvent:
				lifecycleEvent, err := cdppage.DecodeLifecycleEvent(event.Params)
				if err != nil {
					return fmt.Errorf("wait for navigation: %w", err)
				}

				switch until {
				case WaitUntilDOMContentLoaded:
					if lifecycleEvent.Name == cdppage.LifecycleEventDOMContentLoaded {
						return nil
					}
				case WaitUntilLoad:
					if lifecycleEvent.Name == cdppage.LifecycleEventLoad {
						return nil
					}
				case WaitUntilNetworkIdle:
					if lifecycleEvent.Name == cdppage.LifecycleEventNetworkIdle {
						return nil
					}
				default:
					return fmt.Errorf("wait for navigation: unsupported wait mode %q", until)
				}
			}
		}
	}
}
