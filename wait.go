package navigo

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type WaitState string

const (
	WaitStateAttached WaitState = "attached"
	WaitStateVisible  WaitState = "visible"
	WaitStateHidden   WaitState = "hidden"
)

type WaitOption func(*waitConfig)

type waitConfig struct {
	state        WaitState
	pollInterval time.Duration
}

func defaultWaitConfig() waitConfig {
	return waitConfig{
		state:        WaitStateVisible,
		pollInterval: 50 * time.Millisecond,
	}
}

func WithWaitState(state WaitState) WaitOption {
	return func(config *waitConfig) {
		config.state = state
	}
}

func WithPollInterval(interval time.Duration) WaitOption {
	return func(config *waitConfig) {
		config.pollInterval = interval
	}
}

func (p *Page) WaitForSelector(ctx context.Context, selector string, opts ...WaitOption) (*Element, error) {
	config := defaultWaitConfig()
	for _, opt := range opts {
		opt(&config)
	}

	if config.pollInterval <= 0 {
		config.pollInterval = time.Millisecond
	}

	if err := validateWaitState(config.state); err != nil {
		return nil, err
	}

	ticker := time.NewTicker(config.pollInterval)
	defer ticker.Stop()

	for {
		element, err := p.QuerySelector(ctx, selector)
		if err != nil {
			return nil, err
		}

		ready, waitErr := elementMatchesWaitState(ctx, element, config.state)
		if waitErr != nil {
			return nil, waitErr
		}
		if ready {
			return element, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for selector %q: %w", selector, ctx.Err())
		case <-ticker.C:
		}
	}
}

func validateWaitState(state WaitState) error {
	switch state {
	case WaitStateAttached, WaitStateVisible, WaitStateHidden:
		return nil
	default:
		return fmt.Errorf("wait for selector: unsupported wait state %q", state)
	}
}

func elementMatchesWaitState(ctx context.Context, element *Element, state WaitState) (bool, error) {
	switch state {
	case WaitStateAttached:
		return element != nil, nil
	case WaitStateVisible:
		if element == nil {
			return false, nil
		}
		return element.IsVisible(ctx)
	case WaitStateHidden:
		if element == nil {
			return true, nil
		}
		visible, err := element.IsVisible(ctx)
		if err != nil {
			return false, err
		}
		return !visible, nil
	default:
		return false, errors.New("wait for selector: invalid wait state")
	}
}
