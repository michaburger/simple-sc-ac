package registry

import (
	"path/filepath"
	"testing"
)

const testKey = "000102030405060708090A0B0C0D0E0F" // generated, not a device key

func open(t *testing.T) *Registry {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "sc.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestBaseStations(t *testing.T) {
	r := open(t)
	if err := r.AddBaseStation("70-B3-D5-9C-D0-00-09-E2", "pending", "FP"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBaseStation("70-B3-D5-9C-D0-00-09-E2", "pending", "FP"); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := r.Connected(BaseStation{EUI: "70-B3-D5-9C-D0-00-09-E2", Version: "1.0.0", Class: "a", Bidi: true, Vendor: "x"}); err != nil {
		t.Fatal(err)
	}
	_ = r.SetStatus("70-B3-D5-9C-D0-00-09-E2", map[string]any{"code": 0})
	b, err := r.BaseStation("70-B3-D5-9C-D0-00-09-E2")
	if err != nil || !b.Connected || b.Version != "1.0.0" || string(b.LastStatus) != `{"code":0}` {
		t.Fatalf("%+v %v", b, err)
	}
	if err := r.SetBaseStationState("70-B3-D5-9C-D0-00-09-E2", "enabled", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.BaseStation("70-B3-D5-9C-D0-00-09-E2"); b.State != "enabled" || b.Fingerprint != "FP" {
		t.Fatalf("%+v", b)
	}
	if _, err := r.BaseStation("00-00-00-00-00-00-00-01"); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestEndPointLifecycle(t *testing.T) {
	r := open(t)
	ep := EndPoint{EUI: "02-00-00-00-00-00-00-01", Class: "a", NwkKey: testKey, AttachCnt: -1, LastPacketCnt: -1}
	if err := r.PutEndPoint(ep); err != nil {
		t.Fatal(err)
	}
	if err := r.PutEndPoint(EndPoint{EUI: "bad", Class: "a", NwkKey: testKey}); err == nil {
		t.Fatal("bad EUI accepted")
	}
	if err := r.PutEndPoint(EndPoint{EUI: "02-00-00-00-00-00-00-02", Class: "z", NwkKey: testKey, PreAttach: true}); err == nil {
		t.Fatal("pre-attach without short address accepted")
	}
	sh := 0x1A2B
	if err := r.PutEndPoint(EndPoint{EUI: "02-00-00-00-00-00-00-02", Class: "z", NwkKey: testKey, PreAttach: true, ShAddr: &sh, LastPacketCnt: -1}); err != nil {
		t.Fatal(err)
	}
	att, _ := r.EndPoints(true)
	if len(att) != 1 || att[0].NwkSnKey != testKey {
		t.Fatalf("pre-attached: %+v", att)
	}
	if err := r.Attached(ep.EUI, 5, "11111111111111111111111111111111", 0x0042, EndPoint{DualChan: true}, "BS"); err != nil {
		t.Fatal(err)
	}
	e, _ := r.EndPoint(ep.EUI)
	if e.State != "attached" || e.AttachCnt != 5 || *e.ShAddr != 0x42 || !e.DualChan || e.AttachedVia != "BS" {
		t.Fatalf("%+v", e)
	}
	if used, _ := r.ShortAddrInUse(0x42, "other"); !used {
		t.Fatal("short address not in use")
	}
	id, err := r.StoreUplink(Uplink{EPEui: ep.EUI, PacketCnt: 7, RxNs: 1795517042215640215, Payload: "0102",
		Receptions: []Reception{{BSEui: "A", SNR: 1, RSSI: -60}, {BSEui: "B", SNR: 2, RSSI: -70}}})
	if err != nil || id == 0 {
		t.Fatal(err)
	}
	e, _ = r.EndPoint(ep.EUI)
	if e.LastPacketCnt != 7 {
		t.Fatalf("packet counter %d", e.LastPacketCnt)
	}
	ups, _ := r.Uplinks(ep.EUI, 10)
	if len(ups) != 1 || len(ups[0].Receptions) != 2 || ups[0].RxNs != 1795517042215640215 {
		t.Fatalf("%+v", ups)
	}
	_ = r.Detached(ep.EUI)
	if e, _ = r.EndPoint(ep.EUI); e.State != "detached" || e.NwkSnKey != "" {
		t.Fatalf("%+v", e)
	}
	if err := r.Count("AC", "uplinks", 2); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteEndPoint(ep.EUI); err != nil {
		t.Fatal(err)
	}
}
