// Package api is the local REST interface of simple-sc. Until the Application Center
// registers end-points over SCACI, end-points are entered here. It listens on
// 127.0.0.1 by default and has no authentication, do not expose it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/sc/bssci"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
	"github.com/michaburger/simple-sc-ac/internal/sc/scaci"
)

// API serves the admin endpoints.
type API struct {
	Reg   *registry.Registry
	BSSCI *bssci.Server // nil when no BSSCI schema is loaded
	SCACI *scaci.Server // nil when no SCACI schema is loaded
	CA    *labca.CA     // signs the certificates of Application Centers
	SCEui idt.EUI64
}

// EndPointRequest is the body of POST /api/endpoints.
type EndPointRequest struct {
	EUI       string `json:"epEui"`
	NwkKey    string `json:"nwkKey"`
	Class     string `json:"class"`               // z, a, b, c; default a
	ShAddr    string `json:"shAddr,omitempty"`    // 4 hex digits, required with preAttach
	PreAttach bool   `json:"preAttach,omitempty"` // for unidirectional end-points
	AttachCnt int64  `json:"attachCnt,omitempty"`
	PacketCnt int64  `json:"packetCnt,omitempty"`
	OwnerAC   string `json:"ownerAc,omitempty"`
}

// Handler returns the HTTP handler.
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/status", a.status)
	m.HandleFunc("GET /api/basestations", a.listBS)
	m.HandleFunc("POST /api/basestations/{eui}/accept", a.setBS("enabled"))
	m.HandleFunc("POST /api/basestations/{eui}/disable", a.setBS("disabled"))
	m.HandleFunc("GET /api/endpoints", a.listEP)
	m.HandleFunc("POST /api/endpoints", a.putEP)
	m.HandleFunc("GET /api/endpoints/{eui}", a.getEP)
	m.HandleFunc("DELETE /api/endpoints/{eui}", a.deleteEP)
	m.HandleFunc("GET /api/uplinks", a.uplinks)
	m.HandleFunc("GET /api/applicationcenters", a.listAC)
	m.HandleFunc("POST /api/applicationcenters", a.addAC)
	m.HandleFunc("POST /api/applicationcenters/{eui}/enable", a.setAC("enabled"))
	m.HandleFunc("POST /api/applicationcenters/{eui}/disable", a.setAC("disabled"))
	m.HandleFunc("GET /api/usage", a.usage)
	return m
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	reply(w, code, map[string]string{"error": err.Error()})
}

func (a *API) status(w http.ResponseWriter, _ *http.Request) {
	var conn []map[string]string
	if a.BSSCI != nil {
		for _, s := range a.BSSCI.Sessions() {
			conn = append(conn, map[string]string{"bsEui": s.BSEui, "version": s.Version, "state": s.State, "class": s.Class})
		}
	}
	eps, _ := a.Reg.EndPoints(false)
	att := 0
	for _, e := range eps {
		if e.State == "attached" {
			att++
		}
	}
	var acs []string
	if a.SCACI != nil {
		acs = a.SCACI.Connected()
	}
	reply(w, 200, map[string]any{"program": "simple-sc", "version": buildinfo.Version, "scEui": a.SCEui.String(),
		"bssci": a.BSSCI != nil, "connected": conn, "applicationCenters": acs, "endPoints": len(eps), "attached": att})
}

func (a *API) listBS(w http.ResponseWriter, _ *http.Request) {
	l, err := a.Reg.BaseStations()
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, l)
}

func (a *API) setBS(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eui := r.PathValue("eui")
		if _, err := idt.ParseEUI64(eui); err != nil {
			fail(w, 400, err)
			return
		}
		if err := a.Reg.SetBaseStationState(eui, state, ""); err != nil {
			code := 500
			if errors.Is(err, registry.ErrNotFound) {
				code = 404
			}
			fail(w, code, err)
			return
		}
		if a.BSSCI != nil {
			if state == "enabled" {
				a.BSSCI.Enable(context.Background(), eui)
			} else {
				a.BSSCI.CloseSession(eui)
			}
		}
		b, _ := a.Reg.BaseStation(eui)
		reply(w, 200, b)
	}
}

