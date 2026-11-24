package labca

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestRequestAndSign(t *testing.T) {
	ca, _, err := Init(t.TempDir(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	csr, err := CreateRequest(dir, "client", "02-00-00-00-00-00-00-AC")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ca.SignRequest(csr, "02-00-00-00-00-00-00-AD", 0); err == nil {
		t.Fatal("request for another EUI was signed")
	}
	certPEM, fp, err := ca.SignRequest(csr, "02-00-00-00-00-00-00-AC", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := FingerprintPEM(certPEM); got != fp {
		t.Fatal("fingerprint differs")
	}
	keyPEM, _ := os.ReadFile(filepath.Join(dir, "client.key"))
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("certificate does not match the key: %v", err)
	}
	if _, _, err := ca.SignRequest([]byte("junk"), "x", 0); err == nil {
		t.Fatal("junk accepted")
	}
}
