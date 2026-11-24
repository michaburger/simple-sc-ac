// Package schema loads interface schema files and validates messages against them.
//
// A schema file describes one version of one interface: its operations, who may start
// them, and the fields of the three messages of each operation (<cmd>, <cmd>Rsp,
// <cmd>Cmp), plus the error messages. The schema files of the mioty interfaces are not
// part of this repository, the programs load them from a directory at run time.
// testdata/schemas holds synthetic files for the tests.
package schema

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Field describes one field of a message.
type Field struct {
	Type        string   `yaml:"type"`
	Spec        string   `yaml:"spec"`
	Presence    string   `yaml:"presence"` // required (default), optional, conditional
	When        string   `yaml:"when"`
	Default     any      `yaml:"default"`
	DefaultFrom string   `yaml:"default_from"`
	Bits        int      `yaml:"bits"`
	Signed      *bool    `yaml:"signed"`
	Min         *float64 `yaml:"min"`
	Max         *float64 `yaml:"max"`
	Len         *int     `yaml:"len"`
	Items       string   `yaml:"items"`
	Values      []string `yaml:"values"`
	Format      string   `yaml:"format"`
	Unit        string   `yaml:"unit"`
	Secret      bool     `yaml:"secret"`
	Note        string   `yaml:"note"`
}

// Required reports whether the field has to be present in every message.
func (f *Field) Required() bool { return f.Presence == "" || f.Presence == "required" }

// Fields maps field names to their description.
type Fields map[string]*Field

// Operation is one operation of an interface.
type Operation struct {
	Command         string           `yaml:"-"`
	Title           string           `yaml:"title"`
	Clause          string           `yaml:"clause"`
	Initiator       string           `yaml:"initiator"` // a role name, or "both"
	OptionalFeature bool             `yaml:"optional_feature"`
	Note            string           `yaml:"note"`
	ErrorConditions []ErrorCondition `yaml:"error_conditions"`
	Init            Fields           `yaml:"init"`
	Response        Fields           `yaml:"response"`
	Complete        Fields           `yaml:"complete"`
}

// ErrorCondition is a reason the document lists for an error answer to a message part.
type ErrorCondition struct {
	Part   string `yaml:"part"` // init, response or complete
	Code   string `yaml:"code"` // POSIX name, e.g. EOPNOTSUPP
	Reason string `yaml:"reason"`
}

// TypeDef is a named object type that fields can refer to.
type TypeDef struct {
	Clause string `yaml:"clause"`
	Fields Fields `yaml:"fields"`
}

// Errors describes the error and error acknowledge messages.
type Errors struct {
	Clause   string `yaml:"clause"`
	Note     string `yaml:"note"`
	Error    Fields `yaml:"error"`
	ErrorAck Fields `yaml:"errorAck"`
}

// Schema is one version of one interface.
type Schema struct {
	Interface  string                `yaml:"interface"`
	Version    string                `yaml:"version"`
	Identifier string                `yaml:"identifier"`
	Roles      map[string]string     `yaml:"roles"`
	Core       Fields                `yaml:"core"`
	Types      map[string]TypeDef    `yaml:"types"`
	Operations map[string]*Operation `yaml:"operations"`
	Errors     Errors                `yaml:"errors"`

	source string
}

// Part names the message of an operation.
type Part int

const (
	Init Part = iota
	Response
	Complete
	ErrorMsg
	ErrorAck
)

func (p Part) String() string {
	return [...]string{"init", "response", "complete", "error", "errorAck"}[p]
}

// Source is the file the schema was loaded from.
func (s *Schema) Source() string { return s.source }

// Key is "interface-version", e.g. "demo-1.0.0".
func (s *Schema) Key() string { return s.Interface + "-" + s.Version }

// Lookup finds the operation and the message part for a command.
func (s *Schema) Lookup(command string) (*Operation, Part, bool) {
	switch command {
	case "error":
		return nil, ErrorMsg, true
	case "errorAck":
		return nil, ErrorAck, true
	}
	if op, ok := s.Operations[command]; ok {
		return op, Init, true
	}
	for suffix, part := range map[string]Part{"Rsp": Response, "Cmp": Complete} {
		if base, ok := strings.CutSuffix(command, suffix); ok {
			if op, ok := s.Operations[base]; ok {
				return op, part, true
			}
		}
	}
	return nil, 0, false
}

