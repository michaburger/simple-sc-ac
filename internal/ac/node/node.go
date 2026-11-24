// Package node is the core of simple-ac: its end-points, the connections to the Service
// Centers from the enrollment bundles in <data>/sc, and what arrives from them.
package node

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/ac/scclient"
	"github.com/michaburger/simple-sc-ac/internal/ac/store"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/onboard"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
)

// Files of one Service Center below <data>/sc/<name>/.
const (
	BundleFile = "bundle.json"
	KeyFile    = "client.key"
)

// Config of the node.
type Config struct {
	DataDir         string
	ACEui           idt.EUI64
	Schemas         *schema.Set
	Store           *store.Store
	Log             *slog.Logger
	Trace           trace.Sink
	ResponseTimeout time.Duration
	IdlePing        time.Duration
	SwVersion       string
}

// Node is a running Application Center.
type Node struct {
	cfg     Config
	mu      sync.Mutex
	clients map[string]*scclient.Client
}

// New loads the Service Centers from <data>/sc. A broken bundle is logged and skipped.
func New(cfg Config) (*Node, error) {
	n := &Node{cfg: cfg, clients: map[string]*scclient.Client{}}
	dirs, _ := filepath.Glob(filepath.Join(cfg.DataDir, "sc", "*", BundleFile))
	for _, f := range dirs {
		if err := n.load(filepath.Dir(f)); err != nil {
			cfg.Log.Error("Service Center not loaded", "dir", filepath.Dir(f), "err", err)
		}
	}
	return n, nil
}

func (n *Node) load(dir string) error {
	b, err := onboard.Read(filepath.Join(dir, BundleFile))
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(dir, KeyFile))
	if err != nil {
		return fmt.Errorf("client key: %w", err)
	}
	c, err := scclient.New(scclient.Config{Bundle: b, KeyPEM: key, ACEui: n.cfg.ACEui, Schemas: n.cfg.Schemas, Handler: n,
		Log: n.cfg.Log, Trace: n.cfg.Trace, ResponseTimeout: n.cfg.ResponseTimeout, IdlePing: n.cfg.IdlePing,
		Name: "simple-ac", SwVersion: n.cfg.SwVersion})
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.clients[b.Name] = c
	n.mu.Unlock()
	return nil
}

// Run connects to all Service Centers until ctx ends.
func (n *Node) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, c := range n.Clients() {
		wg.Add(1)
		go func(c *scclient.Client) { defer wg.Done(); c.Run(ctx) }(c)
	}
	if len(n.Clients()) == 0 {
		n.cfg.Log.Warn("no Service Center configured, add one with: simple-ac sc add <bundle.json>")
	}
	wg.Wait()
}

// Clients returns the Service Center connections, sorted by name.
func (n *Node) Clients() []*scclient.Client {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]*scclient.Client, 0, len(n.clients))
	for _, c := range n.clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Client returns one connection.
func (n *Node) Client(name string) (*scclient.Client, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, ok := n.clients[name]
	return c, ok
}

func (n *Node) register(ctx context.Context, c *scclient.Client, ep store.EndPoint) error {
	err := c.Register(ctx, ep)
	state, text := "registered", ""
	if err != nil {
		state, text = "failed", err.Error()
		if errors.Is(err, scclient.ErrNotConnected) {
			state, text = "pending", ""
		}
	}
	_ = n.cfg.Store.SetRegistration(ep.EUI, c.Name(), state, text)
	return err
}

// Connected registers every end-point at a Service Center that just connected.
func (n *Node) Connected(c *scclient.Client) {
	eps, err := n.cfg.Store.EndPoints()
	if err != nil {
		n.cfg.Log.Error("end-points", "err", err)
		return
	}
	ok := 0
	for _, ep := range eps {
		if err := n.register(context.Background(), c, ep); err != nil {
			n.cfg.Log.Warn("registration failed", "sc", c.Name(), "epEui", ep.EUI, "err", err)
		} else {
			ok++
		}
	}
	n.cfg.Log.Info("end-points registered", "sc", c.Name(), "registered", ok, "total", len(eps))
}

