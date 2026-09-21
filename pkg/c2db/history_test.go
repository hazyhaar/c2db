// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestEngineRetentionArchiveSupersededColdAsOf prouve le contrat
// RetentionArchiveSuperseded : une compaction déporte les versions supplantées
// vers le segment d'historique avant d'aplatir le tas chaud. La lecture as-of
// d'une version déportée est servie par le froid, la place chaude diminue, et
// chaque déport consigne un événement Archive relu depuis le txnlog.
func TestEngineRetentionArchiveSupersededColdAsOf(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "hist")
	txnDir := filepath.Join(dir, "events")
	s := mustOpenShard(t, dir, txnTestKey(191), 11,
		WithHistoryRetention(RetentionArchiveSuperseded),
		WithHistoryLog(histDir), WithTxLog(txnDir))

	if s.retention != RetentionArchiveSuperseded {
		t.Fatalf("rétention = %d, attendu ArchiveSuperseded", s.retention)
	}

	k := []byte("archive-cold")
	mkVal := func(fill byte) []byte {
		v := make([]byte, 3000)
		for i := range v {
			v[i] = fill
		}
		return v
	}
	v1, v2, v3 := mkVal(0x11), mkVal(0x22), mkVal(0x33)
	if err := s.Put(k, v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	id2 := s.lastID
	if err := s.Put(k, v3); err != nil {
		t.Fatalf("Put v3: %v", err)
	}
	id3 := s.lastID

	if got, err := s.GetAsOf(k, id1); err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("précondition GetAsOf(id1): err=%v", err)
	}

	usedBefore := s.heapUsed
	ok, err := s.tryAutoCompact()
	if err != nil {
		t.Fatalf("tryAutoCompact: %v", err)
	}
	if !ok {
		t.Fatal("tryAutoCompact n'a pas compacté")
	}
	usedAfter := s.heapUsed
	if usedAfter >= usedBefore {
		t.Fatalf("place chaude non diminuée: avant=%d après=%d", usedBefore, usedAfter)
	}

	if _, hotErr := getAsOfHeap(s.pub, s.heapRoot, k, id1); !errors.Is(hotErr, ErrNotFound) {
		t.Fatalf("le tas chaud sert encore id1: err=%v", hotErr)
	}
	if n := s.countAllVersionsLocked(); n != 1 {
		t.Fatalf("versions chaudes=%d, attendu 1 après aplatissement", n)
	}

	got, err := s.GetAsOf(k, id1)
	if err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("GetAsOf(id1) DEPUIS LE FROID: err=%v len=%d", err, len(got))
	}
	got, err = s.GetAsOf(k, id2)
	if err != nil || !bytes.Equal(got, v2) {
		t.Fatalf("GetAsOf(id2) DEPUIS LE FROID: err=%v len=%d", err, len(got))
	}
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v3) {
		t.Fatalf("Get après compaction: err=%v len=%d", err, len(got))
	}
	got, err = s.GetAsOf(k, id3)
	if err != nil || !bytes.Equal(got, v3) {
		t.Fatalf("GetAsOf(id3): err=%v len=%d", err, len(got))
	}

	recs, err := ReadHistoryDir(histDir)
	if err != nil {
		t.Fatalf("ReadHistoryDir: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("enregistrements d'historique=%d, attendu 2", len(recs))
	}
	want := map[[16]byte][]byte{id1: v1, id2: v2}
	for _, rec := range recs {
		w, ok := want[rec.VersionID]
		if !ok {
			t.Fatalf("identifiant déporté inattendu %x", rec.VersionID)
		}
		if rec.Tombstone {
			t.Fatalf("version %x déportée comme pierre tombale", rec.VersionID)
		}
		if !bytes.Equal(rec.Key, k) || !bytes.Equal(rec.Value, w) {
			t.Fatalf("enregistrement %x altéré (clé ou valeur)", rec.VersionID)
		}
		delete(want, rec.VersionID)
	}
	if len(want) != 0 {
		t.Fatalf("versions non déportées: %d", len(want))
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	arch := txnEventsOfKind(evs, EventArchive)
	if len(arch) != 2 {
		t.Fatalf("Archive=%d, attendu 2 (une par version déportée)", len(arch))
	}
	archIDs := map[[16]byte]bool{}
	for _, ev := range arch {
		if !txnHasID(arch, k, ev.VersionID) {
			t.Fatalf("Archive %x ne porte pas la clé déportée", ev.VersionID)
		}
		archIDs[ev.VersionID] = true
	}
	if !archIDs[id1] || !archIDs[id2] {
		t.Fatalf("Archive: identifiants déportés incomplets (%d/2)", len(archIDs))
	}
	if got := len(txnEventsOfKind(evs, EventPrune)); got != 0 {
		t.Fatalf("Prune=%d sous ArchiveSuperseded, attendu 0 (déport, pas perte)", got)
	}
	comps := txnEventsOfKind(evs, EventCompaction)
	if len(comps) != 2 {
		t.Fatalf("Compaction=%d, attendu 2", len(comps))
	}
	for i := range comps {
		if comps[i].Payload[0] != byte(RetentionArchiveSuperseded) {
			t.Fatalf("Compaction %d: rétention=%d, attendu %d", i, comps[i].Payload[0], RetentionArchiveSuperseded)
		}
	}
}

// TestEngineRetentionArchiveSupersededTombstone
// La suppression du moteur est physique (Db_bt_del_heap retire toutes les
// versions de la clé). Sous ArchiveSuperseded, cette suppression physique
// laisse néanmoins dans le segment froid les versions déjà déportées : sans
// pierre tombale versionnée, GetAsOf ressusciterait la clé depuis le froid.
// Le test prouve que Delete pose une pierre tombale datée de l'identifiant de
// suppression, que la clé est absente pour l'instantané de suppression et
// au-delà, qu'un instantané strictement antérieur reste servi par le froid,
// et que la suppression survit à la réouverture.
func TestEngineRetentionArchiveSupersededTombstone(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "hist")
	key := txnTestKey(194)
	s := mustOpenShard(t, dir, key, 11,
		WithHistoryRetention(RetentionArchiveSuperseded),
		WithHistoryLog(histDir))

	k := []byte("archive-delete")
	v1 := bytes.Repeat([]byte{0xA1}, 512)
	v2 := bytes.Repeat([]byte{0xA2}, 512)
	if err := s.Put(k, v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	ok, err := s.tryAutoCompact()
	if err != nil {
		t.Fatalf("tryAutoCompact: %v", err)
	}
	if !ok {
		t.Fatal("tryAutoCompact n'a pas compacté")
	}
	// v1 est déportée au froid, v2 reste chaude : la précondition de
	// résurrection est en place.
	if got, err := s.GetAsOf(k, id1); err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("précondition as-of v1 froid: err=%v", err)
	}

	if err := s.Delete(k); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	delID := s.lastID

	if got, err := s.Get(k); !errors.Is(err, ErrNotFound) || got != nil {
		t.Fatalf("résurrection après suppression: got=%q err=%v", got, err)
	}
	if _, err := s.GetAsOf(k, delID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("as-of à l'instant de suppression: %v, attendu ErrNotFound", err)
	}
	if got, err := s.GetAsOf(k, id1); err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("as-of v1 strictement antérieur: got=%q err=%v", got, err)
	}

	recs, err := ReadHistoryDir(histDir)
	if err != nil {
		t.Fatalf("ReadHistoryDir: %v", err)
	}
	tombs := 0
	var tombID [16]byte
	for _, rec := range recs {
		if bytes.Equal(rec.Key, k) && rec.Tombstone {
			tombs++
			tombID = rec.VersionID
		}
	}
	if tombs != 1 {
		t.Fatalf("pierres tombales=%d, attendu 1", tombs)
	}
	if tombID != delID {
		t.Fatalf("pierre tombale datée %x, attendu %x", tombID, delID)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2 := mustOpenShard(t, dir, key, 11,
		WithHistoryRetention(RetentionArchiveSuperseded),
		WithHistoryLog(histDir))
	defer func() { _ = s2.Close() }()
	if got, err := s2.Get(k); !errors.Is(err, ErrNotFound) || got != nil {
		t.Fatalf("résurrection après réouverture: got=%q err=%v", got, err)
	}
}