// MessageFields returns the fields of a message part, without the core fields.
func (s *Schema) MessageFields(op *Operation, part Part) Fields {
	switch part {
	case Init:
		return op.Init
	case Response:
		return op.Response
	case Complete:
		return op.Complete
	case ErrorMsg:
		return s.Errors.Error
	default:
		return s.Errors.ErrorAck
	}
}

// Commands lists the commands of all operations, sorted.
func (s *Schema) Commands() []string {
	out := make([]string, 0, len(s.Operations))
	for c := range s.Operations {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

var baseTypes = map[string]bool{
	"string": true, "bool": true, "object": true, "integer": true, "number": true,
	"eui64": true, "bytes": true, "array": true, "enum": true, "any": true,
}

// Parse reads one schema file and checks it for consistency.
func Parse(data []byte, source string) (*Schema, error) {
	var s Schema
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	s.source = source
	for c, op := range s.Operations {
		op.Command = c
	}
	if err := s.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &s, nil
}

func (s *Schema) check() error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	if s.Interface == "" || s.Version == "" {
		add("interface and version are required")
	}
	if len(s.Identifier) != 8 {
		add("identifier %q is not 8 characters", s.Identifier)
	}
	roles := map[string]bool{"both": true}
	for _, r := range s.Roles {
		roles[r] = true
	}
	checkFields := func(where string, fs Fields) {
		for name, f := range fs {
			if f == nil {
				add("%s.%s: empty field description", where, name)
				continue
			}
			if !baseTypes[f.Type] {
				if _, ok := s.Types[f.Type]; !ok {
					add("%s.%s: unknown type %q", where, name, f.Type)
				}
			}
			switch f.Presence {
			case "", "required", "optional":
			case "conditional":
				if f.When == "" {
					add("%s.%s: conditional without when", where, name)
				}
			default:
				add("%s.%s: unknown presence %q", where, name, f.Presence)
			}
			if f.Type == "enum" && len(f.Values) == 0 {
				add("%s.%s: enum without values", where, name)
			}
			if f.Type == "array" && f.Items == "" {
				add("%s.%s: array without items", where, name)
			}
			if f.Items != "" && !baseTypes[f.Items] {
				if _, ok := s.Types[f.Items]; !ok {
					add("%s.%s: unknown item type %q", where, name, f.Items)
				}
			}
		}
	}
	checkFields("core", s.Core)
	for n, t := range s.Types {
		checkFields("types."+n, t.Fields)
	}
	for c, op := range s.Operations {
		if !roles[op.Initiator] {
			add("%s: unknown initiator %q", c, op.Initiator)
		}
		checkFields(c, op.Init)
		checkFields(c+"Rsp", op.Response)
		checkFields(c+"Cmp", op.Complete)
	}
	checkFields("error", s.Errors.Error)
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("invalid schema:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// Set holds the loaded schemas, keyed by interface and version.
type Set struct {
	byKey map[string]*Schema
}

// Load reads every *.yaml file in the root of each layer. A later layer replaces an
// interface version of an earlier one, so that a folder can override built-in files.
// Two files for the same version within one layer are an error.
func Load(layers ...fs.FS) (*Set, error) {
	set := &Set{byKey: map[string]*Schema{}}
	for _, fsys := range layers {
		names, err := fs.Glob(fsys, "*.yaml")
		if err != nil {
			return nil, err
		}
		seen := map[string]string{}
		for _, n := range names {
			data, err := fs.ReadFile(fsys, n)
			if err != nil {
				return nil, err
			}
			s, err := Parse(data, path.Base(n))
			if err != nil {
				return nil, err
			}
			if prev, dup := seen[s.Key()]; dup {
				return nil, fmt.Errorf("%s and %s both define %s", prev, s.source, s.Key())
			}
			seen[s.Key()] = s.source
			set.byKey[s.Key()] = s
		}
	}
	return set, nil
}

// Get returns the schema for an interface and version.
func (set *Set) Get(iface, version string) (*Schema, bool) {
	s, ok := set.byKey[iface+"-"+version]
	return s, ok
}

// Versions lists the loaded versions of an interface, sorted.
func (set *Set) Versions(iface string) []string {
	var out []string
	for _, s := range set.byKey {
		if s.Interface == iface {
			out = append(out, s.Version)
		}
	}
	sort.Strings(out)
	return out
}
