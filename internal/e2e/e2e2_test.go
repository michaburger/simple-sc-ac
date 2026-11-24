// Package e2e holds end-to-end tests across simple-sc, simple-ac and the simulators.
package e2e

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/ac/node"
	"github.com/michaburger/simple-sc-ac/internal/ac/scclient"
	acstore "github.com/michaburger/simple-sc-ac/internal/ac/store"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/common/onboard"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/version"
	"github.com/michaburger/simple-sc-ac/internal/sc/bssci"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
	"github.com/michaburger/simple-sc-ac/internal/sc/scaci"
	"github.com/michaburger/simple-sc-ac/internal/sim/bs"
	"github.com/michaburger/simple-sc-ac/schemas"
)

const testKey = "000102030405060708090A0B0C0D0E0F" // generated, not a device key

var scEUI = idt.EUI64(0x02000000000000AA)

func logger() *slog.Logger {
	if os.Getenv("SSCAC_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type sc struct {
	dir            string
	ca             *labca.CA
	reg            *registry.Registry
	bssci          *bssci.Server
	scaci          *scaci.Server
	bsAddr, acAddr string
	set            *schema.Set
}

func startSC(t *testing.T) *sc {
	t.Helper()
	s := &sc{dir: t.TempDir()}
	var err error
	s.set, err = schema.Load(schemas.FS)
	if err != nil {
		t.Fatal(err)
	}
	s.ca, _, _ = labca.Init(filepath.Join(s.dir, "ca"), "test")
	if _, err := s.ca.Issue(s.dir, labca.Request{Name: "sc", Server: true, Hosts: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	cert, _ := tls.LoadX509KeyPair(filepath.Join(s.dir, "sc.pem"), filepath.Join(s.dir, "sc.key"))
	tc := &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: s.ca.Pool(), ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	s.reg, _ = registry.Open(filepath.Join(s.dir, "sc.sqlite"))
	t.Cleanup(func() { s.reg.Close() })
	s.scaci, err = scaci.New(scaci.Config{SCEui: scEUI, Schemas: s.set, Registry: s.reg, Log: logger(), TLS: tc,
		Policy: version.Follow, Mode: engine.Strict, ResponseTimeout: 2 * time.Second, BufferAge: time.Hour, BufferMax: 100})
	if err != nil {
		t.Fatal(err)
	}
	s.bssci, err = bssci.New(bssci.Config{SCEui: scEUI, Schemas: s.set, Registry: s.reg, Log: logger(), TLS: tc,
		Policy: version.Follow, Mode: engine.Lenient, Unknown: "accept", ResponseTimeout: 2 * time.Second,
		DedupWindow: 200 * time.Millisecond, StatusInterval: time.Hour, Events: s.scaci})
	if err != nil {
		t.Fatal(err)
	}
	s.scaci.SetBSSCI(s.bssci)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	for _, l := range []struct {
		addr  *string
		serve func(context.Context, net.Listener) error
	}{{&s.bsAddr, s.bssci.Serve}, {&s.acAddr, s.scaci.Serve}} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		*l.addr = ln.Addr().String()
		go l.serve(ctx, tls.NewListener(ln, tc))
	}
	return s
}

type ac struct {
	dir   string
	eui   idt.EUI64
	store *acstore.Store
	node  *node.Node
	stop  context.CancelFunc
}

// enrollAC goes through the preferred onboarding: the AC creates key and request, the SC
// signs and returns a bundle, the AC imports it. The key never leaves the AC.
func enrollAC(t *testing.T, s *sc, eui idt.EUI64, scName string) string {
	t.Helper()
	dir := t.TempDir()
	csrFile, err := node.Request(dir, scName, eui)
	if err != nil {
		t.Fatal(err)
	}
	csr, _ := os.ReadFile(csrFile)
	b, err := scaci.Enroll(s.reg, s.ca, scEUI, scaci.EnrollRequest{Name: scName, ACEui: eui.String(), ACName: "test", Address: s.acAddr, ServerName: "127.0.0.1", CSR: csr})
	if err != nil {
		t.Fatal(err)
	}
	if b.Key != "" {
		t.Fatal("the bundle contains a key although the AC sent a request")
	}
	bf := filepath.Join(t.TempDir(), "bundle.json")
	if err := b.Write(bf); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Import(dir, bf, eui); err != nil {
		t.Fatal(err)
	}
	return dir
}

func startAC(t *testing.T, s *sc, dir string, eui idt.EUI64) *ac {
	t.Helper()
	a := &ac{dir: dir, eui: eui}
	var err error
	a.store, err = acstore.Open(filepath.Join(dir, "ac.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.store.Close() })
	a.node, err = node.New(node.Config{DataDir: dir, ACEui: eui, Schemas: s.set, Store: a.store, Log: logger(), ResponseTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	t.Cleanup(cancel)
	go a.node.Run(ctx)
	return a
}

func (a *ac) client(t *testing.T) *scclient.Client {
	cs := a.node.Clients()
	if len(cs) != 1 {
		t.Fatalf("%d Service Centers loaded", len(cs))
	}
	return cs[0]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func dialBS(t *testing.T, s *sc, eui idt.EUI64) *bs.BS {
	t.Helper()
	name := "bs-" + eui.String()
	if _, err := s.ca.Issue(s.dir, labca.Request{Name: name, EUI: eui.String()}); err != nil {
		t.Fatal(err)
	}
	cert, _ := tls.LoadX509KeyPair(filepath.Join(s.dir, name+".pem"), filepath.Join(s.dir, name+".key"))
	b, err := bs.Dial(context.Background(), s.bsAddr, bs.Config{EUI: eui, Version: "1.1.0", Bidi: true, Schemas: s.set,
		TLS: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: s.ca.Pool(), ServerName: "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func mustKey(t *testing.T) []byte {
	k, _ := idt.ParseKey(testKey)
	return k[:]
}

// E2E-2: AC onboarding, registration, attach, uplink and end-point status in the AC,
// deregistration.
func TestE2E2(t *testing.T) {
	s := startSC(t)
	acEUI := idt.EUI64(0x02000000000000C1)
	a := startAC(t, s, enrollAC(t, s, acEUI, "lab"), acEUI)
	waitFor(t, "AC connected", func() bool { return a.client(t).Info().Connected })

	epEUI := idt.EUI64(0x0200000000000001)
	regs, err := a.node.AddEndPoint(context.Background(), acstore.EndPoint{EUI: epEUI.String(), NwkKey: testKey, Class: "a"})
	if err != nil || len(regs) != 1 || regs[0].State != "registered" {
		t.Fatalf("registration %+v %v", regs, err)
	}
	if e, err := s.reg.EndPoint(epEUI.String()); err != nil || e.OwnerAC != acEUI.String() {
		t.Fatalf("SC registry %+v %v", e, err)
	}
	st, err := a.client(t).Status(context.Background())
	if err != nil || st["message"] != "ok" {
		t.Fatalf("status %v %v", st, err)
	}

	b := dialBS(t, s, 0x02000000000000B1)
	ep := &bs.EndPoint{EUI: epEUI, NwkKey: mustKey(t)}
	if _, err := b.Attach(context.Background(), ep, 9, -80); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "end-point status attached at the AC", func() bool { st, err := a.store.StateOf(epEUI.String()); return err == nil && st.Status == "attached" })
	for i := uint32(1); i <= 3; i++ {
		if err := b.Uplink(context.Background(), ep, i, []byte{byte(i), 0xAA}, 9, -80); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "three uplinks at the AC", func() bool { u, _ := a.store.Uplinks(epEUI.String(), 10); return len(u) == 3 })
	ups, _ := a.store.Uplinks(epEUI.String(), 1)
	if ups[0].Payload != "03AA" || ups[0].Receptions[0]["bsEui"] != "02-00-00-00-00-00-00-B1" {
		t.Fatalf("uplink %+v", ups[0])
	}
	if err := b.Detach(context.Background(), ep, 9, -80); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "end-point status detached", func() bool { st, _ := a.store.StateOf(epEUI.String()); return st.Status == "detached" })

	if err := a.node.RemoveEndPoint(context.Background(), epEUI.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reg.EndPoint(epEUI.String()); !errors.Is(err, registry.ErrNotFound) {
		t.Fatal("end-point still registered at the SC")
	}
	usage, _ := s.reg.UsageOf(acEUI.String(), 1)
	if len(usage) != 1 || usage[0].Counters["uplinks_delivered"] != 3 || usage[0].Active != 1 {
		t.Fatalf("usage %+v", usage)
	}
}

// Uplinks of an AC that is offline are delivered after it reconnects.
func TestBufferWhileACOffline(t *testing.T) {
	s := startSC(t)
	acEUI := idt.EUI64(0x02000000000000C2)
	dir := enrollAC(t, s, acEUI, "lab")
	a := startAC(t, s, dir, acEUI)
	waitFor(t, "AC connected", func() bool { return a.client(t).Info().Connected })
	epEUI := idt.EUI64(0x0200000000000002)
	if _, err := a.node.AddEndPoint(context.Background(), acstore.EndPoint{EUI: epEUI.String(), NwkKey: testKey, Class: "a"}); err != nil {
		t.Fatal(err)
	}
	b := dialBS(t, s, 0x02000000000000B2)
	ep := &bs.EndPoint{EUI: epEUI, NwkKey: mustKey(t)}
	if _, err := b.Attach(context.Background(), ep, 9, -80); err != nil {
		t.Fatal(err)
	}
	a.stop()
	a.store.Close()
	waitFor(t, "AC gone", func() bool { return len(s.scaci.Connected()) == 0 })
	for i := uint32(1); i <= 2; i++ {
		_ = b.Uplink(context.Background(), ep, i, []byte{byte(i)}, 9, -80)
	}
	time.Sleep(400 * time.Millisecond)
	a2 := startAC(t, s, dir, acEUI)
	waitFor(t, "buffered uplinks delivered", func() bool { u, _ := a2.store.Uplinks(epEUI.String(), 10); return len(u) == 2 })
}

func isCode(err error, code int64) bool {
	var oe *engine.OpError
	return errors.As(err, &oe) && oe.Code == code
}

// 2.9: certificate of another CA, wrong EUI, disabled AC, second session, foreign
// end-point.
func TestTrustNegative(t *testing.T) {
	s := startSC(t)
	ac1 := idt.EUI64(0x02000000000000D1)
	ac2 := idt.EUI64(0x02000000000000D2)
	a1 := startAC(t, s, enrollAC(t, s, ac1, "lab"), ac1)
	waitFor(t, "AC 1 connected", func() bool { return a1.client(t).Info().Connected })
	ep := idt.EUI64(0x0200000000000003)
	if _, err := a1.node.AddEndPoint(context.Background(), acstore.EndPoint{EUI: ep.String(), NwkKey: testKey, Class: "a"}); err != nil {
		t.Fatal(err)
	}
	a2 := startAC(t, s, enrollAC(t, s, ac2, "lab"), ac2)
	waitFor(t, "AC 2 connected", func() bool { return a2.client(t).Info().Connected })
	c2 := a2.client(t)
	if err := c2.Register(context.Background(), acstore.EndPoint{EUI: ep.String(), NwkKey: testKey, Class: "a"}); !isCode(err, engine.EPERM) {
		t.Fatalf("registering a foreign end-point: %v", err)
	}
	if err := c2.Deregister(context.Background(), ep.String()); !isCode(err, engine.EPERM) {
		t.Fatalf("deregistering a foreign end-point: %v", err)
	}

	// second session with the same EUI and certificate
	dir1 := a1.dir
	dup := startAC(t, s, copyDir(t, dir1), ac1)
	time.Sleep(700 * time.Millisecond)
	if info := dup.client(t).Info(); info.Connected || info.LastError == "" {
		t.Fatalf("second session with the same EUI: %+v", info)
	}
	dup.stop()

	// wrong EUI: a certificate issued for AC 1, used by an AC that says it is AC 3
	b, _ := onboard.Read(filepath.Join(dir1, "sc", "lab", node.BundleFile))
	key, _ := os.ReadFile(filepath.Join(dir1, "sc", "lab", node.KeyFile))
	wrong, err := scclient.New(scclient.Config{Bundle: b, KeyPEM: key, ACEui: 0x02000000000000D3, Schemas: s.set, Log: logger(), ResponseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go wrong.Run(ctx)
	time.Sleep(700 * time.Millisecond)
	if info := wrong.Info(); info.Connected || info.LastError == "" {
		t.Fatalf("wrong EUI: %+v", info)
	}
	cancel()
	if _, err := node.Import(t.TempDir(), filepath.Join(dir1, "sc", "lab", node.BundleFile), 0x02000000000000D3); err == nil {
		t.Fatal("bundle for another EUI imported")
	}

	// certificate of another CA
	other, _, _ := labca.Init(t.TempDir(), "other")
	certPEM, keyPEM, _, _ := other.NewClient(ac1.String(), 0)
	foreign := b
	foreign.Cert = string(certPEM)
	fc, err := scclient.New(scclient.Config{Bundle: foreign, KeyPEM: keyPEM, ACEui: ac1, Schemas: s.set, Log: logger(), ResponseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	go fc.Run(ctx2)
	time.Sleep(700 * time.Millisecond)
	if info := fc.Info(); info.Connected {
		t.Fatal("certificate of another CA accepted")
	}
	cancel2()

	// disabled AC: the session is closed and the next connect refused
	if err := s.reg.SetApplicationCenterState(ac2.String(), "disabled"); err != nil {
		t.Fatal(err)
	}
	s.scaci.CloseSession(ac2.String())
	time.Sleep(1500 * time.Millisecond)
	if info := c2.Info(); info.Connected {
		t.Fatal("disabled AC connected again")
	}
}

func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	for _, f := range []string{node.BundleFile, node.KeyFile} {
		data, err := os.ReadFile(filepath.Join(src, "sc", "lab", f))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.MkdirAll(filepath.Join(dst, "sc", "lab"), 0o755)
		_ = os.WriteFile(filepath.Join(dst, "sc", "lab", f), data, 0o600)
	}
	return dst
}
