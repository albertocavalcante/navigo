package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

// serverFrame builds an unmasked server-to-client frame.
func serverFrame(fin bool, opcode byte, payload []byte) []byte {
	return rawFrame(boolByte(fin)|opcode, 0, payload)
}

func boolByte(fin bool) byte {
	if fin {
		return finalBit
	}

	return 0
}

// rawFrame writes b0 verbatim and a minimal length encoding with extra flag
// bits in the second byte.
func rawFrame(b0, b1flags byte, payload []byte) []byte {
	out := []byte{b0}

	switch n := len(payload); {
	case n <= 125:
		out = append(out, b1flags|byte(n))
	case n <= 0xFFFF:
		out = append(out, b1flags|126, byte(n>>8), byte(n))
	default:
		out = append(out, b1flags|127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}

	return append(out, payload...)
}

// declaredLength builds a header declaring length bytes with only body behind.
// Minimal length encoding; forced64 always uses the 64-bit form.
func declaredLength(opcode byte, length uint64, body []byte) []byte {
	switch {
	case length <= 125:
		return append([]byte{finalBit | opcode, byte(length)}, body...)
	case length <= 0xFFFF:
		return append([]byte{finalBit | opcode, 126, byte(length >> 8), byte(length)}, body...)
	}

	return forced64(opcode, length, body)
}

func forced64(opcode byte, length uint64, body []byte) []byte {
	out := []byte{finalBit | opcode, 127}
	out = binary.BigEndian.AppendUint64(out, length)

	return append(out, body...)
}

func readAll(t *testing.T, wire []byte, lim limits) ([]byte, []byte, error) {
	t.Helper()

	var pongs bytes.Buffer
	write := func(opcode byte, payload []byte) error {
		pongs.WriteByte(opcode)
		pongs.Write(payload)

		return nil
	}

	msg, err := readMessage(bufio.NewReader(bytes.NewReader(wire)), write, lim)

	return msg, pongs.Bytes(), err
}

func TestReadMessageRefusals(t *testing.T) {
	t.Parallel()

	small := defaultLimits()
	small.maxMessage = 1024
	small.maxFragments = 4
	small.maxControlFrame = 4

	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	many := func(n int, frame []byte) []byte { return bytes.Repeat(frame, n) }

	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{"declared length over cap, no body", declaredLength(opcodeText, 1<<40, nil), ErrMessageTooLarge},
		{"declared length over cap, short body", declaredLength(opcodeText, 1<<40, []byte("abc")), ErrMessageTooLarge},
		{"declared length just over cap", declaredLength(opcodeText, 1025, bytes.Repeat([]byte("a"), 1025)), ErrMessageTooLarge},
		{"length with top bit set", declaredLength(opcodeText, 1<<63, nil), ErrProtocol},
		{"126 marker carrying 5", []byte{finalBit | opcodeText, 126, 0, 5, 'a', 'b', 'c', 'd', 'e'}, ErrProtocol},
		{"127 marker carrying 0xFFFF", forced64(opcodeText, 0xFFFF, nil), ErrProtocol},
		{"127 marker carrying 125", forced64(opcodeText, 125, nil), ErrProtocol},
		{"max uint64 length", declaredLength(opcodeText, ^uint64(0), nil), ErrProtocol},
		{"size exceeded across fragments", cat(
			serverFrame(false, opcodeText, bytes.Repeat([]byte("a"), 600)),
			serverFrame(true, opcodeContinuation, bytes.Repeat([]byte("a"), 600))), ErrMessageTooLarge},
		{"fragment flood", cat(
			serverFrame(false, opcodeText, []byte("a")),
			many(10, serverFrame(false, opcodeContinuation, []byte("a")))), ErrTooManyFragments},
		{"ping flood", many(10, serverFrame(true, opcodePing, nil)), ErrTooManyControlFrames},
		{"pong flood", many(10, serverFrame(true, opcodePong, nil)), ErrTooManyControlFrames},
		{"control flood between fragments", cat(
			serverFrame(false, opcodeText, []byte("a")),
			many(10, serverFrame(true, opcodePing, nil))), ErrTooManyControlFrames},
		{"control payload over 125", serverFrame(true, opcodePing, bytes.Repeat([]byte("a"), 126)), ErrProtocol},
		{"fragmented control frame", serverFrame(false, opcodePing, []byte("a")), ErrProtocol},
		{"rsv1 set", rawFrame(finalBit|rsv1Bit|opcodeText, 0, []byte("a")), ErrProtocol},
		{"rsv2 set", rawFrame(finalBit|rsv2Bit|opcodeText, 0, []byte("a")), ErrProtocol},
		{"rsv3 set", rawFrame(finalBit|rsv3Bit|opcodeText, 0, []byte("a")), ErrProtocol},
		{"masked server frame", rawFrame(finalBit|opcodeText, maskBit, append([]byte{1, 2, 3, 4}, 'a')), ErrProtocol},
		{"invalid utf-8", serverFrame(true, opcodeText, []byte{0xff, 0xfe}), ErrProtocol},
		{"utf-8 split across fragments is valid only joined", cat(
			serverFrame(false, opcodeText, []byte{0xe2, 0x82}),
			serverFrame(true, opcodeContinuation, []byte{0x41})), ErrProtocol},
		{"continuation without start", serverFrame(true, opcodeContinuation, []byte("a")), ErrProtocol},
		{"new text inside fragmented message", cat(
			serverFrame(false, opcodeText, []byte("a")),
			serverFrame(true, opcodeText, []byte("b"))), ErrProtocol},
		{"binary opcode", serverFrame(true, 0x2, []byte("a")), ErrProtocol},
		{"reserved opcode", serverFrame(true, 0x3, nil), ErrProtocol},
		{"truncated mid-payload", declaredLength(opcodeText, 100, []byte("abc")), io.ErrUnexpectedEOF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := readAll(t, tt.wire, small)
			if !errors.Is(err, tt.want) {
				t.Fatalf("readMessage() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReadMessageAccepts(t *testing.T) {
	t.Parallel()

	lim := defaultLimits()
	lim.maxControlFrame = 2

	tests := []struct {
		name     string
		wire     []byte
		want     string
		wantPong string
	}{
		{"single", serverFrame(true, opcodeText, []byte("hi")), "hi", ""},
		{"extended 16-bit length", serverFrame(true, opcodeText, bytes.Repeat([]byte("a"), 300)), string(bytes.Repeat([]byte("a"), 300)), ""},
		{"fragmented with interleaved ping", bytes.Join([][]byte{
			serverFrame(false, opcodeText, []byte("he")),
			serverFrame(true, opcodePing, []byte("p")),
			serverFrame(true, opcodeContinuation, []byte("llo")),
		}, nil), "hello", string([]byte{opcodePong}) + "p"},
		{"multi-byte rune across fragments", bytes.Join([][]byte{
			serverFrame(false, opcodeText, []byte{0xe2, 0x82}),
			serverFrame(true, opcodeContinuation, []byte{0xac}),
		}, nil), "€", ""},
		{"controls reset by data frame", bytes.Join([][]byte{
			serverFrame(true, opcodePong, nil), serverFrame(true, opcodePong, nil),
			serverFrame(false, opcodeText, []byte("a")),
			serverFrame(true, opcodePong, nil), serverFrame(true, opcodePong, nil),
			serverFrame(true, opcodeContinuation, []byte("b")),
		}, nil), "ab", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, pongs, err := readAll(t, tt.wire, lim)
			if err != nil {
				t.Fatalf("readMessage() error = %v", err)
			}

			if string(got) != tt.want || string(pongs) != tt.wantPong {
				t.Fatalf("got %q pongs %q, want %q pongs %q", got, pongs, tt.want, tt.wantPong)
			}
		})
	}
}

func TestCloseFrameReturnsEOF(t *testing.T) {
	t.Parallel()

	_, _, err := readAll(t, serverFrame(true, opcodeClose, nil), defaultLimits())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want io.EOF", err)
	}
}

// A huge declared length with a short body must be refused without reserving
// anything near the declared size, both over the cap and (with a generous cap)
// while reading incrementally.
func TestOversizeDeclaredLengthDoesNotAllocate(t *testing.T) {
	// Not parallel: it samples process-wide allocation counters.
	cases := []struct {
		name string
		max  int64
	}{
		{"refused by cap", 1 << 20},
		{"incremental under huge cap", 1 << 40},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			lim := defaultLimits()
			lim.maxMessage = tt.max
			wire := declaredLength(opcodeText, 1<<32, []byte("short"))

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)

			_, _, err := readAll(t, wire, lim)

			runtime.ReadMemStats(&after)

			if err == nil {
				t.Fatal("readMessage() error = nil, want failure")
			}

			if grown := after.TotalAlloc - before.TotalAlloc; grown > 8<<20 {
				t.Fatalf("allocated %d bytes for a short hostile frame", grown)
			}
		})
	}
}

