// Package engine runs the operation pattern of BSSCI and SCACI on one connection.
//
// Every operation is three messages: init (<cmd>), response (<cmd>Rsp) and complete
// (<cmd>Cmp). An error message can replace the response or the complete message, the
// other side then answers with errorAck, which ends the operation. The party that opened
// the connection uses operation IDs counting up from 0, the other party counts down from
// -1. The engine validates every message against the schema of the session, traces it,
// answers ping by itself and passes all other operations to a handler.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/proto/codec"
	"github.com/michaburger/simple-sc-ac/internal/proto/framing"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/proto/trace"
)

// Error numbers sent in error messages (Linux values, as listed in BSSCI 1.1, also used
// on 1.0 sessions).
const (
	EPERM           = 1
	EINVAL          = 22
	EPROTONOSUPPORT = 93
	EOPNOTSUPP      = 95
	ETIMEDOUT       = 110
	EINPROGRESS     = 115
)

// OpError is an error answer of the peer, or an error a handler wants to send.
type OpError struct {
	Code    int64
	Message string
}

func (e *OpError) Error() string { return fmt.Sprintf("error %d: %s", e.Code, e.Message) }

// Errorf builds an OpError.
func Errorf(code int64, format string, a ...any) *OpError {
	return &OpError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Side says which party this engine is.
type Side int

const (
	// Opener is the party that opened the connection (base station, AC). Positive IDs.
	Opener Side = iota
	// Acceptor is the party that accepted it (SC). Negative IDs.
	Acceptor
)

// Mode of validation for received messages.
type Mode int

const (
	// Strict answers an invalid init with EINVAL and fails a call on an invalid response.
	Strict Mode = iota
	// Lenient records the findings in the trace and goes on.
	Lenient
)

// Handler handles an operation started by the peer. It returns the fields of the
// response (without command and opId), or an OpError that is sent instead.
type Handler func(ctx context.Context, op *schema.Operation, msg map[string]any) (map[string]any, *OpError)

// Completion is called when an operation started by the peer has ended, with the
// complete message or with the error that ended it (nil msg).
type Completion func(op *schema.Operation, opID int64, msg map[string]any, err error)

// Config of an engine.
type Config struct {
	Schema   *schema.Schema
	Side     Side
	Handler  Handler
	Complete Completion // optional
	Trace    trace.Sink // optional
	ConnName string     // used in the trace
	Mode     Mode

	// ResponseTimeout limits the wait for the response and for the complete message.
	ResponseTimeout time.Duration
	// IdlePing sends a ping after this much idle time, 0 disables it.
	IdlePing time.Duration
	// MaxPayload of a received frame, default framing.DefaultMaxPayload.
	MaxPayload int
}

// Engine runs one connection.
type Engine struct {
	cfg   Config
	idSrc framing.Identifier

	sendMu sync.Mutex
	w      *framing.Writer

	mu       sync.Mutex
	nextID   int64
	lastPeer int64 // last init opId received from the peer
	peerSeen bool
	pending  map[int64]*call      // operations we started, waiting for a response
	serving  map[int64]*operation // operations the peer started, waiting for complete
	lastIO   time.Time
	closed   bool
	closeErr error
	done     chan struct{}
	ready    chan struct{} // closed when Run has set up the writer
}

type call struct {
	op    *schema.Operation
	reply chan map[string]any
}

type operation struct {
	op       *schema.Operation
	answered bool // response or error sent
	timer    *time.Timer
}

// ErrClosed is returned by Call after the connection ended.
var ErrClosed = errors.New("engine: connection closed")

// New checks the configuration and returns an engine that is not running yet.
func New(cfg Config) (*Engine, error) {
	if cfg.Schema == nil {
		return nil, errors.New("engine: schema required")
	}
	id, err := framing.ParseIdentifier(cfg.Schema.Identifier)
	if err != nil {
		return nil, err
	}
	if cfg.Trace == nil {
		cfg.Trace = trace.Discard
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 5 * time.Second
	}
	e := &Engine{
		cfg: cfg, idSrc: id,
		pending: map[int64]*call{}, serving: map[int64]*operation{},
		done: make(chan struct{}), ready: make(chan struct{}),
	}
	if cfg.Side == Opener {
		e.nextID = 0
	} else {
		e.nextID = -1
	}
	return e, nil
}

// Run reads and dispatches messages until the connection ends or ctx is cancelled. It
// closes rw when it returns. The returned error is nil after a clean end of stream.
func (e *Engine) Run(ctx context.Context, rw io.ReadWriteCloser) error {
	e.sendMu.Lock()
	e.w = framing.NewWriter(rw, e.idSrc)
	e.sendMu.Unlock()
	close(e.ready)
	r := framing.NewReader(rw, e.idSrc)
	if e.cfg.MaxPayload > 0 {
		r.MaxPayload = e.cfg.MaxPayload
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = rw.Close()
	}()
	if e.cfg.IdlePing > 0 {
		go e.pinger(ctx)
	}
	e.touch()
	var runErr error
	for {
		payload, err := r.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				runErr = err
			}
			break
		}
		e.touch()
		msg, err := codec.DecodeMap(payload)
		if err != nil {
			e.note(trace.In, nil, "undecodable frame: "+err.Error())
			runErr = err
			break
		}
		e.dispatch(ctx, msg)
	}
	e.shutdown(runErr)
	return runErr
}

