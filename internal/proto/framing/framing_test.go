package framing

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// The tests use an invented identifier, the real ones are configured by the schema files.
var testID = mustID("TESTID01")

func mustID(s string) Identifier {
	id, err := ParseIdentifier(s)
	if err != nil {
		panic(err)
	}
	return id
}

func TestHeaderLayout(t *testing.T) {
	h, err := Header(testID, 0x01020304)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'T', 'E', 'S', 'T', 'I', 'D', '0', '1', 0x04, 0x03, 0x02, 0x01}
	if !bytes.Equal(h, want) {
		t.Fatalf("header % x, want % x", h, want)
	}
}

func TestRoundTripSeveralFrames(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, testID)
	frames := [][]byte{{0x80}, {}, bytes.Repeat([]byte{0xAB}, 5000)}
	for _, f := range frames {
		if err := w.WriteFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	// One byte at a time, as a slow TCP stream may deliver it.
	r := NewReader(iotest.OneByteReader(&buf), testID)
	for i, want := range frames {
		got, err := r.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d: %d bytes, want %d", i, len(got), len(want))
		}
	}
	if _, err := r.ReadFrame(); err != io.EOF {
		t.Fatalf("after the last frame: %v, want io.EOF", err)
	}
}

func TestWrongIdentifier(t *testing.T) {
	var buf bytes.Buffer
	_ = NewWriter(&buf, mustID("OTHERX01")).WriteFrame([]byte{1})
	_, err := NewReader(&buf, testID).ReadFrame()
	if !errors.Is(err, ErrIdentifier) {
		t.Fatalf("err = %v, want ErrIdentifier", err)
	}
}

func TestTooLarge(t *testing.T) {
	h, _ := Header(testID, 100)
	r := NewReader(bytes.NewReader(h), testID)
	r.MaxPayload = 99
	if _, err := r.ReadFrame(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestTruncated(t *testing.T) {
	h, _ := Header(testID, 10)
	stream := append(h, 1, 2, 3)
	if _, err := NewReader(bytes.NewReader(stream), testID).ReadFrame(); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated payload: %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := NewReader(bytes.NewReader(h[:5]), testID).ReadFrame(); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated header: %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestParseIdentifier(t *testing.T) {
	for _, bad := range []string{"SHORT", "TOOLONGXX", "TAB\tXXXX"} {
		if _, err := ParseIdentifier(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
