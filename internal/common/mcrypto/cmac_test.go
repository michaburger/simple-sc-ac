package mcrypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Test vectors from RFC 4493, section 4.
func TestCMACRFC4493(t *testing.T) {
	key := "2b7e151628aed2a6abf7158809cf4f3c"
	msg := "6bc1bee22e409f96e93d7e117393172a" +
		"ae2d8a571e03ac9c9eb76fac45af8e51" +
		"30c81c46a35ce411e5fbc1191a0a52ef" +
		"f69f2445df4f9b17ad2b417be66c3710"
	cases := []struct {
		name string
		n    int // message length in bytes
		tag  string
	}{
		{"empty", 0, "bb1d6929e95937287fa37d129b756746"},
		{"16 bytes", 16, "070a16b46b4d4144f79bdd9dd04a287c"},
		{"40 bytes", 40, "dfa66747de9ae63030ca32611497c827"},
		{"64 bytes", 64, "51f0bebf7e3b9d92fc49741779363cfe"},
	}
	k := mustHex(t, key)
	m := mustHex(t, msg)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CMAC(k, m[:c.n])
			if err != nil {
				t.Fatal(err)
			}
			if want := mustHex(t, c.tag); !bytes.Equal(got, want) {
				t.Fatalf("CMAC = %x, want %x", got, want)
			}
		})
	}
}

// Subkeys from RFC 4493, section 4, "Subkey Generation".
func TestSubkeysRFC4493(t *testing.T) {
	k := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	c, err := newCipherForTest(k)
	if err != nil {
		t.Fatal(err)
	}
	k1, k2 := subkeys(c)
	if want := mustHex(t, "fbeed618357133667c85e08f7236a8de"); !bytes.Equal(k1, want) {
		t.Fatalf("K1 = %x, want %x", k1, want)
	}
	if want := mustHex(t, "f7ddac306ae266ccf90bc11ee46d513b"); !bytes.Equal(k2, want) {
		t.Fatalf("K2 = %x, want %x", k2, want)
	}
}

func TestVerifyCMAC(t *testing.T) {
	k := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	m := mustHex(t, "6bc1bee22e409f96e93d7e117393172a")
	full := mustHex(t, "070a16b46b4d4144f79bdd9dd04a287c")
	for _, n := range []int{4, 8, 16} {
		ok, err := VerifyCMAC(k, m, full[:n])
		if err != nil || !ok {
			t.Fatalf("truncated tag of %d bytes did not verify (err %v)", n, err)
		}
	}
	bad := append([]byte(nil), full[:4]...)
	bad[0] ^= 1
	if ok, _ := VerifyCMAC(k, m, bad); ok {
		t.Fatal("modified tag verified")
	}
	if ok, _ := VerifyCMAC(k, m, nil); ok {
		t.Fatal("empty tag verified")
	}
}

func TestCMACKeySize(t *testing.T) {
	if _, err := CMAC(make([]byte, 15), nil); err != ErrKeySize {
		t.Fatalf("err = %v, want ErrKeySize", err)
	}
}
