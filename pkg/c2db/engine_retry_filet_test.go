package c2db

import (
	"bytes"
	"errors"
	"testing"
)

func countSinkKind(s *capturingSink, kind EventKind) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.events {
		if s.events[i].Kind == kind {
			n++
		}
	}
	return n
}

// TestRetryAfterHeapFullIgnoresErrInsert prouve que le filet de compaction ne
// réagit jamais à errInsert (refus de validation ou de format) : il rend
// l'erreur sans tenter de reconstruction ni rejouer l'opération. Avant
// correctif, errInsert déclenchait tryAutoCompact, donc un événement Compaction
// et le rejeu de l'opération.
func TestRetryAfterHeapFullIgnoresErrInsert(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(140), 11)
	defer func() { _ = s.Close() }()

	if err := s.Put([]byte("seed"), []byte("x")); err != nil {
		t.Fatalf("Put seed: %v", err)
	}
	sink := &capturingSink{}
	s.SetEventSink(sink)
	sink.reset()

	opCalled := false
	got := s.retryAfterHeapFull([]byte("k"), errInsert, func() error {
		opCalled = true
		return nil
	})
	if !errors.Is(got, errInsert) {
		t.Fatalf("erreur rendue %v, attendu errInsert", got)
	}
	if opCalled {
		t.Fatal("opération rejouée alors qu'errInsert n'est pas capacitaire")
	}
	if n := countSinkKind(sink, EventCompaction); n != 0 {
		t.Fatalf("%d compaction(s) déclenchée(s) sur errInsert", n)
	}
}

// TestRetryAfterHeapFullRebuildsOnTreeFull prouve que l'échec structurel de
// l'arbre (ErrTreeFull), distinct d'errInsert, reçoit le traitement approprié :
// une reconstruction bornée (visée par le budget) est tentée, puis l'opération
// est rejouée.
func TestRetryAfterHeapFullRebuildsOnTreeFull(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(141), 11)
	defer func() { _ = s.Close() }()

	if err := s.Put([]byte("seed"), []byte("x")); err != nil {
		t.Fatalf("Put seed: %v", err)
	}
	sink := &capturingSink{}
	s.SetEventSink(sink)
	sink.reset()

	opCalled := false
	got := s.retryAfterHeapFull([]byte("k"), ErrTreeFull, func() error {
		opCalled = true
		return nil
	})
	if got != nil {
		t.Fatalf("retryAfterHeapFull(ErrTreeFull) = %v, attendu nil après reconstruction", got)
	}
	if !opCalled {
		t.Fatal("opération non rejouée après reconstruction sur ErrTreeFull")
	}
	if n := countSinkKind(sink, EventCompaction); n == 0 {
		t.Fatal("aucune compaction déclenchée sur ErrTreeFull")
	}
}

// TestRebuildLatestBudgetRefusesOverBudget prouve que la reconstruction bornée
// sous RetentionPruneSuperseded refuse (ErrHeapFull) au lieu de matérialiser un
// volume supérieur au budget. Avant l'ajout du budget, la reconstruction
// réussissait quelle que soit la taille et l'opération était rejouée.
func TestRebuildLatestBudgetRefusesOverBudget(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenShard(t, dir, txnTestKey(142), 11, withRebuildBudget(1))
	defer func() { _ = s.Close() }()

	if err := s.Put([]byte("k1"), bytes.Repeat([]byte{0x11}, 64)); err != nil {
		t.Fatalf("Put k1: %v", err)
	}
	if err := s.Put([]byte("k2"), bytes.Repeat([]byte{0x22}, 64)); err != nil {
		t.Fatalf("Put k2: %v", err)
	}

	opCalled := false
	got := s.retryAfterHeapFull(nil, ErrHeapFull, func() error {
		opCalled = true
		return nil
	})
	if !errors.Is(got, ErrHeapFull) {
		t.Fatalf("budget dépassé: erreur rendue %v, attendu ErrHeapFull", got)
	}
	if opCalled {
		t.Fatal("opération rejouée malgré un budget de reconstruction dépassé")
	}
}