func TestOptionsOverrideDefaults(t *testing.T) {
	t.Parallel()

	lim := applyOptions([]Option{WithMaxMessageSize(10), WithMaxFragments(2), WithMaxControlFrames(3), WithMaxHandshakeBytes(99), WithMaxMessageSize(0), nil})
	if lim.maxMessage != 10 || lim.maxFragments != 2 || lim.maxControlFrame != 3 || lim.maxHandshake != 99 {
		t.Fatalf("limits = %+v", lim)
	}

	if def := applyOptions(nil); def.maxMessage != DefaultMaxMessageSize {
		t.Fatalf("default maxMessage = %d", def.maxMessage)
	}
}

func TestHandshakeHeadLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		wire string
		want error
	}{
		{"endless header line", "HTTP/1.1 101 Switching Protocols\r\nX: " + string(bytes.Repeat([]byte("a"), 5000)), ErrHandshakeTooLarge},
		{"many header lines", "HTTP/1.1 101 x\r\n" + string(bytes.Repeat([]byte("X: a\r\n"), 5000)), ErrHandshakeTooLarge},
		{"truncated", "HTTP/1.1 101 x\r\nX: a\r\n", io.EOF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := readHandshakeHead(bufio.NewReaderSize(bytes.NewReader([]byte(tt.wire)), 16), 1024)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}

	head, err := readHandshakeHead(bufio.NewReader(bytes.NewReader([]byte("HTTP/1.1 101 x\r\nA: b\r\n\r\nFRAME"))), 1024)
	if err != nil || string(head) != "HTTP/1.1 101 x\r\nA: b\r\n\r\n" {
		t.Fatalf("head = %q, err = %v", head, err)
	}
}

