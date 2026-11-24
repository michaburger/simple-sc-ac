package engine

import (
	"context"
	"io/fs"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
	builtin "github.com/michaburger/simple-sc-ac/schemas"
)

// TestAllOperations: two engines exchange every operation of every interface version,
// with messages generated from the schema, and every message on both sides validates.
// It covers the synthetic test schema, the built-in schema files, and the files in
// SSCAC_SCHEMA_DIR if set.
func TestAllOperations(t *testing.T) {
	layers := []fs.FS{os.DirFS("../../../testdata/schemas"), builtin.FS}
	if dir := os.Getenv("SSCAC_SCHEMA_DIR"); dir != "" {
		layers = append(layers, os.DirFS(dir))
	}
	set, err := schema.Load(layers...)
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range []string{"demo", "bssci", "scaci", "bssci-vm"} {
		for _, v := range set.Versions(iface) {
			s, _ := set.Get(iface, v)
			t.Run(s.Key(), func(t *testing.T) { exchangeAll(t, s) })
		}
	}
}

func exchangeAll(t *testing.T, s *schema.Schema) {
	var mu sync.Mutex
	handled := map[string]int{}
	handler := func(_ context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *OpError) {
		mu.Lock()
		handled[op.Command]++
		mu.Unlock()
		return s.Example(op, schema.Response), nil
	}
	ctr, str := &trace.Memory{}, &trace.Memory{}
	client, _ := New(Config{Schema: s, Side: Opener, Handler: handler, Trace: ctr, Mode: Strict, ResponseTimeout: 2 * time.Second})
	server, _ := New(Config{Schema: s, Side: Acceptor, Handler: handler, Trace: str, Mode: Strict, ResponseTimeout: 2 * time.Second})
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = client.Run(ctx, a) }()
	go func() { defer wg.Done(); _ = server.Run(ctx, b) }()
	defer func() { cancel(); wg.Wait() }()

	calls := 0
	for _, cmd := range s.Commands() {
		op := s.Operations[cmd]
		var sides []*Engine
		switch {
		case op.Initiator == "both":
			sides = []*Engine{client, server}
		case op.Initiator == s.Roles["initiator_positive"]:
			sides = []*Engine{client}
		default:
			sides = []*Engine{server}
		}
		for _, e := range sides {
			if _, err := e.Call(ctx, cmd, s.Example(op, schema.Init)); err != nil {
				t.Errorf("%s: %v", cmd, err)
			}
			calls++
		}
	}
	time.Sleep(50 * time.Millisecond) // last complete messages
	for _, tr := range [][]trace.Record{ctr.Snapshot(), str.Snapshot()} {
		for _, r := range tr {
			if len(r.Findings) > 0 || r.Note != "" {
				t.Errorf("%s %s %v: findings %v note %q", r.Dir, r.Command, r.OpID, r.Findings, r.Note)
			}
		}
	}
	t.Logf("%s: %d operations, %d calls, %d messages traced, every message valid",
		s.Key(), len(s.Operations), calls, len(ctr.Snapshot())+len(str.Snapshot()))
}
