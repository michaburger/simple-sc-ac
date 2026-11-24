// Package codec encodes and decodes MessagePack objects as generic Go values.
//
// Decoded values are: nil, bool, int64 (signed formats), uint64 (unsigned formats and
// positive fixint), float64, string, []byte (bin), []any and map[string]any. 64 bit
// integers are never converted to floating point, so EUIs and operation IDs keep their
// exact value. Only what the interfaces need is supported: maps must have string keys,
// extension types are rejected.
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// ErrTruncated means the data ended inside an object.
var ErrTruncated = errors.New("codec: truncated MessagePack data")

// MaxDepth limits nesting while decoding, to refuse hostile input.
const MaxDepth = 32

// Decode decodes exactly one MessagePack object that fills all of data.
func Decode(data []byte) (any, error) {
	d := decoder{b: data}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if d.pos != len(d.b) {
		return nil, fmt.Errorf("codec: %d bytes after the object", len(d.b)-d.pos)
	}
	return v, nil
}

// DecodeMap decodes one object that has to be a map.
func DecodeMap(data []byte) (map[string]any, error) {
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("codec: top level object is %T, want map", v)
	}
	return m, nil
}

type decoder struct {
	b   []byte
	pos int
}

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || len(d.b)-d.pos < n {
		return nil, ErrTruncated
	}
	out := d.b[d.pos : d.pos+n]
	d.pos += n
	return out, nil
}

func (d *decoder) uint(n int) (uint64, error) {
	b, err := d.take(n)
	if err != nil {
		return 0, err
	}
	switch n {
	case 1:
		return uint64(b[0]), nil
	case 2:
		return uint64(binary.BigEndian.Uint16(b)), nil
	case 4:
		return uint64(binary.BigEndian.Uint32(b)), nil
	default:
		return binary.BigEndian.Uint64(b), nil
	}
}

func (d *decoder) value(depth int) (any, error) {
	if depth > MaxDepth {
		return nil, errors.New("codec: nesting too deep")
	}
	tb, err := d.take(1)
	if err != nil {
		return nil, err
	}
	t := tb[0]
	switch {
	case t <= 0x7f:
		return uint64(t), nil
	case t >= 0xe0:
		return int64(int8(t)), nil
	case t >= 0x80 && t <= 0x8f:
		return d.mapN(int(t&0x0f), depth)
	case t >= 0x90 && t <= 0x9f:
		return d.arrayN(int(t&0x0f), depth)
	case t >= 0xa0 && t <= 0xbf:
		return d.str(int(t & 0x1f))
	}
	switch t {
	case 0xc0:
		return nil, nil
	case 0xc2:
		return false, nil
	case 0xc3:
		return true, nil
	case 0xc4, 0xc5, 0xc6:
		n, err := d.uint(1 << (t - 0xc4))
		if err != nil {
			return nil, err
		}
		b, err := d.take(int(n))
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case 0xca:
		u, err := d.uint(4)
		return float64(math.Float32frombits(uint32(u))), err
	case 0xcb:
		u, err := d.uint(8)
		return math.Float64frombits(u), err
	case 0xcc, 0xcd, 0xce, 0xcf:
		return d.uint(1 << (t - 0xcc))
	case 0xd0:
		u, err := d.uint(1)
		return int64(int8(u)), err
	case 0xd1:
		u, err := d.uint(2)
		return int64(int16(u)), err
	case 0xd2:
		u, err := d.uint(4)
		return int64(int32(u)), err
	case 0xd3:
		u, err := d.uint(8)
		return int64(u), err
	case 0xd9, 0xda, 0xdb:
		n, err := d.uint(1 << (t - 0xd9))
		if err != nil {
			return nil, err
		}
		return d.str(int(n))
	case 0xdc, 0xdd:
		n, err := d.uint(2 << (t - 0xdc))
		if err != nil {
			return nil, err
		}
		return d.arrayN(int(n), depth)
	case 0xde, 0xdf:
		n, err := d.uint(2 << (t - 0xde))
		if err != nil {
			return nil, err
		}
		return d.mapN(int(n), depth)
	}
	return nil, fmt.Errorf("codec: unsupported type byte 0x%02x", t)
}

func (d *decoder) str(n int) (string, error) {
	b, err := d.take(n)
	return string(b), err
}

func (d *decoder) arrayN(n, depth int) ([]any, error) {
	if n > len(d.b)-d.pos { // every element needs at least one byte
		return nil, ErrTruncated
	}
	out := make([]any, n)
	for i := range out {
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (d *decoder) mapN(n, depth int) (map[string]any, error) {
	if 2*n > len(d.b)-d.pos {
		return nil, ErrTruncated
	}
	out := make(map[string]any, n)
	for i := 0; i < n; i++ {
		k, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("codec: map key of type %T, want string", k)
		}
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("codec: duplicate map key %q", key)
		}
		out[key] = v
	}
	return out, nil
}

