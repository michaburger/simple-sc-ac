package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sc.toml")
	if err := os.WriteFile(path, []byte("data_dir = \"/tmp/x\"\n[bssci]\nlisten = \":1\"\nmode = \"strict\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultSC()
	err := Load(&cfg, path, []string{"SSCAC_BSSCI_LISTEN=:2", "SSCAC_EUI=00-00-00-00-00-00-00-00", "SSCAC_SCACI_IDLE_PING=30s", "PATH=x"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/tmp/x" || cfg.BSSCI.Mode != "strict" {
		t.Fatalf("file not applied: %+v", cfg)
	}
	if cfg.BSSCI.Listen != ":2" || cfg.EUI != "00-00-00-00-00-00-00-00" || cfg.SCACI.IdlePing != 30*time.Second {
		t.Fatalf("environment not applied: %+v", cfg)
	}
	if cfg.SCACI.Listen != ":17107" || cfg.BSSCI.Policy != "follow" {
		t.Fatalf("defaults lost: %+v", cfg)
	}
}

func TestUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sc.toml")
	_ = os.WriteFile(path, []byte("[bssci]\nlisen = \":1\"\n"), 0o600)
	cfg := DefaultSC()
	if err := Load(&cfg, path, nil); err == nil || !strings.Contains(err.Error(), "lisen") {
		t.Fatalf("typo not reported: %v", err)
	}
	cfg = DefaultSC()
	if err := Load(&cfg, "", []string{"SSCAC_BSSCI_LISEN=:1"}); err == nil {
		t.Fatal("unknown environment variable not reported")
	}
	cfg = DefaultSC()
	if err := Load(&cfg, "", []string{"SSCAC_BSSCI_IDLE_PING=often"}); err == nil {
		t.Fatal("bad duration accepted")
	}
}

func TestFinish(t *testing.T) {
	c := Common{DataDir: "/d"}
	c.Finish()
	if c.SchemaDir != "/d/schemas" || c.TraceFile != "/d/trace/trace.jsonl" {
		t.Fatalf("%+v", c)
	}
}

func TestOtherProgramsVariablesAreFine(t *testing.T) {
	cfg := DefaultAC()
	if err := Load(&cfg, "", []string{"SSCAC_BSSCI_LISTEN=:1", "SSCAC_SCACI_BUFFER_MAX=3", "SSCAC_CONFIG=x"}); err != nil {
		t.Fatalf("variable of simple-sc refused by simple-ac: %v", err)
	}
	if err := Load(&cfg, "", []string{"SSCAC_BSSCI_LISEN=:1"}); err == nil {
		t.Fatal("typo accepted")
	}
}

func TestACDefaults(t *testing.T) {
	cfg := DefaultAC()
	if err := Load(&cfg, "", []string{"SSCAC_ADMIN_LISTEN=127.0.0.1:9999", "SSCAC_IDLE_PING=1m"}); err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Listen != "127.0.0.1:9999" || cfg.IdlePing != time.Minute {
		t.Fatalf("%+v", cfg)
	}
	sc := DefaultSC()
	if err := Load(&sc, "", []string{"SSCAC_SCACI_BUFFER_MAX=5", "SSCAC_SCACI_LISTEN=:1"}); err != nil {
		t.Fatal(err)
	}
	if sc.SCACI.BufferMax != 5 || sc.SCACI.Listen != ":1" || sc.SCACI.BufferAge != 24*time.Hour {
		t.Fatalf("%+v", sc.SCACI)
	}
}
