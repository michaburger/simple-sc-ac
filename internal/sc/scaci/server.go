// Package scaci is the Application Center side of simple-sc: it accepts TLS connections
// from Application Centers, checks them against the registry, takes end-point
// registrations and delivers uplinks and end-point status to the AC that owns the
// end-point. Uplinks for an AC that is not connected are kept and delivered after its
// next connect, within limits.
package scaci

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/proto/codec"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/framing"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
	"github.com/michaburger/simple-sc-ac/internal/proto/version"
	"github.com/michaburger/simple-sc-ac/internal/sc/bssci"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
)

// Interface name of SCACI in the schema files.
const Interface = "scaci"

// Config of the server.
type Config struct {
	SCEui           idt.EUI64
	Schemas         *schema.Set
	Registry        *registry.Registry
	BSSCI           *bssci.Server // for propagation and the status of base stations, may be nil
	Log             *slog.Logger
	Trace           trace.Sink
	TLS             *tls.Config
	Policy          version.Policy
	Mode            engine.Mode
	ResponseTimeout time.Duration
	IdlePing        time.Duration
	BufferAge       time.Duration // uplinks for an offline AC are kept this long
	BufferMax       int           // and at most this many per AC
	Name, SwVersion string
	started         time.Time
}

// Server accepts Application Centers. It implements bssci.Events.
type Server struct {
	cfg      Config
	versions []version.Version
	mem      *version.Memory
	mu       sync.Mutex
	sessions map[string]*session
}

// New checks the configuration. At least one SCACI schema file has to be loaded.
func New(cfg Config) (*Server, error) {
	var vs []version.Version
	for _, v := range cfg.Schemas.Versions(Interface) {
		pv, err := version.Parse(v)
		if err != nil {
			return nil, err
		}
		vs = append(vs, pv)
	}
	if len(vs) == 0 {
		return nil, errors.New("scaci: no SCACI schema files loaded")
	}
	if cfg.Trace == nil {
		cfg.Trace = trace.Discard
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 5 * time.Second
	}
	if cfg.BufferAge == 0 {
		cfg.BufferAge = 24 * time.Hour
	}
	if cfg.BufferMax == 0 {
		cfg.BufferMax = 10000
	}
	cfg.started = time.Now()
	return &Server{cfg: cfg, versions: vs, mem: version.NewMemory(), sessions: map[string]*session{}}, nil
}

// SetBSSCI connects the base station side after both servers exist.
func (s *Server) SetBSSCI(b *bssci.Server) { s.cfg.BSSCI = b }

type session struct {
	srv     *Server
	ACEui   string
	Version string
	eng     *engine.Engine
	sch     *schema.Schema
	ready   chan struct{}
	once    sync.Once
	queue   chan func()
	log     *slog.Logger
}

type replay struct {
	net.Conn
	r io.Reader
}

func (c *replay) Read(p []byte) (int, error) { return c.r.Read(p) }

