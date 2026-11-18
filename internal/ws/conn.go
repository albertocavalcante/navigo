package ws

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	"unicode/utf8"
)

type Conn struct {
	conn   net.Conn
	reader *bufio.Reader
	limits limits

	writeMu sync.Mutex

	failMu sync.Mutex
	failed error
}

// Dial opens a WebSocket connection. Options tighten or relax the default
// resource limits applied to the peer.
func Dial(ctx context.Context, rawURL string, opts ...Option) (*Conn, error) {
	lim := applyOptions(opts)

	conn, reader, err := dialWebSocket(ctx, rawURL, lim)
	if err != nil {
		return nil, err
	}

	return &Conn{conn: conn, reader: reader, limits: lim}, nil
}

func (c *Conn) Send(ctx context.Context, data []byte) error {
	restoreDeadline, err := prepareWrite(ctx, c.conn)
	if err != nil {
		return err
	}
	defer restoreDeadline()

	if err := c.writeFrame(opcodeText, data); err != nil {
		return fmt.Errorf("send websocket frame: %w", err)
	}

	return nil
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return writeClientFrame(c.conn, opcode, payload)
}

// Read returns the next complete text message. A limit or protocol violation
// by the peer closes the connection and is returned by every later Read.
func (c *Conn) Read(ctx context.Context) ([]byte, error) {
	if err := c.failure(); err != nil {
		return nil, err
	}

	restoreDeadline, err := prepareRead(ctx, c.conn)
	if err != nil {
		return nil, err
	}
	defer restoreDeadline()

	payload, err := readMessage(c.reader, c.writeFrame, c.limits)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}

		err = fmt.Errorf("read websocket frame: %w", err)
		if isPeerViolation(err) {
			c.fail(err)
		}

		return nil, err
	}

	return payload, nil
}

func isPeerViolation(err error) bool {
	return errors.Is(err, ErrProtocol) || errors.Is(err, ErrMessageTooLarge) ||
		errors.Is(err, ErrTooManyFragments) || errors.Is(err, ErrTooManyControlFrames)
}

func (c *Conn) failure() error {
	c.failMu.Lock()
	defer c.failMu.Unlock()

	return c.failed
}

func (c *Conn) fail(err error) {
	c.failMu.Lock()
	c.failed = err
	c.failMu.Unlock()

	_ = c.conn.Close()
}

// readMessage assembles the next text message from r, answering pings through
// writeFrame. It enforces every limit before allocating for the data.
func readMessage(r *bufio.Reader, writeFrame func(opcode byte, payload []byte) error, lim limits) ([]byte, error) {
	var (
		message   []byte
		inMessage bool
		fragments int
		controls  int
	)

	for {
		budget := lim.maxMessage - int64(len(message))

		f, err := readFrame(r, budget)
		if err != nil {
			if errors.Is(err, io.EOF) && (inMessage || len(message) > 0) {
				return nil, io.ErrUnexpectedEOF
			}

			return nil, err
		}

		switch f.opcode {
		case opcodePing, opcodePong, opcodeClose:
			controls++
			if controls > lim.maxControlFrame {
				return nil, ErrTooManyControlFrames
			}

			switch f.opcode {
			case opcodePing:
				if err := writeFrame(opcodePong, f.payload); err != nil {
					return nil, fmt.Errorf("respond to websocket ping: %w", err)
				}
			case opcodeClose:
				return nil, io.EOF
			}

			continue
		case opcodeText, opcodeContinuation:
			controls = 0
			if (f.opcode == opcodeText) == inMessage {
				return nil, protocolError("unexpected data frame opcode %#x", f.opcode)
			}

			fragments++
			if fragments > lim.maxFragments {
				return nil, ErrTooManyFragments
			}

			if !inMessage {
				inMessage = true
				message = f.payload
			} else {
				message = append(message, f.payload...)
			}

			if !f.fin {
				continue
			}

			if !utf8.Valid(message) {
				return nil, protocolError("text message is not valid UTF-8")
			}

			return message, nil
		default:
			return nil, protocolError("unsupported opcode %#x", f.opcode)
		}
	}
}

func (c *Conn) Close() error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_ = c.writeFrame(opcodeClose, nil)

	return c.conn.Close()
}

func prepareRead(ctx context.Context, conn net.Conn) (func(), error) {
	return prepareDeadline(ctx, conn.SetReadDeadline)
}

func prepareWrite(ctx context.Context, conn net.Conn) (func(), error) {
	return prepareDeadline(ctx, conn.SetWriteDeadline)
}

func prepareDeadline(ctx context.Context, setDeadline func(time.Time) error) (func(), error) {
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		if err := setDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set connection deadline: %w", err)
		}
	}

	done := ctx.Done()
	if done == nil {
		return func() {
			_ = setDeadline(time.Time{})
		}, nil
	}

	stop := make(chan struct{})
	go func() {
		select {
		case <-done:
			_ = setDeadline(time.Now())
		case <-stop:
		}
	}()

	return func() {
		close(stop)
		_ = setDeadline(time.Time{})
	}, nil
}
