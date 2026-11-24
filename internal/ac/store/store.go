// Package store is the state of the Application Center: its end-points with their keys,
// where they are registered, their last state, and the received uplinks.
package store

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	cstore "github.com/michaburger/simple-sc-ac/internal/common/store"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when an entry does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps the database.
type Store struct{ db *sql.DB }

// Open opens or creates the store.
func Open(file string) (*Store, error) {
	sub, _ := fs.Sub(migrationFS, "migrations")
	db, err := cstore.Open(file, sub)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().UnixNano() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// EndPoint is an end-point of this AC.
type EndPoint struct {
	EUI         string `json:"epEui"`
	Name        string `json:"name,omitempty"`
	Class       string `json:"class"`
	NwkKey      string `json:"-"`
	ShAddr      *int   `json:"shAddr,omitempty"`
	PreAttach   bool   `json:"preAttach"`
	AttachCnt   int64  `json:"attachCnt"`
	PacketCnt   int64  `json:"packetCnt"`
	DualChan    bool   `json:"dualChan"`
	Repetition  bool   `json:"repetition"`
	WideCarrOff bool   `json:"wideCarrOff"`
	LongBlkDist bool   `json:"longBlkDist"`
}

// Check validates an end-point before it is stored.
func (e EndPoint) Check() error {
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
		return errors.New("store: a pre-attached end-point needs a short address")
	}
	return nil
}

