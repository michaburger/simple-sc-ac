// Package onboard is the enrollment bundle that connects an Application Center to a
// Service Center: what the AC needs to reach the SC and to prove who it is.
package onboard

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
)

// Bundle is written by the SC (`simple-sc ac add`) and imported by the AC
// (`simple-ac sc add`).
type Bundle struct {
	Name       string `json:"name"`             // name of the Service Center on the AC
	Address    string `json:"address"`          // host:port of SCACI
	ServerName string `json:"serverName"`       // name in the server certificate, default the host of Address
	SCEui      string `json:"scEui"`            // pinned, the AC refuses another one
	ACEui      string `json:"acEui"`            // the EUI the certificate was issued for
	CA         string `json:"caPem"`            // CA of the SC, trust anchor for its server certificate
	Cert       string `json:"certPem"`          // client certificate of the AC
	Key        string `json:"keyPem,omitempty"` // only in the lab shortcut: the SC has seen this key
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

// CheckName validates the name of a Service Center on the AC (it is a folder name).
func CheckName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("onboard: name %q: letters, digits, '-', '_', '.' only", name)
	}
	return nil
}

// Check validates the fields.
func (b Bundle) Check() error {
	if err := CheckName(b.Name); err != nil {
		return err
	}
	if b.Address == "" {
		return errors.New("onboard: address missing")
	}
	if _, err := idt.ParseEUI64(b.SCEui); err != nil {
		return err
	}
	if _, err := idt.ParseEUI64(b.ACEui); err != nil {
		return err
	}
	if b.CA == "" || b.Cert == "" {
		return errors.New("onboard: CA or certificate missing")
	}
	return nil
}

// Write stores the bundle as JSON, mode 0600 because it may contain a key.
func (b Bundle) Write(file string) error {
	if err := b.Check(); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(b, "", "  ")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o600)
}

// Read loads and checks a bundle.
func Read(file string) (Bundle, error) {
	var b Bundle
	data, err := os.ReadFile(file)
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("onboard: %s: %w", file, err)
	}
	return b, b.Check()
}

// ClientCertificate pairs the certificate with a key in PEM and checks that they match.
func (b Bundle) ClientCertificate(keyPEM []byte) (tls.Certificate, error) {
	return tls.X509KeyPair([]byte(b.Cert), keyPEM)
}
