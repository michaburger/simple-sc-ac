package schema

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Finding is one problem in a message.
type Finding struct {
	Field   string // dotted path, e.g. "subpackets.snr"; empty for the message itself
	Problem string
	Unknown bool // the field is not in the schema; receivers ignore it, senders must not send it
}

func (f Finding) String() string {
	if f.Field == "" {
		return f.Problem
	}
	return f.Field + ": " + f.Problem
}

// Result of validating one message.
type Result struct {
	Operation *Operation // nil for error and errorAck
	Part      Part
	Findings  []Finding
}

// Errors returns the findings that make the message invalid, i.e. all findings except
// unknown fields.
func (r Result) Errors() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if !f.Unknown {
			out = append(out, f)
		}
	}
	return out
}

// OK reports whether the message is valid for a receiver.
func (r Result) OK() bool { return len(r.Errors()) == 0 }

// Validate checks a decoded message against the schema. The message is a generic map as
// produced by the MessagePack or JSON decoder. Unknown fields are reported with
// Unknown set, so that a receiver can ignore them and a sender can refuse them.
// Conditional fields are checked for type when present, their condition is not
// evaluated here, that is the job of the handler.
func (s *Schema) Validate(msg map[string]any) Result {
	var res Result
	cmd, ok := msg["command"].(string)
	if !ok {
		res.Findings = append(res.Findings, Finding{Field: "command", Problem: "missing or not a string"})
		return res
	}
	op, part, ok := s.Lookup(cmd)
	if !ok {
		res.Findings = append(res.Findings, Finding{Field: "command", Problem: fmt.Sprintf("unknown command %q", cmd)})
		return res
	}
	res.Operation, res.Part = op, part
	all := Fields{}
	for n, f := range s.Core {
		all[n] = f
	}
	for n, f := range s.MessageFields(op, part) {
		all[n] = f
	}
	res.Findings = s.checkObject("", all, msg)
	return res
}

func (s *Schema) checkObject(prefix string, fields Fields, obj map[string]any) []Finding {
	var out []Finding
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := fields[n]
		v, present := obj[n]
		if !present {
			if f.Required() {
				out = append(out, Finding{Field: prefix + n, Problem: "missing required field"})
			}
			continue
		}
		out = append(out, s.checkValue(prefix+n, f.Type, f, v)...)
	}
	var unknown []string
	for n := range obj {
		if _, ok := fields[n]; !ok {
			unknown = append(unknown, n)
		}
	}
	sort.Strings(unknown)
	for _, n := range unknown {
		out = append(out, Finding{Field: prefix + n, Problem: "not in the schema", Unknown: true})
	}
	return out
}

func bad(path, format string, a ...any) []Finding {
	return []Finding{{Field: path, Problem: fmt.Sprintf(format, a...)}}
}

func (s *Schema) checkValue(path, typ string, f *Field, v any) []Finding {
	switch typ {
	case "any":
		return nil
	case "string":
		str, ok := v.(string)
		if !ok {
			return bad(path, "want string, got %T", v)
		}
		if f != nil && f.Format == "semver" && !isSemver(str) {
			return bad(path, "%q is not major.minor.patch", str)
		}
	case "bool":
		if _, ok := v.(bool); !ok {
			return bad(path, "want bool, got %T", v)
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return bad(path, "want object, got %T", v)
		}
	case "enum":
		str, ok := v.(string)
		if !ok {
			return bad(path, "want string, got %T", v)
		}
		for _, allowed := range f.Values {
			if str == allowed {
				return nil
			}
		}
		return bad(path, "%q is not one of %s", str, strings.Join(f.Values, ", "))
	case "number":
		x, ok := asFloat(v)
		if !ok {
			return bad(path, "want number, got %T", v)
		}
		return checkRange(path, f, x)
	case "integer":
		return checkInteger(path, f, v)
	case "eui64":
		if _, ok := AsUint64(v); !ok {
			return bad(path, "want unsigned 64 bit integer, got %v (%T)", v, v)
		}
	case "bytes":
		b, ok := AsBytes(v)
		if !ok {
			return bad(path, "want byte array, got %T", v)
		}
		if f != nil && f.Len != nil && len(b) != *f.Len {
			return bad(path, "want %d bytes, got %d", *f.Len, len(b))
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return bad(path, "want array, got %T", v)
		}
		if f.Len != nil && len(arr) != *f.Len {
			return bad(path, "want %d elements, got %d", *f.Len, len(arr))
		}
		var out []Finding
		for i, e := range arr {
			out = append(out, s.checkValue(fmt.Sprintf("%s[%d]", path, i), f.Items, nil, e)...)
		}
		return out
	default:
		td, ok := s.Types[typ]
		if !ok {
			return bad(path, "unknown type %q", typ)
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return bad(path, "want object %s, got %T", typ, v)
		}
		return s.checkObject(path+".", td.Fields, obj)
	}
	return nil
}

