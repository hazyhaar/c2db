// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/rand"
	"crypto/subtle"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"golang.org/x/crypto/chacha20"
)

const (
	encPadOff        = 104
	EncNoncePadSize  = 8
	EncBodyTagSize   = 16
	encIETFNonceSize = 12
)

func headerEncrypted(hdr []byte) bool {
	pad := hdr[encPadOff : encPadOff+EncNoncePadSize]
	for i := 0; i < len(pad); i++ {
		if pad[i] != 0 {
			return true
		}
	}
	return false
}

func fillEncNonce(pad []byte) error {
	for {
		if _, err := rand.Read(pad); err != nil {
			return err
		}
		for i := 0; i < len(pad); i++ {
			if pad[i] != 0 {
				return nil
			}
		}
	}
}

func ietfNonce(pad []byte) [encIETFNonceSize]byte {
	var n [encIETFNonceSize]byte
	copy(n[:], pad[:EncNoncePadSize])
	return n
}

func xorChaCha(dst, src, encKey, nonce12 []byte) error {
	c, err := newChaCha(encKey, nonce12)
	if err != nil {
		return err
	}
	c.XORKeyStream(dst, src)
	return nil
}

func newChaCha(encKey, nonce12 []byte) (*chacha20.Cipher, error) {
	return chacha20.NewUnauthenticatedCipher(encKey, nonce12)
}

func sealBodyMAC(tag, ciphertext []byte, key [32]byte) {
	c2poly1305.Crypto_poly1305(tag, ciphertext, uint64(len(ciphertext)), key[:])
}

func verifyBodyMAC(rest []byte, key [32]byte) (ct []byte, ok bool) {
	if uint64(len(rest)) < uint64(EncBodyTagSize) {
		return nil, false
	}
	ct = rest[:len(rest)-EncBodyTagSize]
	tag := rest[len(rest)-EncBodyTagSize:]
	var got [EncBodyTagSize]byte
	c2poly1305.Crypto_poly1305(got[:], ct, uint64(len(ct)), key[:])
	if subtle.ConstantTimeCompare(got[:], tag) != 1 {
		return nil, false
	}
	return ct, true
}
