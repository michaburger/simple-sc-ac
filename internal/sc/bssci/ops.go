package bssci

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/mcrypto"
	"github.com/michaburger/simple-sc-ac/internal/proto/engine"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
)

func euiText(v any) (string, bool) {
	u, ok := schema.AsUint64(v)
	if !ok {
		return "", false
	}
	return idt.EUI64(u).String(), true
}

func euiNum(s string) uint64 {
	e, _ := idt.ParseEUI64(s)
	return uint64(e)
}

func flag(m map[string]any, k string) bool { b, _ := m[k].(bool); return b }

func keyBytes(hexKey string) []byte {
	b, _ := hex.DecodeString(hexKey)
	return b
}

// onAttach checks an over-the-air attach request (signature, counter) and answers with
// the session key. The registry is changed only when the base station completes the
// operation.
func (sess *Session) onAttach(msg map[string]any) (map[string]any, *engine.OpError) {
	s := sess.srv
	if sess.State != "enabled" {
		return nil, engine.Errorf(engine.EPERM, "base station not accepted yet")
	}
	eui, _ := euiText(msg["epEui"])
	ep, err := s.cfg.Registry.EndPoint(eui)
	if err != nil {
		sess.log.Info("attach of unknown end-point", "epEui", eui)
		return nil, engine.Errorf(engine.EOPNOTSUPP, "End Point unknown")
	}
	if !ep.Bidi() {
		return nil, engine.Errorf(engine.EPERM, "unidirectional end-point cannot attach over the air")
	}
	cnt, _ := schema.AsUint64(msg["attachCnt"])
	nonce, _ := schema.AsBytes(msg["nonce"])
	sign, _ := schema.AsBytes(msg["sign"])
	ok, err := mcrypto.VerifyAttachSignature(keyBytes(ep.NwkKey), euiNum(eui), uint32(cnt), sign)
	if err != nil || !ok {
		sess.log.Warn("attach refused: signature invalid", "epEui", eui, "attachCnt", cnt)
		return nil, engine.Errorf(engine.EPERM, "End Point signature invalid")
	}
	if int64(cnt) <= ep.AttachCnt {
		sess.log.Warn("attach refused: attach counter not increased", "epEui", eui, "attachCnt", cnt, "last", ep.AttachCnt)
		return nil, engine.Errorf(engine.EPERM, "attach counter %d not above %d", cnt, ep.AttachCnt)
	}
	if !s.claimAttach(eui, sess.BSEui, int64(cnt)) {
		return nil, engine.Errorf(engine.EINPROGRESS, "Attachment handled by other Base Station")
	}
	sk, err := mcrypto.SessionKey(keyBytes(ep.NwkKey), euiNum(eui), nonce, sign)
	if err != nil {
		return nil, engine.Errorf(engine.EINVAL, "%v", err)
	}
	link := registry.EndPoint{DualChan: flag(msg, "dualChan"), Repetition: flag(msg, "repetition"), WideCarrOff: flag(msg, "wideCarrOff"), LongBlkDist: flag(msg, "longBlkDist")}
	fields := map[string]any{
		"nwkSnKey": sk[:], "epClass": ep.Class, "syncBurst": false,
		"dualChan": link.DualChan, "repetition": link.Repetition, "wideCarrOff": link.WideCarrOff, "longBlkDist": link.LongBlkDist,
	}
	var sh int
	if v, ok := schema.AsUint64(msg["shAddr"]); ok {
		sh = int(v) // assigned by the base station
	} else {
		sh = s.allocShortAddr(eui)
		fields["shAddr"] = uint64(sh)
	}
	id, _ := schema.AsInt64(msg["opId"])
	sess.mu.Lock()
	link.EUI = eui
	sess.pending[id] = pendingAttach{ep: link, attachCnt: int64(cnt), snKey: strings.ToUpper(hex.EncodeToString(sk[:])), shAddr: sh}
	sess.mu.Unlock()
	op := sess.sch.Operations["att"]
	return sess.sch.Fit(op, schema.Response, fields), nil
}

func (s *Server) claimAttach(ep, bs string, cnt int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.attaches[ep]
	if ok && c.bs != bs && c.cnt == cnt && time.Now().Before(c.until) {
		return false
	}
	s.attaches[ep] = attachClaim{bs: bs, cnt: cnt, until: time.Now().Add(s.cfg.ResponseTimeout * 2)}
	return true
}

