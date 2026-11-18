package ws

import (
	"errors"
	"fmt"
)

// Default limits applied to every connection. They are generous for any
// legitimate DevTools traffic (the largest messages are base64 screenshots,
// PDFs and accessibility snapshots) while keeping a hostile peer from
// driving unbounded memory or CPU use.
const (
	// DefaultMaxMessageSize is the largest reassembled message accepted.
	DefaultMaxMessageSize = 64 << 20
	// DefaultMaxFragments is the most frames a single message may span.
	DefaultMaxFragments = 1024
	// DefaultMaxControlFrames is the most consecutive control frames (ping,
	// pong, close) accepted without an intervening data frame.
	DefaultMaxControlFrames = 256
	// DefaultMaxHandshakeBytes is the most bytes read for the HTTP upgrade
	// response head.
	DefaultMaxHandshakeBytes = 16 << 10

	maxControlPayload = 125
	readChunkSize     = 64 << 10
)

var (
	// ErrMessageTooLarge reports a frame or message above the size limit.
	ErrMessageTooLarge = errors.New("websocket message exceeds size limit")
	// ErrTooManyFragments reports a message split over too many frames.
	ErrTooManyFragments = errors.New("websocket message has too many fragments")
	// ErrTooManyControlFrames reports a flood of control frames.
	ErrTooManyControlFrames = errors.New("websocket peer sent too many consecutive control frames")
	// ErrProtocol reports a violation of RFC 6455 by the peer.
	ErrProtocol = errors.New("websocket protocol violation")
	// ErrHandshakeTooLarge reports an oversized upgrade response head.
	ErrHandshakeTooLarge = errors.New("websocket handshake response exceeds size limit")
)

func protocolError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, args...))
}

type limits struct {
	maxMessage      int64
	maxFragments    int
	maxControlFrame int
	maxHandshake    int
}

func defaultLimits() limits {
	return limits{
		maxMessage:      DefaultMaxMessageSize,
		maxFragments:    DefaultMaxFragments,
		maxControlFrame: DefaultMaxControlFrames,
		maxHandshake:    DefaultMaxHandshakeBytes,
	}
}

// Option customizes a connection. Non-positive values keep the default.
type Option func(*limits)

// WithMaxMessageSize bounds the size of a reassembled message in bytes.
func WithMaxMessageSize(n int64) Option {
	return func(l *limits) {
		if n > 0 {
			l.maxMessage = n
		}
	}
}

// WithMaxFragments bounds the number of frames in one message.
func WithMaxFragments(n int) Option {
	return func(l *limits) {
		if n > 0 {
			l.maxFragments = n
		}
	}
}

// WithMaxControlFrames bounds consecutive control frames.
func WithMaxControlFrames(n int) Option {
	return func(l *limits) {
		if n > 0 {
			l.maxControlFrame = n
		}
	}
}

// WithMaxHandshakeBytes bounds the HTTP upgrade response head.
func WithMaxHandshakeBytes(n int) Option {
	return func(l *limits) {
		if n > 0 {
			l.maxHandshake = n
		}
	}
}

func applyOptions(opts []Option) limits {
	l := defaultLimits()
	for _, opt := range opts {
		if opt != nil {
			opt(&l)
		}
	}

	return l
}
