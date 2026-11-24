// Package api is the local REST interface of simple-ac, on 127.0.0.1:8108 by default. It
// has no authentication, keep it on your own computer or a private network.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/michaburger/simple-sc-ac/internal/ac/node"
	"github.com/michaburger/simple-sc-ac/internal/ac/scclient"
	"github.com/michaburger/simple-sc-ac/internal/ac/store"
	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
)

// API serves the endpoints.
type API struct {
	Node  *node.Node
	Store *store.Store
	ACEui idt.EUI64
}

// EndPointRequest is the body of POST /api/endpoints.
type EndPointRequest struct {
	EUI       string `json:"epEui"`
	NwkKey    string `json:"nwkKey"`
	Name      string `json:"name,omitempty"`
	Class     string `json:"class,omitempty"`
	ShAddr    string `json:"shAddr,omitempty"`
	PreAttach bool   `json:"preAttach,omitempty"`
	AttachCnt int64  `json:"attachCnt,omitempty"`
	PacketCnt int64  `json:"packetCnt,omitempty"`
}

// Handler returns the HTTP handler.
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/status", a.status)
	m.HandleFunc("GET /api/servicecenters", a.listSC)
	m.HandleFunc("POST /api/servicecenters", a.addSC)
	m.HandleFunc("POST /api/servicecenters/{name}/request", a.requestSC)
	m.HandleFunc("GET /api/servicecenters/{name}/status", a.scStatus)
	m.HandleFunc("GET /api/endpoints", a.listEP)
	m.HandleFunc("POST /api/endpoints", a.putEP)
	m.HandleFunc("GET /api/endpoints/{eui}", a.getEP)
	m.HandleFunc("DELETE /api/endpoints/{eui}", a.deleteEP)
	m.HandleFunc("GET /api/uplinks", a.uplinks)
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

func (a *API) infos() []scclient.Info {
	var out []scclient.Info
	for _, c := range a.Node.Clients() {
		out = append(out, c.Info())
	}
	return out
}

func (a *API) status(w http.ResponseWriter, _ *http.Request) {
	eps, _ := a.Store.EndPoints()
	reply(w, 200, map[string]any{"program": "simple-ac", "version": buildinfo.Version, "acEui": a.ACEui.String(),
		"serviceCenters": a.infos(), "endPoints": len(eps)})
}

func (a *API) listSC(w http.ResponseWriter, _ *http.Request) { reply(w, 200, a.infos()) }

func (a *API) scStatus(w http.ResponseWriter, r *http.Request) {
	c, ok := a.Node.Client(r.PathValue("name"))
	if !ok {
		fail(w, 404, errors.New("no such Service Center"))
		return
	}
	st, err := c.Status(r.Context())
	if err != nil {
		fail(w, 502, err)
		return
	}
	if bss, ok := st["baseStations"].([]any); ok {
		for _, b := range bss {
			if m, ok := b.(map[string]any); ok {
				if u, ok := m["bsEui"].(uint64); ok {
					m["bsEui"] = idt.EUI64(u).String()
				}
			}
		}
	}
	reply(w, 200, st)
}

type epView struct {
	store.EndPoint
	State         *store.State         `json:"state,omitempty"`
	Registrations []store.Registration `json:"registrations"`
}

func (a *API) view(e store.EndPoint) epView {
	v := epView{EndPoint: e}
	if st, err := a.Store.StateOf(e.EUI); err == nil {
		v.State = &st
	}
	v.Registrations, _ = a.Store.Registrations(e.EUI)
	return v
}

func (a *API) listEP(w http.ResponseWriter, _ *http.Request) {
	eps, err := a.Store.EndPoints()
	if err != nil {
		fail(w, 500, err)
		return
	}
	out := make([]epView, 0, len(eps))
	for _, e := range eps {
		out = append(out, a.view(e))
	}
	reply(w, 200, out)
}

func (a *API) getEP(w http.ResponseWriter, r *http.Request) {
	e, err := a.Store.EndPoint(r.PathValue("eui"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	reply(w, 200, a.view(e))
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
	e := store.EndPoint{EUI: q.EUI, NwkKey: q.NwkKey, Name: q.Name, Class: q.Class, PreAttach: q.PreAttach, AttachCnt: q.AttachCnt, PacketCnt: q.PacketCnt}
	if q.ShAddr != "" {
		sh, err := idt.ParseShortAddr(q.ShAddr)
		if err != nil {
			fail(w, 400, err)
			return
		}
		v := int(sh)
		e.ShAddr = &v
	}
	if _, err := a.Node.AddEndPoint(r.Context(), e); err != nil {
		fail(w, 400, err)
		return
	}
	got, _ := a.Store.EndPoint(e.EUI)
	reply(w, 201, a.view(got))
}

func (a *API) deleteEP(w http.ResponseWriter, r *http.Request) {
	if err := a.Node.RemoveEndPoint(r.Context(), r.PathValue("eui")); err != nil {
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
	l, err := a.Store.Uplinks(r.URL.Query().Get("ep"), limit)
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, l)
}

// requestSC creates the key and the certificate request for a Service Center.
func (a *API) requestSC(w http.ResponseWriter, r *http.Request) {
	csr, err := a.Node.RequestFor(r.PathValue("name"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	reply(w, 201, map[string]string{"acEui": a.ACEui.String(), "csr": string(csr)})
}

// addSC imports a bundle and connects.
func (a *API) addSC(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, 400, err)
		return
	}
	b, err := a.Node.AddServiceCenter(body)
	if err != nil {
		fail(w, 400, err)
		return
	}
	c, _ := a.Node.Client(b.Name)
	reply(w, 201, c.Info())
}