// Done is closed when the connection has ended.
func (e *Engine) Done() <-chan struct{} { return e.done }

// Close ends the connection.
func (e *Engine) Close() { e.shutdown(nil) }

func (e *Engine) shutdown(err error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed, e.closeErr = true, err
	for id, c := range e.pending {
		close(c.reply)
		delete(e.pending, id)
	}
	for id, o := range e.serving {
		if o.timer != nil {
			o.timer.Stop()
		}
		delete(e.serving, id)
	}
	e.mu.Unlock()
	close(e.done)
}

func (e *Engine) touch() {
	e.mu.Lock()
	e.lastIO = time.Now()
	e.mu.Unlock()
}

func (e *Engine) note(dir trace.Direction, msg map[string]any, note string) {
	r := trace.Record{
		Time: time.Now().UTC(), Conn: e.cfg.ConnName, Dir: dir, Note: note,
		Interface: e.cfg.Schema.Interface, Version: e.cfg.Schema.Version,
	}
	if msg != nil {
		r.Command, _ = msg["command"].(string)
		r.OpID = msg["opId"]
		r.Message = trace.Prepare(e.cfg.Schema, msg)
	}
	e.cfg.Trace.Trace(r)
}

func (e *Engine) traceMsg(dir trace.Direction, msg map[string]any, res schema.Result) {
	r := trace.Record{
		Time: time.Now().UTC(), Conn: e.cfg.ConnName, Dir: dir,
		Interface: e.cfg.Schema.Interface, Version: e.cfg.Schema.Version,
		Message: trace.Prepare(e.cfg.Schema, msg),
	}
	r.Command, _ = msg["command"].(string)
	r.OpID = msg["opId"]
	for _, f := range res.Findings {
		r.Findings = append(r.Findings, f.String())
	}
	e.cfg.Trace.Trace(r)
}

// send validates and writes one message. Own messages are always checked strictly: a
// missing required field or a field outside the schema is a programming error.
func (e *Engine) send(msg map[string]any) error {
	res := e.cfg.Schema.Validate(msg)
	if len(res.Findings) > 0 {
		e.traceMsg(trace.Out, msg, res)
		return fmt.Errorf("engine: refusing to send invalid %v: %v", msg["command"], res.Findings)
	}
	data, err := codec.Encode(msg)
	if err != nil {
		return err
	}
	select { // a Call may come before Run has started
	case <-e.ready:
	case <-e.done:
		return ErrClosed
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed || e.w == nil {
		return ErrClosed
	}
	e.traceMsg(trace.Out, msg, res)
	if err := e.w.WriteFrame(data); err != nil {
		return err
	}
	e.touch()
	return nil
}

func message(command string, opID int64, fields map[string]any) map[string]any {
	m := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		m[k] = v
	}
	m["command"] = command
	m["opId"] = opID
	return m
}

// Call starts an operation and waits for the response. It sends the complete message
// by itself and returns the response fields. If the peer answers with an error, Call
// sends errorAck and returns the error as *OpError. The caller can veto the response:
// if check returns an OpError, that error is sent instead of the complete message.
func (e *Engine) Call(ctx context.Context, command string, fields map[string]any) (map[string]any, error) {
	return e.CallCheck(ctx, command, fields, nil)
}