// A violation closes the connection and sticks: later reads fail with the same
// error instead of resynchronising on attacker-controlled bytes. It also checks
// that no helper goroutine outlives the failed reads.
func TestConnFailsClosedAndDoesNotLeakGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	client, server := net.Pipe()
	conn := &Conn{conn: client, reader: bufio.NewReader(client), limits: defaultLimits()}

	go func() {
		_, _ = server.Write(rawFrame(finalBit|rsv1Bit|opcodeText, 0, []byte("a")))
	}()

	_, first := conn.Read(t.Context())
	if !errors.Is(first, ErrProtocol) {
		t.Fatalf("first Read() error = %v, want ErrProtocol", first)
	}

	_, second := conn.Read(t.Context())
	if !errors.Is(second, ErrProtocol) {
		t.Fatalf("second Read() error = %v, want sticky ErrProtocol", second)
	}

	_ = server.Close()

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines = %d, want <= %d", after, before)
	}
}

func FuzzReadMessage(f *testing.F) {
	f.Add(serverFrame(true, opcodeText, []byte("hello")))
	f.Add(serverFrame(false, opcodeText, []byte("he")))
	f.Add(serverFrame(true, opcodePing, []byte("p")))
	f.Add(declaredLength(opcodeText, 1<<40, []byte("x")))
	f.Add(rawFrame(finalBit|rsv1Bit|opcodeText, maskBit, []byte("abcde")))

	const limit = 4096

	lim := limits{maxMessage: limit, maxFragments: 8, maxControlFrame: 8, maxHandshake: 1024}

	f.Fuzz(func(t *testing.T, wire []byte) {
		write := func(_ byte, payload []byte) error {
			if len(payload) > maxControlPayload {
				t.Fatalf("pong payload %d bytes", len(payload))
			}

			return nil
		}

		r := bufio.NewReader(bytes.NewReader(wire))
		for range 16 {
			msg, err := readMessage(r, write, lim)
			if err != nil {
				return
			}

			if len(msg) > limit {
				t.Fatalf("message of %d bytes exceeds cap %d", len(msg), limit)
			}
		}
	})
}

func TestDialRefusesOversizeHandshake(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n"))
		for range 100 {
			if _, writeErr := conn.Write(bytes.Repeat([]byte("X-Pad: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\r\n"), 20)); writeErr != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err = Dial(ctx, "ws://"+listener.Addr().String()+"/", WithMaxHandshakeBytes(2048))
	if !errors.Is(err, ErrHandshakeTooLarge) {
		t.Fatalf("Dial() error = %v, want ErrHandshakeTooLarge", err)
	}
}
