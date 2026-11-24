package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
)

func TestEndPointsAndBaseStations(t *testing.T) {
	reg, err := registry.Open(filepath.Join(t.TempDir(), "sc.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	srv := httptest.NewServer((&API{Reg: reg}).Handler())
	defer srv.Close()
	post := func(path, body string) int {
		r, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := post("/api/endpoints", `{"epEui":"02-00-00-00-00-00-00-01","nwkKey":"000102030405060708090A0B0C0D0E0F"}`); c != 201 {
		t.Fatalf("create %d", c)
	}
	if c := post("/api/endpoints", `{"epEui":"02-00-00-00-00-00-00-01","nwkKey":"000102030405060708090a0b0c0d0e0f"}`); c != 400 {
		t.Fatalf("lower case key accepted: %d", c)
	}
	if c := post("/api/endpoints", `{"epEui":"02-00-00-00-00-00-00-02","nwkKey":"000102030405060708090A0B0C0D0E0F","class":"z","preAttach":true}`); c != 400 {
		t.Fatalf("pre-attach without short address: %d", c)
	}
	if c := post("/api/endpoints", `{"epEui":"02-00-00-00-00-00-00-01","nwkKey":"000102030405060708090A0B0C0D0E0F","colour":"red"}`); c != 400 {
		t.Fatalf("unknown field accepted: %d", c)
	}
	r, _ := http.Get(srv.URL + "/api/endpoints")
	var b strings.Builder
	buf := make([]byte, 4096)
	n, _ := r.Body.Read(buf)
	b.Write(buf[:n])
	r.Body.Close()
	if strings.Contains(b.String(), "0102030405") {
		t.Fatal("network key in the list")
	}
	_ = reg.AddBaseStation("70-B3-D5-9C-D0-00-09-E2", "pending", "")
	if c := post("/api/basestations/70-B3-D5-9C-D0-00-09-E2/accept", ""); c != 200 {
		t.Fatalf("accept %d", c)
	}
	if bs, _ := reg.BaseStation("70-B3-D5-9C-D0-00-09-E2"); bs.State != "enabled" {
		t.Fatal(bs.State)
	}
	if c := post("/api/basestations/70-B3-D5-9C-D0-00-09-E3/accept", ""); c != 404 {
		t.Fatalf("unknown base station %d", c)
	}
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/endpoints/02-00-00-00-00-00-00-01", nil)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 204 {
		t.Fatalf("delete %d", r.StatusCode)
	}
}
