package idt

import "testing"

func TestEUI64(t *testing.T) {
	e, err := ParseEUI64("70-B3-D5-9C-D0-00-09-BB")
	if err != nil {
		t.Fatal(err)
	}
	if uint64(e) != 8121069422560414139 {
		t.Fatalf("value %d", uint64(e))
	}
	if e.String() != "70-B3-D5-9C-D0-00-09-BB" {
		t.Fatalf("text %s", e)
	}
	if got := EUI64(18205882861800652800).String(); got != "FC-A8-4A-01-00-00-00-00" {
		t.Fatalf("above 2^63: %s", got)
	}
	for _, bad := range []string{
		"70-b3-d5-9c-d0-00-09-bb", "70B3D59CD00009BB", "70:B3:D5:9C:D0:00:09:BB",
		"70-B3-D5-9C-D0-00-09", "70-B3-D5-9C-D0-00-09-BG", " 70-B3-D5-9C-D0-00-09-B",
	} {
		if _, err := ParseEUI64(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	b, _ := EUI64FromBytes(e.Bytes())
	if b != e {
		t.Fatal("bytes round trip")
	}
	if !EUI64(0x02<<56).IsLocal() || e.IsLocal() || !EUI64(0x01<<56).IsGroup() {
		t.Fatal("U/L or I/G bit")
	}
}

func TestKey(t *testing.T) {
	k, err := ParseKey("2B7E151628AED2A6ABF7158809CF4F3C")
	if err != nil {
		t.Fatal(err)
	}
	if k.String() != "2B7E151628AED2A6ABF7158809CF4F3C" || k[0] != 0x2b {
		t.Fatal("round trip")
	}
	if k.Redacted() == k.String() {
		t.Fatal("redaction")
	}
	for _, bad := range []string{"2b7e151628aed2a6abf7158809cf4f3c", "2B7E", "2B-7E151628AED2A6ABF7158809CF4F3"} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if !(Key{}).IsZero() {
		t.Fatal("zero key")
	}
}

func TestShortAddrClassCounters(t *testing.T) {
	a, err := ParseShortAddr("00AF")
	if err != nil || a != 0xAF || a.String() != "00AF" {
		t.Fatalf("short address %v %v", a, err)
	}
	if _, err := ParseShortAddr("00af"); err == nil {
		t.Error("lower case short address accepted")
	}
	if c, err := ParseClass("b"); err != nil || c != ClassB || !c.Bidirectional() {
		t.Error("class b")
	}
	if _, err := ParseClass("B"); err == nil {
		t.Error("upper case class accepted")
	}
	if ClassZ.Bidirectional() {
		t.Error("class z bidirectional")
	}
	if CheckAttachCnt(1<<24) == nil || CheckAttachCnt(1<<24-1) != nil {
		t.Error("attach counter range")
	}
	if CheckPacketCnt(1<<32) == nil || CheckFormat(256) == nil {
		t.Error("ranges")
	}
}
