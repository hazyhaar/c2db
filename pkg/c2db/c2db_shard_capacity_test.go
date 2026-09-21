// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"strings"
	"testing"
)

// TestShardCapacityContractFixedAtCreation vérifie le contrat de capacité du
// shard : la taille est figée à la création, la réouverture à une autre taille
// rend l'erreur exportée dédiée ErrShardCapacityMismatch avec un message
// explicite, et la réouverture sans option adopte la capacité du fichier.
func TestShardCapacityContractFixedAtCreation(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{0x2A, 0x2B, 0x2C}

	s := mustOpenShard(t, dir, key, 0, WithHeapPages(8192))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Taille différente explicitement demandée : erreur exportée dédiée.
	_, err := OpenShard(dir, key, 0, WithHeapPages(16384))
	if err == nil {
		t.Fatal("réouverture à taille différente acceptée")
	}
	if !errors.Is(err, ErrShardCapacityMismatch) {
		t.Fatalf("erreur non discriminante: %v (want %v)", err, ErrShardCapacityMismatch)
	}
	if errors.Is(err, ErrHeapFull) || errors.Is(err, ErrTreeFull) || errors.Is(err, ErrInvalidHeapPages) {
		t.Fatalf("ErrShardCapacityMismatch confondue avec une autre erreur: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"fixed at creation", "file=8192", "requested=16384"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message sans %q: %q", want, msg)
		}
	}
	if got := ErrShardCapacityMismatch.Error(); !strings.Contains(got, "reopen with the same heap pages") {
		t.Fatalf("message du sentinel incomplet: %q", got)
	}

	// Réouverture sans option : adoption de la capacité du fichier.
	adopted := mustOpenShard(t, dir, key, 0)
	defer adopted.Close()
	if adopted.HeapPages() != 8192 {
		t.Fatalf("capacité adoptée = %d want 8192", adopted.HeapPages())
	}
}
