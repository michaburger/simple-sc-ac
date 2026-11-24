// Package scclient is the connection of simple-ac to one Service Center over SCACI. It
// connects with the certificate from the enrollment bundle, checks the pinned SC EUI,
// reconnects after a loss, and passes uplinks and end-point status to the AC.
package scclient

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/ac/store"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/onboard"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
	"github.com/michaburger/simple-sc-ac/internal/proto/version"
)

// Interface name of SCACI in the schema files.
const Interface = "scaci"

// Handler receives what the SC sends. Calls come from the connection goroutine.
type Handler interface {
	Uplink(sc string, msg map[string]any)
	EndPointStatus(sc string, msg map[string]any)
	Connected(c *Client)
}

// Config of one connection.
type Config struct {
	Bundle          onboard.Bundle
	KeyPEM          []byte
	ACEui           idt.EUI64
	Schemas         *schema.Set
	Handler         Handler
	Log             *slog.Logger
	Trace           trace.Sink
	ResponseTimeout time.Duration
	IdlePing        time.Duration
	Name, SwVersion string
}

// Client is the connection to one Service Center.
type Client struct {
	cfg      Config
	tls      *tls.Config
	versions []version.Version
	mem      *version.Memory

	mu      sync.Mutex
	eng     *engine.Engine
	sch     *schema.Schema
	version string
	lastErr string
}

// New prepares a client. It does not connect yet.
func New(cfg Config) (*Client, error) {
	cert, err := cfg.Bundle.ClientCertificate(cfg.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("scclient %s: certificate and key: %w", cfg.Bundle.Name, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(cfg.Bundle.CA)) {
		return nil, fmt.Errorf("scclient %s: no CA certificate in the bundle", cfg.Bundle.Name)
	}
	sn := cfg.Bundle.ServerName
	if sn == "" {
		sn, _, _ = net.SplitHostPort(cfg.Bundle.Address)
	}
	var vs []version.Version
	for _, v := range cfg.Schemas.Versions(Interface) {
		pv, _ := version.Parse(v)
		vs = append(vs, pv)
	}
	if len(vs) == 0 {
		return nil, errors.New("scclient: no SCACI schema files loaded")
	}
	if cfg.Trace == nil {
		cfg.Trace = trace.Discard
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 5 * time.Second
	}
	return &Client{cfg: cfg, versions: vs, mem: version.NewMemory(),
		tls: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool, ServerName: sn, MinVersion: tls.VersionTLS12}}, nil
}

// Name of the Service Center.
func (c *Client) Name() string { return c.cfg.Bundle.Name }

