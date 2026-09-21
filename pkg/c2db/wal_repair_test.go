// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// zeroWALBlock remet à zéro un bloc scellé réel du journal, fabricant un trou
// médian authentique sans passer par des données de complaisance.
func zeroWALBlock(t *testing.T, path string, lba uint64) {
	t.Helper()
	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	buf := mmapAligned(t, LBASize)
	if err := dev.Read(lba, buf); err != nil {
		_ = dev.Close()
		t.Fatalf("Read lba %d: %v", lba, err)
	}
	clear(buf)
	if err := dev.Write(lba, buf); err != nil {
		_ = dev.Close()
		t.Fatalf("Write lba %d: %v", lba, err)
	}
	if err := dev.Flush(); err != nil {
		_ = dev.Close()
		t.Fatalf("Flush: %v", err)
	}
	_ = dev.Close()
}

func newRepairKey(t *testing.T) [32]byte {
	t.Helper()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return key
}

// TestWALRepair_MedianHole_RedThenGreen fabrique un trou médian réel (bloc
// scellé remis à zéro avec des blocs scellés en aval), constate le refus à
// l'ouverture, applique la réparation, puis rouvre et rejoue.
func TestWALRepair_MedianHole_RedThenGreen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.img")
	key := newRepairKey(t)

	w, err := CreateWAL(path, 1<<20, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté (EINVAL)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	var want []Record
	for i := 0; i < 8; i++ {
		rec := walCrashRecord(i+1, []byte(fmt.Sprintf("repair-block-%02d", i)))
		want = append(want, rec)
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	zeroWALBlock(t, path, 3)

	// ROUGE : le trou médian est refusé fail-closed.
	if wBad, err := OpenWAL(path, key); err == nil {
		_ = wBad.Close()
		t.Fatalf("OpenWAL a réussi malgré le trou médian")
	} else if !errors.Is(err, errWALCorruptedMedian) {
		t.Fatalf("attendu errWALCorruptedMedian, obtenu: %v", err)
	}

	// Prévisualisation sans modification.
	pre, err := RepairedWAL(path, key)
	if err != nil {
		t.Fatalf("RepairedWAL: %v", err)
	}
	if pre.Repaired || pre.StartLBA != 3 || pre.DiscardedBlocks != 0 || pre.QuarantinePath != "" {
		t.Fatalf("prévisualisation incohérente: %+v", pre)
	}

	// VERT : réparation explicite.
	rep, err := RepairWAL(path, key)
	if err != nil {
		t.Fatalf("RepairWAL: %v", err)
	}
	if !rep.Repaired {
		t.Fatalf("Repaired=false sur un trou médian avéré: %+v", rep)
	}
	if rep.StartLBA != 3 {
		t.Fatalf("StartLBA=%d, attendu 3", rep.StartLBA)
	}
	if rep.DiscardedBlocks != 5 {
		t.Fatalf("DiscardedBlocks=%d, attendu 5", rep.DiscardedBlocks)
	}
	if rep.Next != 3 || rep.HasCheck {
		t.Fatalf("état recalculé incohérent: next=%d hasCheck=%v checkLBA=%d", rep.Next, rep.HasCheck, rep.CheckLBA)
	}
	if rep.QuarantinePath == "" {
		t.Fatalf("aucune quarantaine produite")
	}
	qData, err := os.ReadFile(rep.QuarantinePath)
	if err != nil {
		t.Fatalf("ReadFile quarantaine: %v", err)
	}
	if uint64(len(qData)) != rep.DiscardedBlocks*LBASize {
		t.Fatalf("quarantaine de %d octets, attendu %d", len(qData), rep.DiscardedBlocks*LBASize)
	}
	gotSum := sha256.Sum256(qData)
	if gotSum != rep.QuarantineSHA256 {
		t.Fatalf("SHA-256 quarantaine divergent: got %x want %x", gotSum, rep.QuarantineSHA256)
	}

	// Réouverture et rejeu : exactement le préfixe [0, startLBA).
	w2, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL après réparation: %v", err)
	}
	defer w2.Close()
	recs, err := w2.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("Replay: %d enregistrements, attendu 3", len(recs))
	}
	for i := range recs {
		assertRecordEqual(t, recs[i], want[i], i)
	}
}

