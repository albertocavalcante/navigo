package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type Conn struct {
	transport Transport

	nextID atomic.Int64

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[responseKey]chan response

	sessionsMu sync.Mutex
	sessions   map[string]*Session

	closeOnce sync.Once
}

type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

type responseKey struct {
	id        int64
	sessionID string
}

type response struct {
	msg message
	err error
}

func NewConn(transport Transport) *Conn {
	conn := &Conn{
		transport: transport,
		pending:   make(map[responseKey]chan response),
		sessions:  make(map[string]*Session),
	}
	conn.sessions[""] = &Session{conn: conn, subscribers: make(map[int]*subscriber)}
	return conn
}

func (c *Conn) RootSession() *Session {
	return c.Session("")
}

func (c *Conn) Session(id string) *Session {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	if session, ok := c.sessions[id]; ok {
		return session
	}

	session := &Session{conn: c, id: id, subscribers: make(map[int]*subscriber)}
	c.sessions[id] = session

	return session
}

func (c *Conn) Run(ctx context.Context) (runErr error) {
	defer c.closeSessions()
	defer func() {
		if runErr == nil {
			runErr = io.EOF
		}
		c.failPending(runErr)
	}()

	for {
		payload, err := c.transport.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				runErr = ctx.Err()
				return runErr
			}

			runErr = err
			return runErr
		}

		var msg message
		if err := json.Unmarshal(payload, &msg); err != nil {
			runErr = fmt.Errorf("decode CDP message: %w", err)
			return runErr
		}

		if msg.ID != 0 {
			c.resolvePending(msg)
			continue
		}

		c.Session(msg.SessionID).publish(Event{
			Method:    msg.Method,
			SessionID: msg.SessionID,
			Params:    msg.Params,
		})
	}
}

func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.transport.Close()
	})

	return err
}

func (c *Conn) send(ctx context.Context, msg message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal CDP message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.transport.Send(ctx, payload); err != nil {
		return fmt.Errorf("send CDP message: %w", err)
	}

	return nil
}

func (c *Conn) nextMessageID() int64 {
	return c.nextID.Add(1)
}

func (c *Conn) registerPending(key responseKey, ch chan response) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	c.pending[key] = ch
}

func (c *Conn) unregisterPending(key responseKey) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	delete(c.pending, key)
}

func (c *Conn) resolvePending(msg message) {
	key := responseKey{id: msg.ID, sessionID: msg.SessionID}

	c.pendingMu.Lock()
	ch, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.pendingMu.Unlock()

	if ok {
		ch <- response{msg: msg}
	}
}

func (c *Conn) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	for key, ch := range c.pending {
		delete(c.pending, key)
		ch <- response{err: err}
	}
}

func (c *Conn) closeSessions() {
	c.sessionsMu.Lock()
	sessions := make([]*Session, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.sessionsMu.Unlock()

	for _, session := range sessions {
		session.closeSubscribers()
	}
}
