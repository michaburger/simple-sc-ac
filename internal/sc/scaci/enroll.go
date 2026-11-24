package scaci

import (
	"fmt"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
	"github.com/michaburger/simple-sc-ac/internal/common/labca"
	"github.com/michaburger/simple-sc-ac/internal/common/onboard"
	"github.com/michaburger/simple-sc-ac/internal/sc/registry"
)

// EnrollRequest describes an Application Center to add.
type EnrollRequest struct {
	Name       string // name of this SC as the AC will show it
	ACEui      string
	ACName     string // name of the AC in the registry of the SC
	Address    string // host:port of SCACI as the AC reaches it
	ServerName string // name in the server certificate, optional
	CSR        []byte // certificate request of the AC; empty: lab shortcut, the key is created here
}

// Enroll registers an AC with the fingerprint of its new certificate and returns the
// bundle for it. Without a request it creates the key as well and puts it into the
// bundle: convenient in a lab, but this SC has then seen the private key of the AC.
func Enroll(reg *registry.Registry, ca *labca.CA, scEui idt.EUI64, r EnrollRequest) (onboard.Bundle, error) {
	if _, err := idt.ParseEUI64(r.ACEui); err != nil {
		return onboard.Bundle{}, err
	}
	var certPEM, keyPEM []byte
	var fp string
	var err error
	if len(r.CSR) > 0 {
		certPEM, fp, err = ca.SignRequest(r.CSR, r.ACEui, 0)
	} else {
		certPEM, keyPEM, fp, err = ca.NewClient(r.ACEui, 0)
	}
	if err != nil {
		return onboard.Bundle{}, err
	}
	b := onboard.Bundle{Name: r.Name, Address: r.Address, ServerName: r.ServerName, SCEui: scEui.String(), ACEui: r.ACEui,
		CA: string(ca.PEM), Cert: string(certPEM), Key: string(keyPEM)}
	if err := b.Check(); err != nil {
		return b, err
	}
	if err := reg.AddApplicationCenter(r.ACEui, r.ACName, fp); err != nil {
		return b, fmt.Errorf("registry: %w", err)
	}
	return b, nil
}
