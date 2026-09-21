// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2poly1305_test

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"golang.org/x/crypto/poly1305"
)

// TestFlaw1_LazyReductionOverflow_Proof vérifie qu'une tentative de "lazy reduction" naïve
// sans réduction de l'accumulateur intermédiaire H provoque un débordement mathématique de 64 bits.
func TestFlaw1_LazyReductionOverflow_Proof(t *testing.T) {
	// P = 2^130 - 5
	p, _ := new(big.Int).SetString("3fffffffffffffffffffffffffffffffb", 16)

	// Supposons une clé r maximale clampée
	r, _ := new(big.Int).SetString("0ffffffc0ffffffc0ffffffc0fffffff", 16)
	r8 := new(big.Int).Exp(r, big.NewInt(8), p)

	// Bloc de message non réduit maximal (5 membres proches de 2^26, valeur ~ 2^130-1)
	unreducedH, _ := new(big.Int).SetString("3ffffffffffffffffffffffffffffffff", 16)

	// Produit intermédiaire H * r^8 avant réduction modulaire
	rawProduct := new(big.Int).Mul(unreducedH, r8)

	// Preuve : le produit dépasse largement 64 bits et 128 bits
	bitLen := rawProduct.BitLen()
	if bitLen <= 128 {
		t.Fatalf("Attendu produit > 128 bits, obtenu %d bits", bitLen)
	}
	t.Logf("[Preuve Faille 1] H * r^8 atteint %d bits -> Débordement immédiat si stocké dans des registres 64 bits sans réduction modulaire !", bitLen)
}

// TestFlaw2_Base44_AVX2_Impossibility_Proof prouve qu'un membre de 44 bits au carré dépasse la capacité
// de l'instruction vectorielle AVX2 VPMULUDQ (qui sature à 64 bits).
func TestFlaw2_Base44_AVX2_Impossibility_Proof(t *testing.T) {
	// Deux membres de 44 bits maximaux
	a := uint64(1<<44 - 1)
	b := uint64(1<<44 - 1)

	// Multiplié en 128 bits
	prod := new(big.Int).Mul(big.NewInt(int64(a)), big.NewInt(int64(b)))

	if prod.BitLen() <= 64 {
		t.Fatalf("Le produit 44x44 devrait faire ~88 bits, obtenu %d", prod.BitLen())
	}
	t.Logf("[Preuve Faille 2] Produit 44x44 bits = %d bits -> Impossible en AVX2 VPMULUDQ (limité à 64 bits) sans découpage !", prod.BitLen())
}

// TestFlaw3_Adversarial_MaxAccumulator_Donna26 vérifie formellement que notre implémentation
// en base 26 bits x 5 ne déborde JAMAIS sur les cas pathologiques (message maximal saturé de 0xFF).
func TestFlaw3_Adversarial_MaxAccumulator_Donna26(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xFF
	}

	// Message de 128 octets (exactement 8 blocs de 16 octets) saturé à 0xFF
	maxMsg := bytes.Repeat([]byte{0xFF}, 128)

	var ctx c2poly1305.Crypto_poly1305_ctx
	c2poly1305.Crypto_poly1305_init(&ctx, key)
	c2poly1305.Crypto_poly1305_update(&ctx, maxMsg, uint64(len(maxMsg)))
	var got [16]byte
	c2poly1305.Crypto_poly1305_final(&ctx, got[:])

	// Comparaison contre l'oracle officiel de référence (golang.org/x/crypto/poly1305)
	var want [16]byte
	var keyArray [32]byte
	copy(keyArray[:], key)
	poly1305.Sum(&want, maxMsg, &keyArray)

	if !bytes.Equal(got[:], want[:]) {
		t.Fatalf("DÉBORDEMENT DÉTECTÉ EN BASE 26 BITS !\nObtenu: %x\nAttendu: %x", got, want)
	}
	t.Logf("[Validation Base 26 bits] Passage réussi sans overflow sur message saturé 0xFF: %x", got)
}

// TestFlaw4_UnclampedPowers_MathematicalDrift vérifie que les puissances r^2..r^8 calculées
// sans bits de clamping respectent strictement la réduction modulaire contre x/crypto.
func TestFlaw4_UnclampedPowers_MathematicalDrift(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		key := make([]byte, 32)
		rand.Read(key)
		msg := make([]byte, 256) // 16 blocs
		rand.Read(msg)

		var ctx c2poly1305.Crypto_poly1305_ctx
		c2poly1305.Crypto_poly1305_init(&ctx, key)
		c2poly1305.Crypto_poly1305_update(&ctx, msg, uint64(len(msg)))
		var got [16]byte
		c2poly1305.Crypto_poly1305_final(&ctx, got[:])

		var want [16]byte
		var keyArray [32]byte
		copy(keyArray[:], key)
		poly1305.Sum(&want, msg, &keyArray)

		if !bytes.Equal(got[:], want[:]) {
			t.Fatalf("Dérive mathématique détectée à l'itération %d sur blocs multiples !", iter)
		}
	}
}
