package mcrypto

import (
	"crypto/aes"
	"crypto/subtle"
	"encoding/binary"
)

// AttachSignature computes the 4 byte signature of an over-the-air attach request: the
// AES-128 CMAC, keyed with the pre-shared network key of the end-point, over the 16 byte
// vector
//
//	EUI-64 (8) | 0xFF | 0x00 | attach counter (4) | 0xFFFF
//
// truncated to its 4 most significant bytes. EUI-64 and counter are most significant
// octet first. The octet order of the counter is still to be confirmed with a recorded
// attach of a real end-point.
func AttachSignature(networkKey []byte, epEUI uint64, attachCnt uint32) ([4]byte, error) {
	var out [4]byte
	v := make([]byte, 16)
	binary.BigEndian.PutUint64(v[0:8], epEUI)
	v[8], v[9] = 0xFF, 0x00
	binary.BigEndian.PutUint32(v[10:14], attachCnt)
	v[14], v[15] = 0xFF, 0xFF
	mac, err := CMAC(networkKey, v)
	if err != nil {
		return out, err
	}
	copy(out[:], mac[:4])
	return out, nil
}

// VerifyAttachSignature compares a received signature with the expected one in constant
// time.
func VerifyAttachSignature(networkKey []byte, epEUI uint64, attachCnt uint32, sign []byte) (bool, error) {
	want, err := AttachSignature(networkKey, epEUI, attachCnt)
	if err != nil {
		return false, err
	}
	return len(sign) == 4 && subtle.ConstantTimeCompare(want[:], sign) == 1, nil
}

// SessionKey derives the network session key of an over-the-air attachment: one AES-128
// block, encrypted with the pre-shared network key,
//
//	EUI-64 (8) | nonce (4) | signature (4)
//
// The result is the key for encryption and CMAC during that session (nwkSnKey).
func SessionKey(networkKey []byte, epEUI uint64, nonce, sign []byte) ([16]byte, error) {
	var out [16]byte
	if len(networkKey) != 16 {
		return out, ErrKeySize
	}
	if len(nonce) != 4 || len(sign) != 4 {
		return out, errBadLen
	}
	c, err := aes.NewCipher(networkKey)
	if err != nil {
		return out, err
	}
	seed := make([]byte, 16)
	binary.BigEndian.PutUint64(seed[0:8], epEUI)
	copy(seed[8:12], nonce)
	copy(seed[12:16], sign)
	c.Encrypt(out[:], seed)
	return out, nil
}

var errBadLen = errorString("mcrypto: nonce and signature must be 4 bytes")

type errorString string

func (e errorString) Error() string { return string(e) }
