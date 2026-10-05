// Package framing implements the length-prefixed frames shared by the Edgehop
// wire protocol (docs/edgehop-protocol.md §3): a u32 big-endian length
// followed by that many payload bytes. Frames larger than MaxFrame (1 MiB) are
// rejected on both the read and the write side.
package framing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxFrame is the largest payload (in bytes) a frame may carry.
const MaxFrame = 1 << 20

// ErrTooLarge is returned when a payload exceeds MaxFrame, either because the
// caller asked Write to send one or because a peer announced one.
var ErrTooLarge = errors.New("framing: frame exceeds 1 MiB")

// Write sends one frame: a 4-byte big-endian length followed by payload. A
// zero-length payload is a valid frame. The header and payload are written in
// a single Write call so that a concurrent-safe writer sees one frame.
func Write(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrame {
		return ErrTooLarge
	}
	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(len(payload)))
	copy(buf[4:], payload)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("framing: write: %w", err)
	}
	return nil
}

// Read reads one frame and returns its payload. A short read of the header or
// the payload returns io.ErrUnexpectedEOF (or io.EOF when the stream ends
// cleanly before a header). A length above MaxFrame returns ErrTooLarge
// without consuming the payload; the caller must then close the stream.
func Read(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("framing: read header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return nil, ErrTooLarge
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("framing: read payload: %w", err)
	}
	return payload, nil
}
