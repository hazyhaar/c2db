package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hazyhaar/c2db/pkg/cihook55"
)

// capturingSink conserve en mémoire les événements émis par le moteur, pour
// comparer la trace du lot et celle de la boucle unitaire.
type capturingSink struct {
	mu     sync.Mutex
	events []TxnEvent
}

func (c *capturingSink) Emit(ev TxnEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev.Key = append([]byte(nil), ev.Key...)
	c.events = append(c.events, ev)
	return nil
}

func (c *capturingSink) Sync() error  { return nil }
func (c *capturingSink) Close() error { return nil }

func (c *capturingSink) reset() {
	c.mu.Lock()
	c.events = c.events[:0]
	c.mu.Unlock()
}

func (c *capturingSink) labels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.events))
	for i, ev := range c.events {
		out[i] = fmt.Sprintf("%d:%s", int(ev.Kind), ev.Key)
	}
	sort.Strings(out)
	return out
}

// TestDeleteBatchPublishesOnce est l'oracle rouge d'abord : la boucle de
// suppressions unitaires publie autant de fois qu'il y a de clés, tandis qu'un
// DeleteBatch de mêmes clés publie une fois par tranche bornée. Le chemin
// unitaire demeure le chemin de référence, sa sémantique n'est pas modifiée.
func TestDeleteBatchPublishesOnce(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(221), 21)
	defer func() { _ = s.Close() }()

	const n = 16
	keys := make([][]byte, n)
	pairs := make([][2][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("delbatch-%03d", i))
		pairs[i] = [2][]byte{keys[i], []byte("value-batch")}
	}
	if err := s.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}

	var publishes atomic.Int64
	cihook55.Set("before-publish", func() { publishes.Add(1) })
	defer cihook55.Set("before-publish", nil)

	publishes.Store(0)
	for i := range keys {
		if err := s.Delete(keys[i]); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}
	if got := publishes.Load(); got != int64(n) {
		t.Fatalf("suppressions unitaires: publications=%d, attendu %d", got, n)
	}

	if err := s.PutBatch(pairs); err != nil {
		t.Fatalf("re-PutBatch: %v", err)
	}
	publishes.Store(0)
	if err := s.DeleteBatch(keys); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}
	wantBatch := int64((n + deleteBatchChunk - 1) / deleteBatchChunk)
	if got := publishes.Load(); got != wantBatch {
		t.Fatalf("DeleteBatch: publications=%d, attendu %d tranche(s)", got, wantBatch)
	}

	// Lot vide = nil, clé vide refusée comme l'unitaire, clé absente = no-op.
	if err := s.DeleteBatch(nil); err != nil {
		t.Fatalf("DeleteBatch vide: %v", err)
	}
	if err := s.DeleteBatch([][]byte{}); err != nil {
		t.Fatalf("DeleteBatch lot vide: %v", err)
	}
	if err := s.DeleteBatch([][]byte{{}}); err == nil {
		t.Fatal("DeleteBatch clé vide: rejet attendu")
	}
	if err := s.DeleteBatch([][]byte{[]byte("absent-batch-key")}); err != nil {
		t.Fatalf("DeleteBatch clé absente: %v", err)
	}
}

// TestDeleteBatchPublicationsBoundedOnLargeBatch prouve la borne sur un lot
// géant : 100 000 clés réelles sont écrites puis supprimées, DeleteBatch se
// termine et ne publie qu'une fois par tranche de deleteBatchChunk clés, jamais
// N fois. Avant le découpage, le lot unique ne publiait qu'une fois et le test
// échouait sur l'égalité au nombre de tranches attendu.
func TestDeleteBatchPublicationsBoundedOnLargeBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("lot géant ignoré en mode court")
	}
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(224), 23)
	defer func() { _ = s.Close() }()

	const n = 100_000
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("bound-%06d", i))
	}

	// Écriture réelle des clés, elle-même bornée en tranches pour ne pas
	// reporter sur l'insertion le coût que la suppression doit prouver borné.
	pairs := make([][2][]byte, 0, deleteBatchChunk)
	for i := 0; i < n; i++ {
		pairs = append(pairs, [2][]byte{keys[i], []byte("v")})
		if len(pairs) == deleteBatchChunk || i == n-1 {
			if err := s.PutBatch(pairs); err != nil {
				t.Fatalf("PutBatch tranche %d: %v", i, err)
			}
			pairs = pairs[:0]
		}
	}
	for i := range keys {
		if _, err := s.Get(keys[i]); err != nil {
			t.Fatalf("Get préalable %d: %v", i, err)
		}
	}

	var publishes atomic.Int64
	cihook55.Set("before-publish", func() { publishes.Add(1) })
	defer cihook55.Set("before-publish", nil)
	publishes.Store(0)

	if err := s.DeleteBatch(keys); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}
	want := int64((n + deleteBatchChunk - 1) / deleteBatchChunk)
	got := publishes.Load()
	if got != want {
		t.Fatalf("publications=%d, attendu %d tranche(s) pour N=%d", got, want, n)
	}
	if got >= int64(n) {
		t.Fatalf("publications=%d non borné face à N=%d", got, n)
	}
	for i := range keys {
		if _, err := s.Get(keys[i]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get après lot %d: err=%v, attendu ErrNotFound", i, err)
		}
	}
}

