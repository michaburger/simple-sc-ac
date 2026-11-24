package labca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CreateRequest creates a private key and a certificate request for a client with the
// given EUI (common name and URI SAN). It writes <name>.key (mode 0600) and <name>.csr
// into dir and returns the PEM of the request. The key never leaves this computer.
func CreateRequest(dir, name, eui string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: eui},
		URIs:    []*url.URL{{Scheme: "urn", Opaque: "simple-sc-ac:eui64:" + eui}},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(filepath.Join(dir, name+".key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return nil, err
	}
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	return csr, os.WriteFile(filepath.Join(dir, name+".csr"), csr, 0o644)
}

// SignRequest issues a client certificate for a certificate request. The EUI is taken
// from the caller, not from the request, and written into the certificate. It returns the
// certificate PEM and the fingerprint of its public key.
func (ca *CA) SignRequest(csrPEM []byte, eui string, days int) ([]byte, string, error) {
	blk, _ := pem.Decode(csrPEM)
	if blk == nil || blk.Type != "CERTIFICATE REQUEST" {
		return nil, "", errors.New("labca: no certificate request in PEM")
	}
	req, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return nil, "", err
	}
	if err := req.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("labca: request signature: %w", err)
	}
	if cn := strings.TrimSpace(req.Subject.CommonName); cn != "" && cn != eui {
		return nil, "", fmt.Errorf("labca: request is for %s, not %s", cn, eui)
	}
	if days == 0 {
		days = 730
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: eui},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, days),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{{Scheme: "urn", Opaque: "simple-sc-ac:eui64:" + eui}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, req.PublicKey, ca.Key)
	if err != nil {
		return nil, "", err
	}
	cert, _ := x509.ParseCertificate(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), Fingerprint(cert), nil
}

// FingerprintPEM returns the fingerprint of the first certificate in PEM.
func FingerprintPEM(certPEM []byte) (string, error) {
	cert, err := parseCert(certPEM)
	if err != nil {
		return "", err
	}
	return Fingerprint(cert), nil
}

// NewClient creates a key and a signed client certificate in one step, for the lab
// shortcut where the CA side also creates the key. It returns certificate, key and the
// fingerprint.
func (ca *CA) NewClient(eui string, days int) ([]byte, []byte, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: eui}}, key)
	if err != nil {
		return nil, nil, "", err
	}
	certPEM, fp, err := ca.SignRequest(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), eui, days)
	if err != nil {
		return nil, nil, "", err
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	return certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), fp, nil
}