// Options control encoding.
type Options struct {
	// BytesAsBin writes []byte as MessagePack bin. The default writes it as an array of
	// unsigned integers, which is how the interfaces describe byte fields (Numeric[n]).
	BytesAsBin bool
}

// Encode encodes v with default options.
func Encode(v any) ([]byte, error) { return Options{}.Encode(v) }

// Encode encodes v. Maps are written with sorted keys, so the output is deterministic.
// Supported: nil, bool, all integer types, float32, float64, string, []byte, []any,
// map[string]any, and slices of the basic types.
func (o Options) Encode(v any) ([]byte, error) {
	e := encoder{opt: o}
	if err := e.value(v, 0); err != nil {
		return nil, err
	}
	return e.b, nil
}

type encoder struct {
	b   []byte
	opt Options
}

func (e *encoder) u8(t byte, v uint8)   { e.b = append(e.b, t, v) }
func (e *encoder) u16(t byte, v uint16) { e.b = binary.BigEndian.AppendUint16(append(e.b, t), v) }
func (e *encoder) u32(t byte, v uint32) { e.b = binary.BigEndian.AppendUint32(append(e.b, t), v) }
func (e *encoder) u64(t byte, v uint64) { e.b = binary.BigEndian.AppendUint64(append(e.b, t), v) }

func (e *encoder) uint(v uint64) {
	switch {
	case v <= 0x7f:
		e.b = append(e.b, byte(v))
	case v <= math.MaxUint8:
		e.u8(0xcc, uint8(v))
	case v <= math.MaxUint16:
		e.u16(0xcd, uint16(v))
	case v <= math.MaxUint32:
		e.u32(0xce, uint32(v))
	default:
		e.u64(0xcf, v)
	}
}

func (e *encoder) int(v int64) {
	switch {
	case v >= 0:
		e.uint(uint64(v))
	case v >= -32:
		e.b = append(e.b, byte(int8(v)))
	case v >= math.MinInt8:
		e.u8(0xd0, uint8(int8(v)))
	case v >= math.MinInt16:
		e.u16(0xd1, uint16(int16(v)))
	case v >= math.MinInt32:
		e.u32(0xd2, uint32(int32(v)))
	default:
		e.u64(0xd3, uint64(v))
	}
}

func (e *encoder) header(n int, fix, fixMax byte, t16, t32 byte) {
	switch {
	case n <= 15:
		e.b = append(e.b, fix|byte(n))
	case n <= math.MaxUint16:
		e.u16(t16, uint16(n))
	default:
		e.u32(t32, uint32(n))
	}
	_ = fixMax
}

func (e *encoder) str(s string) {
	n := len(s)
	switch {
	case n <= 31:
		e.b = append(e.b, 0xa0|byte(n))
	case n <= math.MaxUint8:
		e.u8(0xd9, uint8(n))
	case n <= math.MaxUint16:
		e.u16(0xda, uint16(n))
	default:
		e.u32(0xdb, uint32(n))
	}
	e.b = append(e.b, s...)
}

func (e *encoder) value(v any, depth int) error {
	if depth > MaxDepth {
		return errors.New("codec: nesting too deep")
	}
	switch x := v.(type) {
	case nil:
		e.b = append(e.b, 0xc0)
	case bool:
		if x {
			e.b = append(e.b, 0xc3)
		} else {
			e.b = append(e.b, 0xc2)
		}
	case int:
		e.int(int64(x))
	case int8:
		e.int(int64(x))
	case int16:
		e.int(int64(x))
	case int32:
		e.int(int64(x))
	case int64:
		e.int(x)
	case uint:
		e.uint(uint64(x))
	case uint8:
		e.uint(uint64(x))
	case uint16:
		e.uint(uint64(x))
	case uint32:
		e.uint(uint64(x))
	case uint64:
		e.uint(x)
	case float32:
		e.u32(0xca, math.Float32bits(x))
	case float64:
		e.u64(0xcb, math.Float64bits(x))
	case string:
		e.str(x)
	case []byte:
		if e.opt.BytesAsBin {
			n := len(x)
			switch {
			case n <= math.MaxUint8:
				e.u8(0xc4, uint8(n))
			case n <= math.MaxUint16:
				e.u16(0xc5, uint16(n))
			default:
				e.u32(0xc6, uint32(n))
			}
			e.b = append(e.b, x...)
			return nil
		}
		e.header(len(x), 0x90, 0x9f, 0xdc, 0xdd)
		for _, c := range x {
			e.uint(uint64(c))
		}
	case []any:
		e.header(len(x), 0x90, 0x9f, 0xdc, 0xdd)
		for _, el := range x {
			if err := e.value(el, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		e.header(len(x), 0x80, 0x8f, 0xde, 0xdf)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e.str(k)
			if err := e.value(x[k], depth+1); err != nil {
				return err
			}
		}
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice {
			e.header(rv.Len(), 0x90, 0x9f, 0xdc, 0xdd)
			for i := 0; i < rv.Len(); i++ {
				if err := e.value(rv.Index(i).Interface(), depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("codec: cannot encode %T", v)
	}
	return nil
}
