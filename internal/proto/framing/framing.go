// Package framing splits a byte stream into frames and writes frames to a stream.
//
// Every frame is a 12 byte header followed by the payload: an 8 byte ASCII identifier
// that names the protocol, and the payload size as unsigned 32 bit little endian. The
// payload itself (a MessagePack object) is not interpreted here.
package framing

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HeaderSize is the size of the frame header in bytes.
const HeaderSize = 12

// DefaultMaxPayload limits the payload size of a received frame unless the caller sets
// another limit. A larger size in a header is treated as a broken stream.
const DefaultMaxPayload = 1 << 20

var (
	// ErrIdentifier means the header does not start with the expected identifier. The
	// stream cannot be resynchronised after that, the connection has to be closed.
	ErrIdentifier = errors.New("framing: unexpected identifier")
	// ErrTooLarge means the header announces a payload above the limit.
	ErrTooLarge = errors.New("framing: payload too large")
)

// Identifier is the 8 byte protocol identifier at the start of every header.
type Identifier [8]byte

// ParseIdentifier checks that s has exactly 8 ASCII characters.
func ParseIdentifier(s string) (Identifier, error) {
	var id Identifier
	if len(s) != len(id) {
		return id, fmt.Errorf("framing: identifier %q is not 8 bytes", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return id, fmt.Errorf("framing: identifier %q is not printable ASCII", s)
		}
	}
	copy(id[:], s)
	return id, nil
}

func (id Identifier) String() string { return string(id[:]) }

// Header builds the 12 byte header for a payload of the given size.
func Header(id Identifier, size int) ([]byte, error) {
	if size < 0 || uint64(size) > 0xFFFFFFFF {
		return nil, ErrTooLarge
	}
	h := make([]byte, HeaderSize)
	copy(h, id[:])
	binary.LittleEndian.PutUint32(h[8:], uint32(size))
	return h, nil
}

// Writer writes frames. It is not safe for concurrent use, callers serialise writes.
type Writer struct {
	w  io.Writer
	id Identifier
}

// NewWriter returns a Writer that prefixes every payload with a header.
func NewWriter(w io.Writer, id Identifier) *Writer { return &Writer{w: w, id: id} }

// WriteFrame writes header and payload with a single Write call, so that a frame is never
// interleaved with another one on the same connection.
func (fw *Writer) WriteFrame(payload []byte) error {
	h, err := Header(fw.id, len(payload))
	if err != nil {
		return err
	}
	_, err = fw.w.Write(append(h, payload...))
	return err
}

// Reader reads frames from a stream.
type Reader struct {
	r          *bufio.Reader
	id         Identifier
	MaxPayload int
}

// NewReader returns a Reader that expects the given identifier in every header.
func NewReader(r io.Reader, id Identifier) *Reader {
	return &Reader{r: bufio.NewReader(r), id: id, MaxPayload: DefaultMaxPayload}
}

// ReadFrame returns the payload of the next frame. It returns io.EOF when the stream ends
// cleanly between two frames and io.ErrUnexpectedEOF when it ends inside a frame.
func (fr *Reader) ReadFrame() ([]byte, error) {
	var h [HeaderSize]byte
	if _, err := io.ReadFull(fr.r, h[:]); err != nil {
		return nil, err
	}
	if [8]byte(h[:8]) != fr.id {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrIdentifier, printable(h[:8]), fr.id)
	}
	size := binary.LittleEndian.Uint32(h[8:])
	if uint64(size) > uint64(fr.MaxPayload) {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, size, fr.MaxPayload)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(fr.r, payload); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return payload, nil
}

func printable(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c < 0x20 || c > 0x7e {
			c = '.'
		}
		out[i] = c
	}
	return string(out)
}
