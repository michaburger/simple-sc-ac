// Package config loads the configuration of simple-sc and simple-ac: built-in defaults,
// then an optional TOML file, then environment variables. Every key can be set by an
// environment variable SSCAC_<SECTION>_<KEY> in upper case, e.g. SSCAC_BSSCI_LISTEN for
// [bssci] listen. Top level keys use SSCAC_<KEY>.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// EnvPrefix of all environment variables.
const EnvPrefix = "SSCAC"

// Common settings of both programs.
type Common struct {
	DataDir   string `toml:"data_dir"`   // all state, default /data in containers, ./data otherwise
	SchemaDir string `toml:"schema_dir"` // interface schema files, default <data_dir>/schemas
	EUI       string `toml:"eui"`        // own EUI-64, empty: derive
	LogLevel  string `toml:"log_level"`  // debug, info, warn, error
	TraceFile string `toml:"trace_file"` // JSON lines, empty: <data_dir>/trace/trace.jsonl, "-" off
}

// Listener of an interface.
type Listener struct {
	Listen          string        `toml:"listen"`
	Policy          string        `toml:"policy"` // version arbitration: follow, offer-highest
	Mode            string        `toml:"mode"`   // strict, lenient
	ResponseTimeout time.Duration `toml:"response_timeout"`
	IdlePing        time.Duration `toml:"idle_ping"`
}

// BSSCIOptions are the base station settings of simple-sc.
type BSSCIOptions struct {
	Listener
	// UnknownBaseStations: reject, pending (default) or accept, see the trust model.
	UnknownBaseStations string        `toml:"unknown_base_stations"`
	DedupWindow         time.Duration `toml:"dedup_window"`
	StatusInterval      time.Duration `toml:"status_interval"`
	// TLS files, default: the lab CA and server certificate under <data_dir>/ca.
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
	CAFile   string `toml:"ca_file"`
	// CertHosts are extra host names or addresses for the lab server certificate,
	// comma separated. On fly.io <app>.fly.dev is added by itself.
	CertHosts string `toml:"cert_hosts"`
}

// Admin is the local REST interface.
type Admin struct {
	Listen string `toml:"listen"`
}

// SCACIOptions are the Application Center settings of simple-sc.
type SCACIOptions struct {
	Listener
	// Uplinks for an AC that is not connected are kept this long, and at most this many.
	BufferAge time.Duration `toml:"buffer_age"`
	BufferMax int           `toml:"buffer_max"`
}

// SC is the configuration of simple-sc.
type SC struct {
	Common
	BSSCI BSSCIOptions `toml:"bssci"`
	SCACI SCACIOptions `toml:"scaci"`
	Admin Admin        `toml:"admin"`
}

// AC is the configuration of simple-ac. The Service Centers come from the enrollment
// bundles in <data_dir>/sc, see "simple-ac sc add".
type AC struct {
	Common
	ResponseTimeout time.Duration `toml:"response_timeout"`
	IdlePing        time.Duration `toml:"idle_ping"`
	Admin           Admin         `toml:"admin"`
}

// DefaultDataDir is /data inside a container (marked by /.dockerenv or the balena
// variables), ./data otherwise.
func DefaultDataDir() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "/data"
	}
	if os.Getenv("BALENA") != "" || os.Getenv("BALENA_DEVICE_UUID") != "" || os.Getenv("FLY_APP_NAME") != "" {
		return "/data"
	}
	return "data"
}

// DefaultSC returns the defaults of simple-sc. Ports 16107 and 17107 as the Fraunhofer
// Service Center.
func DefaultSC() SC {
	return SC{
		Common: Common{LogLevel: "info"},
		BSSCI: BSSCIOptions{
			Listener:            Listener{Listen: ":16107", Policy: "follow", Mode: "lenient", ResponseTimeout: 5 * time.Second, IdlePing: 10 * time.Second},
			UnknownBaseStations: "pending", DedupWindow: 500 * time.Millisecond, StatusInterval: 60 * time.Second,
		},
		Admin: Admin{Listen: "127.0.0.1:8107"},
		SCACI: SCACIOptions{
			Listener:  Listener{Listen: ":17107", Policy: "follow", Mode: "strict", ResponseTimeout: 5 * time.Second, IdlePing: 10 * time.Second},
			BufferAge: 24 * time.Hour, BufferMax: 10000,
		},
	}
}

// DefaultAC returns the defaults of simple-ac.
func DefaultAC() AC {
	return AC{Common: Common{LogLevel: "info"}, ResponseTimeout: 5 * time.Second, IdlePing: 10 * time.Second, Admin: Admin{Listen: "127.0.0.1:8108"}}
}

// Load fills cfg (holding the defaults) from the file at path, if path is not empty, and
// then from the environment. A missing file named explicitly is an error. Unknown keys in
// the file are an error, so that typos do not go unnoticed.
func Load(cfg any, path string, environ []string) error {
	if path != "" {
		md, err := toml.DecodeFile(path, cfg)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			keys := make([]string, len(und))
			for i, k := range und {
				keys[i] = k.String()
			}
			return fmt.Errorf("config: unknown keys in %s: %s", path, strings.Join(keys, ", "))
		}
	}
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, EnvPrefix+"_") {
			env[k] = v
		}
	}
	used := map[string]bool{}
	if err := applyEnv(reflect.ValueOf(cfg).Elem(), EnvPrefix, env, used); err != nil {
		return err
	}
	var unknown []string
	for k := range env {
		if !used[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("config: unknown environment variables: %s", strings.Join(unknown, ", "))
	}
	return nil
}

var durationType = reflect.TypeOf(time.Duration(0))

func applyEnv(v reflect.Value, prefix string, env map[string]string, used map[string]bool) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if f.Anonymous { // embedded Common: same prefix
			if err := applyEnv(fv, prefix, env, used); err != nil {
				return err
			}
			continue
		}
		tag := strings.Split(f.Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		name := prefix + "_" + strings.ToUpper(tag)
		if fv.Kind() == reflect.Struct && fv.Type() != durationType {
			if err := applyEnv(fv, name, env, used); err != nil {
				return err
			}
			continue
		}
		raw, ok := env[name]
		if !ok {
			continue
		}
		used[name] = true
		if err := setValue(fv, raw); err != nil {
			return fmt.Errorf("config: %s: %w", name, err)
		}
	}
	return nil
}

func setValue(fv reflect.Value, raw string) error {
	switch {
	case fv.Type() == durationType:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}
		fv.SetInt(int64(d))
	case fv.Kind() == reflect.String:
		fv.SetString(raw)
	case fv.Kind() == reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case fv.Kind() == reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		fv.SetInt(int64(n))
	default:
		return errors.New("cannot be set by an environment variable, use the configuration file")
	}
	return nil
}

// Finish fills derived defaults of the common part.
func (c *Common) Finish() {
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.SchemaDir == "" {
		c.SchemaDir = c.DataDir + "/schemas"
	}
	if c.TraceFile == "" {
		c.TraceFile = c.DataDir + "/trace/trace.jsonl"
	}
}