// histVersion fabrique un identifiant de version strictement croissant avec n,
// pour éprouver la résolution as-of sur un historique synthétique direct.
func histVersion(n uint64) (v [16]byte) {
	binary.BigEndian.PutUint64(v[8:], n)
	return v
}

// TestHistoryColdIndexBoundedRead prouve que la résolution froide indexée
// cesse de relire l'intégralité du répertoire : une clé absente ne lit aucun
// octet, une clé présente ne lit que son enregistrement. Le coût « avant »
// est le volume total du répertoire, mesuré sur un historique réel généré par
// le test ; le coût « après » est le compteur de lecture des résolutions.
func TestHistoryColdIndexBoundedRead(t *testing.T) {
	dir := t.TempDir()
	const n = 400
	h, err := OpenHistory(dir, 4096)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	keys := make([][]byte, n)
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("hist-key-%04d", i))
		keys[i] = k
		v := bytes.Repeat([]byte{byte(i)}, 256)
		if err := h.Append(HistoryRecord{Key: k, VersionID: histVersion(uint64(i + 1)), Value: v}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var dirBytes int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			t.Fatalf("Info %s: %v", e.Name(), err)
		}
		dirBytes += fi.Size()
	}
	if dirBytes <= 0 {
		t.Fatal("historique vide")
	}

	h2, err := OpenHistory(dir, 4096)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer func() { _ = h2.Close() }()
	if h2.Segments() < 2 {
		t.Fatalf("segments=%d, attendu >=2 pour éprouver la lecture intégrale", h2.Segments())
	}

	allFF := [16]byte{}
	for i := range allFF {
		allFF[i] = 0xFF
	}

	h2.ResetReadBytes()
	if _, found, err := h2.Resolve([]byte("hist-absent"), allFF); err != nil || found {
		t.Fatalf("Resolve clé absente: found=%v err=%v", found, err)
	}
	if rb := h2.ReadBytes(); rb != 0 {
		t.Fatalf("clé absente: %d octets lus, attendu 0", rb)
	}

	h2.ResetReadBytes()
	rec, found, err := h2.Resolve(keys[n/2], allFF)
	if err != nil || !found {
		t.Fatalf("Resolve clé présente: found=%v err=%v", found, err)
	}
	if !bytes.Equal(rec.Key, keys[n/2]) || len(rec.Value) != 256 {
		t.Fatalf("enregistrement résolu altéré: clé=%q len=%d", rec.Key, len(rec.Value))
	}
	after := h2.ReadBytes()
	if after <= 0 || after >= dirBytes/4 {
		t.Fatalf("coût froid non borné: après=%d octets, répertoire=%d", after, dirBytes)
	}

	for _, i := range []int{0, n / 3, n / 2, n - 1} {
		want, ok, err := ResolveHistoryDir(dir, keys[i], allFF)
		if err != nil || !ok {
			t.Fatalf("résolution intégrale clé %d: ok=%v err=%v", i, ok, err)
		}
		got, ok, err := h2.Resolve(keys[i], allFF)
		if err != nil || !ok || !bytes.Equal(got.Value, want.Value) {
			t.Fatalf("parité indexée clé %d: ok=%v err=%v", i, ok, err)
		}
	}
	t.Logf("coût froid: répertoire=%d octets, résolution indexée=%d octets pour une clé", dirBytes, after)
}

