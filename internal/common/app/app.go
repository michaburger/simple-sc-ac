// Package app holds the start-up steps that simple-sc and simple-ac share: logging, the
// state directory, the own EUI and the schema files.
package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/michaburger/simple-sc-ac/internal/common/config"
	"github.com/michaburger/simple-sc-ac/internal/common/identity"
	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/proto/schema"
)

// Env is what a program has after start-up.
type Env struct {
	Log     *slog.Logger
	EUI     idt.EUI64
	Schemas *schema.Set
}

// NewLogger writes JSON to standard error at the configured level.
func NewLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		return nil, fmt.Errorf("log_level %q: want debug, info, warn or error", level)
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})), nil
}

// Start creates the state directory, resolves the own EUI and loads the schema files.
// A missing or empty schema directory is not an error: the interfaces without schema
// files stay switched off, and the log says so.
func Start(role string, c *config.Common) (*Env, error) {
	c.Finish()
	log, err := NewLogger(c.LogLevel)
	if err != nil {
		return nil, err
	}
	log = log.With("program", "simple-"+role)
	for _, d := range []string{c.DataDir, filepath.Join(c.DataDir, "identity")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("data directory: %w", err)
		}
	}
	id, err := identity.Resolve(role, c.EUI, c.DataDir)
	if err != nil {
		return nil, err
	}
	log.Info("identity", "eui", id.EUI.String(), "source", string(id.Source))
	if id.Warning != "" {
		log.Warn(id.Warning)
	}
	set, err := LoadSchemas(c.SchemaDir)
	if err != nil {
		return nil, err
	}
	for _, iface := range []string{"bssci", "scaci", "acint"} {
		if v := set.Versions(iface); len(v) > 0 {
			log.Info("schema files loaded", "interface", iface, "versions", v)
		}
	}
	return &Env{Log: log, EUI: id.EUI, Schemas: set}, nil
}

// LoadSchemas loads all schema files of dir. A missing directory gives an empty set.
func LoadSchemas(dir string) (*schema.Set, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return schema.Load(emptyFS{})
	}
	return schema.Load(os.DirFS(dir))
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