// CallCheck is Call with a check of the response before the complete message is sent.
func (e *Engine) CallCheck(ctx context.Context, command string, fields map[string]any, check func(map[string]any) *OpError) (map[string]any, error) {
	op, part, ok := e.cfg.Schema.Lookup(command)
	if !ok || part != schema.Init {
		return nil, fmt.Errorf("engine: %q is not an operation of %s", command, e.cfg.Schema.Key())
	}
	if !e.mayStart(op) {
		return nil, fmt.Errorf("engine: %q is not started by this side", command)
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, ErrClosed
	}
	id := e.nextID
	if e.cfg.Side == Opener {
		e.nextID++
	} else {
		e.nextID--
	}
	c := &call{op: op, reply: make(chan map[string]any, 1)}
	e.pending[id] = c
	e.mu.Unlock()

	if err := e.send(message(command, id, fields)); err != nil {
		e.drop(id)
		return nil, err
	}
	timer := time.NewTimer(e.cfg.ResponseTimeout)
	defer timer.Stop()
	select {
	case rsp, ok := <-c.reply:
		if !ok {
			return nil, ErrClosed
		}
		return e.finishCall(op, id, rsp, check)
	case <-timer.C:
		e.drop(id)
		e.note(trace.Out, nil, fmt.Sprintf("timeout waiting for %sRsp, opId %d", command, id))
		_ = e.send(message("error", id, map[string]any{"code": int64(ETIMEDOUT), "message": "response timeout"}))
		return nil, Errorf(ETIMEDOUT, "no response to %s within %v", command, e.cfg.ResponseTimeout)
	case <-ctx.Done():
		e.drop(id)
		return nil, ctx.Err()
	}
}

func (e *Engine) finishCall(op *schema.Operation, id int64, rsp map[string]any, check func(map[string]any) *OpError) (map[string]any, error) {
	if rsp["command"] == "error" {
		code, _ := schema.AsInt64(rsp["code"])
		text, _ := rsp["message"].(string)
		err := e.send(message("errorAck", id, nil))
		return nil, errors.Join(&OpError{Code: code, Message: text}, err)
	}
	fields := make(map[string]any, len(rsp))
	for k, v := range rsp {
		if k != "command" && k != "opId" {
			fields[k] = v
		}
	}
	if check != nil {
		if oe := check(fields); oe != nil {
			e.mu.Lock()
			e.pending[id] = &call{op: op, reply: make(chan map[string]any, 1)}
			ack := e.pending[id].reply
			e.mu.Unlock()
			if err := e.send(message("error", id, map[string]any{"code": oe.Code, "message": oe.Message})); err != nil {
				e.drop(id)
				return nil, err
			}
			select {
			case <-ack:
			case <-time.After(e.cfg.ResponseTimeout):
				e.drop(id)
			}
			return nil, oe
		}
	}
	if err := e.send(message(op.Command+"Cmp", id, nil)); err != nil {
		return nil, err
	}
	return fields, nil
}

func (e *Engine) drop(id int64) {
	e.mu.Lock()
	delete(e.pending, id)
	e.mu.Unlock()
}

// mayStart reports whether this side may start op.
func (e *Engine) mayStart(op *schema.Operation) bool {
	if op.Initiator == "both" {
		return true
	}
	role := e.cfg.Schema.Roles["initiator_positive"]
	if e.cfg.Side == Acceptor {
		role = e.cfg.Schema.Roles["initiator_negative"]
	}
	return op.Initiator == role
}

func (e *Engine) peerMayStart(op *schema.Operation) bool {
	if op.Initiator == "both" {
		return true
	}
	role := e.cfg.Schema.Roles["initiator_negative"]
	if e.cfg.Side == Acceptor {
		role = e.cfg.Schema.Roles["initiator_positive"]
	}
	return op.Initiator == role
}

// ours reports whether an operation ID belongs to operations this side starts.
func (e *Engine) ours(id int64) bool {
	if e.cfg.Side == Opener {
		return id >= 0
	}
	return id < 0
}

func (e *Engine) dispatch(ctx context.Context, msg map[string]any) {
	res := e.cfg.Schema.Validate(msg)
	e.traceMsg(trace.In, msg, res)
	id, idOK := schema.AsInt64(msg["opId"])
	cmd, _ := msg["command"].(string)
	if !idOK {
		e.note(trace.In, msg, "no usable opId, message dropped")
		return
	}
	if e.ours(id) {
		e.onReplyToOurs(id, cmd, msg, res)
		return
	}
	e.onPeerOperation(ctx, id, cmd, msg, res)
}

func (e *Engine) onReplyToOurs(id int64, cmd string, msg map[string]any, res schema.Result) {
	e.mu.Lock()
	c := e.pending[id]
	delete(e.pending, id)
	e.mu.Unlock()
	if c == nil {
		e.note(trace.In, msg, "no pending operation for this opId")
		return
	}
	switch {
	case cmd == "error", cmd == "errorAck", cmd == c.op.Command+"Rsp":
	default:
		e.note(trace.In, msg, "unexpected command for operation "+c.op.Command)
		c.reply <- message("error", id, map[string]any{"code": int64(EINVAL), "message": "unexpected " + cmd})
		return
	}
	if cmd != "error" && cmd != "errorAck" && !res.OK() && e.cfg.Mode == Strict {
		c.reply <- message("error", id, map[string]any{"code": int64(EINVAL), "message": "invalid response"})
		return
	}
	c.reply <- msg
}

