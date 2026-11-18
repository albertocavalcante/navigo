package ws

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

const (
	opcodeText  = 0x1
	opcodeClose = 0x8
	opcodePing  = 0x9
	opcodePong  = 0xA

	opcodeContinuation = 0x0

	finalBit  = 0x80
	rsv1Bit   = 0x40
	rsv2Bit   = 0x20
	rsv3Bit   = 0x10
	maskBit   = 0x80
	maxHeader = 14
)

type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// readFrame reads one frame. budget is the most payload bytes the caller is
// willing to accept for this frame; the declared length is checked against it
// before any allocation, and the payload buffer grows only as bytes arrive.
func readFrame(r *bufio.Reader, budget int64) (frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}

	if header[0]&(rsv1Bit|rsv2Bit|rsv3Bit) != 0 {
		return frame{}, protocolError("reserved bits set without a negotiated extension")
	}

	f := frame{fin: header[0]&finalBit != 0, opcode: header[0] & 0x0F}
	control := f.opcode&0x8 != 0

	if header[1]&maskBit != 0 {
		return frame{}, protocolError("server frame is masked")
	}

	length, err := readPayloadLength(r, header[1]&0x7F)
	if err != nil {
		return frame{}, err
	}

	if control {
		if !f.fin {
			return frame{}, protocolError("fragmented control frame")
		}

		if length > maxControlPayload {
			return frame{}, protocolError("control frame payload of %d bytes exceeds %d", length, maxControlPayload)
		}
	}

	if budget < 0 || length > uint64(budget) {
		return frame{}, fmt.Errorf("%w: frame declares %d bytes, at most %d allowed", ErrMessageTooLarge, length, max(budget, 0))
	}

	if length == 0 {
		return f, nil
	}

	f.payload, err = readPayload(r, int64(length))
	if err != nil {
		return frame{}, fmt.Errorf("read websocket payload: %w", err)
	}

	return f, nil
}

// readPayload reads exactly n bytes, growing the buffer in bounded steps so a
// short body behind a large declared length never reserves the declared size.
func readPayload(r io.Reader, n int64) ([]byte, error) {
	buf := make([]byte, 0, min(n, readChunkSize))
	for int64(len(buf)) < n {
		chunk := int(min(n-int64(len(buf)), readChunkSize))
		buf = slices.Grow(buf, chunk)
		start := len(buf)
		buf = buf[:start+chunk]

		_, err := io.ReadFull(r, buf[start:])
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}

			return nil, err
		}
	}

	return buf, nil
}

func writeClientFrame(w io.Writer, opcode byte, payload []byte) error {
	header, maskKey, err := buildHeader(opcode, len(payload), true)
	if err != nil {
		return err
	}

	// One Write per frame keeps frames contiguous on the wire.
	out := make([]byte, 0, len(header)+len(payload))
	out = append(out, header...)
	out = append(out, payload...)
	maskPayload(out[len(header):], maskKey)

	if _, err := w.Write(out); err != nil {
		return fmt.Errorf("write websocket frame: %w", err)
	}

	return nil
}

func buildHeader(opcode byte, payloadLen int, masked bool) ([]byte, [4]byte, error) {
	if payloadLen < 0 {
		return nil, [4]byte{}, fmt.Errorf("build websocket header: negative payload length")
	}

	header := make([]byte, 0, maxHeader)
	header = append(header, finalBit|opcode)

	maskKey := [4]byte{}
	maskFlag := byte(0)
	if masked {
		maskFlag = maskBit
		if _, err := rand.Read(maskKey[:]); err != nil {
			return nil, [4]byte{}, fmt.Errorf("generate websocket mask: %w", err)
		}
	}

	switch {
	case payloadLen <= 125:
		header = append(header, maskFlag|byte(payloadLen))
	case payloadLen <= 65535:
		header = append(header, maskFlag|126)

		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(payloadLen))
		header = append(header, ext[:]...)
	default:
		header = append(header, maskFlag|127)

		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(payloadLen))
		header = append(header, ext[:]...)
	}

	if masked {
		header = append(header, maskKey[:]...)
	}

	return header, maskKey, nil
}

func readPayloadLength(r io.Reader, marker byte) (uint64, error) {
	switch marker {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, fmt.Errorf("read websocket extended length: %w", err)
		}

		length := uint64(binary.BigEndian.Uint16(ext[:]))
		if length < 126 {
			return 0, protocolError("non-minimal 16-bit payload length %d", length)
		}

		return length, nil
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, fmt.Errorf("read websocket extended length: %w", err)
		}

		length := binary.BigEndian.Uint64(ext[:])
		if length>>63 != 0 {
			return 0, protocolError("payload length has the most significant bit set")
		}

		if length <= 0xFFFF {
			return 0, protocolError("non-minimal 64-bit payload length %d", length)
		}

		return length, nil
	default:
		return uint64(marker), nil
	}
}

func maskPayload(payload []byte, key [4]byte) {
	for index := range payload {
		payload[index] ^= key[index%len(key)]
	}
}
