// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

// buildReadOnlyBenchFixture construit hors mesure un shard durable dont le tas
// est effectivement peuplé de usedPages pages, puis le referme. Le coût
// d'ouverture en lecture seule mesuré par le banc porte donc sur le rechargement
// des pages du data.img et sur les copies internes pub->dirty, jamais sur
// l'insertion.
func buildReadOnlyBenchFixture(b *testing.B, heapPages, usedPages uint64) string {
	b.Helper()
	dir := b.TempDir()
	s, err := OpenShard(dir, [32]byte{}, 0, WithHeapPages(heapPages), WithWALBytes(256*1024*1024))
	if err != nil {
		b.Fatalf("OpenShard: %v", err)
	}
	val := bytes.Repeat([]byte("x"), 8000)
	const batch = 2048
	pairs := make([][2][]byte, batch)
	target := usedPages * 2
	written := 0
	for written < int(target) {
		n := batch
		if remaining := int(target) - written; remaining < n {
			n = remaining
		}
		for i := 0; i < n; i++ {
			var key [16]byte
			binary.LittleEndian.PutUint64(key[:8], uint64(written+i))
			pairs[i] = [2][]byte{key[:8], val}
		}
		if err := s.PutBatch(pairs[:n]); err != nil {
			b.Fatalf("PutBatch: %v", err)
		}
		written += n
	}
	if err := s.Checkpoint(); err != nil {
		b.Fatalf("Checkpoint: %v", err)
	}
	if err := s.Close(); err != nil {
		b.Fatalf("Close: %v", err)
	}
	return dir
}

// BenchmarkReadOnlyOpen mesure l'ouverture d'une vue en lecture seule sur un
// shard durable dont le tas est peuplé. La référence à battre est l'ouverture
// qui alloue dirty et recopie pub->dirty sur toutes les pages utilisées.
func BenchmarkReadOnlyOpen(b *testing.B) {
	const usedPages = 4096
	dir := buildReadOnlyBenchFixture(b, 8192, usedPages)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
		if err != nil {
			b.Fatal(err)
		}
		if got, err := s.Get([]byte{1, 0, 0, 0, 0, 0, 0, 0}); err != nil || len(got) == 0 {
			b.Fatalf("Get: %v len=%d", err, len(got))
		}
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReadOnlyReplayOpen mesure l'ouverture en lecture seule lorsqu'un
// rejeu WAL est réellement à appliquer : l'allocation paresseuse de dirty y est
// légitime et le banc vérifie qu'elle ne transforme pas l'ouverture en régression.
func BenchmarkReadOnlyReplayOpen(b *testing.B) {
	dir := buildReadOnlyBenchFixture(b, 4096, 8)
	w, err := OpenShard(dir, [32]byte{}, 0)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if err := w.Put([]byte(fmt.Sprintf("pending-%02d", i)), []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.wal.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := w.KillWithoutFlush(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
		if err != nil {
			b.Fatal(err)
		}
		if got, err := s.Get([]byte("pending-15")); err != nil || string(got) != "value" {
			b.Fatalf("replayed Get=%q err=%v", got, err)
		}
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// benchDirtySync prépare un shard synthétique à taille maximale (65 536 pages
// de 16 Ko, soit 1 Gio) et mesure le coût d'une recopie intégrale pub->dirty.
// Ces recopies sont exactement le travail supprimé pour les vues en lecture
// seule sans rejeu : une telle vue ne les exécute plus du tout.
func benchDirtySync(b *testing.B, full bool) {
	const pages = 65536
	shardBytes := uint64(pages) * pageN
	pub, err := mmapAnon(int(shardBytes))
	if err != nil {
		b.Fatal(err)
	}
	dirty, err := mmapAnon(int(shardBytes))
	if err != nil {
		unmapHeap(pub)
		b.Fatal(err)
	}
	s := &Shard{
		pub:        pub,
		dirty:      dirty,
		shardBytes: shardBytes,
		heapPages:  pages,
		heapUsed:   pages,
		dirtyMask:  make([]uint64, pages/64),
		dirtyList:  make([]uint32, pages),
	}
	b.Cleanup(func() {
		unmapHeap(pub)
		unmapHeap(dirty)
	})
	b.SetBytes(int64(shardBytes))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if full {
			s.syncDirtyFull()
		} else {
			s.syncDirtyFromPub()
		}
	}
}

func BenchmarkRemovedSyncDirtyFromPubMax(b *testing.B) { benchDirtySync(b, false) }
func BenchmarkRemovedSyncDirtyFullMax(b *testing.B)    { benchDirtySync(b, true) }
