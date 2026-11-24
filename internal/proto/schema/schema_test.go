package schema

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func loadDemo(t *testing.T) *Schema {
	t.Helper()
	set, err := Load(os.DirFS("../../../testdata/schemas"))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := set.Get("demo", "1.0.0")
	if !ok {
		t.Fatal("demo 1.0.0 not loaded")
	}
	return s
}

func TestLoadAndLookup(t *testing.T) {
	s := loadDemo(t)
	if s.Identifier != "DEMOXX01" {
		t.Fatalf("identifier %q", s.Identifier)
	}
	cases := map[string]Part{"hello": Init, "helloRsp": Response, "helloCmp": Complete, "error": ErrorMsg, "errorAck": ErrorAck}
	for cmd, want := range cases {
		_, part, ok := s.Lookup(cmd)
		if !ok || part != want {
			t.Errorf("Lookup(%q) = %v %v, want %v", cmd, part, ok, want)
		}
	}
	if _, _, ok := s.Lookup("helloXyz"); ok {
		t.Error("unknown command found")
	}
	if got := strings.Join(s.Commands(), ","); got != "hello,notify,ping,store" {
		t.Errorf("Commands = %s", got)
	}
}

func valid(t *testing.T, s *Schema, msg map[string]any) {
	t.Helper()
	if r := s.Validate(msg); !r.OK() {
		t.Fatalf("unexpected findings: %v", r.Findings)
	}
}

func invalid(t *testing.T, s *Schema, msg map[string]any, field string) {
	t.Helper()
	r := s.Validate(msg)
	for _, f := range r.Errors() {
		if f.Field == field {
			return
		}
	}
	t.Fatalf("want a finding on %q, got %v", field, r.Findings)
}

func store() map[string]any {
	return map[string]any{
		"command": "store", "opId": int64(7),
		"slot": uint8(3), "token": []any{int64(1), int64(2), int64(3), int64(4)},
		"payload": []byte{}, "reading": map[string]any{"values": []any{1.5, int64(2)}},
	}
}

func TestValidate(t *testing.T) {
	s := loadDemo(t)
	valid(t, s, map[string]any{"command": "hello", "opId": int64(0), "version": "1.0.0", "deviceId": uint64(18205882861800652800)})
	valid(t, s, store())
	valid(t, s, map[string]any{"command": "pingCmp", "opId": int64(-4)})

	m := store()
	delete(m, "slot")
	invalid(t, s, m, "slot")

	m = store()
	m["slot"] = int64(256)
	invalid(t, s, m, "slot")

	m = store()
	m["slot"] = int64(-1)
	invalid(t, s, m, "slot")

	m = store()
	m["token"] = []byte{1, 2, 3}
	invalid(t, s, m, "token")

	m = store()
	m["token"] = []any{int64(1), int64(2), int64(3), int64(300)}
	invalid(t, s, m, "token")

	m = store()
	m["level"] = 1.5
	invalid(t, s, m, "level")

	m = store()
	m["mode"] = "slow"
	invalid(t, s, m, "mode")

	m = store()
	m["reading"] = map[string]any{"values": []any{"x"}}
	invalid(t, s, m, "reading.values[0]")

	m = store()
	m["tags"] = []any{"a"}
	invalid(t, s, m, "tags")

	invalid(t, s, map[string]any{"command": "hello", "opId": int64(0), "version": "1.0", "deviceId": uint64(1)}, "version")
	invalid(t, s, map[string]any{"command": "hello", "opId": int64(0), "version": "1.0.0", "deviceId": int64(-1)}, "deviceId")
	invalid(t, s, map[string]any{"command": "hello", "opId": uint64(1) << 63, "version": "1.0.0", "deviceId": uint64(1)}, "opId")
	invalid(t, s, map[string]any{"opId": int64(1)}, "command")
	invalid(t, s, map[string]any{"command": "nope", "opId": int64(1)}, "command")
}

func TestUnknownFieldsAreNotErrors(t *testing.T) {
	s := loadDemo(t)
	m := store()
	m["extra"] = true
	r := s.Validate(m)
	if !r.OK() {
		t.Fatalf("unknown field made the message invalid: %v", r.Findings)
	}
	if len(r.Findings) != 1 || !r.Findings[0].Unknown || r.Findings[0].Field != "extra" {
		t.Fatalf("want one unknown finding on extra, got %v", r.Findings)
	}
}

func TestApplyDefaults(t *testing.T) {
	s := loadDemo(t)
	m := store()
	s.ApplyDefaults(m, nil)
	if m["level"] != 0.5 || m["mode"] != "safe" {
		t.Fatalf("defaults not applied: level %v mode %v", m["level"], m["mode"])
	}
	if _, ok := m["tags"]; ok {
		t.Fatal("field without default was added")
	}
	init := map[string]any{"command": "hello", "version": "1.0.0"}
	rsp := map[string]any{"command": "helloRsp", "opId": int64(0), "accepted": true}
	s.ApplyDefaults(rsp, init)
	if rsp["version"] != "1.0.0" {
		t.Fatalf("default_from not applied: %v", rsp["version"])
	}
}

func TestSchemaChecks(t *testing.T) {
	base := "interface: x\nversion: \"1.0.0\"\nidentifier: ABCDEFGH\nroles: {initiator_positive: a, initiator_negative: b}\n"
	cases := map[string]string{
		"unknown type":             "operations:\n  op: {initiator: a, init: {f: {type: blob}}}\n",
		"conditional without when": "operations:\n  op: {initiator: a, init: {f: {type: bool, presence: conditional}}}\n",
		"enum without values":      "operations:\n  op: {initiator: a, init: {f: {type: enum}}}\n",
		"array without items":      "operations:\n  op: {initiator: a, init: {f: {type: array}}}\n",
		"unknown initiator":        "operations:\n  op: {initiator: c, init: {}}\n",
		"unknown key":              "operations:\n  op: {initiator: a, init: {f: {type: bool, optinal: true}}}\n",
	}
	for name, body := range cases {
		if _, err := Parse([]byte(base+body), name); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := Parse([]byte(base+"operations:\n  op: {initiator: both, init: {}}\n"), "ok"); err != nil {
		t.Errorf("valid schema rejected: %v", err)
	}
}

func TestDuplicateVersion(t *testing.T) {
	doc := []byte("interface: x\nversion: \"1.0.0\"\nidentifier: ABCDEFGH\noperations: {}\n")
	_, err := Load(fstest.MapFS{"a.yaml": {Data: doc}, "b.yaml": {Data: doc}})
	if err == nil {
		t.Fatal("duplicate version not reported")
	}
}
