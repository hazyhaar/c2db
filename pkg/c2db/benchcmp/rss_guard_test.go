// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"unsafe"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"golang.org/x/sys/unix"
)

const (
	rssGuardOps     = 20000
	rssWorkKeys     = 4096
	rssQuotaMax     = 64 << 20
	rssLeakMax      = 16 << 20
	rssCacheSlack   = 4 << 20
	rssSQLiteCache  = 32 << 10
	rssSampleEvery  = 256
	rssCompactEvery = 2500
)

type rssSnap struct {
	Resident uint64
	MaxRSS   uint64
	CachedKB int64
}

func TestRSSGuard_ODirectIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du garde RSS sous -short")
	}
	dir := t.TempDir()
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 19)
	}
	c2dir := filepath.Join(dir, "c2")
	if err := os.MkdirAll(c2dir, 0o700); err != nil {
		t.Fatal(err)
	}

	reclaimRSS()
	initSnap := snapRSS(t)
	ks, vs, prov := loadRealBenchmarkKV(t, rssWorkKeys)
	t.Logf("Provenance corpus réel: %s", prov)
	rng := rand.New(rand.NewSource(0x525353))

	s, err := c2db.OpenShard(c2dir, mac, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("OpenShard: %v", err)
	}

	beforeIO := snapProbe()
	peak := initSnap.Resident
	sample := func() {
		r := readRSS(t)
		if r > peak {
			peak = r
		}
	}
	sample()

	// Contrôle d'isolation O_DIRECT et d'empreinte mémoire MOTEUR OUVERT (exigence R2.6)
	var rssPaliers []uint64
	checkLiveMincoreAndRSS := func(stepName string, stepOps int) {
		t.Helper()
		dataCached, dataSize, err := fileCachedBytes(filepath.Join(c2dir, "data.img"))
		if err != nil {
			t.Fatalf("[%s] mincore data.img: %v", stepName, err)
		}
		// R2.6 : Isolation O_DIRECT stricte moteur ouvert : AUCUN octet de data.img ne doit résider en page cache OS
		if dataCached > 0 {
			t.Fatalf("[%s] violation O_DIRECT moteur ouvert: data.img pollue le cache noyau (%d / %d octets en cache)",
				stepName, dataCached, dataSize)
		}
		curRSS := readRSS(t)
		rssPaliers = append(rssPaliers, curRSS)
		t.Logf("[%s ops=%d] MOTEUR OUVERT: cached_data=%d/%d (isolation O_DIRECT stricte), RSS=%d Mio",
			stepName, stepOps, dataCached, dataSize, curRSS>>20)
	}

	put := func(k, v []byte) {
		t.Helper()
		err := s.Put(k, v)
		if errors.Is(err, c2db.ErrHeapFull) {
			if cErr := s.Compact(); cErr != nil {
				t.Fatalf("Compact: %v", cErr)
			}
			err = s.Put(k, v)
		}
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	const quarter = rssGuardOps / 4
	// Phase 1 : écritures séquentielles
	for i := 0; i < quarter; i++ {
		idx := i % rssWorkKeys
		put(ks[idx], vs[idx])
		if i%rssCompactEvery == rssCompactEvery-1 {
			if err := s.Compact(); err != nil {
				t.Fatalf("Compact seq: %v", err)
			}
		}
		if i%rssSampleEvery == 0 {
			sample()
		}
	}
	checkLiveMincoreAndRSS("Palier 1 - écritures séquentielles", quarter)

	// Phase 2 : lectures séquentielles
	for i := 0; i < quarter; i++ {
		idx := i % rssWorkKeys
		got, err := s.Get(ks[idx])
		if err != nil {
			t.Fatalf("Get seq %d: %v", i, err)
		}
		if !bytes.Equal(got, vs[idx]) {
			t.Fatalf("Get seq %d: divergence bit-exacte got=%x want=%x", i, got, vs[idx])
		}
		if i%rssSampleEvery == 0 {
			sample()
		}
	}
	checkLiveMincoreAndRSS("Palier 2 - lectures séquentielles", quarter*2)

	// Phase 3 : écritures aléatoires
	for i := 0; i < quarter; i++ {
		idx := rng.Intn(rssWorkKeys)
		put(ks[idx], vs[idx])
		if i%rssCompactEvery == rssCompactEvery-1 {
			if err := s.Compact(); err != nil {
				t.Fatalf("Compact rand: %v", err)
			}
		}
		if i%rssSampleEvery == 0 {
			sample()
		}
	}
	checkLiveMincoreAndRSS("Palier 3 - écritures aléatoires", quarter*3)

	// Phase 4 : lectures aléatoires
	for i := 0; i < quarter; i++ {
		idx := rng.Intn(rssWorkKeys)
		got, err := s.Get(ks[idx])
		if err != nil {
			t.Fatalf("Get rand %d: %v", i, err)
		}
		if !bytes.Equal(got, vs[idx]) {
			t.Fatalf("Get rand %d: divergence bit-exacte got=%x want=%x", i, got, vs[idx])
		}
		if i%rssSampleEvery == 0 {
			sample()
		}
	}
	checkLiveMincoreAndRSS("Palier 4 - lectures aléatoires", quarter*4)

	sample()
	ioDelta := snapProbe().Sub(beforeIO)

	// Contrôle de stabilité d'empreinte moteur ouvert : excursion globale max(RSS) - min(RSS) sur TOUS les paliers
	if len(rssPaliers) >= 2 {
		minPalier := rssPaliers[0]
		maxPalier := rssPaliers[0]
		for _, v := range rssPaliers {
			if v < minPalier {
				minPalier = v
			}
			if v > maxPalier {
				maxPalier = v
			}
		}
		excursionOpen := maxPalier - minPalier
		if excursionOpen > rssLeakMax {
			t.Fatalf("excursion RSS moteur ouvert excessive entre paliers: max=%d Mio min=%d Mio excursion=%d Mio > max %d Mio",
				maxPalier>>20, minPalier>>20, excursionOpen>>20, rssLeakMax>>20)
		}
		t.Logf("MOTEUR OUVERT : excursion globale RSS inter-paliers (max-min) = %d Ko (< plafond de fuite %d Mio)",
			excursionOpen>>10, rssLeakMax>>20)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dataCached, dataSize, err := fileCachedBytes(filepath.Join(c2dir, "data.img"))
	if err != nil {
		t.Fatalf("mincore data.img: %v", err)
	}
	walCached, walSize, err := fileCachedBytes(filepath.Join(c2dir, "wal.img"))
	if err != nil {
		t.Fatalf("mincore wal.img: %v", err)
	}
	tagsCached, _, err := fileCachedBytes(filepath.Join(c2dir, "tags.img"))
	if err != nil {
		t.Fatalf("mincore tags.img: %v", err)
	}
	c2Cached := dataCached + walCached + tagsCached

	reclaimRSS()
	stable := snapRSS(t)
	deltaPeak := int64(peak) - int64(initSnap.Resident)
	deltaStable := int64(stable.Resident) - int64(initSnap.Resident)
	if deltaPeak < 0 {
		deltaPeak = 0
	}
	if deltaStable < 0 {
		deltaStable = 0
	}

	t.Logf("rss_init=%d rss_peak=%d rss_stable=%d delta_peak=%d delta_stable=%d quota=%d maxrss_kb=%d→%d",
		initSnap.Resident, peak, stable.Resident, deltaPeak, deltaStable, rssQuotaMax,
		initSnap.MaxRSS, stable.MaxRSS)
	t.Logf("c2db io write_bytes=%d sysc_w=%d cached_data=%d/%d cached_wal=%d/%d cached_sum=%d meminfo_cached_kb=%d→%d",
		ioDelta.WriteBytes, ioDelta.SyscWrite, dataCached, dataSize, walCached, walSize, c2Cached,
		initSnap.CachedKB, stable.CachedKB)

	if ioDelta.WriteBytes == 0 && ioDelta.SyscWrite == 0 {
		t.Fatalf("aucune écriture noyau après %d opérations: %+v", rssGuardOps, ioDelta)
	}
	if uint64(deltaPeak) > rssQuotaMax {
		t.Fatalf("ΔRSS pic %d dépasse QuotaMax %d (pools de pages c2db)", deltaPeak, rssQuotaMax)
	}
	if uint64(deltaStable) > rssLeakMax {
		t.Fatalf("ΔRSS après GC %d dépasse le plafond de fuite %d", deltaStable, rssLeakMax)
	}
	if c2Cached > rssCacheSlack {
		t.Fatalf("O_DIRECT a peuplé le page cache: cached=%d slack=%d write_bytes=%d", c2Cached, rssCacheSlack, ioDelta.WriteBytes)
	}
}

func TestRSSGuard_SQLiteBufferCache(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du garde RSS sous -short")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "s.db")
	db, err := openSQLiteBuffered(dbPath)
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}

	ks, vs, _ := loadRealBenchmarkKV(t, rssWorkKeys)
	rng := rand.New(rand.NewSource(0x51544c))
	const quarter = rssGuardOps / 4
	for i := 0; i < quarter; i++ {
		idx := i % rssWorkKeys
		if _, err := db.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, ks[idx], vs[idx]); err != nil {
			t.Fatalf("sqlite seq put %d: %v", i, err)
		}
	}
	for i := 0; i < quarter; i++ {
		idx := i % rssWorkKeys
		var v []byte
		if err := db.QueryRow(`SELECT v FROM kv WHERE k=?`, ks[idx]).Scan(&v); err != nil {
			t.Fatalf("sqlite seq get %d: %v", i, err)
		}
		if !bytes.Equal(v, vs[idx]) {
			t.Fatalf("sqlite seq get %d: divergence bit-exacte got=%x want=%x", i, v, vs[idx])
		}
	}
	for i := 0; i < quarter; i++ {
		idx := rng.Intn(rssWorkKeys)
		if _, err := db.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, ks[idx], vs[idx]); err != nil {
			t.Fatalf("sqlite rand put %d: %v", i, err)
		}
	}
	for i := 0; i < quarter; i++ {
		idx := rng.Intn(rssWorkKeys)
		var v []byte
		if err := db.QueryRow(`SELECT v FROM kv WHERE k=?`, ks[idx]).Scan(&v); err != nil {
			t.Fatalf("sqlite rand get %d: %v", i, err)
		}
		if !bytes.Equal(v, vs[idx]) {
			t.Fatalf("sqlite rand get %d: divergence bit-exacte got=%x want=%x", i, v, vs[idx])
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("sqlite Close: %v", err)
	}

	dbCached, dbSize, err := fileCachedBytes(dbPath)
	if err != nil {
		t.Fatalf("mincore sqlite: %v", err)
	}
	walCached, walSize, _ := fileCachedBytes(dbPath + "-wal")
	sqlCached := dbCached + walCached
	t.Logf("sqlite cached_db=%d/%d cached_wal=%d/%d sum=%d", dbCached, dbSize, walCached, walSize, sqlCached)
	if sqlCached < rssSQLiteCache {
		t.Fatalf("SQLite sans O_DIRECT n'a pas peuplé le buffer cache OS: cached=%d want>=%d", sqlCached, rssSQLiteCache)
	}
}

