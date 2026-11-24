// Package trace records every message that a connection sends or receives, as one JSON
// object per line. Key material marked as secret in the schema is never written.
package trace

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
)

// Direction of a message.
type Direction string

const (
	In  Direction = "in"
	Out Direction = "out"
)

// Record is one traced message.
type Record struct {
	Time      time.Time      `json:"time"`
	Conn      string         `json:"conn"`
	Interface string         `json:"iface,omitempty"`
	Version   string         `json:"version,omitempty"`
	Dir       Direction      `json:"dir"`
	Command   string         `json:"command,omitempty"`
	OpID      any            `json:"opId,omitempty"`
	Message   map[string]any `json:"msg,omitempty"`
	Findings  []string       `json:"findings,omitempty"`
	Note      string         `json:"note,omitempty"`
}

// Sink receives trace records.
type Sink interface {
	Trace(Record)
}

// Discard drops all records.
var Discard Sink = discard{}

type discard struct{}

func (discard) Trace(Record) {}

// JSONLines writes records as JSON lines. Safe for concurrent use.
type JSONLines struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewJSONLines writes to w.
func NewJSONLines(w io.Writer) *JSONLines { return &JSONLines{enc: json.NewEncoder(w)} }

// Trace writes one record. Write errors are ignored, tracing never stops a connection.
func (j *JSONLines) Trace(r Record) {
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = j.enc.Encode(r)
}

// Memory keeps records in memory, for tests.
type Memory struct {
	mu      sync.Mutex
	Records []Record
}

// Trace appends a record.
func (m *Memory) Trace(r Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Records = append(m.Records, r)
}

// Snapshot returns a copy of the records.
func (m *Memory) Snapshot() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.Records...)
}

// Redacted is the value written in place of secret fields.
const Redacted = "<redacted>"

// Prepare returns a copy of msg that is safe and readable in a trace: fields marked
// secret in the schema are replaced, byte arrays are written as hex strings.
func Prepare(s *schema.Schema, msg map[string]any) map[string]any {
	var fields schema.Fields
	if s != nil {
		if cmd, ok := msg["command"].(string); ok {
			if op, part, ok := s.Lookup(cmd); ok {
				fields = s.MessageFields(op, part)
			}
		}
	}
	return prepareObject(s, fields, msg)
}

func prepareObject(s *schema.Schema, fields schema.Fields, obj map[string]any) map[string]any {
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		f := fields[k]
		if f != nil && f.Secret {
			out[k] = Redacted
			continue
		}
		var sub schema.Fields
		if f != nil && s != nil {
			if td, ok := s.Types[f.Type]; ok {
				sub = td.Fields
			}
		}
		if f != nil && f.Type == "bytes" {
			if b, ok := schema.AsBytes(v); ok {
				out[k] = hex.EncodeToString(b)
				continue
			}
		}
		out[k] = prepareValue(s, sub, v)
	}
	return out
}

func prepareValue(s *schema.Schema, sub schema.Fields, v any) any {
	switch x := v.(type) {
	case []byte:
		return hex.EncodeToString(x)
	case map[string]any:
		return prepareObject(s, sub, x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = prepareValue(s, nil, e)
		}
		return out
	}
	return v
}