// allocShortAddr picks a short address that no other attached end-point uses, starting
// from a value derived from the EUI. Short addresses need not be unique, but unique ones
// keep the signature search of the base station short.
func (s *Server) allocShortAddr(eui string) int {
	h := sha256.Sum256([]byte(eui))
	start := int(binary.BigEndian.Uint16(h[:2]))
	for i := 0; i < 1<<16; i++ {
		a := (start + i) & 0xFFFF
		if used, err := s.cfg.Registry.ShortAddrInUse(a, eui); err == nil && !used {
			return a
		}
	}
	return start
}

func (s *Server) commitAttach(sess *Session, pa pendingAttach) {
	eui := pa.ep.EUI
	if err := s.cfg.Registry.Attached(eui, pa.attachCnt, pa.snKey, pa.shAddr, pa.ep, sess.BSEui); err != nil {
		sess.log.Error("attach not stored", "epEui", eui, "err", err)
		return
	}
	s.mu.Lock()
	delete(s.attaches, eui)
	s.mu.Unlock()
	sess.log.Info("end-point attached", "epEui", eui, "shAddr", idt.ShortAddr(pa.shAddr).String(), "attachCnt", pa.attachCnt)
	if ep, err := s.cfg.Registry.EndPoint(eui); err == nil {
		s.propagate(ep, sess.BSEui)
	}
}

// onDetach answers a detach request. How the SC computes the signature for the end-point
// in the response is not described in the documents available (finding F-09), so it sends
// zeros for now and logs it.
func (sess *Session) onDetach(msg map[string]any) (map[string]any, *engine.OpError) {
	s := sess.srv
	eui, _ := euiText(msg["epEui"])
	if _, err := s.cfg.Registry.EndPoint(eui); err != nil {
		return nil, engine.Errorf(engine.EOPNOTSUPP, "End Point unknown")
	}
	sess.log.Warn("detach: response signature not defined in the specification, sending zeros", "epEui", eui)
	if err := s.cfg.Registry.Detached(eui); err != nil {
		return nil, engine.Errorf(engine.EINVAL, "%v", err)
	}
	sess.log.Info("end-point detached", "epEui", eui)
	go s.propagateDetach(eui, sess.BSEui)
	return map[string]any{"sign": []byte{0, 0, 0, 0}}, nil
}

// onUplink feeds an uplink into the deduplication. It always answers, so that the base
// station does not repeat it, and drops uplinks it cannot use.
func (sess *Session) onUplink(msg map[string]any) (map[string]any, *engine.OpError) {
	s := sess.srv
	eui, _ := euiText(msg["epEui"])
	cnt, _ := schema.AsUint64(msg["packetCnt"])
	if sess.State != "enabled" {
		sess.log.Info("uplink from a pending base station dropped", "epEui", eui)
		return map[string]any{}, nil
	}
	ep, err := s.cfg.Registry.EndPoint(eui)
	if err != nil || ep.State != "attached" {
		sess.log.Info("uplink of an unknown or detached end-point dropped", "epEui", eui, "packetCnt", cnt)
		return map[string]any{}, nil
	}
	rx, _ := schema.AsInt64(msg["rxTime"])
	data, _ := schema.AsBytes(msg["userData"])
	format, _ := schema.AsInt64(msg["format"])
	rec := registry.Reception{BSEui: sess.BSEui, RxNs: rx, SNR: num(msg["snr"]), RSSI: num(msg["rssi"]), Extra: map[string]any{}}
	for _, k := range []string{"eqSnr", "profile", "mode", "rxDuration", "subpackets", "snrSymMin", "frequency"} {
		if v, ok := msg[k]; ok {
			rec.Extra[k] = v
		}
	}
	if !s.dedup.add(eui, int64(cnt), ep.LastPacketCnt, rec, data, format) {
		sess.log.Info("uplink with an old packet counter dropped", "epEui", eui, "packetCnt", cnt, "last", ep.LastPacketCnt)
	}
	return map[string]any{}, nil
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	}
	if i, ok := schema.AsInt64(v); ok {
		return float64(i)
	}
	return 0
}

func (s *Server) storeUplink(u registry.Uplink) {
	if _, err := s.cfg.Registry.StoreUplink(u); err != nil {
		s.cfg.Log.Error("uplink not stored", "epEui", u.EPEui, "err", err)
		return
	}
	bss := make([]string, len(u.Receptions))
	for i, r := range u.Receptions {
		bss[i] = r.BSEui
	}
	s.cfg.Log.Info("uplink", "epEui", u.EPEui, "packetCnt", u.PacketCnt, "bytes", len(u.Payload)/2, "baseStations", bss)
	if ep, err := s.cfg.Registry.EndPoint(u.EPEui); err == nil {
		_ = s.cfg.Registry.Count(ep.OwnerAC, "uplinks", 1)
	}
}

