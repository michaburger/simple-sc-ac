// Package mcrypto holds the cryptographic helpers of simple-sc-ac.
package mcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"errors"
)

// ErrKeySize is returned when a key is not 16 bytes long.
var ErrKeySize = errors.New("mcrypto: AES-128 key must be 16 bytes")

const blockSize = aes.BlockSize

// CMAC computes the AES-128 CMAC of msg as defined in RFC 4493 and returns the full
// 16 byte tag. Callers that need a shorter tag take its leading bytes.
func CMAC(key, msg []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, ErrKeySize
	}
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	k1, k2 := subkeys(c)

	n := (len(msg) + blockSize - 1) / blockSize
	complete := n > 0 && len(msg)%blockSize == 0
	if n == 0 {
		n = 1
	}

	last := make([]byte, blockSize)
	tail := msg[(n-1)*blockSize:]
	if complete {
		subtle.XORBytes(last, tail, k1)
	} else {
		copy(last, tail)
		last[len(tail)] = 0x80
		subtle.XORBytes(last, last, k2)
	}

	x := make([]byte, blockSize)
	for i := 0; i < n-1; i++ {
		subtle.XORBytes(x, x, msg[i*blockSize:(i+1)*blockSize])
		c.Encrypt(x, x)
	}
	subtle.XORBytes(x, x, last)
	c.Encrypt(x, x)
	return x, nil
}

// VerifyCMAC reports whether tag equals the leading len(tag) bytes of the CMAC of msg,
// in constant time. An empty tag never verifies.
func VerifyCMAC(key, msg, tag []byte) (bool, error) {
	if len(tag) == 0 || len(tag) > blockSize {
		return false, nil
	}
	full, err := CMAC(key, msg)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(full[:len(tag)], tag) == 1, nil
}

// subkeys derives K1 and K2 (RFC 4493, section 2.3).
func subkeys(c cipher.Block) (k1, k2 []byte) {
	l := make([]byte, blockSize)
	c.Encrypt(l, l)
	k1 = shiftLeft(l)
	k2 = shiftLeft(k1)
	return k1, k2
}

// shiftLeft returns b shifted left by one bit, with the conditional XOR of Rb (0x87).
func shiftLeft(b []byte) []byte {
	out := make([]byte, len(b))
	var carry byte
	for i := len(b) - 1; i >= 0; i-- {
		out[i] = b[i]<<1 | carry
		carry = b[i] >> 7
	}
	if b[0]&0x80 != 0 {
		out[len(out)-1] ^= 0x87
	}
	return out
}
