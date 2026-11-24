package engine

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/proto/codec"
	"github.com/michaburger/simple-sc-ac/internal/proto/framing"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
)

func demoSchema(t *testing.T) *schema.Schema {
	t.Helper()
	set, err := schema.Load(os.DirFS("../../../testdata/schemas"))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := set.Get("demo", "1.0.0")
	return s
}

type pair struct {
	client, server *Engine
	ctrace, strace *trace.Memory
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func newPair(t *testing.T, clientCfg, serverCfg Config) *pair {
	t.Helper()
	s := demoSchema(t)
	p := &pair{ctrace: &trace.Memory{}, strace: &trace.Memory{}}
	clientCfg.Schema, clientCfg.Side, clientCfg.Trace, clientCfg.ConnName = s, Opener, p.ctrace, "client"
	serverCfg.Schema, serverCfg.Side, serverCfg.Trace, serverCfg.ConnName = s, Acceptor, p.strace, "server"
	if clientCfg.ResponseTimeout == 0 {
		clientCfg.ResponseTimeout = time.Second
	}
	if serverCfg.ResponseTimeout == 0 {
		serverCfg.ResponseTimeout = time.Second
	}
	var err error
	if p.client, err = New(clientCfg); err != nil {
		t.Fatal(err)
	}
	if p.server, err = New(serverCfg); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(2)
	go func() { defer p.wg.Done(); _ = p.client.Run(ctx, a) }()
	go func() { defer p.wg.Done(); _ = p.server.Run(ctx, b) }()
	time.Sleep(10 * time.Millisecond) // let both writers be set up
	t.Cleanup(func() { cancel(); p.wg.Wait() })
	return p
}

func hello() map[string]any {
	return map[string]any{"version": "1.0.0", "deviceId": uint64(18205882861800652800)}
}

func TestCallAndComplete(t *testing.T) {
	done := make(chan map[string]any, 1)
	p := newPair(t, Config{}, Config{
		Handler: func(_ context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *OpError) {
			if msg["deviceId"] != uint64(18205882861800652800) {
				return nil, Errorf(EINVAL, "deviceId changed: %v", msg["deviceId"])
			}
			return map[string]any{"accepted": true}, nil
		},
		Complete: func(op *schema.Operation, id int64, msg map[string]any, err error) { done <- msg },
	})
	rsp, err := p.client.Call(context.Background(), "hello", hello())
	if err != nil {
		t.Fatal(err)
	}
	if rsp["accepted"] != true {
		t.Fatalf("response %v", rsp)
	}
	select {
	case m := <-done:
		if m["command"] != "helloCmp" || m["opId"] != uint64(0) {
			t.Fatalf("complete %v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no complete on the server")
	}
	// opIds 0, 1, 2 from the opener
	for i := 1; i <= 2; i++ {
		if _, err := p.client.Call(context.Background(), "hello", hello()); err != nil {
			t.Fatal(err)
		}
		<-done
	}
	var ids []any
	for _, r := range p.ctrace.Snapshot() {
		if r.Command == "hello" {
			ids = append(ids, r.OpID)
		}
	}
	if len(ids) != 3 || ids[0] != int64(0) || ids[2] != int64(2) {
		t.Fatalf("opIds %v", ids)
	}
}

func TestAcceptorCallsWithNegativeIDs(t *testing.T) {
	got := make(chan string, 2)
	p := newPair(t, Config{
		Handler: func(_ context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *OpError) {
			got <- msg["text"].(string)
			return nil, nil
		},
	}, Config{})
	for _, txt := range []string{"a", "b"} {
		if _, err := p.server.Call(context.Background(), "notify", map[string]any{"text": txt}); err != nil {
			t.Fatal(err)
		}
	}
	if <-got != "a" || <-got != "b" {
		t.Fatal("order")
	}
	var ids []any
	for _, r := range p.strace.Snapshot() {
		if r.Command == "notify" {
			ids = append(ids, r.OpID)
		}
	}
	if len(ids) != 2 || ids[0] != int64(-1) || ids[1] != int64(-2) {
		t.Fatalf("opIds %v", ids)
	}
}

func TestHandlerErrorAndUnsupported(t *testing.T) {
	p := newPair(t, Config{}, Config{
		Handler: func(_ context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *OpError) {
			return nil, Errorf(EPERM, "not today")
		},
	})
	_, err := p.client.Call(context.Background(), "hello", hello())
	var oe *OpError
	if !errors.As(err, &oe) || oe.Code != EPERM {
		t.Fatalf("err = %v, want EPERM", err)
	}
	// The client has no handler: notify from the server is answered with EOPNOTSUPP.
	_, err = p.server.Call(context.Background(), "notify", map[string]any{"text": "x"})
	if !errors.As(err, &oe) || oe.Code != EOPNOTSUPP {
		t.Fatalf("err = %v, want EOPNOTSUPP", err)
	}
}

func TestWrongSideAndInvalidOwnMessages(t *testing.T) {
	p := newPair(t, Config{}, Config{})
	if _, err := p.client.Call(context.Background(), "notify", map[string]any{"text": "x"}); err == nil {
		t.Fatal("client started a server operation")
	}
	if _, err := p.client.Call(context.Background(), "hello", map[string]any{"version": "1.0.0"}); err == nil {
		t.Fatal("invalid own message was sent")
	}
	if _, err := p.client.Call(context.Background(), "hello", map[string]any{"version": "1.0.0", "deviceId": uint64(1), "extra": 1}); err == nil {
		t.Fatal("own message with unknown field was sent")
	}
}

func TestPingIsAnsweredByTheEngine(t *testing.T) {
	p := newPair(t, Config{}, Config{})
	if _, err := p.server.Call(context.Background(), "ping", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.client.Call(context.Background(), "ping", nil); err != nil {
		t.Fatal(err)
	}
}

func TestIdlePing(t *testing.T) {
	p := newPair(t, Config{IdlePing: 80 * time.Millisecond}, Config{})
	time.Sleep(300 * time.Millisecond)
	n := 0
	for _, r := range p.strace.Snapshot() {
		if r.Command == "pingCmp" && r.Dir == trace.In {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no idle ping")
	}
}

func TestTraceRedactsSecrets(t *testing.T) {
	p := newPair(t, Config{}, Config{
		Handler: func(context.Context, *schema.Operation, map[string]any) (map[string]any, *OpError) {
			return map[string]any{"code": int64(0)}, nil
		},
	})
	_, err := p.client.Call(context.Background(), "store", map[string]any{
		"slot": uint64(1), "token": []byte{0xCA, 0xFE, 0xBA, 0xBE}, "payload": []byte{0x01, 0x02},
		"reading": map[string]any{"values": []any{1.0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range append(p.ctrace.Snapshot(), p.strace.Snapshot()...) {
		if r.Command != "store" {
			continue
		}
		if r.Message["token"] != trace.Redacted {
			t.Fatalf("token in trace: %v", r.Message["token"])
		}
		if r.Message["payload"] != "0102" {
			t.Fatalf("payload not hex: %v", r.Message["payload"])
		}
	}
}

// rawPeer lets a test write frames by hand to an engine.
func rawPeer(t *testing.T, side Side, cfg Config) (*framing.Writer, *framing.Reader, *Engine) {
	t.Helper()
	s := demoSchema(t)
	cfg.Schema, cfg.Side = s, side
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 200 * time.Millisecond
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.Run(ctx, a); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	id, _ := framing.ParseIdentifier(s.Identifier)
	return framing.NewWriter(b, id), framing.NewReader(b, id), e
}

func send(t *testing.T, w *framing.Writer, msg map[string]any) {
	t.Helper()
	data, err := codec.Encode(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFrame(data); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, r *framing.Reader) map[string]any {
	t.Helper()
	data, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	m, err := codec.DecodeMap(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestServerRejects(t *testing.T) {
	w, r, _ := rawPeer(t, Acceptor, Config{
		Handler: func(context.Context, *schema.Operation, map[string]any) (map[string]any, *OpError) {
			return map[string]any{"accepted": true}, nil
		},
	})
	// first init must use opId 0
	send(t, w, map[string]any{"command": "hello", "opId": int64(5), "version": "1.0.0", "deviceId": uint64(1)})
	if m := recv(t, r); m["command"] != "error" || m["code"] != uint64(EINVAL) {
		t.Fatalf("got %v", m)
	}
	send(t, w, map[string]any{"command": "errorAck", "opId": int64(5)})
	send(t, w, map[string]any{"command": "hello", "opId": int64(0), "version": "1.0.0", "deviceId": uint64(1)})
	if m := recv(t, r); m["command"] != "helloRsp" {
		t.Fatalf("got %v", m)
	}
	send(t, w, map[string]any{"command": "helloCmp", "opId": int64(0)})
	// unknown command
	send(t, w, map[string]any{"command": "fly", "opId": int64(1)})
	if m := recv(t, r); m["code"] != uint64(EOPNOTSUPP) {
		t.Fatalf("got %v", m)
	}
	// operation of the other side
	send(t, w, map[string]any{"command": "notify", "opId": int64(2), "text": "x"})
	if m := recv(t, r); m["code"] != uint64(EPERM) {
		t.Fatalf("got %v", m)
	}
	// invalid init in strict mode
	send(t, w, map[string]any{"command": "hello", "opId": int64(3), "version": "1.0"})
	if m := recv(t, r); m["code"] != uint64(EINVAL) || !strings.Contains(m["message"].(string), "deviceId") {
		t.Fatalf("got %v", m)
	}
	// unknown fields are ignored
	send(t, w, map[string]any{"command": "hello", "opId": int64(4), "version": "1.0.0", "deviceId": uint64(1), "future": "x"})
	if m := recv(t, r); m["command"] != "helloRsp" {
		t.Fatalf("got %v", m)
	}
}

func TestLenientAcceptsInvalidInit(t *testing.T) {
	w, r, _ := rawPeer(t, Acceptor, Config{
		Mode: Lenient,
		Handler: func(context.Context, *schema.Operation, map[string]any) (map[string]any, *OpError) {
			return map[string]any{"accepted": true}, nil
		},
	})
	send(t, w, map[string]any{"command": "hello", "opId": int64(0), "version": "1.0"})
	if m := recv(t, r); m["command"] != "helloRsp" {
		t.Fatalf("got %v", m)
	}
}

func TestResponseTimeout(t *testing.T) {
	w, r, e := rawPeer(t, Opener, Config{})
	_ = w
	errc := make(chan error, 1)
	go func() { _, err := e.Call(context.Background(), "hello", hello()); errc <- err }()
	if m := recv(t, r); m["command"] != "hello" {
		t.Fatalf("got %v", m)
	}
	// no answer: the engine gives up and sends an error
	if m := recv(t, r); m["command"] != "error" || m["code"] != uint64(ETIMEDOUT) {
		t.Fatalf("got %v", m)
	}
	var oe *OpError
	if err := <-errc; !errors.As(err, &oe) || oe.Code != ETIMEDOUT {
		t.Fatalf("err = %v", err)
	}
}

func TestPeerErrorInsteadOfComplete(t *testing.T) {
	ended := make(chan error, 1)
	w, r, _ := rawPeer(t, Acceptor, Config{
		Handler: func(context.Context, *schema.Operation, map[string]any) (map[string]any, *OpError) {
			return map[string]any{"accepted": true}, nil
		},
		Complete: func(_ *schema.Operation, _ int64, _ map[string]any, err error) { ended <- err },
	})
	send(t, w, map[string]any{"command": "hello", "opId": int64(0), "version": "1.0.0", "deviceId": uint64(1)})
	recv(t, r)
	send(t, w, map[string]any{"command": "error", "opId": int64(0), "code": int64(EINVAL), "message": "bad response"})
	if m := recv(t, r); m["command"] != "errorAck" {
		t.Fatalf("got %v", m)
	}
	if err := <-ended; err == nil {
		t.Fatal("operation ended without error")
	}
}

func TestCloseEndsCalls(t *testing.T) {
	_, r, e := rawPeer(t, Opener, Config{ResponseTimeout: 5 * time.Second})
	errc := make(chan error, 1)
	go func() { _, err := e.Call(context.Background(), "hello", hello()); errc <- err }()
	recv(t, r)
	e.Close()
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}