func openSQLiteBuffered(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (k BLOB PRIMARY KEY, v BLOB)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func reclaimRSS() {
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
}

func snapRSS(t *testing.T) rssSnap {
	t.Helper()
	return rssSnap{
		Resident: readRSS(t),
		MaxRSS:   readMaxRSSKB(),
		CachedKB: readMeminfoKB("Cached"),
	}
}

func readRSS(t *testing.T) uint64 {
	t.Helper()
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		t.Fatalf("/proc/self/statm: %v", err)
	}
	var total, resident uint64
	if _, err := fmt.Sscanf(string(data), "%d %d", &total, &resident); err != nil {
		t.Fatalf("parse statm %q: %v", data, err)
	}
	return resident * uint64(os.Getpagesize())
}

func readMaxRSSKB() uint64 {
	var ru unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return uint64(ru.Maxrss)
}

func readMeminfoKB(key string) int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	want := key + ":"
	for _, line := range splitLines(data) {
		if len(line) < len(want) || line[:len(want)] != want {
			continue
		}
		var kb int64
		if _, err := fmt.Sscanf(line[len(want):], "%d", &kb); err == nil {
			return kb
		}
	}
	return -1
}

func splitLines(b []byte) []string {
	out := make([]string, 0, 64)
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}

func fileCachedBytes(path string) (cached, size uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	if st.Size() <= 0 {
		return 0, 0, nil
	}
	page := os.Getpagesize()
	mapped := int(st.Size())
	mapped -= mapped % page
	if mapped <= 0 {
		return 0, uint64(st.Size()), nil
	}
	buf, err := unix.Mmap(int(f.Fd()), 0, mapped, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return 0, uint64(st.Size()), err
	}
	defer unix.Munmap(buf)
	vec := make([]byte, (len(buf)+page-1)/page)
	if _, _, errno := unix.Syscall(unix.SYS_MINCORE, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&vec[0]))); errno != 0 {
		return 0, uint64(st.Size()), errno
	}
	var n uint64
	for _, v := range vec {
		if v&1 != 0 {
			n++
		}
	}
	return n * uint64(page), uint64(st.Size()), nil
}
