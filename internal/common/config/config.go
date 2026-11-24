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
	EUI       string `toml:"eui"`        // own EUI-64, empty: derive (D-29)
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

// SC is the configuration of simple-sc.
type SC struct {
	Common
	BSSCI Listener `toml:"bssci"`
	SCACI Listener `toml:"scaci"`
}

// ServiceCenter is one SC that simple-ac connects to.
type ServiceCenter struct {
	Name    string `toml:"name"`
	Address string `toml:"address"`
}

// AC is the configuration of simple-ac.
type AC struct {
	Common
	ResponseTimeout time.Duration   `toml:"response_timeout"`
	ServiceCenters  []ServiceCenter `toml:"service_center"`
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
// Service Center (D-23).
func DefaultSC() SC {
	return SC{
		Common: Common{LogLevel: "info"},
		BSSCI:  Listener{Listen: ":16107", Policy: "follow", Mode: "lenient", ResponseTimeout: 5 * time.Second, IdlePing: 10 * time.Second},
		SCACI:  Listener{Listen: ":17107", Policy: "follow", Mode: "strict", ResponseTimeout: 5 * time.Second, IdlePing: 10 * time.Second},
	}
}

// DefaultAC returns the defaults of simple-ac.
func DefaultAC() AC {
	return AC{Common: Common{LogLevel: "info"}, ResponseTimeout: 5 * time.Second}
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
