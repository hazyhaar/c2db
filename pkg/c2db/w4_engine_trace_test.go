package c2db

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

// TestW4ManualRepackEmitsCompaction prouve que les deux repacks manuels de
// pages (RepackHeap et Compact) consultent la rétention et bornent leur
// exécution par deux événements Compaction (phase 0 avant, phase 1 après)
// portant la rétention et la frontière (compteur de mutations), sans que la
// sémantique du repack ne change.
func TestW4ManualRepackEmitsCompaction(t *testing.T) {
	dir := t.TempDir()
	txnDir := filepath.Join(dir, "events")
	s := mustOpenShard(t, dir, txnTestKey(201), 11, WithTxLog(txnDir))

	for i := 0; i < 32; i++ {
		k := []byte(fmt.Sprintf("w4-repack-%02d", i))
		if err := s.Put(k, bytes.Repeat([]byte{byte(i)}, 64)); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
	}
	frontier := s.counter

	if err := s.RepackHeap(); err != nil {
		t.Fatalf("RepackHeap: %v", err)
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	comps := txnEventsOfKind(evs, EventCompaction)
	if len(comps) != 4 {
		t.Fatalf("Compaction=%d, attendu 4 (avant/après par repack manuel)", len(comps))
	}
	phases := map[byte]int{}
	for i := range comps {
		if len(comps[i].Payload) < 2 {
			t.Fatalf("Compaction %d: charge utile trop courte", i)
		}
		if comps[i].Payload[0] != byte(RetentionPruneSuperseded) {
			t.Fatalf("Compaction %d: rétention=%d, attendu %d (défaut)", i, comps[i].Payload[0], RetentionPruneSuperseded)
		}
		if comps[i].Payload[1] > 1 {
			t.Fatalf("Compaction %d: phase invalide %d", i, comps[i].Payload[1])
		}
		phases[comps[i].Payload[1]]++
		if comps[i].Watermark != frontier {
			t.Fatalf("Compaction %d: frontière=%d, attendu %d", i, comps[i].Watermark, frontier)
		}
	}
	if phases[0] != 2 || phases[1] != 2 {
		t.Fatalf("phases avant/après incomplètes: %v", phases)
	}
}

// TestW4ReplayArchiveSupersededDeports prouve que le filet de rejeu, sous
// RetentionArchiveSuperseded, déporte l'historique supplanté vers le segment
// d'historique au lieu d'échouer fermé sur ErrHeapFull. Le rejeu se fait par
// une véritable réouverture : le tas scellé au seuil, un enregistrement WAL
// neuf force la compaction, le budget de matérialisation nul fait basculer
// l'histoire vers le froid. Les versions déportées restent adressables par
// GetAsOf.
func TestW4ReplayArchiveSupersededDeports(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "hist")
	key := txnTestKey(202)

	s, err := OpenShard(dir, key, 0,
		WithHeapPages(4096),
		WithHistoryRetention(RetentionArchiveSuperseded),
		WithHistoryLog(histDir))
	if err != nil {
		t.Fatalf("OpenShard base: %v", err)
	}

	k := []byte("w4-archive-replay")
	v1 := bytes.Repeat([]byte{0x11}, 128)
	v2 := bytes.Repeat([]byte{0x22}, 128)
	v3 := bytes.Repeat([]byte{0x33}, 128)
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

	// Le tas est porté au seuil de capacité puis scellé : la première
	// insertion du rejeu manquera d'espace et déclenchera le filet.
	s.heapUsed = s.heapWatermark
	if err := s.publish(); err != nil {
		t.Fatalf("publish seuil: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close base: %v", err)
	}

	// Un enregistrement WAL neuf, postérieur à la base durable.
	walPath := filepath.Join(dir, "wal.img")
	w, err := OpenWAL(walPath, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	newKey := []byte("w4-archive-new")
	nid := [16]byte{0x77, 0x04, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D}
	if err := w.Append(Record{ID: nid, Type: RecPut, Payload: packKV(newKey, []byte("nv"))}); err != nil {
		t.Fatalf("Append WAL: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush WAL: %v", err)
	}
	_ = w.Close()

	// Réouverture avec un budget de matérialisation nul : le filet de rejeu
	// doit déporter au lieu de rendre ErrHeapFull.
	s2, err := OpenShard(dir, key, 0,
		WithHeapPages(4096),
		WithHistoryRetention(RetentionArchiveSuperseded),
		WithHistoryLog(histDir),
		withRebuildBudget(1))
	if err != nil {
		t.Fatalf("rejeu ArchiveSuperseded: %v", err)
	}
	defer func() { _ = s2.Close() }()

	if got, err := s2.GetAsOf(k, id1); err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("version déportée id1 illisible depuis le froid: got=%q err=%v", got, err)
	}
	if got, err := s2.GetAsOf(k, id2); err != nil || !bytes.Equal(got, v2) {
		t.Fatalf("version déportée id2 illisible depuis le froid: got=%q err=%v", got, err)
	}
	if got, err := s2.Get(k); err != nil || !bytes.Equal(got, v3) {
		t.Fatalf("version active v3: got=%q err=%v", got, err)
	}
	if got, err := s2.Get(newKey); err != nil || !bytes.Equal(got, []byte("nv")) {
		t.Fatalf("mutation rejouée absente: got=%q err=%v", got, err)
	}
	if n := s2.countAllVersionsLocked(); n != 2 {
		t.Fatalf("versions chaudes=%d, attendu 2 (v3 + mutation rejouée)", n)
	}

	recs, err := ReadHistoryDir(histDir)
	if err != nil {
		t.Fatalf("ReadHistoryDir: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("enregistrements déportés=%d, attendu 2 (id1, id2)", len(recs))
	}
	want := map[[16]byte][]byte{id1: v1, id2: v2}
	for _, rec := range recs {
		wv, ok := want[rec.VersionID]
		if !ok {
			t.Fatalf("identifiant déporté inattendu %x", rec.VersionID)
		}
		if !bytes.Equal(rec.Key, k) || !bytes.Equal(rec.Value, wv) {
			t.Fatalf("enregistrement %x altéré", rec.VersionID)
		}
		delete(want, rec.VersionID)
	}
	if len(want) != 0 {
		t.Fatalf("versions non déportées: %d", len(want))
	}
}
