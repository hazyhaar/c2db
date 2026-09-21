// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/hex"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

// TestTenantMACGoldenVector fige la valeur du contexte données : toute
// évolution de DeriveTenantMAC romprait la lecture des données existantes.
func TestTenantMACGoldenVector(t *testing.T) {
	master := testMaster()
	ep := c2uuidv7.Compose(1_700_000_000_000_000_000, 1)

	const wantHex = "9fc3cc93eaf4f599b936e44716c8257648c9a26a7e3009ffbe9638d3960333a5"
	got := DeriveTenantMAC(master, 1, ep)
	if hex.EncodeToString(got[:]) != wantHex {
		t.Fatalf("vecteur données modifié: got %x want %s", got, wantHex)
	}
}

// TestSessionMACGoldenVector fige la valeur du contexte session : portée par
// c2client avant la factorisation, elle doit rester stable pour ne pas
// invalider les identités mTLS déjà émises.
func TestSessionMACGoldenVector(t *testing.T) {
	master := testMaster()
	ep := c2uuidv7.Compose(1_700_000_000_000_000_000, 1)

	const wantHex = "e956b8e087b850d6fb14886556afb72e46708d57087dd16e6733a7381cb5e875"
	got := DeriveSessionMAC(master, 1, ep)
	if hex.EncodeToString(got[:]) != wantHex {
		t.Fatalf("vecteur session modifié: got %x want %s", got, wantHex)
	}

	if DeriveSessionMAC(master, 1, ep) == DeriveTenantMAC(master, 1, ep) {
		t.Fatal("les contextes données et session doivent produire des graines distinctes")
	}
}
