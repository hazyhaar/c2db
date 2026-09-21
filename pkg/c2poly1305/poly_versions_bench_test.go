//go:build ignore

// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2poly1305_test

import (
	"crypto/rand"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"github.com/hazyhaar/c2db/pkg/c2poly1305x2"
	"github.com/hazyhaar/c2db/pkg/c2poly1305x8"
	"golang.org/x/crypto/poly1305"
)

// BenchmarkPoly1305_Generations_64KiB compare les 4 générations d'implémentation de Poly1305 sur un chunk de 64 KiB :
// 1. Go Standard / x/crypto (scalaire 1-voie)
// 2. c2poly1305 (transpilé sgoiter 1-voie)
// 3. c2poly1305x2 (transpilé sgoiter 2-voies)
// 4. c2poly1305x8 (transpilé sgoiter 8-voies superposées)
func BenchmarkPoly1305_Generations_64KiB(b *testing.B) {
	key := make([]byte, 32)
	rand.Read(key)
	var keyArray [32]byte
	copy(keyArray[:], key)

	payload := make([]byte, 64*1024)
	rand.Read(payload)

	b.Run("1_x_crypto_official_64KiB", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		var tag [16]byte
		for i := 0; i < b.N; i++ {
			poly1305.Sum(&tag, payload, &keyArray)
		}
	})

	b.Run("2_c2poly1305_1way_64KiB", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		var ctx c2poly1305.Crypto_poly1305_ctx
		var tag [16]byte
		for i := 0; i < b.N; i++ {
			c2poly1305.Crypto_poly1305_init(&ctx, key)
			c2poly1305.Crypto_poly1305_update(&ctx, payload, uint64(len(payload)))
			c2poly1305.Crypto_poly1305_final(&ctx, tag[:])
		}
	})

	b.Run("3_c2poly1305x2_2way_64KiB", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		var ctx c2poly1305x2.Crypto_poly1305x2_ctx
		var tag [16]byte
		for i := 0; i < b.N; i++ {
			c2poly1305x2.Crypto_poly1305x2_init(&ctx, key)
			c2poly1305x2.Crypto_poly1305x2_update(&ctx, payload, uint64(len(payload)))
			c2poly1305x2.Crypto_poly1305x2_final(&ctx, tag[:])
		}
	})

	b.Run("4_c2poly1305x8_8way_superposed_64KiB", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		var ctx c2poly1305x8.Crypto_poly1305x8_ctx
		var tag [16]byte
		for i := 0; i < b.N; i++ {
			c2poly1305x8.Crypto_poly1305x8_init(&ctx, key)
			c2poly1305x8.Crypto_poly1305x8_update(&ctx, payload, uint64(len(payload)))
			c2poly1305x8.Crypto_poly1305x8_final(&ctx, tag[:])
		}
	})
}