func (a *API) listEP(w http.ResponseWriter, _ *http.Request) {
	l, err := a.Reg.EndPoints(false)
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, l)
}

func (a *API) getEP(w http.ResponseWriter, r *http.Request) {
	e, err := a.Reg.EndPoint(r.PathValue("eui"))
	if errors.Is(err, registry.ErrNotFound) {
		fail(w, 404, err)
		return
	}
	reply(w, 200, e)
}

func (a *API) putEP(w http.ResponseWriter, r *http.Request) {
	var q EndPointRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		fail(w, 400, err)
		return
	}
	if q.Class == "" {
		q.Class = "a"
	}
	e := registry.EndPoint{EUI: q.EUI, NwkKey: q.NwkKey, Class: q.Class, PreAttach: q.PreAttach, OwnerAC: q.OwnerAC,
		AttachCnt: q.AttachCnt - 1, LastPacketCnt: q.PacketCnt - 1}
	if q.ShAddr != "" {
		sh, err := idt.ParseShortAddr(q.ShAddr)
		if err != nil {
			fail(w, 400, err)
			return
		}
		v := int(sh)
		e.ShAddr = &v
	}
	if err := a.Reg.PutEndPoint(e); err != nil {
		fail(w, 400, err)
		return
	}
	if q.PreAttach && a.BSSCI != nil {
		a.BSSCI.PropagateEndPoint(q.EUI)
	}
	got, _ := a.Reg.EndPoint(q.EUI)
	reply(w, 201, got)
}

func (a *API) deleteEP(w http.ResponseWriter, r *http.Request) {
	eui := r.PathValue("eui")
	if a.BSSCI != nil {
		a.BSSCI.RevokeEndPoint(eui)
	}
	if err := a.Reg.DeleteEndPoint(eui); err != nil {
		fail(w, 404, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) uplinks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	l, err := a.Reg.Uplinks(r.URL.Query().Get("ep"), limit)
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, l)
}

func (a *API) listAC(w http.ResponseWriter, _ *http.Request) {
	l, err := a.Reg.ApplicationCenters()
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, l)
}

func (a *API) setAC(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eui := r.PathValue("eui")
		if err := a.Reg.SetApplicationCenterState(eui, state); err != nil {
			code := 500
			if errors.Is(err, registry.ErrNotFound) {
				code = 404
			}
			fail(w, code, err)
			return
		}
		if state == "disabled" && a.SCACI != nil {
			a.SCACI.CloseSession(eui)
		}
		ac, _ := a.Reg.ApplicationCenter(eui)
		reply(w, 200, ac)
	}
}

func (a *API) usage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 366 {
		days = 7
	}
	acs, err := a.Reg.ApplicationCenters()
	if err != nil {
		fail(w, 500, err)
		return
	}
	out := []registry.Usage{}
	for _, ac := range acs {
		u, err := a.Reg.UsageOf(ac.EUI, days)
		if err != nil {
			fail(w, 500, err)
			return
		}
		out = append(out, u...)
	}
	reply(w, 200, out)
}

// ACRequest is the body of POST /api/applicationcenters.
type ACRequest struct {
	ACEui      string `json:"acEui"`
	Name       string `json:"name,omitempty"`
	SCName     string `json:"scName,omitempty"`
	Address    string `json:"address"`
	ServerName string `json:"serverName,omitempty"`
	CSR        string `json:"csr,omitempty"`
}

// addAC registers an Application Center and returns its bundle. Without a csr the key is
// created here and is part of the bundle (lab shortcut).
func (a *API) addAC(w http.ResponseWriter, r *http.Request) {
	if a.CA == nil {
		fail(w, 500, errors.New("no certificate authority"))
		return
	}
	var q ACRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		fail(w, 400, err)
		return
	}
	if q.SCName == "" {
		q.SCName = "sc"
	}
	if q.Address == "" {
		fail(w, 400, errors.New("address is required"))
		return
	}
	b, err := scaci.Enroll(a.Reg, a.CA, a.SCEui, scaci.EnrollRequest{Name: q.SCName, ACEui: q.ACEui, ACName: q.Name,
		Address: q.Address, ServerName: q.ServerName, CSR: []byte(q.CSR)})
	if err != nil {
		fail(w, 400, err)
		return
	}
	reply(w, 201, b)
}
