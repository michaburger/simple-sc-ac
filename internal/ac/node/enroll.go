package node

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/common/onboard"
)

// Request creates the key and the certificate request for a Service Center called name.
// The key stays in <data>/sc/<name>/client.key, the request goes to the SC operator.
func Request(dataDir, name string, acEui idt.EUI64) (string, error) {
	if err := onboard.CheckName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "sc", name)
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); err == nil {
		return "", fmt.Errorf("%s already has a key, remove %s to start again", name, dir)
	}
	if _, err := labca.CreateRequest(dir, "client", acEui.String()); err != nil {
		return "", err
	}
	return filepath.Join(dir, "client.csr"), nil
}

// Import checks a bundle and stores it under <data>/sc/<name>. The certificate has to be
// for this AC and has to match the key: either the key from "simple-ac sc request", or
// the key inside the bundle (lab shortcut).
func Import(dataDir, file string, acEui idt.EUI64) (onboard.Bundle, error) {
	b, err := onboard.Read(file)
	if err != nil {
		return b, err
	}
	if b.ACEui != acEui.String() {
		return b, fmt.Errorf("the bundle is for %s, this Application Center is %s", b.ACEui, acEui)
	}
	dir := filepath.Join(dataDir, "sc", b.Name)
	keyFile := filepath.Join(dir, KeyFile)
	key := []byte(b.Key)
	if len(key) == 0 {
		if key, err = os.ReadFile(keyFile); err != nil {
			return b, fmt.Errorf("no key in the bundle and none from 'simple-ac sc request %s': %w", b.Name, err)
		}
	}
	if _, err := b.ClientCertificate(key); err != nil {
		return b, fmt.Errorf("certificate and key do not match: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return b, err
	}
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		return b, err
	}
	b.Key = "" // the key is kept in client.key only
	return b, b.Write(filepath.Join(dir, BundleFile))
}