// TestHistoryOpenRecoversTornTail prouve que l'ouverture d'un historique dont
// le segment courant porte une queue partielle (panne en pleine écriture)
// tronque cette queue, reconstruit un index sain et accepte de nouveaux
// déports, sans exiger la réécriture des segments antérieurs.
func TestHistoryOpenRecoversTornTail(t *testing.T) {
	dir := t.TempDir()
	h, err := OpenHistory(dir, 4096)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	for i := 0; i < 20; i++ {
		k := []byte(fmt.Sprintf("torn-key-%02d", i))
		if err := h.Append(HistoryRecord{Key: k, VersionID: histVersion(uint64(i + 1)), Value: []byte("v")}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	highest := uint32(0)
	path := ""
	for _, e := range entries {
		idx, ok := parseHistorySegmentName(e.Name())
		if !ok {
			continue
		}
		if idx >= highest {
			highest = idx
			path = filepath.Join(dir, e.Name())
		}
	}
	if path == "" {
		t.Fatal("aucun segment")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF}); err != nil {
		t.Fatalf("Write queue tronquée: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close queue: %v", err)
	}

	h2, err := OpenHistory(dir, 4096)
	if err != nil {
		t.Fatalf("réouverture sur queue tronquée: %v", err)
	}
	defer func() { _ = h2.Close() }()
	allFF := [16]byte{}
	for i := range allFF {
		allFF[i] = 0xFF
	}
	if _, found, err := h2.Resolve([]byte("torn-key-10"), allFF); err != nil || !found {
		t.Fatalf("résolution après récupération: found=%v err=%v", found, err)
	}
	if err := h2.Append(HistoryRecord{Key: []byte("torn-key-after"), VersionID: histVersion(99), Value: []byte("w")}); err != nil {
		t.Fatalf("Append après récupération: %v", err)
	}
	if _, found, err := h2.Resolve([]byte("torn-key-after"), allFF); err != nil || !found {
		t.Fatalf("nouvel ajout après récupération: found=%v err=%v", found, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("segment courant vide après récupération")
	}
}

// TestEngineArchiveSupersededSurvivesReopen prouve que l'historique froid
// survit au redémarrage et que l'index reconstruit à l'ouverture accepte de
// nouveaux déports. Trois ouvertures successives vérifient l'as-of de
// versions déportées à des époques différentes.
func TestEngineArchiveSupersededSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "hist")
	key := txnTestKey(203)
	open := func() *Shard {
		return mustOpenShard(t, dir, key, 11,
			WithHistoryRetention(RetentionArchiveSuperseded),
			WithHistoryLog(histDir))
	}
	mk := func(fill byte) []byte { return bytes.Repeat([]byte{fill}, 3000) }
	k := []byte("cold-restart")
	v1, v2, v3, v4 := mk(0x11), mk(0x22), mk(0x33), mk(0x44)

	s := open()
	if err := s.Put(k, v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	id2 := s.lastID
	if err := s.Put(k, v3); err != nil {
		t.Fatalf("Put v3: %v", err)
	}
	id3 := s.lastID
	if ok, err := s.tryAutoCompact(); err != nil || !ok {
		t.Fatalf("première compaction: ok=%v err=%v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	s2 := open()
	if got, err := s2.GetAsOf(k, id1); err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("reprise as-of id1: err=%v", err)
	}
	if got, err := s2.GetAsOf(k, id2); err != nil || !bytes.Equal(got, v2) {
		t.Fatalf("reprise as-of id2: err=%v", err)
	}
	if got, err := s2.Get(k); err != nil || !bytes.Equal(got, v3) {
		t.Fatalf("reprise Get v3: err=%v", err)
	}
	if err := s2.Put(k, v4); err != nil {
		t.Fatalf("Put v4: %v", err)
	}
	if ok, err := s2.tryAutoCompact(); err != nil || !ok {
		t.Fatalf("seconde compaction: ok=%v err=%v", ok, err)
	}
	if got, err := s2.GetAsOf(k, id2); err != nil || !bytes.Equal(got, v2) {
		t.Fatalf("as-of id2 après second déport: err=%v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}

	s3 := open()
	defer func() { _ = s3.Close() }()
	if got, err := s3.GetAsOf(k, id1); err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("froid id1 après 3e ouverture: err=%v", err)
	}
	if got, err := s3.GetAsOf(k, id2); err != nil || !bytes.Equal(got, v2) {
		t.Fatalf("froid id2 après 3e ouverture: err=%v", err)
	}
	// v3 est déportée par la seconde compaction, effectuée après réouverture :
	// l'index reconstruit doit accepter les ajouts sans perdre les versions v1, v2.
	if got, err := s3.GetAsOf(k, id3); err != nil || !bytes.Equal(got, v3) {
		t.Fatalf("froid v3 après 3e ouverture: err=%v", err)
	}
	if got, err := s3.Get(k); err != nil || !bytes.Equal(got, v4) {
		t.Fatalf("actif v4 après 3e ouverture: err=%v", err)
	}
}