// Serve accepts connections on ln until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); ln.Close() }()
	_ = s.cfg.Registry.ResetACConnections()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, c)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	log := s.cfg.Log.With("peer", conn.RemoteAddr().String())
	defer conn.Close()
	var fingerprint string
	if tc, ok := conn.(*tls.Conn); ok {
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			if errors.Is(err, io.EOF) {
				log.Debug("connection closed before the TLS handshake")
			} else {
				log.Warn("TLS handshake failed", "err", err)
			}
			return
		}
		_ = tc.SetDeadline(time.Time{})
		if pc := tc.ConnectionState().PeerCertificates; len(pc) > 0 {
			fingerprint = labca.Fingerprint(pc[0])
		}
	}
	top, _ := s.cfg.Schemas.Get(Interface, s.versions[len(s.versions)-1].String())
	id, _ := framing.ParseIdentifier(top.Identifier)
	fr := framing.NewReader(conn, id)
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	first, err := fr.ReadFrame()
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		log.Warn("no connect message", "err", err)
		return
	}
	con, err := codec.DecodeMap(first)
	if err != nil || con["command"] != "con" {
		log.Warn("first message is not con", "err", err)
		return
	}
	reqStr, _ := con["version"].(string)
	acN, _ := schema.AsUint64(con["acEui"])
	acEui := idt.EUI64(acN).String()
	log = log.With("acEui", acEui, "requested", reqStr)
	req, perr := version.Parse(reqStr)
	var chosen version.Version
	if perr == nil {
		chosen, perr = version.Accept(s.cfg.Policy, req, s.versions, acEui, s.mem)
	}
	sch := top
	if perr == nil {
		sch, _ = s.cfg.Schemas.Get(Interface, chosen.String())
	}
	header, _ := framing.Header(id, len(first))
	rc := &replay{Conn: conn, r: io.MultiReader(bytes.NewReader(append(header, first...)), conn)}
	sess := &session{srv: s, ACEui: acEui, Version: sch.Version, sch: sch, ready: make(chan struct{}),
		queue: make(chan func(), 256), log: log.With("version", sch.Version)}
	conErr := s.checkConnect(sess, con, fingerprint, perr)
	eng, err := engine.New(engine.Config{
		Schema: sch, Side: engine.Acceptor, Mode: s.cfg.Mode, Trace: s.cfg.Trace, ConnName: "scaci/" + acEui,
		ResponseTimeout: s.cfg.ResponseTimeout, IdlePing: s.cfg.IdlePing,
		Handler: func(ctx context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *engine.OpError) {
			return sess.handle(op, msg, conErr)
		},
		Complete: sess.complete,
	})
	if err != nil {
		log.Error("engine", "err", err)
		return
	}
	sess.eng = eng
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-sess.ready:
			s.established(runCtx, sess)
		case <-done:
		}
	}()
	err = eng.Run(runCtx, rc)
	close(done)
	s.drop(sess)
	if conErr == nil {
		log.Info("Application Center disconnected", "err", err)
	}
}

// checkConnect applies check 2 of the trust model: the certificate is bound to the AC
// registry entry of the EUI in con, the entry is enabled, no other session uses the EUI.
func (s *Server) checkConnect(sess *session, con map[string]any, fingerprint string, verr error) *engine.OpError {
	if verr != nil {
		sess.log.Warn("connect refused: version", "err", verr)
		return engine.Errorf(engine.EPROTONOSUPPORT, "protocol version not supported")
	}
	if id, _ := schema.AsInt64(con["opId"]); id != 0 {
		return engine.Errorf(engine.EINVAL, "connect must use operation ID 0")
	}
	ac, err := s.cfg.Registry.ApplicationCenter(sess.ACEui)
	switch {
	case errors.Is(err, registry.ErrNotFound):
		sess.log.Warn("connect refused: Application Center not registered")
		return engine.Errorf(engine.EPERM, "Application Center not registered")
	case err != nil:
		return engine.Errorf(engine.EPERM, "registry: %v", err)
	case ac.State != "enabled":
		sess.log.Warn("connect refused: Application Center disabled")
		return engine.Errorf(engine.EPERM, "Application Center disabled")
	case fingerprint == "" || ac.Fingerprint != fingerprint:
		sess.log.Warn("connect refused: certificate not bound to this Application Center EUI")
		return engine.Errorf(engine.EPERM, "certificate not bound to this Application Center EUI")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.sessions[sess.ACEui]; busy {
		sess.log.Warn("connect refused: EUI already connected")
		return engine.Errorf(engine.EPERM, "Application Center EUI already in use")
	}
	s.sessions[sess.ACEui] = sess
	return nil
}

func (s *Server) drop(sess *session) {
	s.mu.Lock()
	if s.sessions[sess.ACEui] == sess {
		delete(s.sessions, sess.ACEui)
		s.mu.Unlock()
		_ = s.cfg.Registry.ACConnected(sess.ACEui, "", false)
		return
	}
	s.mu.Unlock()
}

func (s *Server) session(acEui string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[acEui]
}

// Connected lists the EUIs of the connected ACs.
func (s *Server) Connected() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.sessions))
	for k := range s.sessions {
		out = append(out, k)
	}
	return out
}

