package labca

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInitIssueAndMutualTLS(t *testing.T) {
	dir := t.TempDir()
	ca, created, err := Init(dir, "lab")
	if err != nil || !created {
		t.Fatalf("init: %v %v", created, err)
	}
	if _, created, _ := Init(dir, "lab"); created {
		t.Fatal("second init created a new CA")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(dir, CAKey)); st.Mode().Perm() != 0o600 {
			t.Fatalf("key mode %v", st.Mode().Perm())
		}
	}
	if _, err := ca.Issue(dir, Request{Name: "sc", Server: true, Hosts: []string{"127.0.0.1", "localhost"}}); err != nil {
		t.Fatal(err)
	}
	fp, err := ca.Issue(dir, Request{Name: "bs", EUI: "70-B3-D5-9C-D0-00-09-BB"})
	if err != nil || len(fp) != 64 {
		t.Fatalf("fingerprint %q %v", fp, err)
	}

	serverCert, _ := tls.LoadX509KeyPair(filepath.Join(dir, "sc.pem"), filepath.Join(dir, "sc.key"))
	clientCert, _ := tls.LoadX509KeyPair(filepath.Join(dir, "bs.pem"), filepath.Join(dir, "bs.key"))
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{serverCert}, ClientCAs: ca.Pool(),
		ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan *x509.Certificate, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		tc := c.(*tls.Conn)
		_ = tc.Handshake()
		st := tc.ConnectionState()
		if len(st.PeerCertificates) > 0 {
			got <- st.PeerCertificates[0]
		} else {
			got <- nil
		}
		_, _ = io.Copy(io.Discard, tc)
	}()
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		Certificates: []tls.Certificate{clientCert}, RootCAs: ca.Pool(), ServerName: "localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	peer := <-got
	if peer == nil {
		t.Fatal("no client certificate seen by the server")
	}
	if Fingerprint(peer) != fp {
		t.Fatal("fingerprint differs")
	}
	if len(peer.URIs) != 1 || peer.URIs[0].String() != "urn:simple-sc-ac:eui64:70-B3-D5-9C-D0-00-09-BB" {
		t.Fatalf("EUI SAN %v", peer.URIs)
	}

	// A certificate of another CA is refused.
	other, _, _ := Init(t.TempDir(), "other")
	odir := t.TempDir()
	_, _ = other.Issue(odir, Request{Name: "bad"})
	badCert, _ := tls.LoadX509KeyPair(filepath.Join(odir, "bad.pem"), filepath.Join(odir, "bad.key"))
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	bad, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		Certificates: []tls.Certificate{badCert}, RootCAs: ca.Pool(), ServerName: "localhost",
	})
	if err == nil {
		_, err = bad.Read(make([]byte, 1))
		bad.Close()
	}
	if err == nil {
		t.Fatal("certificate of another CA accepted")
	}
}
