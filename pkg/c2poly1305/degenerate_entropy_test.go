// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2poly1305

import (
	"bytes"
	"testing"
	"golang.org/x/crypto/poly1305"
)

func genRamp(n int, start byte) []byte {
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = start + byte(i)
	}
	return b
}

func genRepeat(n int, pattern []byte) []byte {
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = pattern[i%len(pattern)]
	}
	return b
}

func TestPoly1305DegenerateEntropy(t *testing.T) {
	patterns := [][]byte{
		genRepeat(32, []byte{0x00}),
		genRepeat(32, []byte{0xFF}),
		genRepeat(32, []byte{0xAA}),
		genRepeat(32, []byte{0x55}),
		genRepeat(32, []byte{0x00, 0xFF}),
		genRepeat(32, []byte{0xAA, 0x55}),
		genRamp(32, 0),
		genRamp(32, 128),
		// r = 0 (clamped). Top 16 bytes are s (can be anything). Bottom 16 bytes are r.
		append(genRepeat(16, []byte{0x00}), genRepeat(16, []byte{0x42})...),
		append(genRepeat(16, []byte{0xFF}), genRepeat(16, []byte{0x42})...),
	}

	lengths := []int{0, 1, 16, 17, 31, 32, 33, 64, 128, 255, 1024, 4096}

	for _, pKey := range patterns {
		key := pKey[:32]
		var k [32]byte
		copy(k[:], key)

		for _, n := range lengths {
			for _, pMsg := range patterns {
				msg := make([]byte, n)
				for i := 0; i < n; i++ {
					msg[i] = pMsg[i%len(pMsg)]
				}

				var want [16]byte
				poly1305.Sum(&want, msg, &k)

				var got [16]byte
				Crypto_poly1305(got[:], msg, uint64(len(msg)), key)

				if !bytes.Equal(got[:], want[:]) {
					t.Fatalf("Poly1305 diverge on degenerate inputs (n=%d)", n)
				}
			}
		}
	}
}
