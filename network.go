package navigo

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/albertocavalcante/navigo/cdp"
	cdpnetwork "github.com/albertocavalcante/navigo/cdp/proto/network"
)

type NetworkEventKind string

const (
	NetworkEventRequestStarted   NetworkEventKind = "request_started"
	NetworkEventResponseReceived NetworkEventKind = "response_received"
	NetworkEventLoadingFinished  NetworkEventKind = "loading_finished"
	NetworkEventLoadingFailed    NetworkEventKind = "loading_failed"
)

type NetworkObserverOption func(*networkObserverConfig)

type networkObserverConfig struct {
	buffer int
}

type NetworkObserver struct {
	events <-chan NetworkEvent
	cancel func()
}

type NetworkEvent struct {
	Kind            NetworkEventKind
	RequestID       string
	URL             string
	Method          string
	Status          int
	StatusText      string
	MIMEType        string
	ResourceType    string
	FrameID         string
	LoaderID        string
	ErrorText       string
	Canceled        bool
	EncodedDataSize float64
}

func defaultNetworkObserverConfig() networkObserverConfig {
	return networkObserverConfig{buffer: 32}
}

func WithNetworkObserverBuffer(size int) NetworkObserverOption {
	return func(config *networkObserverConfig) {
		config.buffer = size
	}
}

func (p *Page) ObserveNetwork(ctx context.Context, opts ...NetworkObserverOption) (*NetworkObserver, error) {
	config := defaultNetworkObserverConfig()
	for _, opt := range opts {
		opt(&config)
	}
	if config.buffer < 0 {
		config.buffer = 0
	}

	if err := cdpnetwork.Enable().Do(ctx, p.session); err != nil {
		return nil, fmt.Errorf("observe network: enable network domain: %w", err)
	}

	rawEvents, cancel := p.session.Subscribe(config.buffer)
	decoded := make(chan NetworkEvent, config.buffer)

	go func() {
		defer close(decoded)
		defer cancel()

		for event := range rawEvents {
			decodedEvent, ok, err := decodeNetworkEvent(event)
			if err != nil {
				return
			}
			if !ok {
				continue
			}

			select {
			case decoded <- decodedEvent:
			default:
			}
		}
	}()

	return &NetworkObserver{
		events: decoded,
		cancel: cancel,
	}, nil
}

func (o *NetworkObserver) Events() <-chan NetworkEvent {
	return o.events
}

func (o *NetworkObserver) Close() {
	if o.cancel != nil {
		o.cancel()
		o.cancel = nil
	}
}

func (p *Page) ResponseBody(ctx context.Context, requestID string) ([]byte, error) {
	result, err := cdpnetwork.GetResponseBody(cdpnetwork.RequestID(requestID)).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("get response body: %w", err)
	}

	if result.Base64Encoded {
		data, err := base64.StdEncoding.DecodeString(result.Body)
		if err != nil {
			return nil, fmt.Errorf("get response body: decode body: %w", err)
		}
		return data, nil
	}

	return []byte(result.Body), nil
}

func decodeNetworkEvent(event cdp.Event) (NetworkEvent, bool, error) {
	switch event.Method {
	case cdpnetwork.EventRequestWillBeSent:
		decoded, err := cdpnetwork.DecodeRequestWillBeSent(event.Params)
		if err != nil {
			return NetworkEvent{}, false, fmt.Errorf("decode network request event: %w", err)
		}
		return NetworkEvent{
			Kind:         NetworkEventRequestStarted,
			RequestID:    string(decoded.RequestID),
			URL:          decoded.Request.URL,
			Method:       decoded.Request.Method,
			ResourceType: string(decoded.Type),
			FrameID:      string(decoded.FrameID),
			LoaderID:     string(decoded.LoaderID),
		}, true, nil
	case cdpnetwork.EventResponseReceived:
		decoded, err := cdpnetwork.DecodeResponseReceived(event.Params)
		if err != nil {
			return NetworkEvent{}, false, fmt.Errorf("decode network response event: %w", err)
		}
		return NetworkEvent{
			Kind:         NetworkEventResponseReceived,
			RequestID:    string(decoded.RequestID),
			URL:          decoded.Response.URL,
			Status:       decoded.Response.Status,
			StatusText:   decoded.Response.StatusText,
			MIMEType:     decoded.Response.MimeType,
			ResourceType: string(decoded.Type),
			FrameID:      string(decoded.FrameID),
			LoaderID:     string(decoded.LoaderID),
		}, true, nil
	case cdpnetwork.EventLoadingFinished:
		decoded, err := cdpnetwork.DecodeLoadingFinished(event.Params)
		if err != nil {
			return NetworkEvent{}, false, fmt.Errorf("decode network loading finished event: %w", err)
		}
		return NetworkEvent{
			Kind:            NetworkEventLoadingFinished,
			RequestID:       string(decoded.RequestID),
			EncodedDataSize: decoded.EncodedDataLength,
		}, true, nil
	case cdpnetwork.EventLoadingFailed:
		decoded, err := cdpnetwork.DecodeLoadingFailed(event.Params)
		if err != nil {
			return NetworkEvent{}, false, fmt.Errorf("decode network loading failed event: %w", err)
		}
		return NetworkEvent{
			Kind:         NetworkEventLoadingFailed,
			RequestID:    string(decoded.RequestID),
			ResourceType: string(decoded.Type),
			ErrorText:    decoded.ErrorText,
			Canceled:     decoded.Canceled,
		}, true, nil
	default:
		return NetworkEvent{}, false, nil
	}
}
