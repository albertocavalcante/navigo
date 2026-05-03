// Package page provides Chrome DevTools Protocol commands and types
// for the Page domain.
package page

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	CommandEnable                    = "Page.enable"
	CommandNavigate                  = "Page.navigate"
	CommandSetLifecycleEventsEnabled = "Page.setLifecycleEventsEnabled"

	EventLoadEventFired = "Page.loadEventFired"
	EventLifecycleEvent = "Page.lifecycleEvent"
)

type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

type EnableParams struct{}

func Enable() *EnableParams {
	return &EnableParams{}
}

func (p *EnableParams) Do(ctx context.Context, caller Caller) error {
	if caller == nil {
		return fmt.Errorf("execute %s: nil caller", CommandEnable)
	}

	if err := caller.Call(ctx, CommandEnable, p, nil); err != nil {
		return fmt.Errorf("execute %s: %w", CommandEnable, err)
	}

	return nil
}

type NavigateParams struct {
	URL string `json:"url"`
}

type NavigateResult struct {
	FrameID   string `json:"frameId"`
	LoaderID  string `json:"loaderId,omitempty"`
	ErrorText string `json:"errorText,omitempty"`
}

func Navigate(url string) *NavigateParams {
	return &NavigateParams{URL: url}
}

func (p *NavigateParams) Do(ctx context.Context, caller Caller) (*NavigateResult, error) {
	if caller == nil {
		return nil, fmt.Errorf("execute %s: nil caller", CommandNavigate)
	}

	var result NavigateResult
	if err := caller.Call(ctx, CommandNavigate, p, &result); err != nil {
		return nil, fmt.Errorf("execute %s: %w", CommandNavigate, err)
	}

	return &result, nil
}

type SetLifecycleEventsEnabledParams struct {
	Enabled bool `json:"enabled"`
}

func SetLifecycleEventsEnabled(enabled bool) *SetLifecycleEventsEnabledParams {
	return &SetLifecycleEventsEnabledParams{Enabled: enabled}
}

func (p *SetLifecycleEventsEnabledParams) Do(ctx context.Context, caller Caller) error {
	if caller == nil {
		return fmt.Errorf("execute %s: nil caller", CommandSetLifecycleEventsEnabled)
	}

	if err := caller.Call(ctx, CommandSetLifecycleEventsEnabled, p, nil); err != nil {
		return fmt.Errorf("execute %s: %w", CommandSetLifecycleEventsEnabled, err)
	}

	return nil
}

// LoadEventFired is emitted when the page load event fires.
type LoadEventFired struct {
	Timestamp float64 `json:"timestamp"`
}

// DecodeLoadEventFired decodes the raw CDP event payload.
func DecodeLoadEventFired(data json.RawMessage) (*LoadEventFired, error) {
	var event LoadEventFired
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("decode %s: %w", EventLoadEventFired, err)
	}

	return &event, nil
}

type LifecycleEventName string

const (
	LifecycleEventDOMContentLoaded LifecycleEventName = "DOMContentLoaded"
	LifecycleEventLoad             LifecycleEventName = "load"
	LifecycleEventNetworkIdle      LifecycleEventName = "networkIdle"
)

// LifecycleEvent is emitted for page lifecycle milestones.
type LifecycleEvent struct {
	FrameID   string             `json:"frameId"`
	LoaderID  string             `json:"loaderId,omitempty"`
	Name      LifecycleEventName `json:"name"`
	Timestamp float64            `json:"timestamp"`
}

// DecodeLifecycleEvent decodes the raw CDP event payload.
func DecodeLifecycleEvent(data json.RawMessage) (*LifecycleEvent, error) {
	var event LifecycleEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("decode %s: %w", EventLifecycleEvent, err)
	}

	return &event, nil
}