// TestWALRepair_Intact_NoOp prouve qu'un journal intact n'est pas touché.
func TestWALRepair_Intact_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.img")
	key := newRepairKey(t)

	w, err := CreateWAL(path, 1<<20, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté (EINVAL)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	for i := 0; i < 8; i++ {
		if err := w.Append(walCrashRecord(i+1, []byte(fmt.Sprintf("intact-%02d", i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile avant: %v", err)
	}
	rep, err := RepairWAL(path, key)
	if err != nil {
		t.Fatalf("RepairWAL: %v", err)
	}
	if rep.Repaired || rep.DiscardedBlocks != 0 || rep.QuarantinePath != "" {
		t.Fatalf("journal intact modifié: %+v", rep)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile après: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("le journal intact a été altéré")
	}
	matches, _ := filepath.Glob(path + ".quarantine-*")
	if len(matches) != 0 {
		t.Fatalf("quarantaine produite sur journal intact: %v", matches)
	}
}

// TestWALRepair_TruncatedTail_NoRepair prouve qu'une queue tronquée normale est
// acceptée telle quelle et ne déclenche aucune coupe.
func TestWALRepair_TruncatedTail_NoRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.img")
	key := newRepairKey(t)

	w, err := CreateWAL(path, 1<<20, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté (EINVAL)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := w.Append(walCrashRecord(i+1, []byte(fmt.Sprintf("tail-%02d", i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	for i := 5; i < 8; i++ {
		if err := w.Append(walCrashRecord(i+1, []byte(fmt.Sprintf("tail-%02d", i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	// Arrêt brutal : les trois derniers enregistrements restent dans le quantum
	// non vidangé et ne sont jamais écrits.
	if err := w.KillWithoutFlush(); err != nil {
		t.Fatalf("KillWithoutFlush: %v", err)
	}

	rep, err := RepairWAL(path, key)
	if err != nil {
		t.Fatalf("RepairWAL: %v", err)
	}
	if rep.Repaired || rep.DiscardedBlocks != 0 || rep.QuarantinePath != "" {
		t.Fatalf("réparation déclenchée sur une queue tronquée: %+v", rep)
	}
	if rep.StartLBA != 5 {
		t.Fatalf("startLBA=%d, attendu 5", rep.StartLBA)
	}
	w2, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL après no-op: %v", err)
	}
	_ = w2.Close()
}

// populateRepairShard construit un shard réel par des Put successifs et rend,
// par clé, la suite des identifiants de version produits.
func populateRepairShard(t *testing.T, dir string, key [32]byte) map[string][][16]byte {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s, err := OpenShard(dir, key, 0)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté (EINVAL)")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	vers := make(map[string][][16]byte)
	put := func(k, v string) {
		t.Helper()
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			_ = s.Close()
			t.Fatalf("Put %s=%s: %v", k, v, err)
		}
		vers[k] = append(vers[k], s.lastID)
	}
	put("alpha", "alpha-v1")
	put("alpha", "alpha-v2")
	put("beta", "beta-v1")
	// Au-delà de walCoalesceN (32), le WAL vidange son tampon et matérialise
	// plusieurs blocs scellés réels : le trou médian devient possible.
	for i := 0; i < 70; i++ {
		put(fmt.Sprintf("bulk-%03d", i), fmt.Sprintf("bulk-v-%03d", i))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return vers
}

func shardGet(t *testing.T, s *Shard, k string) []byte {
	t.Helper()
	got, err := s.Get([]byte(k))
	if err != nil {
		t.Fatalf("Get %s: %v", k, err)
	}
	return got
}

// TestShardRepair_MedianHole_Parity fabrique un trou médian dans le journal
// d'un shard réel, répare sous verrou, et compare Get et GetAsOf à un shard
// équivalent dont le journal est intact.
func TestShardRepair_MedianHole_Parity(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	key := newRepairKey(t)

	versA := populateRepairShard(t, dirA, key)
	versB := populateRepairShard(t, dirB, key)

	walB := filepath.Join(dirB, "wal.img")
	fi, err := os.Stat(walB)
	if err != nil {
		t.Fatalf("Stat wal B: %v", err)
	}
	if fi.Size() < 4*LBASize {
		t.Fatalf("journal B trop court (%d octets) pour un trou médian", fi.Size())
	}
	zeroWALBlock(t, walB, 1)

	// ROUGE : ouverture refusée fail-closed.
	if sBad, err := OpenShard(dirB, key, 0); err == nil {
		_ = sBad.Close()
		t.Fatalf("OpenShard B a réussi malgré le trou médian")
	} else if !errors.Is(err, errWALCorruptedMedian) {
		t.Fatalf("attendu errWALCorruptedMedian, obtenu: %v", err)
	}

	// Réparation explicite sous verrou écrivain.
	rep, err := RepairShardWAL(dirB, key)
	if err != nil {
		t.Fatalf("RepairShardWAL: %v", err)
	}
	if !rep.Repaired || rep.StartLBA != 1 || rep.DiscardedBlocks == 0 {
		t.Fatalf("compte-rendu incohérent: %+v", rep)
	}

	sA, err := OpenShard(dirA, key, 0)
	if err != nil {
		t.Fatalf("OpenShard A: %v", err)
	}
	defer sA.Close()
	sB, err := OpenShard(dirB, key, 0)
	if err != nil {
		t.Fatalf("OpenShard B après réparation: %v", err)
	}
	defer sB.Close()

	keys := []string{"alpha", "beta"}
	for i := 0; i < 70; i++ {
		keys = append(keys, fmt.Sprintf("bulk-%03d", i))
	}
	for _, k := range keys {
		gA := shardGet(t, sA, k)
		gB := shardGet(t, sB, k)
		if !bytes.Equal(gA, gB) {
			t.Fatalf("Get(%s) divergent: A=%q B=%q", k, gA, gB)
		}
	}
	// Parité GetAsOf sur les versions réelles de chaque shard.
	for _, k := range []string{"alpha", "beta"} {
		if len(versA[k]) != len(versB[k]) {
			t.Fatalf("nombre de versions de %s divergent: A=%d B=%d", k, len(versA[k]), len(versB[k]))
		}
		for i := range versA[k] {
			a, err := sA.GetAsOf([]byte(k), versA[k][i])
			if err != nil {
				t.Fatalf("GetAsOf A(%s, v%d): %v", k, i, err)
			}
			b, err := sB.GetAsOf([]byte(k), versB[k][i])
			if err != nil {
				t.Fatalf("GetAsOf B(%s, v%d): %v", k, i, err)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("GetAsOf(%s, v%d) divergent: A=%q B=%q", k, i, a, b)
			}
		}
	}
	// Le shard réparé reste écrivable et relisible.
	if err := sB.Put([]byte("post-repair"), []byte("ok")); err != nil {
		t.Fatalf("Put après réparation: %v", err)
	}
	if got := shardGet(t, sB, "post-repair"); !bytes.Equal(got, []byte("ok")) {
		t.Fatalf("Put après réparation non relu: %q", got)
	}
}

// TestShardRepair_ExclusiveUnderLock prouve que la réparation décline tant que
// le verrou écrivain du shard est détenu.
func TestShardRepair_ExclusiveUnderLock(t *testing.T) {
	dir := t.TempDir()
	key := newRepairKey(t)
	s, err := OpenShard(dir, key, 0)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté (EINVAL)")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lockFd, err := unix.Open(filepath.Join(dir, ".lock"), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("Open .lock: %v", err)
	}
	defer func() { _ = unix.Close(lockFd) }()
	fl := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
	if err := unix.FcntlFlock(uintptr(lockFd), unix.F_OFD_SETLK, &fl); err != nil {
		t.Fatalf("verrou OFD: %v", err)
	}
	if _, err := RepairShardWAL(dir, key); !errors.Is(err, ErrWriterBusy) {
		t.Fatalf("attendu ErrWriterBusy sous verrou, obtenu: %v", err)
	}
}
