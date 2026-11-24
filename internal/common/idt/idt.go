// Package idt converts mioty identifiers and values between their binary form (as
// carried in BSSCI and SCACI) and their single strict text form (as used in JSON
// interfaces, configuration and logs). Parsing is strict: only the one text form is
// accepted, anything else is an error.
package idt

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// EUI64 is an IEEE EUI-64. The text form is upper case hex, most significant octet
// first, a hyphen between octets: 70-B3-D5-67-70-11-15-0A.
type EUI64 uint64

// String returns the text form.
func (e EUI64) String() string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(e))
	var sb strings.Builder
	for i, c := range b {
		if i > 0 {
			sb.WriteByte('-')
		}
		sb.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
	}
	return sb.String()
}

// Bytes returns the 8 octets, most significant first.
func (e EUI64) Bytes() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(e))
	return b
}

// ParseEUI64 parses the strict text form.
func ParseEUI64(s string) (EUI64, error) {
	if len(s) != 23 {
		return 0, fmt.Errorf("idt: EUI-64 %q: want 23 characters like 70-B3-D5-67-70-11-15-0A", s)
	}
	var v uint64
	for i := 0; i < 8; i++ {
		p := s[i*3 : i*3+2]
		if i < 7 && s[i*3+2] != '-' {
			return 0, fmt.Errorf("idt: EUI-64 %q: want '-' between octets", s)
		}
		if !isUpperHex(p) {
			return 0, fmt.Errorf("idt: EUI-64 %q: octets are two upper case hex digits", s)
		}
		b, _ := hex.DecodeString(p)
		v = v<<8 | uint64(b[0])
	}
	return EUI64(v), nil
}

// EUI64FromBytes reads 8 octets, most significant first.
func EUI64FromBytes(b []byte) (EUI64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("idt: EUI-64 needs 8 bytes, got %d", len(b))
	}
	return EUI64(binary.BigEndian.Uint64(b)), nil
}

// IsLocal reports whether the universal/local bit is set (locally administered).
func (e EUI64) IsLocal() bool { return uint64(e)>>56&0x02 != 0 }

// IsGroup reports whether the individual/group bit is set.
func (e EUI64) IsGroup() bool { return uint64(e)>>56&0x01 != 0 }

// Key is a 128 bit AES key. Text form: 32 upper case hex digits without separator.
type Key [16]byte

// String returns the text form. Do not log keys, use Redacted.
func (k Key) String() string { return strings.ToUpper(hex.EncodeToString(k[:])) }

// Redacted returns a form that is safe for logs and traces.
func (k Key) Redacted() string { return "********************************" }

// IsZero reports whether all bits are zero, which is never a usable key.
func (k Key) IsZero() bool { return k == Key{} }

// ParseKey parses the strict text form.
func ParseKey(s string) (Key, error) {
	var k Key
	if len(s) != 32 || !isUpperHex(s) {
		return k, errors.New("idt: key: want 32 upper case hex digits")
	}
	b, _ := hex.DecodeString(s)
	copy(k[:], b)
	return k, nil
}

// KeyFromBytes copies 16 bytes into a Key.
func KeyFromBytes(b []byte) (Key, error) {
	var k Key
	if len(b) != len(k) {
		return k, fmt.Errorf("idt: key needs 16 bytes, got %d", len(b))
	}
	copy(k[:], b)
	return k, nil
}

// ShortAddr is the 16 bit short address. Text form: 4 upper case hex digits.
type ShortAddr uint16

func (a ShortAddr) String() string { return fmt.Sprintf("%04X", uint16(a)) }

// ParseShortAddr parses the strict text form.
func ParseShortAddr(s string) (ShortAddr, error) {
	if len(s) != 4 || !isUpperHex(s) {
		return 0, fmt.Errorf("idt: short address %q: want 4 upper case hex digits", s)
	}
	b, _ := hex.DecodeString(s)
	return ShortAddr(binary.BigEndian.Uint16(b)), nil
}

// Class is the end-point or base station class: z, a, b or c, lower case.
type Class string

const (
	ClassZ Class = "z"
	ClassA Class = "a"
	ClassB Class = "b"
	ClassC Class = "c"
)

// ParseClass accepts only the lower case letters.
func ParseClass(s string) (Class, error) {
	switch c := Class(s); c {
	case ClassZ, ClassA, ClassB, ClassC:
		return c, nil
	}
	return "", fmt.Errorf("idt: class %q: want z, a, b or c", s)
}

// Bidirectional reports whether the class can receive downlinks.
func (c Class) Bidirectional() bool { return c != ClassZ }

// Value ranges of counters and formats.
const (
	MaxPacketCnt = 1<<32 - 1
	MaxAttachCnt = 1<<24 - 1
	MaxFormat    = 255
)

// CheckPacketCnt, CheckAttachCnt and CheckFormat validate the range of a counter.
func CheckPacketCnt(v uint64) error { return checkMax("packet counter", v, MaxPacketCnt) }
func CheckAttachCnt(v uint64) error { return checkMax("attach counter", v, MaxAttachCnt) }
func CheckFormat(v uint64) error    { return checkMax("format", v, MaxFormat) }

func checkMax(what string, v, max uint64) error {
	if v > max {
		return fmt.Errorf("idt: %s %d above %d", what, v, max)
	}
	return nil
}

func isUpperHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return s != ""
}
