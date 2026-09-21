package c2db

import (
	"bytes"
	"errors"
	"testing"
)

// TestEngineRetentionKeepAllPreservesOldVersions prouve que le contrat
// explicite RetentionKeepAll fait survivre les versions anciennes à une
// compaction : la réécriture d'une clé n'efface pas l'instantané antérieur.
// Le défaut du moteur étant désormais RetentionPruneSuperseded, ce régime est
// sollicité par l'option WithHistoryRetention.
func TestEngineRetentionKeepAllPreservesOldVersions(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 131)
	}
	s := mustOpenShard(t, dir, key, 11, WithHistoryRetention(RetentionKeepAll))
	defer func() { _ = s.Close() }()

	if s.retention != RetentionKeepAll {
		t.Fatalf("rétention = %d, attendu RetentionKeepAll", s.retention)
	}

	k := []byte("retention-keep-all")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	id2 := s.lastID

	ok, err := s.tryAutoCompact()
	if err != nil {
		t.Fatalf("tryAutoCompact: %v", err)
	}
	if !ok {
		t.Fatal("tryAutoCompact n'a pas compacté")
	}

	got, err := s.GetAsOf(k, id1)
	if err != nil || !bytes.Equal(got, []byte("v1")) {
		t.Fatalf("version ancienne v1 perdue après compaction: err=%v val=%q", err, got)
	}
	got, err = s.GetAsOf(k, id2)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("version active v2 altérée après compaction: err=%v val=%q", err, got)
	}
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("Get après compaction: err=%v val=%q", err, got)
	}
}

// TestEngineRetentionPruneSupersededDropsOldVersions fige le contrat opposable
// du défaut : sous RetentionPruneSuperseded, la compaction aplatit l'historique
// et la version supersédée n'est plus adressable. C'est le régime historique du
// moteur, rétabli comme défaut, et le seul où l'aplatissement destructeur reste
// licite.
func TestEngineRetentionPruneSupersededDropsOldVersions(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 132)
	}
	s := mustOpenShard(t, dir, key, 11)
	defer func() { _ = s.Close() }()

	if s.retention != RetentionPruneSuperseded {
		t.Fatalf("rétention par défaut = %d, attendu RetentionPruneSuperseded", s.retention)
	}

	k := []byte("retention-prune")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	id2 := s.lastID

	ok, err := s.tryAutoCompact()
	if err != nil {
		t.Fatalf("tryAutoCompact: %v", err)
	}
	if !ok {
		t.Fatal("tryAutoCompact n'a pas compacté")
	}

	if _, err := s.GetAsOf(k, id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("version ancienne attendue absente après aplatissement, err=%v", err)
	}
	got, err := s.GetAsOf(k, id2)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("version active v2 attendue après aplatissement: err=%v val=%q", err, got)
	}
}

// TestEngineRetentionPruneSupersededOnlineCollapse prouve que la rétention
// RetentionPruneSuperseded est effective EN RÉGIME NOMINAL, sans attendre une
// compaction : N réécritures de la même clé avec la même valeur ne conservent
// qu'une seule cellule. Le banc est rouge avant le correctif (N cellules
// empilées) et vert après (remplacement en place de la cellule).
func TestEngineRetentionPruneSupersededOnlineCollapse(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(133), 11)
	defer func() { _ = s.Close() }()

	if s.retention != RetentionPruneSuperseded {
		t.Fatalf("rétention par défaut = %d, attendu RetentionPruneSuperseded", s.retention)
	}

	k := []byte("retention-online")
	v := []byte("valeur-stable")
	const n = 64
	for i := 0; i < n; i++ {
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	if got := s.countAllVersionsLocked(); got != 1 {
		t.Fatalf("cellules=%d, attendu 1 (rétention en ligne) après %d réécritures", got, n)
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get après réécritures: err=%v val=%q", err, got)
	}
	if got, err := s.GetAsOf(k, s.lastID); err != nil || !bytes.Equal(got, v) {
		t.Fatalf("GetAsOf(dernier id) après réécritures: err=%v val=%q", err, got)
	}
}

// TestEngineRetentionPruneSupersededDistinctValuesStack fixe la limite de la
// rétention en ligne : seules les réécritures à valeur IDENTIQUE remplacent la
// cellule. Une valeur réellement modifiée empile une version, afin que
// l'historique MVCC (GetAsOf sur instantané antérieur) reste adressable tant
// qu'aucune compaction ne l'a aplati.
func TestEngineRetentionPruneSupersededDistinctValuesStack(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(134), 11)
	defer func() { _ = s.Close() }()

	k := []byte("retention-change")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	if got := s.countAllVersionsLocked(); got != 2 {
		t.Fatalf("cellules=%d, attendu 2 (valeurs distinctes empilées)", got)
	}
	if got, err := s.GetAsOf(k, id1); err != nil || !bytes.Equal(got, []byte("v1")) {
		t.Fatalf("GetAsOf(v1) sous PruneSuperseded doit rester adressable avant compaction: err=%v val=%q", err, got)
	}
}

// TestEngineRetentionKeepAllOnlineRewriteStacks prouve que le régime explicite
// RetentionKeepAll n'est pas altéré par la rétention en ligne : la réécriture
// d'une valeur identique continue d'empiler une version.
func TestEngineRetentionKeepAllOnlineRewriteStacks(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(135), 11, WithHistoryRetention(RetentionKeepAll))
	defer func() { _ = s.Close() }()

	k := []byte("retention-keepall-online")
	v := []byte("identique")
	const n = 16
	for i := 0; i < n; i++ {
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if got := s.countAllVersionsLocked(); got != n {
		t.Fatalf("cellules=%d, attendu %d (KeepAll conserve toutes les versions)", got, n)
	}
}
