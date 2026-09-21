package c2db

import (
	"path/filepath"
	"testing"
)

// TestReplayResumableAfterCheckpoint verrouille W1-Q1 au niveau du journal : un
// marqueur de pointage intercale coupe le journal en un prefixe deja durable et
// une queue a reappliquer. La reprise doit tomber juste apres le marqueur, par
// index d'enregistrement (le WAL empaquette plusieurs records par LBA), et non
// au LBA suivant, qui sauterait la queue partageant ce LBA.
func TestReplayResumableAfterCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w.img")
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 0x71)
	}
	w := mustCreateWAL(t, path, key)
	rec := func(n uint64, cle string) Record {
		id, err := NewID(n, 3, n)
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		return Record{ID: id, Type: RecPut, Payload: packKV([]byte(cle), []byte("v"))}
	}
	for _, r := range []Record{rec(1, "a"), rec(2, "b")} {
		if err := w.Append(r); err != nil {
			t.Fatalf("Append prefixe: %v", err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	for _, r := range []Record{rec(3, "c"), rec(4, "d")} {
		if err := w.Append(r); err != nil {
			t.Fatalf("Append queue: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w2 := mustOpenWAL(t, path, key)
	defer func() { _ = w2.Close() }()
	recs, resumeAt, err := w2.ReplayResumable()
	if err != nil {
		t.Fatalf("ReplayResumable: %v", err)
	}
	if resumeAt < 1 || resumeAt > len(recs) {
		t.Fatalf("point de reprise incoherent: resumeAt=%d sur %d enregistrements", resumeAt, len(recs))
	}
	queue := make(map[string]bool)
	for i := resumeAt; i < len(recs); i++ {
		if _, cle, _, ok := unpackKV(recs[i].Payload); ok {
			queue[string(cle)] = true
		}
	}
	if !queue["c"] || !queue["d"] {
		t.Fatalf("queue incomplete apres le pointage: %v (resumeAt=%d sur %d)", queue, resumeAt, len(recs))
	}
	// Le prefixe ne doit pas etre dans la queue a reappliquer.
	if queue["a"] || queue["b"] {
		t.Fatalf("prefixe reapplique apres le pointage: %v", queue)
	}
}