func (e *Engine) onPeerOperation(ctx context.Context, id int64, cmd string, msg map[string]any, res schema.Result) {
	op, part, known := e.cfg.Schema.Lookup(cmd)
	e.mu.Lock()
	o := e.serving[id]
	e.mu.Unlock()

	if o != nil { // second or third message of an operation the peer started
		switch {
		case cmd == o.op.Command+"Cmp" || cmd == "errorAck":
			e.endServing(id, msg, nil)
		case cmd == "error":
			_ = e.send(message("errorAck", id, nil)) // before the end, which may close the connection
			e.endServing(id, nil, Errorf(EINVAL, "%v", msg["message"]))
		default:
			e.note(trace.In, msg, "unexpected command in running operation")
		}
		return
	}
	if cmd == "error" { // error for an operation we no longer know
		_ = e.send(message("errorAck", id, nil))
		return
	}
	if cmd == "errorAck" || (known && part != schema.Init) {
		e.note(trace.In, msg, "no running operation for this opId")
		return
	}
	if !e.checkPeerID(id) {
		e.reject(id, Errorf(EINVAL, "operation ID %d out of order", id))
		return
	}
	if !known {
		e.reject(id, Errorf(EOPNOTSUPP, "unknown command %q", cmd))
		return
	}
	if !e.peerMayStart(op) {
		e.reject(id, Errorf(EPERM, "%s is not started by this party", cmd))
		return
	}
	if !res.OK() && e.cfg.Mode == Strict {
		e.reject(id, Errorf(EINVAL, "invalid %s: %v", cmd, res.Errors()))
		return
	}
	e.cfg.Schema.ApplyDefaults(msg, nil)

	o = &operation{op: op}
	e.mu.Lock()
	e.serving[id] = o
	e.mu.Unlock()

	var fields map[string]any
	var oe *OpError
	if op.Command == "ping" {
		fields = nil
	} else if e.cfg.Handler == nil {
		oe = Errorf(EOPNOTSUPP, "%s not supported", cmd)
	} else {
		fields, oe = e.cfg.Handler(ctx, op, msg)
	}
	var err error
	if oe != nil {
		err = e.send(message("error", id, map[string]any{"code": oe.Code, "message": oe.Message}))
	} else {
		err = e.send(message(op.Command+"Rsp", id, fields))
	}
	if err != nil {
		e.endServing(id, nil, err)
		return
	}
	e.mu.Lock()
	o.answered = true
	o.timer = time.AfterFunc(e.cfg.ResponseTimeout, func() {
		e.endServing(id, nil, Errorf(ETIMEDOUT, "no %sCmp within %v", op.Command, e.cfg.ResponseTimeout))
	})
	e.mu.Unlock()
}

// reject answers an init that will not be handled with an error. The operation ends with
// the errorAck of the peer, which is accepted but not required.
func (e *Engine) reject(id int64, oe *OpError) {
	e.mu.Lock()
	e.serving[id] = &operation{op: &schema.Operation{Command: "error"}, answered: true}
	e.mu.Unlock()
	_ = e.send(message("error", id, map[string]any{"code": oe.Code, "message": oe.Message}))
	e.mu.Lock()
	if o := e.serving[id]; o != nil {
		o.timer = time.AfterFunc(e.cfg.ResponseTimeout, func() { e.endServing(id, nil, oe) })
	}
	e.mu.Unlock()
}

func (e *Engine) endServing(id int64, msg map[string]any, err error) {
	e.mu.Lock()
	o := e.serving[id]
	delete(e.serving, id)
	e.mu.Unlock()
	if o == nil {
		return
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	if e.cfg.Complete != nil && o.op.Command != "error" {
		e.cfg.Complete(o.op, id, msg, err)
	}
}

// checkPeerID enforces strictly increasing IDs from an opener and strictly decreasing IDs
// from an acceptor. The first init of an opener uses 0.
func (e *Engine) checkPeerID(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	ok := false
	switch {
	case e.cfg.Side == Acceptor && !e.peerSeen:
		ok = id == 0
	case e.cfg.Side == Acceptor:
		ok = id > e.lastPeer
	case !e.peerSeen:
		ok = id < 0
	default:
		ok = id < e.lastPeer
	}
	if ok {
		e.lastPeer, e.peerSeen = id, true
	}
	return ok
}

func (e *Engine) pinger(ctx context.Context) {
	t := time.NewTicker(e.cfg.IdlePing / 4)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.done:
			return
		case <-t.C:
			e.mu.Lock()
			idle := time.Since(e.lastIO)
			busy := len(e.pending) > 0
			e.mu.Unlock()
			if idle >= e.cfg.IdlePing && !busy {
				if _, err := e.Call(ctx, "ping", nil); err != nil {
					e.note(trace.Out, nil, "ping failed: "+err.Error())
				}
			}
		}
	}
}
