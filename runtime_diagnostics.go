package navigo

import (
	"context"
	"fmt"

	"github.com/albertocavalcante/navigo/cdp"
	cdpruntime "github.com/albertocavalcante/navigo/cdp/proto/runtime"
)

type ConsoleObserverOption func(*consoleObserverConfig)

type consoleObserverConfig struct {
	buffer int
}

type ConsoleObserver struct {
	events <-chan ConsoleEvent
	cancel func()
}

type ConsoleEvent struct {
	Type               string
	Args               []any
	ExecutionContextID int
	Timestamp          float64
	Context            string
}

type ExceptionObserverOption func(*exceptionObserverConfig)

type exceptionObserverConfig struct {
	buffer int
}

type ExceptionObserver struct {
	events <-chan ExceptionEvent
	cancel func()
}

type ExceptionEvent struct {
	Timestamp          float64
	ExceptionID        int
	Text               string
	LineNumber         int
	ColumnNumber       int
	ScriptID           string
	URL                string
	ExecutionContextID int
}

func defaultConsoleObserverConfig() consoleObserverConfig {
	return consoleObserverConfig{buffer: 32}
}

func defaultExceptionObserverConfig() exceptionObserverConfig {
	return exceptionObserverConfig{buffer: 32}
}

func WithConsoleObserverBuffer(size int) ConsoleObserverOption {
	return func(config *consoleObserverConfig) {
		config.buffer = size
	}
}

func WithExceptionObserverBuffer(size int) ExceptionObserverOption {
	return func(config *exceptionObserverConfig) {
		config.buffer = size
	}
}

func (p *Page) ObserveConsole(ctx context.Context, opts ...ConsoleObserverOption) (*ConsoleObserver, error) {
	_ = ctx

	config := defaultConsoleObserverConfig()
	for _, opt := range opts {
		opt(&config)
	}
	if config.buffer < 0 {
		config.buffer = 0
	}

	rawEvents, cancel := p.session.Subscribe(config.buffer)
	decoded := make(chan ConsoleEvent, config.buffer)

	go func() {
		defer close(decoded)
		defer cancel()

		for event := range rawEvents {
			consoleEvent, ok, err := decodeConsoleEvent(event)
			if err != nil {
				return
			}
			if !ok {
				continue
			}

			select {
			case decoded <- consoleEvent:
			default:
			}
		}
	}()

	return &ConsoleObserver{
		events: decoded,
		cancel: cancel,
	}, nil
}

func (o *ConsoleObserver) Events() <-chan ConsoleEvent {
	return o.events
}

func (o *ConsoleObserver) Close() {
	if o.cancel != nil {
		o.cancel()
		o.cancel = nil
	}
}

func (p *Page) ObserveExceptions(ctx context.Context, opts ...ExceptionObserverOption) (*ExceptionObserver, error) {
	_ = ctx

	config := defaultExceptionObserverConfig()
	for _, opt := range opts {
		opt(&config)
	}
	if config.buffer < 0 {
		config.buffer = 0
	}

	rawEvents, cancel := p.session.Subscribe(config.buffer)
	decoded := make(chan ExceptionEvent, config.buffer)

	go func() {
		defer close(decoded)
		defer cancel()

		for event := range rawEvents {
			exceptionEvent, ok, err := decodeExceptionEvent(event)
			if err != nil {
				return
			}
			if !ok {
				continue
			}

			select {
			case decoded <- exceptionEvent:
			default:
			}
		}
	}()

	return &ExceptionObserver{
		events: decoded,
		cancel: cancel,
	}, nil
}

func (o *ExceptionObserver) Events() <-chan ExceptionEvent {
	return o.events
}

func (o *ExceptionObserver) Close() {
	if o.cancel != nil {
		o.cancel()
		o.cancel = nil
	}
}

func decodeConsoleEvent(event cdp.Event) (ConsoleEvent, bool, error) {
	if event.Method != cdpruntime.EventConsoleAPICalled {
		return ConsoleEvent{}, false, nil
	}

	decoded, err := cdpruntime.DecodeConsoleAPICalled(event.Params)
	if err != nil {
		return ConsoleEvent{}, false, fmt.Errorf("decode console event: %w", err)
	}

	args := make([]any, 0, len(decoded.Args))
	for _, arg := range decoded.Args {
		value, valueErr := decodeRemoteValue(arg)
		if valueErr != nil {
			value = arg.Description
		}
		args = append(args, value)
	}

	return ConsoleEvent{
		Type:               string(decoded.Type),
		Args:               args,
		ExecutionContextID: int(decoded.ExecutionContextID),
		Timestamp:          float64(decoded.Timestamp),
		Context:            decoded.Context,
	}, true, nil
}

func decodeExceptionEvent(event cdp.Event) (ExceptionEvent, bool, error) {
	if event.Method != cdpruntime.EventExceptionThrown {
		return ExceptionEvent{}, false, nil
	}

	decoded, err := cdpruntime.DecodeExceptionThrown(event.Params)
	if err != nil {
		return ExceptionEvent{}, false, fmt.Errorf("decode exception event: %w", err)
	}

	return ExceptionEvent{
		Timestamp:          float64(decoded.Timestamp),
		ExceptionID:        decoded.ExceptionDetails.ExceptionID,
		Text:               decoded.ExceptionDetails.Text,
		LineNumber:         decoded.ExceptionDetails.LineNumber,
		ColumnNumber:       decoded.ExceptionDetails.ColumnNumber,
		ScriptID:           string(decoded.ExceptionDetails.ScriptID),
		URL:                decoded.ExceptionDetails.URL,
		ExecutionContextID: int(decoded.ExceptionDetails.ExecutionContextID),
	}, true, nil
}
