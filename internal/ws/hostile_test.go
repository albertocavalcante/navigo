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
	"sync"
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

// A context with no deadline must still cancel a handshake the server stalls,
// and nothing may be left running afterwards.
func TestDialCancelledMidHandshake(t *testing.T) {
	// Not parallel: it samples the process-wide goroutine count.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	accepted := make(chan struct{})
	release := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		close(accepted)
		<-release // accept, read nothing useful, answer nothing
	}()

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(t.Context())

	result := make(chan error, 1)
	go func() {
		_, dialErr := Dial(ctx, "ws://"+listener.Addr().String()+"/")
		result <- dialErr
	}()

	<-accepted
	time.Sleep(50 * time.Millisecond) // let Dial block reading the response
	cancel()

	select {
	case dialErr := <-result:
		if !errors.Is(dialErr, context.Canceled) {
			t.Fatalf("Dial() error = %v, want context.Canceled", dialErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Dial() did not return after the context was canceled")
	}

	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines = %d, want <= %d", after, before)
	}
}

func TestCloseFrameEchoedOnceAndMalformedRejected(t *testing.T) {
	t.Parallel()

	code := []byte{0x03, 0xe8}

	tests := []struct {
		name     string
		payload  []byte
		wantEcho []byte
		wantErr  error
	}{
		{"empty payload", nil, []byte{opcodeClose}, io.EOF},
		{"status code only", code, append([]byte{opcodeClose}, code...), io.EOF},
		{"status code and reason is echoed as code", append(append([]byte{}, code...), "bye"...), append([]byte{opcodeClose}, code...), io.EOF},
		{"one-byte payload", []byte{0x03}, nil, ErrProtocol},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, echoed, err := readAll(t, serverFrame(true, opcodeClose, tt.payload), defaultLimits())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}

			if !bytes.Equal(echoed, tt.wantEcho) {
				t.Fatalf("echoed %q, want %q", echoed, tt.wantEcho)
			}
		})
	}
}

// Over a real pipe: the echo reaches the peer exactly once, Close afterwards
// adds no second frame, and a one-byte close fails the connection.
func TestConnEchoesCloseOnceAndCloseDoesNotBlock(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	conn := &Conn{conn: client, reader: bufio.NewReader(client), limits: defaultLimits()}

	go func() {
		_, _ = server.Write(serverFrame(true, opcodeClose, []byte{0x03, 0xe8}))
	}()

	frames := make(chan []byte, 4)
	go func() {
		defer close(frames)

		buf := make([]byte, 64)
		for {
			n, err := server.Read(buf)
			if n > 0 {
				frames <- append([]byte(nil), buf[:n]...)
			}

			if err != nil {
				return
			}
		}
	}()

	if _, err := conn.Read(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("Read() error = %v, want io.EOF", err)
	}

	first := <-frames
	if len(first) < 2 || first[0] != finalBit|opcodeClose {
		t.Fatalf("echo frame = %x, want a close frame", first)
	}

	closed := make(chan struct{})
	go func() {
		_ = conn.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() blocked")
	}

	if extra, ok := <-frames; ok {
		t.Fatalf("unexpected second frame %x after echo", extra)
	}
}

func TestCloseDoesNotBlockOnStuckWriter(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() { _ = server.Close() }()

	conn := &Conn{conn: client, reader: bufio.NewReader(client), limits: defaultLimits()}

	conn.writeMu.Lock() // a writer stuck mid-frame

	closed := make(chan struct{})
	go func() {
		_ = conn.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() blocked behind a stuck writer")
	}
}

func TestClosePayloadSanitizesCode(t *testing.T) {
	t.Parallel()

	be := func(code uint16, reason string) []byte {
		return append(binary.BigEndian.AppendUint16(nil, code), reason...)
	}

	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"empty", nil, nil},
		{"normal", be(1000, ""), be(1000, "")},
		{"reason dropped", be(1001, "secret reason"), be(1001, "")},
		{"app code", be(4000, "x"), be(4000, "")},
		{"1004 reserved", be(1004, ""), be(1000, "")},
		{"1005 never sent", be(1005, ""), be(1000, "")},
		{"1006 never sent", be(1006, ""), be(1000, "")},
		{"1015 never sent", be(1015, ""), be(1000, "")},
		{"2999 unassigned", be(2999, ""), be(1000, "")},
		{"5000 out of range", be(5000, ""), be(1000, "")},
		{"zero", be(0, ""), be(1000, "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := closePayload(tt.in); !bytes.Equal(got, tt.want) {
				t.Fatalf("closePayload(%x) = %x, want %x", tt.in, got, tt.want)
			}
		})
	}
}

// deadlineConn records the last write deadline set on it.
type deadlineConn struct {
	net.Conn

	mu   sync.Mutex
	last time.Time
}

func (d *deadlineConn) SetWriteDeadline(t time.Time) error {
	d.mu.Lock()
	d.last = t
	d.mu.Unlock()

	return d.Conn.SetWriteDeadline(t)
}

func (d *deadlineConn) lastDeadline() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.last
}

func TestSendAfterCloseFrameIsRefusedAndDeadlineCleared(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() { _ = server.Close() }()

	recorder := &deadlineConn{Conn: client}
	conn := &Conn{conn: recorder, reader: bufio.NewReader(client), limits: defaultLimits()}

	go func() { _, _ = io.Copy(io.Discard, server) }()

	if err := conn.sendClose(nil); err != nil {
		t.Fatalf("sendClose() error = %v", err)
	}

	if !recorder.lastDeadline().IsZero() {
		t.Fatalf("write deadline %v left set after the close frame", recorder.lastDeadline())
	}

	if err := conn.Send(t.Context(), []byte(`{}`)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send() after close error = %v, want ErrClosed", err)
	}

	if err := conn.writeControl(opcodePong, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("pong after close error = %v, want ErrClosed", err)
	}
}
