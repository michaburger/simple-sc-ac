package bssci

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
	"github.com/michaburger/simple-sc-ac/internal/proto/version"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
	"github.com/michaburger/simple-sc-ac/internal/sim/bs"
	builtin "github.com/michaburger/simple-sc-ac/schemas"
)

// schemas returns the built-in schema files, or the ones in SSCAC_SCHEMA_DIR on top.
func schemas(t *testing.T) *schema.Set {
	t.Helper()
	layers := []fs.FS{builtin.FS}
	if dir := os.Getenv("SSCAC_SCHEMA_DIR"); dir != "" {
		layers = append(layers, os.DirFS(dir))
	}
	set, err := schema.Load(layers...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

type lab struct {
	t      *testing.T
	set    *schema.Set
	srv    *Server
	reg    *registry.Registry
	addr   string
	ca     *labca.CA
	dir    string
	trace  *trace.Memory
	cancel context.CancelFunc
}

func newLab(t *testing.T, policy version.Policy, unknown string) *lab {
	t.Helper()
	l := &lab{t: t, set: schemas(t), dir: t.TempDir(), trace: &trace.Memory{}}
	var err error
	l.ca, _, err = labca.Init(filepath.Join(l.dir, "ca"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ca.Issue(l.dir, labca.Request{Name: "sc", Server: true, Hosts: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	cert, _ := tls.LoadX509KeyPair(filepath.Join(l.dir, "sc.pem"), filepath.Join(l.dir, "sc.key"))
	l.reg, err = registry.Open(filepath.Join(l.dir, "sc.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.reg.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("SSCAC_TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	l.srv, err = New(Config{
		SCEui: 0x02000000000000AA, Schemas: l.set, Registry: l.reg, Log: log, Trace: l.trace,
		TLS:    &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: l.ca.Pool(), ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12},
		Policy: policy, Mode: engine.Lenient, Unknown: unknown, ResponseTimeout: 2 * time.Second, DedupWindow: 300 * time.Millisecond,
		StatusInterval: time.Hour, Name: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	go func() { _ = l.srv.Serve(ctx, tls.NewListener(ln, l.srv.cfg.TLS)) }()
	t.Cleanup(cancel)
	return l
}

func (l *lab) dial(eui idt.EUI64, ver string) (*bs.BS, error) {
	name := "bs-" + eui.String()
	if _, err := l.ca.Issue(l.dir, labca.Request{Name: name, EUI: eui.String()}); err != nil {
		l.t.Fatal(err)
	}
	cert, _ := tls.LoadX509KeyPair(filepath.Join(l.dir, name+".pem"), filepath.Join(l.dir, name+".key"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return bs.Dial(ctx, l.addr, bs.Config{EUI: eui, Version: ver, Bidi: true, Schemas: l.set,
		TLS: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: l.ca.Pool(), ServerName: "127.0.0.1"}})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

const testKey = "000102030405060708090A0B0C0D0E0F" // generated, not a device key

// E2E-1: connect, attach over the air, propagation, deduplicated uplink, detach.
func TestE2E1(t *testing.T) {
	for _, ver := range []string{"1.1.0", "1.0.0"} {
		t.Run(ver, func(t *testing.T) {
			l := newLab(t, version.Follow, "accept")
			if _, ok := l.set.Get(Interface, ver); !ok {
				t.Skip("schema", ver, "not loaded")
			}
			epEUI := idt.EUI64(0x0200000000000001)
			if err := l.reg.PutEndPoint(registry.EndPoint{EUI: epEUI.String(), Class: "a", NwkKey: testKey, AttachCnt: -1, LastPacketCnt: -1}); err != nil {
				t.Fatal(err)
			}
			bs1, err := l.dial(0x02000000000000B1, ver)
			if err != nil {
				t.Fatal(err)
			}
			defer bs1.Close()
			bs2, err := l.dial(0x02000000000000B2, ver)
			if err != nil {
				t.Fatal(err)
			}
			defer bs2.Close()
			if bs1.Session != ver {
				t.Fatalf("session version %s", bs1.Session)
			}
			ep := &bs.EndPoint{EUI: epEUI, NwkKey: mustKey(t)}
			ctx := context.Background()
			res, err := bs1.Attach(ctx, ep, 10, -80)
			if err != nil {
				t.Fatal(err)
			}
			if string(res.NwkSnKey) != string(res.Expected) {
				t.Fatal("session key of the SC differs from the one the end-point derives")
			}
			waitFor(t, "attach stored", func() bool { e, _ := l.reg.EndPoint(epEUI.String()); return e.State == "attached" })
			waitFor(t, "propagation to the second base station", func() bool { p, ok := bs2.Known(epEUI.String()); return ok && p.ShAddr == res.ShAddr })
			if p, _ := bs2.Known(epEUI.String()); string(p.NwkSnKey) != string(res.Expected) {
				t.Fatal("propagated session key differs")
			}
			// replayed attach counter is refused
			ep.AttachCnt--
			if _, err := bs1.Attach(ctx, ep, 10, -80); !isCode(err, engine.EPERM) {
				t.Fatalf("replayed attach: %v", err)
			}
			// the same uplink through both base stations is stored once
			data := []byte{0x11, 0x25}
			if err := bs1.Uplink(ctx, ep, 1, data, 10, -80); err != nil {
				t.Fatal(err)
			}
			if err := bs2.Uplink(ctx, ep, 1, data, 3, -110); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "deduplicated uplink", func() bool { u, _ := l.reg.Uplinks(epEUI.String(), 10); return len(u) == 1 })
			ups, _ := l.reg.Uplinks(epEUI.String(), 10)
			if len(ups[0].Receptions) != 2 || ups[0].Payload != "1125" {
				t.Fatalf("uplink %+v", ups[0])
			}
			// an old packet counter is dropped
			_ = bs1.Uplink(ctx, ep, 1, data, 10, -80)
			time.Sleep(400 * time.Millisecond)
			if u, _ := l.reg.Uplinks(epEUI.String(), 10); len(u) != 1 {
				t.Fatalf("old packet counter stored again: %d uplinks", len(u))
			}
			if err := bs1.Detach(ctx, ep, 10, -80); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "detach", func() bool { e, _ := l.reg.EndPoint(epEUI.String()); return e.State == "detached" })
			waitFor(t, "detach propagation", func() bool { p, _ := bs2.Known(epEUI.String()); return p.Detached })
			for _, r := range l.trace.Snapshot() {
				if r.Command == "attRsp" || r.Command == "attPrp" {
					if r.Message["nwkSnKey"] != trace.Redacted {
						t.Fatal("session key in the trace")
					}
				}
			}
		})
	}
}

func mustKey(t *testing.T) []byte {
	k, err := idt.ParseKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return k[:]
}

func isCode(err error, code int64) bool {
	var oe *engine.OpError
	return errors.As(err, &oe) && oe.Code == code
}

// Connect checks of the trust model and unknown base station modes.
func TestConnectChecks(t *testing.T) {
	l := newLab(t, version.Follow, "reject")
	if _, err := l.dial(0x02000000000000C1, "1.1.0"); !isCode(err, engine.EPERM) {
		t.Fatalf("unknown base station in reject mode: %v", err)
	}
	_ = l.reg.AddBaseStation(idt.EUI64(0x02000000000000C1).String(), "enabled", "")
	b, err := l.dial(0x02000000000000C1, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := l.dial(0x02000000000000C1, "1.1.0"); !isCode(err, engine.EPERM) {
		t.Fatalf("second session with the same EUI: %v", err)
	}
	_ = l.reg.AddBaseStation(idt.EUI64(0x02000000000000C2).String(), "disabled", "")
	if _, err := l.dial(0x02000000000000C2, "1.1.0"); !isCode(err, engine.EPERM) {
		t.Fatalf("disabled base station: %v", err)
	}

	p := newLab(t, version.Follow, "pending")
	pb, err := p.dial(0x02000000000000C3, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer pb.Close()
	if st, _ := p.reg.BaseStation(idt.EUI64(0x02000000000000C3).String()); st.State != "pending" {
		t.Fatalf("state %s", st.State)
	}
	_ = p.reg.PutEndPoint(registry.EndPoint{EUI: "02-00-00-00-00-00-00-01", Class: "a", NwkKey: testKey, AttachCnt: -1, LastPacketCnt: -1})
	ep := &bs.EndPoint{EUI: 0x0200000000000001, NwkKey: mustKey(t)}
	if _, err := pb.Attach(context.Background(), ep, 1, -90); !isCode(err, engine.EPERM) {
		t.Fatalf("attach through a pending base station: %v", err)
	}
}

// E2E-10, BSSCI part: version arbitration branches.
func TestVersionArbitration(t *testing.T) {
	l := newLab(t, version.Follow, "accept")
	if len(l.set.Versions(Interface)) < 2 {
		t.Skip("needs BSSCI 1.0 and 1.1")
	}
	b, err := l.dial(0x02000000000000D1, "1.2.0") // newer minor: SC answers 1.1, the simulator refuses
	if err == nil || !isCode(err, engine.EPROTONOSUPPORT) {
		t.Fatalf("1.2 request: %v (session %v)", err, b)
	}
	if _, err := l.dial(0x02000000000000D2, "2.0.0"); !isCode(err, engine.EPROTONOSUPPORT) {
		t.Fatalf("2.0 request: %v", err)
	}
	o := newLab(t, version.OfferHighest, "accept")
	if _, err := o.dial(0x02000000000000D3, "1.0.0"); !isCode(err, engine.EPROTONOSUPPORT) {
		t.Fatalf("offer-highest to a 1.0 base station should offer 1.1 first: %v", err)
	}
}