// AddEndPoint stores an end-point and registers it at every connected Service Center.
func (n *Node) AddEndPoint(ctx context.Context, ep store.EndPoint) ([]store.Registration, error) {
	if err := n.cfg.Store.PutEndPoint(ep); err != nil {
		return nil, err
	}
	for _, c := range n.Clients() {
		if err := n.register(ctx, c, ep); err != nil && !errors.Is(err, scclient.ErrNotConnected) {
			n.cfg.Log.Warn("registration failed", "sc", c.Name(), "epEui", ep.EUI, "err", err)
		}
	}
	return n.cfg.Store.Registrations(ep.EUI)
}

// RemoveEndPoint deregisters an end-point everywhere and deletes it.
func (n *Node) RemoveEndPoint(ctx context.Context, eui string) error {
	if _, err := n.cfg.Store.EndPoint(eui); err != nil {
		return err
	}
	for _, c := range n.Clients() {
		if err := c.Deregister(ctx, eui); err != nil && !errors.Is(err, scclient.ErrNotConnected) {
			n.cfg.Log.Warn("deregistration failed", "sc", c.Name(), "epEui", eui, "err", err)
		}
	}
	return n.cfg.Store.DeleteEndPoint(eui)
}

func euiText(v any) string {
	u, _ := schema.AsUint64(v)
	return idt.EUI64(u).String()
}

// receptions turns the baseStations array into JSON-friendly maps with EUIs as text.
func receptions(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		r := make(map[string]any, len(m))
		for k, val := range m {
			if k == "bsEui" {
				r[k] = euiText(val)
			} else {
				r[k] = val
			}
		}
		out = append(out, r)
	}
	return out
}

// Uplink handles an uplink from a Service Center.
func (n *Node) Uplink(sc string, msg map[string]any) {
	ep := euiText(msg["epEui"])
	cnt, _ := schema.AsInt64(msg["packetCnt"])
	format, _ := schema.AsInt64(msg["format"])
	data, _ := schema.AsBytes(msg["userData"])
	recs := receptions(msg["baseStations"])
	var rx int64
	if len(recs) > 0 {
		rx, _ = schema.AsInt64(recs[0]["rxTime"])
	}
	if _, err := n.cfg.Store.EndPoint(ep); err != nil {
		n.cfg.Log.Warn("uplink of an end-point this AC does not know", "sc", sc, "epEui", ep)
		return
	}
	fresh, err := n.cfg.Store.AddUplink(store.Uplink{SC: sc, EPEui: ep, PacketCnt: cnt, RxNs: rx, Format: format,
		Payload: strings.ToUpper(hex.EncodeToString(data)), Receptions: recs})
	if err != nil {
		n.cfg.Log.Error("uplink not stored", "epEui", ep, "err", err)
		return
	}
	if !fresh {
		n.cfg.Log.Info("uplink already received", "sc", sc, "epEui", ep, "packetCnt", cnt)
		return
	}
	n.cfg.Log.Info("uplink", "sc", sc, "epEui", ep, "packetCnt", cnt, "bytes", len(data), "baseStations", len(recs))
}

// EndPointStatus handles an end-point status from a Service Center.
func (n *Node) EndPointStatus(sc string, msg map[string]any) {
	ep := euiText(msg["epEui"])
	status, _ := msg["epStatus"].(string)
	bs := ""
	if recs := receptions(msg["baseStations"]); len(recs) > 0 {
		bs, _ = recs[0]["bsEui"].(string)
	}
	_ = n.cfg.Store.SetStatus(ep, status, sc, bs)
	n.cfg.Log.Info("end-point status", "sc", sc, "epEui", ep, "status", status)
}
