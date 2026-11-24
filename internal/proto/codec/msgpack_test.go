package codec

import (
	"bytes"
	"encoding/hex"
	"math"
	"reflect"
	"testing"
)

// Encodings from the MessagePack specification.
func TestKnownEncodings(t *testing.T) {
	cases := []struct {
		v   any
		hex string
	}{
		{nil, "c0"},
		{true, "c3"},
		{int64(0), "00"},
		{int64(127), "7f"},
		{int64(128), "cc80"},
		{int64(-1), "ff"},
		{int64(-32), "e0"},
		{int64(-33), "d0df"},
		{int64(-129), "d1ff7f"},
		{uint64(math.MaxUint64), "cfffffffffffffffff"},
		{int64(math.MinInt64), "d38000000000000000"},
		{1.5, "cb3ff8000000000000"},
		{"a", "a161"},
		{[]any{int64(1), int64(2)}, "920102"},
		{map[string]any{"b": int64(2), "a": int64(1)}, "82a16101a16202"},
		{[]byte{1, 255}, "9201ccff"},
	}
	for _, c := range cases {
		got, err := Encode(c.v)
		if err != nil {
			t.Fatalf("%v: %v", c.v, err)
		}
		if hex.EncodeToString(got) != c.hex {
			t.Errorf("Encode(%#v) = %x, want %s", c.v, got, c.hex)
		}
	}
	bin, _ := Options{BytesAsBin: true}.Encode([]byte{1, 2})
	if hex.EncodeToString(bin) != "c4020102" {
		t.Errorf("bin = %x", bin)
	}
}

func TestRoundTrip64Bit(t *testing.T) {
	msg := map[string]any{
		"command":  "test",
		"opId":     int64(-9223372036854775807),
		"eui":      uint64(18205882861800652800), // above 2^63
		"time":     uint64(1795517042215640215),  // above 2^53
		"neg":      int64(math.MinInt64),
		"f32":      float32(0.25),
		"key":      []byte{0xde, 0xad},
		"nested":   map[string]any{"arr": []any{uint64(1), "x", nil, false}},
		"floats":   []float64{1, 2.5},
		"empty":    []any{},
		"longText": string(bytes.Repeat([]byte("x"), 300)),
	}
	data, err := Encode(msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMap(data)
	if err != nil {
		t.Fatal(err)
	}
	if got["eui"] != uint64(18205882861800652800) || got["time"] != uint64(1795517042215640215) {
		t.Fatalf("64 bit values changed: %v %v", got["eui"], got["time"])
	}
	if got["opId"] != int64(-9223372036854775807) || got["neg"] != int64(math.MinInt64) {
		t.Fatalf("signed values changed: %v %v", got["opId"], got["neg"])
	}
	if got["f32"] != 0.25 {
		t.Fatalf("float32: %v", got["f32"])
	}
	if !reflect.DeepEqual(got["key"], []any{uint64(0xde), uint64(0xad)}) {
		t.Fatalf("bytes as array: %#v", got["key"])
	}
	if !reflect.DeepEqual(got["nested"], map[string]any{"arr": []any{uint64(1), "x", nil, false}}) {
		t.Fatalf("nested: %#v", got["nested"])
	}
	if len(got["longText"].(string)) != 300 {
		t.Fatal("str8 length")
	}
}

func TestDecodeErrors(t *testing.T) {
	bad := map[string]string{
		"truncated int":   "cd01",
		"truncated str":   "a3616263"[:6],
		"trailing bytes":  "c0c0",
		"int key":         "810102",
		"duplicate key":   "82a16101a16102",
		"extension":       "d40100",
		"huge array":      "ddffffffff",
		"truncated array": "9301",
	}
	for name, h := range bad {
		b, _ := hex.DecodeString(h)
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	deep := bytes.Repeat([]byte{0x91}, MaxDepth+2)
	if _, err := Decode(append(deep, 0xc0)); err == nil {
		t.Error("deep nesting accepted")
	}
}

func FuzzDecode(f *testing.F) {
	for _, h := range []string{"82a16101a16202", "cfffffffffffffffff", "9201ccff"} {
		b, _ := hex.DecodeString(h)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Decode(data)
		if err != nil {
			return
		}
		again, err := Encode(v)
		if err != nil {
			t.Fatalf("decoded value does not encode: %v", err)
		}
		if _, err := Decode(again); err != nil {
			t.Fatalf("re-encoded value does not decode: %v", err)
		}
	})
}