// CloseSession ends the session of an AC, e.g. after it was disabled.
func (s *Server) CloseSession(acEui string) {
	if sess := s.session(acEui); sess != nil {
		sess.eng.Close()
	}
}

func uuid() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return b
}

func (sess *session) handle(op *schema.Operation, msg map[string]any, conErr *engine.OpError) (map[string]any, *engine.OpError) {
	s := sess.srv
	if op.Command == "con" {
		if conErr != nil {
			return nil, conErr
		}
		return sess.sch.Fit(op, schema.Response, map[string]any{
			"version": sess.Version, "scEui": uint64(s.cfg.SCEui), "snResume": false, "snScUuid": uuid(),
			"name": s.cfg.Name, "vendor": "simple-sc-ac", "model": "simple-sc", "swVersion": s.cfg.SwVersion,
		}), nil
	}
	select {
	case <-sess.ready:
	default:
		return nil, engine.Errorf(engine.EPERM, "no operation before the connect is complete")
	}
	switch op.Command {
	case "reg":
		return sess.onRegister(msg)
	case "dereg":
		return sess.onDeregister(msg)
	case "status":
		return sess.onStatus(op)
	case "discon":
		return sess.sch.Fit(op, schema.Response, map[string]any{}), nil
	}
	return nil, engine.Errorf(engine.EOPNOTSUPP, "%s not supported by simple-sc yet", op.Command)
}

func (sess *session) complete(op *schema.Operation, _ int64, msg map[string]any, err error) {
	switch op.Command {
	case "con":
		if err == nil && msg != nil && msg["command"] == "conCmp" {
			sess.once.Do(func() { close(sess.ready) })
		} else {
			sess.eng.Close()
		}
	case "discon":
		sess.eng.Close()
	}
}

// established runs after conCmp: registry, delivery worker, buffered uplinks.
func (s *Server) established(ctx context.Context, sess *session) {
	_ = s.cfg.Registry.ACConnected(sess.ACEui, sess.Version, true)
	sess.log.Info("Application Center connected")
	go func() { // one worker per session keeps the order of uplinks and status
		for {
			select {
			case f := <-sess.queue:
				f()
			case <-sess.eng.Done():
				return
			}
		}
	}()
	ups, dropped, err := s.cfg.Registry.UndeliveredUplinks(sess.ACEui, s.cfg.BufferAge, s.cfg.BufferMax)
	if err != nil {
		sess.log.Error("buffered uplinks", "err", err)
		return
	}
	if dropped > 0 {
		_ = s.cfg.Registry.Count(sess.ACEui, "uplinks_dropped", dropped)
		sess.log.Warn("buffered uplinks dropped, older or more than the limit", "count", dropped)
	}
	if len(ups) > 0 {
		sess.log.Info("delivering buffered uplinks", "count", len(ups))
	}
	for _, u := range ups {
		s.enqueueUplink(sess, u)
	}
}

func bool2(m map[string]any, k string) bool { b, _ := m[k].(bool); return b }

