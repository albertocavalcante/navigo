package ws

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	opcodeText  = 0x1
	opcodeClose = 0x8
	opcodePing  = 0x9
	opcodePong  = 0xA

	finalBit  = 0x80
	maskBit   = 0x80
	maxHeader = 14
)

type frame struct {
	opcode  byte
	payload []byte
}

func readFrame(r *bufio.Reader) (frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}

	f := frame{opcode: header[0] & 0x0F}
	if header[0]&finalBit == 0 {
		return frame{}, fmt.Errorf("read websocket frame: fragmented frames are not supported")
	}

	masked := header[1]&maskBit != 0
	length, err := readPayloadLength(r, header[1]&0x7F)
	if err != nil {
		return frame{}, err
	}

	if length == 0 {
		return f, nil
	}

	f.payload = make([]byte, length)
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return frame{}, fmt.Errorf("read websocket mask: %w", err)
		}
	}

	if _, err := io.ReadFull(r, f.payload); err != nil {
		return frame{}, fmt.Errorf("read websocket payload: %w", err)
	}

	if masked {
		maskPayload(f.payload, maskKey)
	}

	return f, nil
}

func writeClientFrame(w io.Writer, opcode byte, payload []byte) error {
	header, maskKey, err := buildHeader(opcode, len(payload), true)
	if err != nil {
		return err
	}

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write websocket header: %w", err)
	}

	masked := append([]byte(nil), payload...)
	maskPayload(masked, maskKey)

	if len(masked) == 0 {
		return nil
	}

	if _, err := w.Write(masked); err != nil {
		return fmt.Errorf("write websocket payload: %w", err)
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

func readPayloadLength(r io.Reader, marker byte) (int, error) {
	switch marker {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, fmt.Errorf("read websocket extended length: %w", err)
		}

		return int(binary.BigEndian.Uint16(ext[:])), nil
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, fmt.Errorf("read websocket extended length: %w", err)
		}

		length := binary.BigEndian.Uint64(ext[:])
		if length > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("read websocket extended length: frame too large")
		}

		return int(length), nil
	default:
		return int(marker), nil
	}
}

func maskPayload(payload []byte, key [4]byte) {
	for index := range payload {
		payload[index] ^= key[index%len(key)]
	}
}