func checkRange(path string, f *Field, x float64) []Finding {
	if f == nil {
		return nil
	}
	if f.Min != nil && x < *f.Min {
		return bad(path, "%v is below %v", x, *f.Min)
	}
	if f.Max != nil && x > *f.Max {
		return bad(path, "%v is above %v", x, *f.Max)
	}
	return nil
}

func checkInteger(path string, f *Field, v any) []Finding {
	signedOnly := f != nil && f.Signed != nil && *f.Signed
	unsignedOnly := f != nil && f.Signed != nil && !*f.Signed
	if u, ok := AsUint64(v); ok {
		if signedOnly && u > math.MaxInt64 {
			return bad(path, "%d does not fit a signed 64 bit integer", u)
		}
		if f != nil && f.Bits > 0 && f.Bits < 64 {
			limit := uint64(1)<<f.Bits - 1
			if signedOnly {
				limit = uint64(1)<<(f.Bits-1) - 1
			}
			if u > limit {
				return bad(path, "%d does not fit %d bits", u, f.Bits)
			}
		}
		if f != nil && f.Max != nil && float64(u) > *f.Max {
			return bad(path, "%d is above %v", u, *f.Max)
		}
		return nil
	}
	i, ok := AsInt64(v)
	if !ok {
		return bad(path, "want integer, got %v (%T)", v, v)
	}
	// i is negative here, AsUint64 handled the rest.
	if unsignedOnly {
		return bad(path, "%d is negative, want unsigned", i)
	}
	if f != nil && f.Bits > 0 && f.Bits < 64 && i < -(int64(1)<<(f.Bits-1)) {
		return bad(path, "%d does not fit %d bits", i, f.Bits)
	}
	if f != nil && f.Min != nil && float64(i) < *f.Min {
		return bad(path, "%d is below %v", i, *f.Min)
	}
	return nil
}

// ApplyDefaults adds the default value of every missing optional field that has one, as
// the "Message interpretation" rule of BSSCI and SCACI requires. default_from copies
// another field of the init message, which the caller passes as init (may be nil).
func (s *Schema) ApplyDefaults(msg map[string]any, init map[string]any) {
	cmd, _ := msg["command"].(string)
	op, part, ok := s.Lookup(cmd)
	if !ok {
		return
	}
	for n, f := range s.MessageFields(op, part) {
		if _, present := msg[n]; present {
			continue
		}
		switch {
		case f.Default != nil:
			msg[n] = f.Default
		case strings.HasPrefix(f.DefaultFrom, "init.") && init != nil:
			if v, ok := init[strings.TrimPrefix(f.DefaultFrom, "init.")]; ok {
				msg[n] = v
			}
		}
	}
}

// AsUint64 converts a non-negative integer of any Go integer type.
func AsUint64(v any) (uint64, bool) {
	switch x := v.(type) {
	case uint64:
		return x, true
	case uint32:
		return uint64(x), true
	case uint16:
		return uint64(x), true
	case uint8:
		return uint64(x), true
	case uint:
		return uint64(x), true
	case int64:
		return uint64(x), x >= 0
	case int32:
		return uint64(x), x >= 0
	case int16:
		return uint64(x), x >= 0
	case int8:
		return uint64(x), x >= 0
	case int:
		return uint64(x), x >= 0
	}
	return 0, false
}

// AsInt64 converts an integer that fits a signed 64 bit integer.
func AsInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int32:
		return int64(x), true
	case int16:
		return int64(x), true
	case int8:
		return int64(x), true
	case int:
		return int64(x), true
	}
	if u, ok := AsUint64(v); ok && u <= math.MaxInt64 {
		return int64(u), true
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	if i, ok := AsInt64(v); ok {
		return float64(i), true
	}
	if u, ok := AsUint64(v); ok {
		return float64(u), true
	}
	return 0, false
}

// AsBytes accepts a binary value or an array of integers from 0 to 255, the two ways a
// byte array can arrive in MessagePack.
func AsBytes(v any) ([]byte, bool) {
	switch x := v.(type) {
	case []byte:
		return x, true
	case []any:
		out := make([]byte, len(x))
		for i, e := range x {
			u, ok := AsUint64(e)
			if !ok || u > 255 {
				return nil, false
			}
			out[i] = byte(u)
		}
		return out, true
	}
	return nil, false
}

func isSemver(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.ParseUint(p, 10, 32); err != nil {
			return false
		}
	}
	return true
}