// Info describes the state of the connection.
type Info struct {
	Name      string `json:"name"`
	Address   string `json:"address"`
	SCEui     string `json:"scEui"`
	Connected bool   `json:"connected"`
	Version   string `json:"version,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

// Info returns the state of the connection.
func (c *Client) Info() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Info{Name: c.cfg.Bundle.Name, Address: c.cfg.Bundle.Address, SCEui: c.cfg.Bundle.SCEui,
		Connected: c.eng != nil, Version: c.version, LastError: c.lastErr}
}

// Run keeps the connection up until ctx ends, with a backoff between attempts.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	opener := version.NewOpener(c.versions, c.cfg.Bundle.Name, c.mem)
	for ctx.Err() == nil {
		v, ok := opener.Request()
		if !ok { // every version refused: start over after the backoff
			opener = version.NewOpener(c.versions, c.cfg.Bundle.Name, nil)
			v, _ = opener.Request()
		}
		err := c.session(ctx, v)
		var oe *engine.OpError
		switch {
		case errors.As(err, &oe) && oe.Code == engine.EPROTONOSUPPORT:
			c.cfg.Log.Warn("SC refused the version, trying the next lower one", "sc", c.Name(), "version", v.String())
			opener.Refused()
			continue
		case err == nil:
			backoff = time.Second
		default:
			c.setErr(err)
			c.cfg.Log.Warn("connection to Service Center failed", "sc", c.Name(), "err", err, "retry", backoff.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (c *Client) setErr(err error) {
	c.mu.Lock()
	c.lastErr = err.Error()
	c.mu.Unlock()
}

// session runs one connection until it ends. It returns nil after a session that was up.
func (c *Client) session(ctx context.Context, v version.Version) error {
	sch, ok := c.cfg.Schemas.Get(Interface, v.String())
	if !ok {
		return fmt.Errorf("no schema for SCACI %s", v)
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: c.tls}
	conn, err := d.DialContext(ctx, "tcp", c.cfg.Bundle.Address)
	if err != nil {
		return err
	}
	eng, err := engine.New(engine.Config{Schema: sch, Side: engine.Opener, Handler: c.handle, Trace: c.cfg.Trace,
		ConnName: "scaci/" + c.cfg.Bundle.Name, ResponseTimeout: c.cfg.ResponseTimeout, IdlePing: c.cfg.IdlePing, Mode: engine.Strict})
	if err != nil {
		conn.Close()
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- eng.Run(runCtx, conn) }()

	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	con := sch.Fit(sch.Operations["con"], schema.Init, map[string]any{
		"version": v.String(), "acEui": uint64(c.cfg.ACEui), "snAcUuid": uuid,
		"vendor": "simple-sc-ac", "model": "simple-ac", "name": c.cfg.Name, "swVersion": c.cfg.SwVersion,
	})
	pin, _ := idt.ParseEUI64(c.cfg.Bundle.SCEui)
	_, err = eng.CallCheck(ctx, "con", con, func(rsp map[string]any) *engine.OpError {
		if u, _ := schema.AsUint64(rsp["scEui"]); idt.EUI64(u) != pin {
			return engine.Errorf(engine.EPERM, "Service Center EUI %s, expected %s", idt.EUI64(u), pin)
		}
		if ans, _ := rsp["version"].(string); ans != "" {
			if av, err := version.Parse(ans); err != nil || !av.Compatible(v) {
				return engine.Errorf(engine.EPROTONOSUPPORT, "version %s not supported", ans)
			}
		}
		return nil
	})
	if err != nil {
		eng.Close()
		<-runErr
		return err
	}
	c.mu.Lock()
	c.eng, c.sch, c.version, c.lastErr = eng, sch, v.String(), ""
	c.mu.Unlock()
	c.cfg.Log.Info("connected to Service Center", "sc", c.Name(), "address", c.cfg.Bundle.Address, "version", v.String())
	if c.cfg.Handler != nil {
		go c.cfg.Handler.Connected(c)
	}
	err = <-runErr
	c.mu.Lock()
	c.eng = nil
	c.mu.Unlock()
	c.cfg.Log.Warn("disconnected from Service Center", "sc", c.Name(), "err", err)
	return nil
}

func (c *Client) handle(_ context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *engine.OpError) {
	switch op.Command {
	case "ulData":
		if c.cfg.Handler != nil {
			c.cfg.Handler.Uplink(c.Name(), msg)
		}
		return map[string]any{}, nil
	case "epStat":
		if c.cfg.Handler != nil {
			c.cfg.Handler.EndPointStatus(c.Name(), msg)
		}
		return map[string]any{}, nil
	case "discon":
		go func() { time.Sleep(100 * time.Millisecond); c.close() }()
		return map[string]any{}, nil
	}
	return nil, engine.Errorf(engine.EOPNOTSUPP, "%s not supported by simple-ac yet", op.Command)
}

func (c *Client) close() {
	c.mu.Lock()
	e := c.eng
	c.mu.Unlock()
	if e != nil {
		e.Close()
	}
}

// ErrNotConnected is returned when the SC is not connected.
var ErrNotConnected = errors.New("scclient: Service Center not connected")

func (c *Client) call(ctx context.Context, command string, values map[string]any) (map[string]any, error) {
	c.mu.Lock()
	e, sch := c.eng, c.sch
	c.mu.Unlock()
	if e == nil {
		return nil, ErrNotConnected
	}
	op, ok := sch.Operations[command]
	if !ok {
		return nil, fmt.Errorf("scclient: %s not in SCACI %s", command, sch.Version)
	}
	return e.Call(ctx, command, sch.Fit(op, schema.Init, values))
}

// Register sends the registration of an end-point.
func (c *Client) Register(ctx context.Context, ep store.EndPoint) error {
	key, _ := hex.DecodeString(ep.NwkKey)
	e, _ := idt.ParseEUI64(ep.EUI)
	values := map[string]any{
		"epEui": uint64(e), "epClass": ep.Class, "preAttach": ep.PreAttach, "nwkKey": key,
		"attachCnt": uint64(ep.AttachCnt), "packetCnt": uint64(ep.PacketCnt),
		"dualChan": ep.DualChan, "repetition": ep.Repetition, "wideCarrOff": ep.WideCarrOff, "longBlkDist": ep.LongBlkDist,
		"syncBurst": false,
	}
	if ep.ShAddr != nil {
		values["shAddr"] = uint64(*ep.ShAddr)
	}
	_, err := c.call(ctx, "reg", values)
	return err
}

// Deregister removes an end-point at the SC.
func (c *Client) Deregister(ctx context.Context, eui string) error {
	e, err := idt.ParseEUI64(eui)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "dereg", map[string]any{"epEui": uint64(e)})
	return err
}

// Status asks the SC for its status.
func (c *Client) Status(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "status", map[string]any{})
}

// Disconnect ends the session gracefully.
func (c *Client) Disconnect(ctx context.Context) {
	_, _ = c.call(ctx, "discon", map[string]any{})
	c.close()
}
