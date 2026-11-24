// Package bssci is the base station side of simple-sc: it accepts TLS connections from
// base stations, negotiates the BSSCI version, checks the base station, and handles
// attach, detach and uplinks. Message definitions come from the loaded schema files, the
// version of each session selects the schema.
package bssci

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
)

// Interface name of BSSCI in the schema files.
const Interface = "bssci"

// Events lets another part of simple-sc (the SCACI side) follow what happens on BSSCI.
type Events interface {
	Uplink(u registry.Uplink)
	Attached(ep registry.EndPoint, a AttachInfo)
	Detached(epEui string, d DetachInfo)
}

// AttachInfo describes an accepted over-the-air attach.
type AttachInfo struct {
	BSEui     string
	AttachCnt int64
	Nonce     []byte
	Sign      []byte
	ShAddr    int
	Reception registry.Reception
}

// DetachInfo describes a detach.
type DetachInfo struct {
	BSEui     string
	AttachCnt int64
	PacketCnt int64
	Sign      []byte
	Reception registry.Reception
}

// Config of the server.
type Config struct {
	Events          Events // optional
	SCEui           idt.EUI64
	Schemas         *schema.Set
	Registry        *registry.Registry
	Log             *slog.Logger
	Trace           trace.Sink
	TLS             *tls.Config // server certificate, client CA pool, RequireAndVerifyClientCert
	Policy          version.Policy
	Mode            engine.Mode
	Unknown         string // reject, pending, accept
	ResponseTimeout time.Duration
	IdlePing        time.Duration
	StatusInterval  time.Duration
	DedupWindow     time.Duration
	Name            string
	SwVersion       string
}

// Server accepts base stations.
type Server struct {
	cfg      Config
	versions []version.Version
	mem      *version.Memory
	dedup    *dedup

	mu       sync.Mutex
	sessions map[string]*Session // by bsEui text
	attaches map[string]attachClaim
}

type attachClaim struct {
	bs    string
	cnt   int64
	until time.Time
}

// New checks the configuration. At least one BSSCI schema file has to be loaded.
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
		return nil, errors.New("bssci: no BSSCI schema files loaded")
	}
	if cfg.Trace == nil {
		cfg.Trace = trace.Discard
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 5 * time.Second
	}
	if cfg.DedupWindow == 0 {
		cfg.DedupWindow = 500 * time.Millisecond
	}
	s := &Server{cfg: cfg, versions: vs, mem: version.NewMemory(), sessions: map[string]*Session{}, attaches: map[string]attachClaim{}}
	s.dedup = newDedup(cfg.DedupWindow, s.storeUplink)
	return s, nil
}

// Serve accepts connections on ln until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); ln.Close() }()
	_ = s.cfg.Registry.ResetConnections()
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

// Session is one connected base station.
type Session struct {
	srv     *Server
	BSEui   string
	Version string
	State   string // registry state at connect: enabled or pending
	Class   string
	Bidi    bool
	eng     *engine.Engine
	sch     *schema.Schema
	ready   chan struct{} // closed after conCmp
	once    sync.Once
	mu      sync.Mutex
	pending map[int64]pendingAttach // attach opId -> data, committed on attCmp
	log     *slog.Logger
}

type pendingAttach struct {
	ep        registry.EndPoint
	attachCnt int64
	snKey     string
	shAddr    int
	nonce     []byte
	sign      []byte
	rec       registry.Reception
}

// replay is a connection whose first frame was already read: it serves those bytes first.
type replay struct {
	net.Conn
	r io.Reader
}

func (c *replay) Read(p []byte) (int, error) { return c.r.Read(p) }

