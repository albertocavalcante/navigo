package ws

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestClientFrameRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte("hello, world")
	buffer := bytes.NewBuffer(nil)

	if err := writeClientFrame(buffer, opcodeText, payload); err != nil {
		t.Fatalf("writeClientFrame() error = %v", err)
	}

	frame, err := readFrame(bufio.NewReader(buffer))
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}

	if frame.opcode != opcodeText {
		t.Fatalf("frame.opcode = %#x, want %#x", frame.opcode, opcodeText)
	}

	if got := string(frame.payload); got != string(payload) {
		t.Fatalf("frame.payload = %q, want %q", got, payload)
	}
}

func TestBuildHeaderLargePayload(t *testing.T) {
	t.Parallel()

	header, _, err := buildHeader(opcodeText, 70000, true)
	if err != nil {
		t.Fatalf("buildHeader() error = %v", err)
	}

	if got, want := len(header), 14; got != want {
		t.Fatalf("len(header) = %d, want %d", got, want)
	}
}

func TestReadContextCancellationUsesReadDeadlineOnly(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	conn := &Conn{conn: client, reader: bufio.NewReader(client)}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := conn.Read(ctx)
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("conn.Read() error = nil, want cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for conn.Read cancellation")
	}
}