// PutEndPoint creates or replaces an end-point.
func (s *Store) PutEndPoint(e EndPoint) error {
	if err := e.Check(); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO end_point (ep_eui, class, nwk_key, sh_addr, pre_attach, attach_cnt, packet_cnt, dual_chan, repetition, wide_carr_off, long_blk_dist, name, created_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (ep_eui) DO UPDATE SET class = excluded.class, nwk_key = excluded.nwk_key, sh_addr = excluded.sh_addr, pre_attach = excluded.pre_attach,
		attach_cnt = excluded.attach_cnt, packet_cnt = excluded.packet_cnt, dual_chan = excluded.dual_chan, repetition = excluded.repetition,
		wide_carr_off = excluded.wide_carr_off, long_blk_dist = excluded.long_blk_dist, name = excluded.name`,
		e.EUI, e.Class, e.NwkKey, e.ShAddr, b2i(e.PreAttach), e.AttachCnt, e.PacketCnt, b2i(e.DualChan), b2i(e.Repetition), b2i(e.WideCarrOff), b2i(e.LongBlkDist), e.Name, now())
	return err
}

const epCols = `ep_eui, class, nwk_key, sh_addr, pre_attach, attach_cnt, packet_cnt, dual_chan, repetition, wide_carr_off, long_blk_dist, name`

type scanner interface{ Scan(...any) error }

func scanEP(r scanner) (EndPoint, error) {
	var e EndPoint
	var sh sql.NullInt64
	var pre, dc, rep, wco, lbd int
	err := r.Scan(&e.EUI, &e.Class, &e.NwkKey, &sh, &pre, &e.AttachCnt, &e.PacketCnt, &dc, &rep, &wco, &lbd, &e.Name)
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

// EndPoint returns one end-point.
func (s *Store) EndPoint(eui string) (EndPoint, error) {
	return scanEP(s.db.QueryRow(`SELECT `+epCols+` FROM end_point WHERE ep_eui = ?`, eui))
}

// EndPoints lists all end-points.
func (s *Store) EndPoints() ([]EndPoint, error) {
	rows, err := s.db.Query(`SELECT ` + epCols + ` FROM end_point ORDER BY ep_eui`)
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

// DeleteEndPoint removes an end-point with its registrations and state.
func (s *Store) DeleteEndPoint(eui string) error {
	res, err := s.db.Exec(`DELETE FROM end_point WHERE ep_eui = ?`, eui)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.db.Exec(`DELETE FROM registration WHERE ep_eui = ?`, eui)
	_, _ = s.db.Exec(`DELETE FROM ep_state WHERE ep_eui = ?`, eui)
	return nil
}

// Registration of an end-point at one SC.
type Registration struct {
	SC    string `json:"serviceCenter"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// SetRegistration records the result of a registration.
func (s *Store) SetRegistration(ep, sc, state, errText string) error {
	_, err := s.db.Exec(`INSERT INTO registration VALUES (?, ?, ?, ?, ?) ON CONFLICT (ep_eui, sc_name) DO UPDATE SET state = excluded.state, error = excluded.error, updated_ns = excluded.updated_ns`,
		ep, sc, state, errText, now())
	return err
}

// Registrations of an end-point.
func (s *Store) Registrations(ep string) ([]Registration, error) {
	rows, err := s.db.Query(`SELECT sc_name, state, error FROM registration WHERE ep_eui = ? ORDER BY sc_name`, ep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registration
	for rows.Next() {
		var r Registration
		if err := rows.Scan(&r.SC, &r.State, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// State is the last known state of an end-point.
type State struct {
	Status        string `json:"status"`
	LastSeenNs    int64  `json:"lastSeenNs"`
	LastPacketCnt int64  `json:"lastPacketCnt"`
	LastSC        string `json:"lastServiceCenter"`
	LastBS        string `json:"lastBaseStation"`
}

// SetStatus records an end-point status (attached, detached, ...).
func (s *Store) SetStatus(ep, status, sc, bs string) error {
	_, err := s.db.Exec(`INSERT INTO ep_state (ep_eui, status, last_seen_ns, last_sc, last_bs) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (ep_eui) DO UPDATE SET status = excluded.status, last_seen_ns = excluded.last_seen_ns, last_sc = excluded.last_sc,
		last_bs = CASE WHEN excluded.last_bs = '' THEN last_bs ELSE excluded.last_bs END`, ep, status, now(), sc, bs)
	return err
}

// StateOf returns the state of an end-point.
func (s *Store) StateOf(ep string) (State, error) {
	var st State
	err := s.db.QueryRow(`SELECT status, last_seen_ns, last_packet_cnt, last_sc, last_bs FROM ep_state WHERE ep_eui = ?`, ep).
		Scan(&st.Status, &st.LastSeenNs, &st.LastPacketCnt, &st.LastSC, &st.LastBS)
	if errors.Is(err, sql.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, err
}

// Uplink as received from a Service Center.
type Uplink struct {
	ID         int64            `json:"id"`
	SC         string           `json:"serviceCenter"`
	EPEui      string           `json:"epEui"`
	PacketCnt  int64            `json:"packetCnt"`
	RxNs       int64            `json:"rxTime"`
	Format     int64            `json:"format"`
	Payload    string           `json:"userData"`
	Receptions []map[string]any `json:"baseStations"`
}

// AddUplink stores an uplink and updates the state. It returns false for a packet
// counter the AC has already stored (the same uplink through a second SC, or a replay).
func (s *Store) AddUplink(u Uplink) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM uplink WHERE ep_eui = ? AND packet_cnt = ? AND rx_ns > ?`, u.EPEui, u.PacketCnt, u.RxNs-int64(time.Minute)).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	recs, _ := json.Marshal(u.Receptions)
	if _, err := tx.Exec(`INSERT INTO uplink (sc_name, ep_eui, packet_cnt, rx_ns, format, payload, receptions, received_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.SC, u.EPEui, u.PacketCnt, u.RxNs, u.Format, u.Payload, string(recs), now()); err != nil {
		return false, err
	}
	bs := ""
	if len(u.Receptions) > 0 {
		if b, ok := u.Receptions[0]["bsEui"].(string); ok {
			bs = b
		}
	}
	if _, err := tx.Exec(`INSERT INTO ep_state (ep_eui, status, last_seen_ns, last_packet_cnt, last_sc, last_bs) VALUES (?, 'attached', ?, ?, ?, ?)
		ON CONFLICT (ep_eui) DO UPDATE SET last_seen_ns = excluded.last_seen_ns, last_packet_cnt = MAX(last_packet_cnt, excluded.last_packet_cnt),
		last_sc = excluded.last_sc, last_bs = excluded.last_bs`, u.EPEui, now(), u.PacketCnt, u.SC, bs); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Uplinks returns the newest uplinks, optionally of one end-point.
func (s *Store) Uplinks(ep string, limit int) ([]Uplink, error) {
	q, args := `SELECT id, sc_name, ep_eui, packet_cnt, rx_ns, format, payload, receptions FROM uplink`, []any{}
	if ep != "" {
		q += ` WHERE ep_eui = ?`
		args = append(args, ep)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Uplink
	for rows.Next() {
		var u Uplink
		var recs string
		if err := rows.Scan(&u.ID, &u.SC, &u.EPEui, &u.PacketCnt, &u.RxNs, &u.Format, &u.Payload, &recs); err != nil {
			return nil, err
		}
		dec := json.NewDecoder(strings.NewReader(recs))
		dec.UseNumber() // nanosecond times above 2^53 stay exact
		_ = dec.Decode(&u.Receptions)
		out = append(out, u)
	}
	return out, rows.Err()
}