func (s *Server) handle(ctx context.Context, raw net.Conn) {
	log := s.cfg.Log.With("peer", raw.RemoteAddr().String())
	defer raw.Close()
	conn := raw
	var fingerprint string
	if tc, ok := raw.(*tls.Conn); ok {
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			if errors.Is(err, io.EOF) { // TCP health checks and port scans
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
	any1, _ := s.cfg.Schemas.Get(Interface, s.versions[len(s.versions)-1].String())
	id, _ := framing.ParseIdentifier(any1.Identifier)
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
	bsEuiN, _ := schema.AsUint64(con["bsEui"])
	bsEui := idt.EUI64(bsEuiN).String()
	log = log.With("bsEui", bsEui, "requested", reqStr)
	req, perr := version.Parse(reqStr)
	var chosen version.Version
	if perr == nil {
		chosen, perr = version.Accept(s.cfg.Policy, req, s.versions, bsEui, s.mem)
	}
	sessSchema := any1
	if perr == nil {
		sessSchema, _ = s.cfg.Schemas.Get(Interface, chosen.String())
	}
	header, _ := framing.Header(id, len(first))
	rc := &replay{Conn: conn, r: io.MultiReader(bytes.NewReader(append(header, first...)), conn)}

	sess := &Session{srv: s, BSEui: bsEui, Version: sessSchema.Version, sch: sessSchema, ready: make(chan struct{}),
		pending: map[int64]pendingAttach{}, log: log.With("version", sessSchema.Version)}
	// The decision about the connect is taken before the engine starts, the handler only
	// answers it.
	conErr := s.checkConnect(sess, con, fingerprint, perr)
	if conErr == nil {
		sess.Bidi, _ = con["bidi"].(bool)
		sess.Class, _ = con["bsClass"].(string)
		if sess.Class == "" { // BSSCI 1.0 has no class (D-26)
			sess.Class = "z"
			if sess.Bidi {
				sess.Class = "a"
			}
		}
	}
	eng, err := engine.New(engine.Config{
		Schema: sessSchema, Side: engine.Acceptor, Mode: s.cfg.Mode, Trace: s.cfg.Trace, ConnName: "bssci/" + bsEui,
		ResponseTimeout: s.cfg.ResponseTimeout, IdlePing: s.cfg.IdlePing,
		Handler: func(ctx context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *engine.OpError) {
			return sess.handle(ctx, op, msg, conErr)
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
		log.Info("base station disconnected", "err", err)
	}
}

// checkConnect applies checks 2 of the trust model to a connect.
func (s *Server) checkConnect(sess *Session, con map[string]any, fingerprint string, verr error) *engine.OpError {
	if verr != nil {
		sess.log.Warn("connect refused: version", "err", verr)
		return engine.Errorf(engine.EPROTONOSUPPORT, "protocol version not supported")
	}
	if id, _ := schema.AsInt64(con["opId"]); id != 0 {
		return engine.Errorf(engine.EINVAL, "connect must use operation ID 0")
	}
	reg := s.cfg.Registry
	bs, err := reg.BaseStation(sess.BSEui)
	switch {
	case errors.Is(err, registry.ErrNotFound):
		switch s.cfg.Unknown {
		case "accept":
			if err := reg.AddBaseStation(sess.BSEui, "enabled", fingerprint); err != nil {
				return engine.Errorf(engine.EPERM, "registry: %v", err)
			}
			sess.State = "enabled"
			sess.log.Info("unknown base station accepted (lab mode)", "fingerprint", fingerprint)
		case "pending":
			if err := reg.AddBaseStation(sess.BSEui, "pending", fingerprint); err != nil {
				return engine.Errorf(engine.EPERM, "registry: %v", err)
			}
			sess.State = "pending"
			sess.log.Warn("unknown base station connected as pending, accept it to give it end-point data", "fingerprint", fingerprint)
		default:
			sess.log.Warn("connect refused: unknown base station")
			return engine.Errorf(engine.EPERM, "base station not registered")
		}
	case err != nil:
		return engine.Errorf(engine.EPERM, "registry: %v", err)
	default:
		if bs.State == "disabled" {
			sess.log.Warn("connect refused: base station disabled")
			return engine.Errorf(engine.EPERM, "base station disabled")
		}
		if bs.Fingerprint != "" && fingerprint != "" && bs.Fingerprint != fingerprint {
			sess.log.Warn("connect refused: certificate does not match the registered base station")
			return engine.Errorf(engine.EPERM, "certificate not bound to this base station EUI")
		}
		if bs.Fingerprint == "" && fingerprint != "" {
			_ = reg.SetBaseStationState(sess.BSEui, bs.State, fingerprint)
		}
		sess.State = bs.State
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.sessions[sess.BSEui]; busy {
		sess.log.Warn("connect refused: EUI already connected")
		return engine.Errorf(engine.EPERM, "base station EUI already in use")
	}
	s.sessions[sess.BSEui] = sess
	return nil
}

func (s *Server) drop(sess *Session) {
	s.mu.Lock()
	if s.sessions[sess.BSEui] == sess {
		delete(s.sessions, sess.BSEui)
		s.mu.Unlock()
		_ = s.cfg.Registry.Disconnected(sess.BSEui)
		return
	}
	s.mu.Unlock()
}

// Sessions returns the connected base stations.
func (s *Server) Sessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	return out
}

func randomUUID() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return b
}

func (sess *Session) handle(ctx context.Context, op *schema.Operation, msg map[string]any, conErr *engine.OpError) (map[string]any, *engine.OpError) {
	if op.Command == "con" {
		if conErr != nil {
			return nil, conErr
		}
		return sess.sch.Fit(op, schema.Response, map[string]any{
			"version": sess.Version, "scEui": uint64(sess.srv.cfg.SCEui), "snResume": false, "snScUuid": randomUUID(),
			"name": sess.srv.cfg.Name, "vendor": "simple-sc-ac", "model": "simple-sc", "swVersion": sess.srv.cfg.SwVersion,
		}), nil
	}
	select {
	case <-sess.ready:
	default:
		return nil, engine.Errorf(engine.EPERM, "no operation before the connect is complete")
	}
	switch op.Command {
	case "att":
		return sess.onAttach(msg)
	case "det":
		return sess.onDetach(msg)
	case "ulData":
		return sess.onUplink(msg)
	case "discon":
		return sess.sch.Fit(op, schema.Response, map[string]any{}), nil
	case "dlDataRes", "dlRxStat":
		sess.log.Info("received, downlink handling is not implemented yet", "command", op.Command)
		return map[string]any{}, nil
	}
	return nil, engine.Errorf(engine.EOPNOTSUPP, "%s not supported by simple-sc yet", op.Command)
}

func (sess *Session) complete(op *schema.Operation, opID int64, msg map[string]any, err error) {
	switch op.Command {
	case "con":
		if err == nil && msg != nil && msg["command"] == "conCmp" {
			sess.once.Do(func() { close(sess.ready) })
		} else {
			sess.eng.Close()
		}
	case "att":
		sess.mu.Lock()
		pa, ok := sess.pending[opID]
		delete(sess.pending, opID)
		sess.mu.Unlock()
		if ok && err == nil {
			sess.srv.commitAttach(sess, pa)
		}
	case "discon":
		sess.eng.Close()
	}
}

// established runs after conCmp: registry update, state sync, status polling.
func (s *Server) established(ctx context.Context, sess *Session) {
	_ = s.cfg.Registry.Connected(registry.BaseStation{EUI: sess.BSEui, Version: sess.Version, Class: sess.Class, Bidi: sess.Bidi})
	sess.log.Info("base station connected", "state", sess.State, "class", sess.Class)
	if sess.State == "enabled" {
		s.syncEndPoints(ctx, sess)
	}
	if s.cfg.StatusInterval > 0 {
		go s.pollStatus(ctx, sess)
	}
}

// Enable is called by the admin interface when a pending base station is accepted.
func (s *Server) Enable(ctx context.Context, bsEui string) {
	s.mu.Lock()
	sess := s.sessions[bsEui]
	s.mu.Unlock()
	if sess != nil {
		sess.State = "enabled"
		s.syncEndPoints(ctx, sess)
	}
}

// Close ends the session of a base station, e.g. after it was disabled.
func (s *Server) CloseSession(bsEui string) {
	s.mu.Lock()
	sess := s.sessions[bsEui]
	s.mu.Unlock()
	if sess != nil {
		sess.eng.Close()
	}
}

// ClientCAPool reads a CA file for the TLS configuration.
func ClientCAPool(pem []byte) (*x509.CertPool, error) {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("bssci: no certificate in CA file")
	}
	return p, nil
}
