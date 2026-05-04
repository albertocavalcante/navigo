package navigo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/albertocavalcante/navigo/cdp"
	cdplog "github.com/albertocavalcante/navigo/cdp/proto/log"
)

type LogObserverOption func(*logObserverConfig)

type logObserverConfig struct {
	buffer int
}

type LogObserver struct {
	page   *Page
	events <-chan LogEvent
	cancel func()
	once   sync.Once
}

type LogEvent struct {
	Source           string
	Level            string
	Text             string
	Category         string
	Timestamp        float64
	URL              string
	LineNumber       int
	NetworkRequestID string
	WorkerID         string
}

func defaultLogObserverConfig() logObserverConfig {
	return logObserverConfig{buffer: 32}
}

func WithLogObserverBuffer(size int) LogObserverOption {
	return func(config *logObserverConfig) {
		config.buffer = size
	}
}

func (p *Page) ObserveLog(ctx context.Context, opts ...LogObserverOption) (*LogObserver, error) {
	config := defaultLogObserverConfig()
	for _, opt := range opts {
		opt(&config)
	}
	if config.buffer < 0 {
		config.buffer = 0
	}

	if err := p.acquireLogDomain(ctx); err != nil {
		return nil, err
	}

	rawEvents, cancel := p.session.Subscribe(config.buffer)
	decoded := make(chan LogEvent, config.buffer)

	go func() {
		defer close(decoded)
		defer cancel()

		for event := range rawEvents {
			logEvent, ok, err := decodeLogEvent(event)
			if err != nil {
				return
			}
			if !ok {
				continue
			}

			select {
			case decoded <- logEvent:
			default:
			}
		}
	}()

	return &LogObserver{
		page:   p,
		events: decoded,
		cancel: cancel,
	}, nil
}

func (o *LogObserver) Events() <-chan LogEvent {
	return o.events
}

func (o *LogObserver) Close() {
	o.once.Do(func() {
		if o.cancel != nil {
			o.cancel()
		}
		if o.page != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = o.page.releaseLogDomain(ctx)
		}
	})
}

func (p *Page) ClearLog(ctx context.Context) error {
	if err := cdplog.Clear().Do(ctx, p.session); err != nil {
		return fmt.Errorf("clear log: %w", err)
	}
	return nil
}

func (p *Page) acquireLogDomain(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("observe log: page closed")
	}
	shouldEnable := p.logObserverCount == 0
	p.logObserverCount++
	p.mu.Unlock()

	if !shouldEnable {
		return nil
	}

	if err := cdplog.Enable().Do(ctx, p.session); err != nil {
		p.mu.Lock()
		p.logObserverCount--
		p.mu.Unlock()
		return fmt.Errorf("observe log: enable log domain: %w", err)
	}

	return nil
}

func (p *Page) releaseLogDomain(ctx context.Context) error {
	p.mu.Lock()
	if p.logObserverCount == 0 {
		p.mu.Unlock()
		return nil
	}
	p.logObserverCount--
	shouldDisable := p.logObserverCount == 0 && !p.closed
	p.mu.Unlock()

	if !shouldDisable {
		return nil
	}

	if err := cdplog.Disable().Do(ctx, p.session); err != nil {
		return fmt.Errorf("observe log: disable log domain: %w", err)
	}

	return nil
}

func decodeLogEvent(event cdp.Event) (LogEvent, bool, error) {
	if event.Method != cdplog.EventEntryAdded {
		return LogEvent{}, false, nil
	}

	entryAdded, err := cdplog.DecodeEntryAdded(event.Params)
	if err != nil {
		return LogEvent{}, false, fmt.Errorf("decode log event: %w", err)
	}

	return LogEvent{
		Source:           string(entryAdded.Entry.Source),
		Level:            string(entryAdded.Entry.Level),
		Text:             entryAdded.Entry.Text,
		Category:         string(entryAdded.Entry.Category),
		Timestamp:        float64(entryAdded.Entry.Timestamp),
		URL:              entryAdded.Entry.URL,
		LineNumber:       entryAdded.Entry.LineNumber,
		NetworkRequestID: string(entryAdded.Entry.NetworkRequestID),
		WorkerID:         entryAdded.Entry.WorkerID,
	}, true, nil
}