// TestDeleteBatchParityWithUnitDelete prouve la parité d'état lisible : le lot
// et la boucle unitaire laissent les mêmes clés absentes, les mêmes résolutions
// as-of sous rétention ArchiveSuperseded, les mêmes pierres tombales au froid et
// les mêmes événements (à l'ordre près).
func TestDeleteBatchParityWithUnitDelete(t *testing.T) {
	const n = 8
	type fixture struct {
		s    *Shard
		sink *capturingSink
		hist string
	}

	open := func(seed byte) fixture {
		dir := t.TempDir()
		hist := filepath.Join(dir, "hist")
		s := mustOpenShard(t, dir, txnTestKey(seed), 22,
			WithHistoryRetention(RetentionArchiveSuperseded),
			WithHistoryLog(hist))
		sink := &capturingSink{}
		s.SetEventSink(sink)
		return fixture{s: s, sink: sink, hist: hist}
	}

	prep := func(f fixture) (keys [][]byte, latest [][16]byte) {
		for i := 0; i < n; i++ {
			k := []byte(fmt.Sprintf("parity-%03d", i))
			v1 := bytes.Repeat([]byte{byte('A' + i)}, 512)
			v2 := bytes.Repeat([]byte{byte('a' + i)}, 512)
			if err := f.s.Put(k, v1); err != nil {
				t.Fatalf("Put v1 %d: %v", i, err)
			}
			if err := f.s.Put(k, v2); err != nil {
				t.Fatalf("Put v2 %d: %v", i, err)
			}
			keys = append(keys, k)
			latest = append(latest, f.s.lastID)
		}
		ok, err := f.s.tryAutoCompact()
		if err != nil {
			t.Fatalf("tryAutoCompact: %v", err)
		}
		if !ok {
			t.Fatal("tryAutoCompact n'a pas compacté")
		}
		f.sink.reset()
		return keys, latest
	}

	unit := open(222)
	defer func() { _ = unit.s.Close() }()
	batch := open(223)
	defer func() { _ = batch.s.Close() }()

	unitKeys, unitLatest := prep(unit)
	batchKeys, batchLatest := prep(batch)

	for i := range unitKeys {
		if err := unit.s.Delete(unitKeys[i]); err != nil {
			t.Fatalf("Delete unitaire %d: %v", i, err)
		}
	}
	if err := batch.s.DeleteBatch(batchKeys); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}

	for i := range unitKeys {
		if _, err := unit.s.Get(unitKeys[i]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unit Get %d: err=%v, attendu ErrNotFound", i, err)
		}
		if _, err := batch.s.Get(batchKeys[i]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("batch Get %d: err=%v, attendu ErrNotFound", i, err)
		}
		ug, uerr := unit.s.GetAsOf(unitKeys[i], unitLatest[i])
		bg, berr := batch.s.GetAsOf(batchKeys[i], batchLatest[i])
		if (uerr == nil) != (berr == nil) {
			t.Fatalf("as-of %d: unit err=%v, batch err=%v", i, uerr, berr)
		}
		if uerr == nil && !bytes.Equal(ug, bg) {
			t.Fatalf("as-of %d: unit val=%q, batch val=%q", i, ug, bg)
		}
	}

	unitTomb := tombstoneCounts(t, unit.hist)
	batchTomb := tombstoneCounts(t, batch.hist)
	if len(unitTomb) != len(batchTomb) {
		t.Fatalf("pierres tombales: unit=%v, batch=%v", unitTomb, batchTomb)
	}
	for k, c := range unitTomb {
		if batchTomb[k] != c || c != 1 {
			t.Fatalf("pierre tombale %q: unit=%d, batch=%d", k, c, batchTomb[k])
		}
	}

	ul := unit.sink.labels()
	bl := batch.sink.labels()
	if len(ul) != len(bl) {
		t.Fatalf("événements: unit=%v, batch=%v", ul, bl)
	}
	for i := range ul {
		if ul[i] != bl[i] {
			t.Fatalf("événements divergents à %d: unit=%q, batch=%q\nunit=%v\nbatch=%v", i, ul[i], bl[i], ul, bl)
		}
	}
}

func tombstoneCounts(t *testing.T, histDir string) map[string]int {
	t.Helper()
	recs, err := ReadHistoryDir(histDir)
	if err != nil {
		t.Fatalf("ReadHistoryDir: %v", err)
	}
	counts := make(map[string]int)
	for _, rec := range recs {
		if rec.Tombstone {
			counts[string(rec.Key)]++
		}
	}
	return counts
}
