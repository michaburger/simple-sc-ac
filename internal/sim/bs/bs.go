// Package bs is a simulated base station for tests and for the sim-bs tool. It speaks
// BSSCI through the same engine as simple-sc, from the opener side, and produces attach
// requests and uplinks of simulated end-points with real signatures.
package bs

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/mcrypto"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
	"github.com/michaburger/simple-sc-ac/internal/proto/version"
)

// Config of a simulated base station.
type Config struct {
	EUI     idt.EUI64
	Version string // version to request, e.g. "1.1.0"
	Bidi    bool
	Class   string // bsClass on 1.1, default "a"
	Schemas *schema.Set
	TLS     *tls.Config
	Trace   trace.Sink
	Timeout time.Duration
}

// Propagated is what the SC told this base station about an end-point.
type Propagated struct {
	ShAddr   uint64
	NwkSnKey []byte
	Detached bool
}

// BS is a connected simulated base station.
type BS struct {
	cfg     Config
	Session string // version of the session as answered by the SC
	eng     *engine.Engine
	conn    net.Conn
	sch     *schema.Schema

	mu    sync.Mutex
	known map[string]Propagated
	polls int
	done  chan error
}

// Dial connects to the SC at addr and runs the connect operation.
func Dial(ctx context.Context, addr string, cfg Config) (*BS, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Class == "" {
		cfg.Class = "a"
	}
	req, err := version.Parse(cfg.Version)
	if err != nil {
		return nil, err
	}
	sch, ok := cfg.Schemas.Get("bssci", cfg.Version)
	if !ok { // a version we have no schema for: talk with the closest one we know
		for _, v := range cfg.Schemas.Versions("bssci") {
			if pv, _ := version.Parse(v); pv.Major == req.Major {
				sch, _ = cfg.Schemas.Get("bssci", v)
			}
		}
	}
	if sch == nil { // other major version: frame and connect with the highest schema we have
		vs := cfg.Schemas.Versions("bssci")
		if len(vs) == 0 {
			return nil, fmt.Errorf("sim-bs: no BSSCI schema loaded")
		}
		sch, _ = cfg.Schemas.Get("bssci", vs[len(vs)-1])
	}
	d := &tls.Dialer{Config: cfg.TLS}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	b := &BS{cfg: cfg, conn: conn, sch: sch, known: map[string]Propagated{}, done: make(chan error, 1)}
	b.eng, err = engine.New(engine.Config{Schema: sch, Side: engine.Opener, Handler: b.handle, Trace: cfg.Trace,
		ConnName: "sim-bs/" + cfg.EUI.String(), ResponseTimeout: cfg.Timeout, Mode: engine.Strict})
	if err != nil {
		conn.Close()
		return nil, err
	}
	go func() { b.done <- b.eng.Run(context.Background(), conn) }()
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	con := sch.Fit(sch.Operations["con"], schema.Init, map[string]any{
		"version": cfg.Version, "bsEui": uint64(cfg.EUI), "bidi": cfg.Bidi, "bsClass": cfg.Class, "snBsUuid": uuid,
		"vendor": "simple-sc-ac", "model": "sim-bs",
	})
	rsp, err := b.eng.CallCheck(ctx, "con", con, func(rsp map[string]any) *engine.OpError {
		answered, _ := rsp["version"].(string)
		if answered == "" {
			answered = cfg.Version
		}
		av, err := version.Parse(answered)
		if err != nil || !av.Compatible(req) {
			return engine.Errorf(engine.EPROTONOSUPPORT, "sim-bs speaks %s only", cfg.Version)
		}
		return nil
	})
	if err != nil {
		b.Close()
		return nil, err
	}
	b.Session, _ = rsp["version"].(string)
	if b.Session == "" {
		b.Session = cfg.Version
	}
	return b, nil
}

// Close ends the connection.
func (b *BS) Close() { b.eng.Close(); b.conn.Close() }

// Done is closed when the connection ended.
func (b *BS) Done() <-chan struct{} { return b.eng.Done() }

