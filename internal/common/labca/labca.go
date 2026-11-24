// Package labca is a small certificate authority for labs and tests. The Service Center
// operator is the certificate authority for its base stations and Application Centers,
// and every peer is identified by the fingerprint of its certificate's public key.
// Keys are ECDSA P-256. Files are PEM, private keys are written with mode 0600.
package labca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File names inside the CA directory.
const (
	CACert = "ca.pem"
	CAKey  = "ca.key"
)

// CA is a loaded certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte
}

// Init creates a CA in dir unless one exists. It returns the CA and whether it was
// created now.
func Init(dir, name string) (*CA, bool, error) {
	if ca, err := Load(dir); err == nil {
		return ca, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, false, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: name, Organization: []string{"simple-sc-ac lab CA"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, false, err
	}
	if err := writePair(dir, "ca", der, key); err != nil {
		return nil, false, err
	}
	ca, err := Load(dir)
	return ca, true, err
}

// Load reads the CA from dir.
func Load(dir string) (*CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, CACert))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, CAKey))
	if err != nil {
		return nil, err
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, PEM: certPEM}, nil
}

// Request describes a certificate to issue.
type Request struct {
	Name   string   // common name and file name, e.g. "sc" or "bs-70-B3-D5-..."
	EUI    string   // EUI-64 text form, written as URI SAN urn:simple-sc-ac:eui64:<eui>
	Server bool     // server certificate (SC) or client certificate (base station, AC)
	Hosts  []string // DNS names or IP addresses of a server
	Days   int      // validity, default 730
}

// Issue creates a key and a certificate signed by the CA and writes <name>.pem and
// <name>.key into outDir. It returns the SHA-256 fingerprint of the public key.
func (ca *CA) Issue(outDir string, r Request) (string, error) {
	if r.Name == "" || strings.ContainsAny(r.Name, `/\ `) {
		return "", fmt.Errorf("labca: bad name %q", r.Name)
	}
	if r.Days == 0 {
		r.Days = 730
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: r.Name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, r.Days),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if r.Server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, h := range r.Hosts {
			if ip := net.ParseIP(h); ip != nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			} else {
				tmpl.DNSNames = append(tmpl.DNSNames, h)
			}
		}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	if r.EUI != "" {
		tmpl.URIs = []*url.URL{{Scheme: "urn", Opaque: "simple-sc-ac:eui64:" + r.EUI}}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return "", err
	}
	if err := writePair(outDir, r.Name, der, key); err != nil {
		return "", err
	}
	cert, _ := x509.ParseCertificate(der)
	return Fingerprint(cert), nil
}

// Fingerprint is the SHA-256 of the DER encoded public key, upper case hex. It is what a
// peer registry stores.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// Pool returns a certificate pool with the CA, for TLS configuration.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

func writePair(dir, name string, der []byte, key *ecdsa.PrivateKey) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func parseCert(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("labca: no certificate in PEM")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseKey(b []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "EC PRIVATE KEY" {
		return nil, errors.New("labca: no EC private key in PEM")
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