// onRegister takes an end-point registration. An end-point belongs to the AC that
// registered it first, another AC gets EPERM.
func (sess *session) onRegister(msg map[string]any) (map[string]any, *engine.OpError) {
	s := sess.srv
	u, _ := schema.AsUint64(msg["epEui"])
	eui := idt.EUI64(u).String()
	if old, err := s.cfg.Registry.EndPoint(eui); err == nil && old.OwnerAC != "" && old.OwnerAC != sess.ACEui {
		sess.log.Warn("registration refused: end-point belongs to another Application Center", "epEui", eui)
		return nil, engine.Errorf(engine.EPERM, "End Point registered by another Application Center")
	}
	key, _ := schema.AsBytes(msg["nwkKey"])
	class, _ := msg["epClass"].(string)
	attachCnt, _ := schema.AsInt64(msg["attachCnt"])
	packetCnt, _ := schema.AsInt64(msg["packetCnt"])
	e := registry.EndPoint{
		EUI: eui, OwnerAC: sess.ACEui, Class: class, NwkKey: strings.ToUpper(hex.EncodeToString(key)),
		PreAttach: bool2(msg, "preAttach"), AttachCnt: attachCnt - 1, LastPacketCnt: packetCnt - 1,
		DualChan: bool2(msg, "dualChan"), Repetition: bool2(msg, "repetition"), WideCarrOff: bool2(msg, "wideCarrOff"), LongBlkDist: bool2(msg, "longBlkDist"),
	}
	if sh, ok := schema.AsUint64(msg["shAddr"]); ok {
		v := int(sh)
		e.ShAddr = &v
	}
	if err := s.cfg.Registry.PutEndPoint(e); err != nil {
		return nil, engine.Errorf(engine.EINVAL, "%v", err)
	}
	_ = s.cfg.Registry.Count(sess.ACEui, "registrations", 1)
	sess.log.Info("end-point registered", "epEui", eui, "class", class, "preAttach", e.PreAttach)
	if e.PreAttach && s.cfg.BSSCI != nil {
		s.cfg.BSSCI.PropagateEndPoint(eui)
	}
	return map[string]any{}, nil
}

func (sess *session) onDeregister(msg map[string]any) (map[string]any, *engine.OpError) {
	s := sess.srv
	u, _ := schema.AsUint64(msg["epEui"])
	eui := idt.EUI64(u).String()
	e, err := s.cfg.Registry.EndPoint(eui)
	if err != nil {
		return nil, engine.Errorf(engine.EOPNOTSUPP, "End Point unknown")
	}
	if e.OwnerAC != sess.ACEui {
		return nil, engine.Errorf(engine.EPERM, "End Point not associated")
	}
	if s.cfg.BSSCI != nil {
		s.cfg.BSSCI.RevokeEndPoint(eui)
	}
	if err := s.cfg.Registry.DeleteEndPoint(eui); err != nil {
		return nil, engine.Errorf(engine.EINVAL, "%v", err)
	}
	sess.log.Info("end-point deregistered", "epEui", eui)
	return map[string]any{}, nil
}

func (sess *session) onStatus(op *schema.Operation) (map[string]any, *engine.OpError) {
	s := sess.srv
	var bss []any
	if s.cfg.BSSCI != nil {
		for _, b := range s.cfg.BSSCI.Sessions() {
			bss = append(bss, map[string]any{"bsEui": euiNum(b.BSEui), "bsClass": b.Class, "code": uint64(0), "message": "connected"})
		}
	}
	return sess.sch.Fit(op, schema.Response, map[string]any{
		"code": uint64(0), "message": "ok", "time": uint64(time.Now().UnixNano()),
		"uptime": uint64(time.Since(s.cfg.started).Seconds()), "baseStations": bss,
	}), nil
}

func euiNum(s string) uint64 {
	e, _ := idt.ParseEUI64(s)
	return uint64(e)
}

