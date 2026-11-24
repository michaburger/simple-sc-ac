package mcrypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Vector computed independently with the Python "cryptography" package (CMAC and
// AES-ECB) from a generated key, not from a real device. It checks the byte layout of
// this implementation, not the layout itself, which is confirmed with a recorded attach.
const (
	vecKey   = "000102030405060708090A0B0C0D0E0F"
	vecEUI   = uint64(0x0200000000000001)
	vecCnt   = uint32(5)
	vecNonce = "A1B2C3D4"
	vecSign  = "9657DCC5"
	vecSess  = "3200F6DF3D5CFC4ABFA13E0287BD1CC5"
)

func TestAttachSignatureVector(t *testing.T) {
	key := mustHex(t, vecKey)
	sign, err := AttachSignature(key, vecEUI, vecCnt)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToUpper(hex.EncodeToString(sign[:])); got != vecSign {
		t.Fatalf("signature %s, want %s", got, vecSign)
	}
	ok, _ := VerifyAttachSignature(key, vecEUI, vecCnt, sign[:])
	if !ok {
		t.Fatal("own signature does not verify")
	}
	for _, c := range []struct {
		eui uint64
		cnt uint32
	}{{vecEUI + 1, vecCnt}, {vecEUI, vecCnt + 1}} {
		if ok, _ := VerifyAttachSignature(key, c.eui, c.cnt, sign[:]); ok {
			t.Fatalf("signature verified for eui %x cnt %d", c.eui, c.cnt)
		}
	}
	if ok, _ := VerifyAttachSignature(key, vecEUI, vecCnt, sign[:3]); ok {
		t.Fatal("short signature verified")
	}
}

func TestSessionKeyVector(t *testing.T) {
	sk, err := SessionKey(mustHex(t, vecKey), vecEUI, mustHex(t, vecNonce), mustHex(t, vecSign))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToUpper(hex.EncodeToString(sk[:])); got != vecSess {
		t.Fatalf("session key %s, want %s", got, vecSess)
	}
	if _, err := SessionKey(mustHex(t, vecKey), vecEUI, []byte{1}, mustHex(t, vecSign)); err == nil {
		t.Fatal("short nonce accepted")
	}
}
