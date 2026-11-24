package version

import (
	"errors"
	"testing"
)

var (
	v100 = MustParse("1.0.0")
	v101 = MustParse("1.0.1")
	v110 = MustParse("1.1.0")
	v120 = MustParse("1.2.0")
	v200 = MustParse("2.0.0")
	both = []Version{v110, v100}
)

func TestParse(t *testing.T) {
	for _, bad := range []string{"1.0", "1.0.0.0", "a.b.c", "1.-1.0", "01.0.0", ""} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if v := MustParse("1.10.3"); v.Minor != 10 || v.String() != "1.10.3" {
		t.Fatal(v)
	}
	if !MustParse("1.9.0").Less(MustParse("1.10.0")) {
		t.Fatal("numeric order")
	}
}

func accept(t *testing.T, p Policy, req Version, sup []Version, peer string, mem *Memory) (Version, bool) {
	t.Helper()
	v, err := Accept(p, req, sup, peer, mem)
	if err != nil {
		var ns ErrNotSupported
		if !errors.As(err, &ns) {
			t.Fatalf("unexpected error type %v", err)
		}
		return Version{}, false
	}
	return v, true
}

// Every branch of the table "Version arbitration" in the architecture.
func TestAcceptFollow(t *testing.T) {
	cases := []struct {
		name string
		req  Version
		sup  []Version
		want Version
		ok   bool
	}{
		{"1.1 requested, both known", v110, both, v110, true},
		{"1.0 requested, both known", v100, both, v100, true},
		{"patch ignored", v101, both, v100, true},
		{"newer minor, answer our highest", v120, both, v110, true},
		{"other major", v200, both, Version{}, false},
		{"1.0 requested, only 1.1 known", v100, []Version{v110}, Version{}, false},
		{"nothing loaded", v110, nil, Version{}, false},
	}
	for _, c := range cases {
		got, ok := accept(t, Follow, c.req, c.sup, "bs", nil)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestAcceptOfferHighest(t *testing.T) {
	mem := NewMemory()
	if got, _ := accept(t, OfferHighest, v100, both, "bs1", mem); got != v110 {
		t.Fatalf("first offer %v, want 1.1", got)
	}
	mem.Refused("bs1", v110)
	if got, _ := accept(t, OfferHighest, v100, both, "bs1", mem); got != v100 {
		t.Fatalf("after refusal %v, want 1.0", got)
	}
	if got, _ := accept(t, OfferHighest, v100, both, "bs2", mem); got != v110 {
		t.Fatalf("other peer %v, want 1.1", got)
	}
	mem.Forget("bs1")
	if got, _ := accept(t, OfferHighest, v100, both, "bs1", mem); got != v110 {
		t.Fatalf("after forget %v, want 1.1", got)
	}
}

func TestOpener(t *testing.T) {
	mem := NewMemory()
	o := NewOpener(both, "sc", mem)
	if v, _ := o.Request(); v != v110 {
		t.Fatalf("first request %v", v)
	}
	// SC answers 1.0 in conRsp: session on 1.0
	if v, err := o.Answer(v100); err != nil || v != v100 {
		t.Fatalf("answer 1.0: %v %v", v, err)
	}
	// SC answers 2.0: refuse
	if _, err := o.Answer(v200); err == nil {
		t.Fatal("2.0 accepted")
	}
	// SC refuses 1.1: next request 1.0, then nothing
	o.Refused()
	if v, ok := o.Request(); !ok || v != v100 {
		t.Fatalf("after refusal %v %v", v, ok)
	}
	o.Refused()
	if _, ok := o.Request(); ok {
		t.Fatal("request after all were refused")
	}
	// remembered per SC
	mem.Refused("sc", v110)
	if v, _ := NewOpener(both, "sc", mem).Request(); v != v100 {
		t.Fatalf("remembered refusal not used: %v", v)
	}
	if v, _ := NewOpener(both, "other", mem).Request(); v != v110 {
		t.Fatalf("other SC: %v", v)
	}
}

func TestParsePolicy(t *testing.T) {
	if _, err := ParsePolicy("follow"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePolicy("best"); err == nil {
		t.Fatal("unknown policy accepted")
	}
}
