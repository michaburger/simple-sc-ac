package registry

import (
	"database/sql"
	"errors"
	"time"
)

// ApplicationCenter is one registered Application Center.
type ApplicationCenter struct {
	EUI         string `json:"acEui"`
	Name        string `json:"name"`
	State       string `json:"state"` // enabled, disabled
	Fingerprint string `json:"certFingerprint"`
	Version     string `json:"version"`
	Connected   bool   `json:"connected"`
	LastSeenNs  int64  `json:"lastSeenNs"`
}

// AddApplicationCenter registers an AC with the fingerprint of its certificate. An
// existing entry is replaced (new certificate), its end-points stay.
func (r *Registry) AddApplicationCenter(eui, name, fingerprint string) error {
	_, err := r.db.Exec(`INSERT INTO application_center (ac_eui, name, state, cert_fingerprint) VALUES (?, ?, 'enabled', ?)
		ON CONFLICT (ac_eui) DO UPDATE SET name = excluded.name, cert_fingerprint = excluded.cert_fingerprint, state = 'enabled'`, eui, name, fingerprint)
	return err
}

// ApplicationCenter returns one entry.
func (r *Registry) ApplicationCenter(eui string) (ApplicationCenter, error) {
	var a ApplicationCenter
	var conn int
	err := r.db.QueryRow(`SELECT ac_eui, name, state, cert_fingerprint, version, connected, last_seen_ns FROM application_center WHERE ac_eui = ?`, eui).
		Scan(&a.EUI, &a.Name, &a.State, &a.Fingerprint, &a.Version, &conn, &a.LastSeenNs)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Connected = conn == 1
	return a, err
}

// ApplicationCenters lists all entries.
func (r *Registry) ApplicationCenters() ([]ApplicationCenter, error) {
	rows, err := r.db.Query(`SELECT ac_eui, name, state, cert_fingerprint, version, connected, last_seen_ns FROM application_center ORDER BY ac_eui`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApplicationCenter
	for rows.Next() {
		var a ApplicationCenter
		var conn int
		if err := rows.Scan(&a.EUI, &a.Name, &a.State, &a.Fingerprint, &a.Version, &conn, &a.LastSeenNs); err != nil {
			return nil, err
		}
		a.Connected = conn == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetApplicationCenterState enables or disables an AC.
func (r *Registry) SetApplicationCenterState(eui, state string) error {
	res, err := r.db.Exec(`UPDATE application_center SET state = ? WHERE ac_eui = ?`, state, eui)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ACConnected records the connect or disconnect of an AC.
func (r *Registry) ACConnected(eui, version string, connected bool) error {
	_, err := r.db.Exec(`UPDATE application_center SET version = CASE WHEN ? = '' THEN version ELSE ? END, connected = ?, last_seen_ns = ? WHERE ac_eui = ?`,
		version, version, b2i(connected), now(), eui)
	return err
}

// ResetACConnections marks all ACs as disconnected, at start-up.
func (r *Registry) ResetACConnections() error {
	_, err := r.db.Exec(`UPDATE application_center SET connected = 0`)
	return err
}

// UndeliveredUplinks returns the stored uplinks of the end-points of an AC that were not
// delivered yet, oldest first, not older than maxAge and at most limit. The second value
// counts the undelivered ones beyond these limits, which are marked delivered (dropped).
func (r *Registry) UndeliveredUplinks(acEui string, maxAge time.Duration, limit int) ([]Uplink, int64, error) {
	cut := time.Now().Add(-maxAge).UnixNano()
	tx, err := r.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE uplink SET delivered = 1 WHERE delivered = 0 AND rx_ns < ? AND ep_eui IN (SELECT ep_eui FROM end_point WHERE owner_ac = ?)`, cut, acEui)
	if err != nil {
		return nil, 0, err
	}
	dropped, _ := res.RowsAffected()
	var total int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM uplink WHERE delivered = 0 AND ep_eui IN (SELECT ep_eui FROM end_point WHERE owner_ac = ?)`, acEui).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total > int64(limit) { // keep the newest limit entries
		res, err := tx.Exec(`UPDATE uplink SET delivered = 1 WHERE id IN (SELECT id FROM uplink WHERE delivered = 0 AND ep_eui IN (SELECT ep_eui FROM end_point WHERE owner_ac = ?) ORDER BY id LIMIT ?)`, acEui, total-int64(limit))
		if err != nil {
			return nil, 0, err
		}
		n, _ := res.RowsAffected()
		dropped += n
	}
	rows, err := tx.Query(`SELECT id, ep_eui, packet_cnt, rx_ns, format, payload, receptions, meta FROM uplink WHERE delivered = 0 AND ep_eui IN (SELECT ep_eui FROM end_point WHERE owner_ac = ?) ORDER BY id`, acEui)
	if err != nil {
		return nil, 0, err
	}
	var out []Uplink
	for rows.Next() {
		u, err := scanUplink(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, u)
	}
	rows.Close()
	return out, dropped, tx.Commit()
}

// MarkDelivered marks an uplink as delivered to its AC.
func (r *Registry) MarkDelivered(id int64) error {
	_, err := r.db.Exec(`UPDATE uplink SET delivered = 1 WHERE id = ?`, id)
	return err
}

// Active records that an end-point of an AC sent today, for the usage counter
// "active end-points per day".
func (r *Registry) Active(acEui, epEui string) error {
	day := time.Now().UTC().Format("2006-01-02")
	_, err := r.db.Exec(`INSERT OR IGNORE INTO ep_active VALUES (?, ?, ?)`, day, acEui, epEui)
	return err
}

// Usage is the usage of one AC on one day.
type Usage struct {
	ACEui    string           `json:"acEui"`
	Day      string           `json:"day"`
	Counters map[string]int64 `json:"counters"`
	Active   int64            `json:"activeEndPoints"`
}

// UsageOf returns the counters of the last days, newest first.
func (r *Registry) UsageOf(acEui string, days int) ([]Usage, error) {
	rows, err := r.db.Query(`SELECT day, name, value FROM usage_counter WHERE ac_eui = ? ORDER BY day DESC`, acEui)
	if err != nil {
		return nil, err
	}
	byDay := map[string]*Usage{}
	var order []string
	for rows.Next() {
		var day, name string
		var v int64
		if err := rows.Scan(&day, &name, &v); err != nil {
			rows.Close()
			return nil, err
		}
		u, ok := byDay[day]
		if !ok {
			if len(order) >= days {
				continue
			}
			u = &Usage{ACEui: acEui, Day: day, Counters: map[string]int64{}}
			byDay[day] = u
			order = append(order, day)
		}
		u.Counters[name] = v
	}
	rows.Close()
	out := make([]Usage, 0, len(order))
	for _, d := range order {
		u := byDay[d]
		_ = r.db.QueryRow(`SELECT COUNT(*) FROM ep_active WHERE ac_eui = ? AND day = ?`, acEui, d).Scan(&u.Active)
		out = append(out, *u)
	}
	return out, nil
}
