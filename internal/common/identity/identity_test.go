package identity

import (
	"net"
	"strings"
	"testing"
)

func withHardware(t *testing.T, macs []string, balena string) {
	t.Helper()
	old := Hardware
	t.Cleanup(func() { Hardware = old })
	Hardware = func() ([]net.HardwareAddr, string) {
		var out []net.HardwareAddr
		for _, m := range macs {
			hw, err := net.ParseMAC(m)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, hw)
		}
		return out, balena
	}
}

func TestConfiguredAndZero(t *testing.T) {
	r, err := Resolve("sc", "70-B3-D5-9C-D0-00-09-BB", t.TempDir())
	if err != nil || r.Source != Configured || r.Warning != "" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = Resolve("sc", "00-00-00-00-00-00-00-00", t.TempDir())
	if err != nil || r.EUI != 0 || r.Warning == "" {
		t.Fatalf("zero EUI without warning: %+v %v", r, err)
	}
	if _, err := Resolve("sc", "70b3d59cd00009bb", t.TempDir()); err == nil {
		t.Fatal("lenient text form accepted")
	}
}

func TestDerivedIsLocalStableAndPerRole(t *testing.T) {
	withHardware(t, []string{"02:42:ac:11:00:02", "dc:a6:32:01:02:03"}, "")
	dir := t.TempDir()
	sc, err := Resolve("sc", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Source != FromMAC || !strings.HasPrefix(sc.EUI.String(), "02-") || !sc.EUI.IsLocal() || sc.EUI.IsGroup() {
		t.Fatalf("%+v", sc)
	}
	ac, _ := Resolve("ac", "", dir)
	if ac.EUI == sc.EUI {
		t.Fatal("SC and AC got the same EUI")
	}
	again, _ := Resolve("sc", "", dir)
	if again.EUI != sc.EUI || again.Source != Stored {
		t.Fatalf("not stored: %+v", again)
	}
	// The universally administered MAC is used, not the virtual container MAC.
	if fromInput("sc", "dc:a6:32:01:02:03") != sc.EUI {
		t.Fatal("wrong MAC used")
	}
}

func TestBalenaFallbackAndRandom(t *testing.T) {
	withHardware(t, []string{"02:42:ac:11:00:02"}, "8dc645682e14dc9296ddf3f4fad50409")
	r, _ := Resolve("sc", "", t.TempDir())
	if r.Source != FromBalena {
		t.Fatalf("%+v", r)
	}
	withHardware(t, nil, "")
	r, _ = Resolve("ac", "", t.TempDir())
	if r.Source != FromRandom || !r.EUI.IsLocal() {
		t.Fatalf("%+v", r)
	}
}