func (b *BS) handle(_ context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *engine.OpError) {
	eui := func() string { u, _ := schema.AsUint64(msg["epEui"]); return idt.EUI64(u).String() }
	switch op.Command {
	case "attPrp":
		sh, _ := schema.AsUint64(msg["shAddr"])
		key, _ := schema.AsBytes(msg["nwkSnKey"])
		b.mu.Lock()
		b.known[eui()] = Propagated{ShAddr: sh, NwkSnKey: key}
		b.mu.Unlock()
		return map[string]any{}, nil
	case "detPrp":
		b.mu.Lock()
		b.known[eui()] = Propagated{Detached: true}
		b.mu.Unlock()
		return map[string]any{}, nil
	case "status":
		b.mu.Lock()
		b.polls++
		b.mu.Unlock()
		return b.sch.Fit(op, schema.Response, map[string]any{
			"code": uint64(0), "message": "ok", "time": uint64(time.Now().UnixNano()), "dutyCycle": 0.0, "uptime": uint64(1),
		}), nil
	}
	return nil, engine.Errorf(engine.EOPNOTSUPP, "sim-bs does not handle %s", op.Command)
}

// Known returns what the SC propagated for an end-point.
func (b *BS) Known(ep string) (Propagated, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.known[ep]
	return p, ok
}

// StatusPolls counts the status operations of the SC.
func (b *BS) StatusPolls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.polls
}

// EndPoint is a simulated end-point.
type EndPoint struct {
	EUI       idt.EUI64
	NwkKey    []byte // pre-shared network key, generated for tests
	AttachCnt uint32
	PacketCnt uint32
}

// AttachResult is what the SC answered.
type AttachResult struct {
	ShAddr   uint64
	NwkSnKey []byte
	Expected []byte // session key the end-point derives itself
}

// Attach sends an over-the-air attach request of ep. The signature is computed from the
// network key, as the end-point would do it.
func (b *BS) Attach(ctx context.Context, ep *EndPoint, snr, rssi float64) (AttachResult, error) {
	ep.AttachCnt++
	nonce := make([]byte, 4)
	_, _ = rand.Read(nonce)
	sign, err := mcrypto.AttachSignature(ep.NwkKey, uint64(ep.EUI), ep.AttachCnt)
	if err != nil {
		return AttachResult{}, err
	}
	op := b.sch.Operations["att"]
	rsp, err := b.eng.Call(ctx, "att", b.sch.Fit(op, schema.Init, map[string]any{
		"epEui": uint64(ep.EUI), "rxTime": uint64(time.Now().UnixNano()), "attachCnt": uint64(ep.AttachCnt),
		"snr": snr, "rssi": rssi, "nonce": nonce, "sign": sign[:], "dualChan": false, "repetition": false,
		"wideCarrOff": false, "longBlkDist": false, "profile": "eu1",
	}))
	if err != nil {
		return AttachResult{}, err
	}
	exp, _ := mcrypto.SessionKey(ep.NwkKey, uint64(ep.EUI), nonce, sign[:])
	key, _ := schema.AsBytes(rsp["nwkSnKey"])
	sh, _ := schema.AsUint64(rsp["shAddr"])
	ep.PacketCnt = 0
	return AttachResult{ShAddr: sh, NwkSnKey: key, Expected: exp[:]}, nil
}

// Uplink sends one uplink of ep with the given packet counter.
func (b *BS) Uplink(ctx context.Context, ep *EndPoint, packetCnt uint32, data []byte, snr, rssi float64) error {
	op := b.sch.Operations["ulData"]
	_, err := b.eng.Call(ctx, "ulData", b.sch.Fit(op, schema.Init, map[string]any{
		"epEui": uint64(ep.EUI), "rxTime": uint64(time.Now().UnixNano()), "packetCnt": uint64(packetCnt),
		"snr": snr, "rssi": rssi, "userData": data, "dlOpen": false, "responseExp": false, "dlAck": false,
		"profile": "eu1", "mode": "ulp",
	}))
	return err
}

// Detach sends a detach request of ep. The end-point signature is not defined in the
// documents available, so it is zeros.
func (b *BS) Detach(ctx context.Context, ep *EndPoint, snr, rssi float64) error {
	op := b.sch.Operations["det"]
	_, err := b.eng.Call(ctx, "det", b.sch.Fit(op, schema.Init, map[string]any{
		"epEui": uint64(ep.EUI), "rxTime": uint64(time.Now().UnixNano()), "packetCnt": uint64(ep.PacketCnt),
		"snr": snr, "rssi": rssi, "sign": []byte{0, 0, 0, 0},
	}))
	return err
}

// Disconnect ends the session gracefully on 1.1 and closes the connection on 1.0.
func (b *BS) Disconnect(ctx context.Context) error {
	if _, ok := b.sch.Operations["discon"]; ok {
		if _, err := b.eng.Call(ctx, "discon", nil); err != nil && !errors.Is(err, engine.ErrClosed) {
			return err
		}
	}
	b.Close()
	return nil
}
