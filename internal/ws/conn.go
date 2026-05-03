package ws

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

type Conn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func Dial(ctx context.Context, rawURL string) (*Conn, error) {
	conn, reader, err := dialWebSocket(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	return &Conn{conn: conn, reader: reader}, nil
}

func (c *Conn) Send(ctx context.Context, data []byte) error {
	restoreDeadline, err := prepareWrite(ctx, c.conn)
	if err != nil {
		return err
	}
	defer restoreDeadline()

	if err := writeClientFrame(c.conn, opcodeText, data); err != nil {
		return fmt.Errorf("send websocket frame: %w", err)
	}

	return nil
}

func (c *Conn) Read(ctx context.Context) ([]byte, error) {
	restoreDeadline, err := prepareRead(ctx, c.conn)
	if err != nil {
		return nil, err
	}
	defer restoreDeadline()

	for {
		frame, err := readFrame(c.reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}

			return nil, fmt.Errorf("read websocket frame: %w", err)
		}

		switch frame.opcode {
		case opcodeText:
			return frame.payload, nil
		case opcodePing:
			if err := writeClientFrame(c.conn, opcodePong, frame.payload); err != nil {
				return nil, fmt.Errorf("respond to websocket ping: %w", err)
			}
		case opcodePong:
			continue
		case opcodeClose:
			return nil, io.EOF
		default:
			return nil, fmt.Errorf("read websocket frame: unsupported opcode %#x", frame.opcode)
		}
	}
}

func (c *Conn) Close() error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_ = writeClientFrame(c.conn, opcodeClose, nil)

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