// propagate sends the attachment of an end-point to every enabled base station except one.
func (s *Server) propagate(ep registry.EndPoint, except string) {
	for _, sess := range s.Sessions() {
		if sess.BSEui == except || sess.State != "enabled" {
			continue
		}
		go sess.attPrp(context.Background(), ep)
	}
}

func (s *Server) propagateDetach(eui, except string) {
	for _, sess := range s.Sessions() {
		if sess.BSEui == except || sess.State != "enabled" {
			continue
		}
		op := sess.sch.Operations["detPrp"]
		if _, err := sess.eng.Call(context.Background(), "detPrp", sess.sch.Fit(op, schema.Init, map[string]any{"epEui": euiNum(eui)})); err != nil {
			sess.log.Warn("detach propagate failed", "epEui", eui, "err", err)
		}
	}
}

func (sess *Session) attPrp(ctx context.Context, ep registry.EndPoint) {
	if ep.State != "attached" || ep.ShAddr == nil {
		return
	}
	last := ep.LastPacketCnt
	if last < 0 {
		last = 0
	}
	op := sess.sch.Operations["attPrp"]
	fields := sess.sch.Fit(op, schema.Init, map[string]any{
		"epEui": euiNum(ep.EUI), "bidi": ep.Bidi(), "epClass": ep.Class, "nwkSnKey": keyBytes(ep.NwkSnKey),
		"shAddr": uint64(*ep.ShAddr), "lastPacketCnt": uint64(last), "dualChan": ep.DualChan, "repetition": ep.Repetition,
		"wideCarrOff": ep.WideCarrOff, "longBlkDist": ep.LongBlkDist, "syncBurst": false,
	})
	if _, err := sess.eng.Call(ctx, "attPrp", fields); err != nil {
		sess.log.Warn("attach propagate failed", "epEui", ep.EUI, "err", err)
		return
	}
	sess.log.Info("attachment propagated", "epEui", ep.EUI)
}

// syncEndPoints propagates every attached end-point to a base station after its connect.
func (s *Server) syncEndPoints(ctx context.Context, sess *Session) {
	eps, err := s.cfg.Registry.EndPoints(true)
	if err != nil {
		sess.log.Error("state sync", "err", err)
		return
	}
	for _, ep := range eps {
		sess.attPrp(ctx, ep)
	}
}

// PropagateEndPoint sends a pre-attached or re-registered end-point to all base stations.
func (s *Server) PropagateEndPoint(eui string) {
	if ep, err := s.cfg.Registry.EndPoint(eui); err == nil {
		s.propagate(ep, "")
	}
}

// RevokeEndPoint detaches an end-point at all base stations.
func (s *Server) RevokeEndPoint(eui string) { s.propagateDetach(eui, "") }

func (s *Server) pollStatus(ctx context.Context, sess *Session) {
	t := time.NewTicker(s.cfg.StatusInterval)
	defer t.Stop()
	poll := func() {
		rsp, err := sess.eng.Call(ctx, "status", nil)
		if err != nil {
			sess.log.Warn("status failed", "err", err)
			return
		}
		_ = s.cfg.Registry.SetStatus(sess.BSEui, rsp)
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sess.eng.Done():
			return
		case <-t.C:
			poll()
		}
	}
}

// dedup merges receptions of the same uplink from several base stations within a window.
type dedup struct {
	window time.Duration
	flush  func(registry.Uplink)
	mu     sync.Mutex
	open   map[string]*registry.Uplink
}

func newDedup(window time.Duration, flush func(registry.Uplink)) *dedup {
	return &dedup{window: window, flush: flush, open: map[string]*registry.Uplink{}}
}

// add returns false when the uplink is older than the last stored one and not part of an
// open window.
func (d *dedup) add(ep string, cnt, last int64, rec registry.Reception, data []byte, format int64) bool {
	key := ep + "/" + strconv.FormatInt(cnt, 10)
	d.mu.Lock()
	defer d.mu.Unlock()
	if u, ok := d.open[key]; ok {
		u.Receptions = append(u.Receptions, rec)
		return true
	}
	if cnt <= last {
		return false
	}
	u := &registry.Uplink{EPEui: ep, PacketCnt: cnt, RxNs: rec.RxNs, Format: format, Payload: strings.ToUpper(hex.EncodeToString(data)), Receptions: []registry.Reception{rec}}
	d.open[key] = u
	time.AfterFunc(d.window, func() {
		d.mu.Lock()
		delete(d.open, key)
		d.mu.Unlock()
		d.flush(*u)
	})
	return true
}
