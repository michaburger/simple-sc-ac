package mcrypto

import (
	"crypto/aes"
	"crypto/cipher"
)

func newCipherForTest(key []byte) (cipher.Block, error) { return aes.NewCipher(key) }