// receptionObject builds one entry of baseStations for ulData and epStat. Fields the base
// station did not give, or that do not fit SCACI (such as subpackets without rxTime from
// BSSCI 1.0), are left out.
func (sess *session) receptionObject(r registry.Reception) map[string]any {
	td := sess.sch.Types["uldata.baseStation"].Fields
	out := map[string]any{"bsEui": euiNum(r.BSEui), "rxTime": uint64(r.RxNs), "snr": r.SNR, "rssi": r.RSSI}
	for k, v := range r.Extra {
		if _, ok := td[k]; !ok {
			continue
		}
		if k == "subpackets" {
			sp, ok := v.(map[string]any)
			if _, has := sp["rxTime"]; !ok || !has {
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (s *Server) enqueueUplink(sess *session, u registry.Uplink) {
	select {
	case sess.queue <- func() { s.deliver(sess, u) }:
	default:
		sess.log.Warn("delivery queue full, uplink stays buffered", "epEui", u.EPEui)
	}
}

func (s *Server) deliver(sess *session, u registry.Uplink) {
	op := sess.sch.Operations["ulData"]
	recs := make([]any, 0, len(u.Receptions))
	for _, r := range u.Receptions {
		recs = append(recs, sess.receptionObject(r))
	}
	data, _ := hex.DecodeString(u.Payload)
	fields := sess.sch.Fit(op, schema.Init, map[string]any{
		"epEui": euiNum(u.EPEui), "baseStations": recs, "packetCnt": uint64(u.PacketCnt), "userData": data,
		"format": uint64(u.Format), "dlOpen": u.DlOpen, "responseExp": u.ResponseExp, "dlAck": u.DlAck,
	})
	if _, err := sess.eng.Call(context.Background(), "ulData", fields); err != nil {
		sess.log.Warn("uplink not delivered, stays buffered", "epEui", u.EPEui, "packetCnt", u.PacketCnt, "err", err)
		return
	}
	_ = s.cfg.Registry.MarkDelivered(u.ID)
	_ = s.cfg.Registry.Count(sess.ACEui, "uplinks_delivered", 1)
}

// Uplink is called by the BSSCI side for every deduplicated uplink.
func (s *Server) Uplink(u registry.Uplink) {
	ep, err := s.cfg.Registry.EndPoint(u.EPEui)
	if err != nil || ep.OwnerAC == "" {
		return
	}
	sess := s.session(ep.OwnerAC)
	if sess == nil {
		_ = s.cfg.Registry.Count(ep.OwnerAC, "uplinks_buffered", 1)
		return
	}
	select {
	case <-sess.ready:
		s.enqueueUplink(sess, u)
	default:
	}
}

func (s *Server) sendStatus(ownerAC string, fields func(sess *session) map[string]any) {
	sess := s.session(ownerAC)
	if sess == nil {
		return
	}
	select {
	case <-sess.ready:
	default:
		return
	}
	f := fields(sess)
	select {
	case sess.queue <- func() {
		if _, err := sess.eng.Call(context.Background(), "epStat", f); err != nil {
			sess.log.Warn("end-point status not delivered", "err", err)
		}
	}:
	default:
	}
}

// Attached is called by the BSSCI side after an over-the-air attach.
func (s *Server) Attached(ep registry.EndPoint, a bssci.AttachInfo) {
	s.sendStatus(ep.OwnerAC, func(sess *session) map[string]any {
		return sess.sch.Fit(sess.sch.Operations["epStat"], schema.Init, map[string]any{
			"epEui": euiNum(ep.EUI), "epStatus": "attached", "baseStations": []any{sess.receptionObject(a.Reception)},
			"attachCnt": uint64(a.AttachCnt), "nonce": a.Nonce, "sign": a.Sign, "shAddr": uint64(a.ShAddr),
		})
	})
}

// Detached is called by the BSSCI side after a detach.
func (s *Server) Detached(epEui string, d bssci.DetachInfo) {
	ep, err := s.cfg.Registry.EndPoint(epEui)
	if err != nil {
		return
	}
	s.sendStatus(ep.OwnerAC, func(sess *session) map[string]any {
		attach := d.AttachCnt
		if attach < 0 {
			attach = 0
		}
		return sess.sch.Fit(sess.sch.Operations["epStat"], schema.Init, map[string]any{
			"epEui": euiNum(epEui), "epStatus": "detached", "baseStations": []any{sess.receptionObject(d.Reception)},
			"attachCnt": uint64(attach), "packetCnt": uint64(d.PacketCnt), "sign": d.Sign,
		})
	})
}
