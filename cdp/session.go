package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type Event struct {
	Method    string
	SessionID string
	Params    json.RawMessage
}

type Session struct {
	conn *Conn
	id   string

	mu          sync.RWMutex
	subscribers map[int]*subscriber
	nextID      int
}

type subscriber struct {
	ch chan Event
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) Call(ctx context.Context, method string, params any, result any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return fmt.Errorf("marshal CDP params for %s: %w", method, err)
	}

	envelope := message{
		ID:        s.conn.nextMessageID(),
		Method:    method,
		SessionID: s.id,
		Params:    rawParams,
	}

	responseCh := make(chan response, 1)
	key := responseKey{id: envelope.ID, sessionID: s.id}
	s.conn.registerPending(key, responseCh)
	defer s.conn.unregisterPending(key)

	if err := s.conn.send(ctx, envelope); err != nil {
		return fmt.Errorf("send CDP command %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for CDP command %s: %w", method, ctx.Err())
	case response := <-responseCh:
		if response.err != nil {
			return fmt.Errorf("wait for CDP command %s: %w", method, response.err)
		}

		if response.msg.Error != nil {
			return response.msg.Error
		}

		if result == nil || len(response.msg.Result) == 0 {
			return nil
		}

		if err := json.Unmarshal(response.msg.Result, result); err != nil {
			return fmt.Errorf("decode CDP result for %s: %w", method, err)
		}

		return nil
	}
}

func (s *Session) ContextCall(ctx context.Context, method string, params any, result any) error {
	return s.Call(ctx, method, params, result)
}

func (s *Session) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer < 0 {
		buffer = 0
	}

	ch := make(chan Event, buffer)

	s.mu.Lock()
	id := s.nextID
	s.nextID++
	if s.subscribers == nil {
		s.subscribers = make(map[int]*subscriber)
	}
	s.subscribers[id] = &subscriber{ch: ch}
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		subscriber, ok := s.subscribers[id]
		if ok {
			delete(s.subscribers, id)
			close(subscriber.ch)
		}
		s.mu.Unlock()
	}

	return ch, cancel
}

func (s *Session) publish(event Event) {
	s.mu.RLock()
	for _, subscriber := range s.subscribers {
		select {
		case subscriber.ch <- event:
		default:
		}
	}
	s.mu.RUnlock()
}

func (s *Session) closeSubscribers() {
	s.mu.Lock()
	subscribers := make([]*subscriber, 0, len(s.subscribers))
	for id, subscriber := range s.subscribers {
		delete(s.subscribers, id)
		subscribers = append(subscribers, subscriber)
	}
	s.mu.Unlock()

	for _, subscriber := range subscribers {
		close(subscriber.ch)
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	return raw, nil
}
