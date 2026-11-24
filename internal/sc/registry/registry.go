// Package registry is the state of the Service Center: base stations, end-points,
// receptions and deduplicated uplinks, in one SQLite file.
package registry

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/store"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when an entry does not exist.
var ErrNotFound = errors.New("registry: not found")

// Registry wraps the database.
type Registry struct{ db *sql.DB }

// Open opens or creates the registry file.
func Open(file string) (*Registry, error) {
	sub, _ := fs.Sub(migrationFS, "migrations")
	db, err := store.Open(file, sub)
	if err != nil {
		return nil, err
	}
	return &Registry{db: db}, nil
}

// Close closes the database.
func (r *Registry) Close() error { return r.db.Close() }

func now() int64 { return time.Now().UnixNano() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// BaseStation is one base station entry.
type BaseStation struct {
	EUI         string          `json:"bsEui"`
	Name        string          `json:"name"`
	State       string          `json:"state"` // pending, enabled, disabled
	Fingerprint string          `json:"certFingerprint"`
	Version     string          `json:"version"`
	Class       string          `json:"class"`
	Bidi        bool            `json:"bidi"`
	Vendor      string          `json:"vendor"`
	Model       string          `json:"model"`
	SwVersion   string          `json:"swVersion"`
	Connected   bool            `json:"connected"`
	LastSeenNs  int64           `json:"lastSeenNs"`
	LastStatus  json.RawMessage `json:"lastStatus"`
}

// BaseStation returns one entry.
func (r *Registry) BaseStation(eui string) (BaseStation, error) {
	row := r.db.QueryRow(`SELECT bs_eui, name, state, cert_fingerprint, version, class, bidi, vendor, model, sw_version, connected, last_seen_ns, last_status FROM base_station WHERE bs_eui = ?`, eui)
	return scanBS(row)
}

type scanner interface{ Scan(...any) error }

func scanBS(s scanner) (BaseStation, error) {
	var b BaseStation
	var bidi, conn int
	var status string
	err := s.Scan(&b.EUI, &b.Name, &b.State, &b.Fingerprint, &b.Version, &b.Class, &bidi, &b.Vendor, &b.Model, &b.SwVersion, &conn, &b.LastSeenNs, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	b.Bidi, b.Connected, b.LastStatus = bidi == 1, conn == 1, json.RawMessage(status)
	return b, err
}

// BaseStations lists all entries.
func (r *Registry) BaseStations() ([]BaseStation, error) {
	rows, err := r.db.Query(`SELECT bs_eui, name, state, cert_fingerprint, version, class, bidi, vendor, model, sw_version, connected, last_seen_ns, last_status FROM base_station ORDER BY bs_eui`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BaseStation
	for rows.Next() {
		b, err := scanBS(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddBaseStation creates an entry, e.g. a pending one on first contact.
func (r *Registry) AddBaseStation(eui, state, fingerprint string) error {
	_, err := r.db.Exec(`INSERT INTO base_station (bs_eui, state, cert_fingerprint, last_seen_ns) VALUES (?, ?, ?, ?)`, eui, state, fingerprint, now())
	return err
}

// SetBaseStationState changes the state, and binds the fingerprint when it is not empty.
func (r *Registry) SetBaseStationState(eui, state, fingerprint string) error {
	res, err := r.db.Exec(`UPDATE base_station SET state = ?, cert_fingerprint = CASE WHEN ? = '' THEN cert_fingerprint ELSE ? END WHERE bs_eui = ?`, state, fingerprint, fingerprint, eui)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Connected records a connect with the data from con.
func (r *Registry) Connected(b BaseStation) error {
	_, err := r.db.Exec(`UPDATE base_station SET version = ?, class = ?, bidi = ?, vendor = ?, model = ?, sw_version = ?, connected = 1, last_seen_ns = ? WHERE bs_eui = ?`,
		b.Version, b.Class, b2i(b.Bidi), b.Vendor, b.Model, b.SwVersion, now(), b.EUI)
	return err
}

// Disconnected marks a base station as not connected.
func (r *Registry) Disconnected(eui string) error {
	_, err := r.db.Exec(`UPDATE base_station SET connected = 0, last_seen_ns = ? WHERE bs_eui = ?`, now(), eui)
	return err
}

// ResetConnections marks all base stations as disconnected, at start-up.
func (r *Registry) ResetConnections() error {
	_, err := r.db.Exec(`UPDATE base_station SET connected = 0`)
	return err
}

// SetStatus stores the last status response.
func (r *Registry) SetStatus(eui string, status map[string]any) error {
	b, err := json.Marshal(status)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`UPDATE base_station SET last_status = ?, last_seen_ns = ? WHERE bs_eui = ?`, string(b), now(), eui)
	return err
}

// EndPoint is one end-point entry.
type EndPoint struct {
	EUI           string `json:"epEui"`
	OwnerAC       string `json:"ownerAc"`
	Class         string `json:"class"`
	NwkKey        string `json:"-"`
	ShAddr        *int   `json:"shAddr,omitempty"`
	PreAttach     bool   `json:"preAttach"`
	AttachCnt     int64  `json:"attachCnt"`
	LastPacketCnt int64  `json:"lastPacketCnt"`
	State         string `json:"state"`
	NwkSnKey      string `json:"-"`
	DualChan      bool   `json:"dualChan"`
	Repetition    bool   `json:"repetition"`
	WideCarrOff   bool   `json:"wideCarrOff"`
	LongBlkDist   bool   `json:"longBlkDist"`
	AttachedVia   string `json:"attachedVia"`
}

// Bidi reports whether the end-point receives downlinks.
func (e EndPoint) Bidi() bool { return e.Class != "z" }

// PutEndPoint creates or replaces the entry of an end-point (state registered, or attached
// with the network key as session key if pre-attached).
func (r *Registry) PutEndPoint(e EndPoint) error {
	if _, err := idt.ParseEUI64(e.EUI); err != nil {
		return err
	}
	if _, err := idt.ParseKey(e.NwkKey); err != nil {
		return err
	}
	if _, err := idt.ParseClass(e.Class); err != nil {
		return err
	}
	if e.PreAttach && e.ShAddr == nil {
		return errors.New("registry: a pre-attached end-point needs a short address")
	}
	state, snKey := "registered", ""
	if e.PreAttach {
		state, snKey = "attached", e.NwkKey
	}
	_, err := r.db.Exec(`INSERT INTO end_point (ep_eui, owner_ac, class, nwk_key, sh_addr, pre_attach, attach_cnt, last_packet_cnt, state, nwk_sn_key, dual_chan, repetition, wide_carr_off, long_blk_dist, updated_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (ep_eui) DO UPDATE SET owner_ac = excluded.owner_ac, class = excluded.class, nwk_key = excluded.nwk_key, sh_addr = excluded.sh_addr,
		pre_attach = excluded.pre_attach, attach_cnt = excluded.attach_cnt, last_packet_cnt = excluded.last_packet_cnt, state = excluded.state,
		nwk_sn_key = excluded.nwk_sn_key, dual_chan = excluded.dual_chan, repetition = excluded.repetition, wide_carr_off = excluded.wide_carr_off,
		long_blk_dist = excluded.long_blk_dist, attached_via = '', updated_ns = excluded.updated_ns`,
		e.EUI, e.OwnerAC, e.Class, e.NwkKey, e.ShAddr, b2i(e.PreAttach), e.AttachCnt, e.LastPacketCnt, state, snKey,
		b2i(e.DualChan), b2i(e.Repetition), b2i(e.WideCarrOff), b2i(e.LongBlkDist), now())
	return err
}

const epCols = `ep_eui, owner_ac, class, nwk_key, sh_addr, pre_attach, attach_cnt, last_packet_cnt, state, nwk_sn_key, dual_chan, repetition, wide_carr_off, long_blk_dist, attached_via`

func scanEP(s scanner) (EndPoint, error) {
	var e EndPoint
	var sh sql.NullInt64
	var pre, dc, rep, wco, lbd int
	err := s.Scan(&e.EUI, &e.OwnerAC, &e.Class, &e.NwkKey, &sh, &pre, &e.AttachCnt, &e.LastPacketCnt, &e.State, &e.NwkSnKey, &dc, &rep, &wco, &lbd, &e.AttachedVia)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if sh.Valid {
		v := int(sh.Int64)
		e.ShAddr = &v
	}
	e.PreAttach, e.DualChan, e.Repetition, e.WideCarrOff, e.LongBlkDist = pre == 1, dc == 1, rep == 1, wco == 1, lbd == 1
	return e, err
}

// EndPoint returns one entry.
func (r *Registry) EndPoint(eui string) (EndPoint, error) {
	return scanEP(r.db.QueryRow(`SELECT `+epCols+` FROM end_point WHERE ep_eui = ?`, eui))
}

// EndPoints lists the entries, optionally only the attached ones.
func (r *Registry) EndPoints(attachedOnly bool) ([]EndPoint, error) {
	q := `SELECT ` + epCols + ` FROM end_point`
	if attachedOnly {
		q += ` WHERE state = 'attached'`
	}
	rows, err := r.db.Query(q + ` ORDER BY ep_eui`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndPoint
	for rows.Next() {
		e, err := scanEP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEndPoint removes an entry.
func (r *Registry) DeleteEndPoint(eui string) error {
	res, err := r.db.Exec(`DELETE FROM end_point WHERE ep_eui = ?`, eui)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = r.db.Exec(`DELETE FROM ep_reception WHERE ep_eui = ?`, eui)
	return nil
}

// Attached records an accepted over-the-air attach.
func (r *Registry) Attached(eui string, attachCnt int64, snKey string, shAddr int, e EndPoint, viaBS string) error {
	_, err := r.db.Exec(`UPDATE end_point SET state = 'attached', attach_cnt = ?, nwk_sn_key = ?, sh_addr = ?, last_packet_cnt = -1,
		dual_chan = ?, repetition = ?, wide_carr_off = ?, long_blk_dist = ?, attached_via = ?, updated_ns = ? WHERE ep_eui = ?`,
		attachCnt, snKey, shAddr, b2i(e.DualChan), b2i(e.Repetition), b2i(e.WideCarrOff), b2i(e.LongBlkDist), viaBS, now(), eui)
	return err
}

// Detached records a detach.
func (r *Registry) Detached(eui string) error {
	_, err := r.db.Exec(`UPDATE end_point SET state = 'detached', nwk_sn_key = '', updated_ns = ? WHERE ep_eui = ?`, now(), eui)
	return err
}

// ShortAddrInUse reports whether another attached end-point uses addr.
func (r *Registry) ShortAddrInUse(addr int, except string) (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM end_point WHERE sh_addr = ? AND state = 'attached' AND ep_eui <> ?`, addr, except).Scan(&n)
	return n > 0, err
}

// Reception of one uplink at one base station.
type Reception struct {
	BSEui string         `json:"bsEui"`
	RxNs  int64          `json:"rxTime"`
	SNR   float64        `json:"snr"`
	RSSI  float64        `json:"rssi"`
	Extra map[string]any `json:"extra,omitempty"` // other reception fields as received
}

// Uplink is one deduplicated uplink.
type Uplink struct {
	ID         int64       `json:"id"`
	EPEui      string      `json:"epEui"`
	PacketCnt  int64       `json:"packetCnt"`
	RxNs       int64       `json:"rxTime"`
	Format     int64       `json:"format"`
	Payload    string      `json:"userData"` // upper case hex
	Receptions []Reception `json:"receptions"`
}

// StoreUplink stores a deduplicated uplink and the receptions, and advances the packet
// counter of the end-point.
func (r *Registry) StoreUplink(u Uplink) (int64, error) {
	recs, err := json.Marshal(u.Receptions)
	if err != nil {
		return 0, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO uplink (ep_eui, packet_cnt, rx_ns, format, payload, receptions) VALUES (?, ?, ?, ?, ?, ?)`,
		u.EPEui, u.PacketCnt, u.RxNs, u.Format, u.Payload, string(recs))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`UPDATE end_point SET last_packet_cnt = MAX(last_packet_cnt, ?), updated_ns = ? WHERE ep_eui = ?`, u.PacketCnt, now(), u.EPEui); err != nil {
		return 0, err
	}
	for _, rc := range u.Receptions {
		if _, err := tx.Exec(`INSERT INTO ep_reception VALUES (?, ?, ?, ?, ?) ON CONFLICT (ep_eui, bs_eui) DO UPDATE SET rx_ns = excluded.rx_ns, snr = excluded.snr, rssi = excluded.rssi`,
			u.EPEui, rc.BSEui, rc.RxNs, rc.SNR, rc.RSSI); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// Uplinks returns the newest uplinks, optionally of one end-point.
func (r *Registry) Uplinks(ep string, limit int) ([]Uplink, error) {
	q, args := `SELECT id, ep_eui, packet_cnt, rx_ns, format, payload, receptions FROM uplink`, []any{}
	if ep != "" {
		q += ` WHERE ep_eui = ?`
		args = append(args, ep)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Uplink
	for rows.Next() {
		var u Uplink
		var recs string
		if err := rows.Scan(&u.ID, &u.EPEui, &u.PacketCnt, &u.RxNs, &u.Format, &u.Payload, &recs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(recs), &u.Receptions); err != nil {
			return nil, fmt.Errorf("registry: uplink %d: %w", u.ID, err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Count adds delta to a usage counter of today (G-36).
func (r *Registry) Count(acEui, name string, delta int64) error {
	day := time.Now().UTC().Format("2006-01-02")
	_, err := r.db.Exec(`INSERT INTO usage_counter VALUES (?, ?, ?, ?) ON CONFLICT (ac_eui, day, name) DO UPDATE SET value = value + excluded.value`, acEui, day, name, delta)
	return err
}
