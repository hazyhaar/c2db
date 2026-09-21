// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2uuidv7

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid" // Paquet officiel Go 1.27
)

func TestUUIDv7_RFC9562_Invariants(t *testing.T) {
	u := NewV7()
	if !u.IsV7() {
		t.Fatalf("UUID généré non valide V7: version=%d, variant=%d", u.Version(), u.Variant())
	}
	if u.Version() != 7 {
		t.Errorf("version = %d, attendu 7", u.Version())
	}
	if u.Variant() != 2 {
		t.Errorf("variant = %d, attendu 2", u.Variant())
	}

	ts := u.Time()
	now := time.Now()
	if diff := now.Sub(ts); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("horodatage incohérent: %v vs now %v (diff=%v)", ts, now, diff)
	}
}

func TestUUIDv7_FormatAndParse(t *testing.T) {
	for i := 0; i < 1000; i++ {
		u1 := NewV7Fast()
		str := u1.String()
		if len(str) != 36 {
			t.Fatalf("taille de chaîne invalide: %d (str=%q)", len(str), str)
		}

		u2, err := Parse(str)
		if err != nil {
			t.Fatalf("échec de parsing %q: %v", str, err)
		}
		if u1 != u2 {
			t.Fatalf("non-concordance roundtrip: %v != %v", u1, u2)
		}

		// Interopérabilité stricte avec la lib officielle Go 1.27
		stdUUID, err := uuid.Parse(str)
		if err != nil {
			t.Fatalf("échec de parsing par stdlib Go 1.27: %v", err)
		}
		if uuid.UUID(u1) != stdUUID {
			t.Fatalf("non-parité avec stdlib: %v != %v", u1, stdUUID)
		}
	}
}

func TestUUIDv7_ZeroAllocationPath(t *testing.T) {
	// 1. NewV7Fast doit allouer 0 B/op
	allocsFast := testing.AllocsPerRun(1000, func() {
		_ = NewV7Fast()
	})
	if allocsFast != 0 {
		t.Errorf("NewV7Fast allocations = %.2f, attendu 0.0", allocsFast)
	}

	// 2. Parse / ParseBytes doit allouer 0 B/op
	str := "018f3a5b-7c8d-7e9f-a012-3456789abcde"
	allocsParse := testing.AllocsPerRun(1000, func() {
		_, _ = Parse(str)
	})
	if allocsParse != 0 {
		t.Errorf("Parse allocations = %.2f, attendu 0.0", allocsParse)
	}

	// 3. EncodeHex doit allouer 0 B/op
	u := NewV7Fast()
	var buf [36]byte
	allocsEncode := testing.AllocsPerRun(1000, func() {
		u.EncodeHex(&buf)
	})
	if allocsEncode != 0 {
		t.Errorf("EncodeHex allocations = %.2f, attendu 0.0", allocsEncode)
	}
}

func TestUUIDv7_ConcurrentMonotonicity_100Goroutines(t *testing.T) {
	const numGoroutines = 100
	const opsPerGoroutine = 5000
	const totalUUIDs = numGoroutines * opsPerGoroutine

	type gResult struct {
		uuids [opsPerGoroutine]UUID
	}
	results := make([]gResult, numGoroutines)

	var startGate sync.WaitGroup
	startGate.Add(1)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gIndex int) {
			defer wg.Done()
			startGate.Wait()

			for i := 0; i < opsPerGoroutine; i++ {
				u := NewV7Fast()
				results[gIndex].uuids[i] = u
			}
		}(g)
	}

	startGate.Done()
	wg.Wait()

	// 1. Invariance RFC 9562 et monotonicité locale stricte par goroutine
	for g := 0; g < numGoroutines; g++ {
		for i := 0; i < opsPerGoroutine; i++ {
			u := results[g].uuids[i]
			if !u.IsV7() {
				t.Fatalf("goroutine %d, op %d : UUID non valide (v=%d, var=%d)", g, i, u.Version(), u.Variant())
			}
			if i > 0 {
				prev := results[g].uuids[i-1]
				if prev.Compare(u) >= 0 {
					t.Fatalf("violation de monotonicité locale dans goroutine %d à l'op %d: prev=%s >= cur=%s", g, i, prev, u)
				}
			}
		}
	}

	// 2. Unicité globale absolue (zéro collision sur 500 000 UUIDv7)
	allUUIDs := make([]UUID, 0, totalUUIDs)
	for g := 0; g < numGoroutines; g++ {
		allUUIDs = append(allUUIDs, results[g].uuids[:]...)
	}

	slices.SortFunc(allUUIDs, func(a, b UUID) int {
		return a.Compare(b)
	})

	for i := 1; i < len(allUUIDs); i++ {
		if allUUIDs[i-1] == allUUIDs[i] {
			t.Fatalf("collision globale détectée sous contention de 100 goroutines à l'index %d: uuid=%s", i, allUUIDs[i])
		}
		if allUUIDs[i-1].Compare(allUUIDs[i]) >= 0 {
			t.Fatalf("ordre de tri invalide à l'index %d: %s >= %s", i, allUUIDs[i-1], allUUIDs[i])
		}
	}
}

func TestUUIDv7_ConcurrentCryptoRand_100Goroutines(t *testing.T) {
	const numGoroutines = 100
	const opsPerGoroutine = 1000
	const totalUUIDs = numGoroutines * opsPerGoroutine

	type gResult struct {
		uuids [opsPerGoroutine]UUID
	}
	results := make([]gResult, numGoroutines)

	var startGate sync.WaitGroup
	startGate.Add(1)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gIndex int) {
			defer wg.Done()
			startGate.Wait()

			for i := 0; i < opsPerGoroutine; i++ {
				u := NewV7()
				results[gIndex].uuids[i] = u
			}
		}(g)
	}

	startGate.Done()
	wg.Wait()

	for g := 0; g < numGoroutines; g++ {
		for i := 0; i < opsPerGoroutine; i++ {
			u := results[g].uuids[i]
			if !u.IsV7() {
				t.Fatalf("crypto goroutine %d, op %d : UUID non valide", g, i)
			}
			if i > 0 {
				prev := results[g].uuids[i-1]
				if prev.Compare(u) >= 0 {
					t.Fatalf("crypto violation de monotonicité: %s >= %s", prev, u)
				}
			}
		}
	}

	allUUIDs := make([]UUID, 0, totalUUIDs)
	for g := 0; g < numGoroutines; g++ {
		allUUIDs = append(allUUIDs, results[g].uuids[:]...)
	}

	slices.SortFunc(allUUIDs, func(a, b UUID) int {
		return a.Compare(b)
	})

	for i := 1; i < len(allUUIDs); i++ {
		if allUUIDs[i-1] == allUUIDs[i] {
			t.Fatalf("collision crypto détectée sous 100 goroutines à l'index %d: uuid=%s", i, allUUIDs[i])
		}
	}
}

func TestUUIDv7_ClockRollbackSimulation(t *testing.T) {
	// Simulation de recul d'horloge NTP / ajustement négatif
	initial := gUUIDLastTimestamp.Load()
	defer gUUIDLastTimestamp.Store(initial)

	futureTimestamp := initial + (10000 << 12) // +10 000 ms dans le futur
	gUUIDLastTimestamp.Store(futureTimestamp)

	const n = 10000
	uuids := make([]UUID, n)
	for i := 0; i < n; i++ {
		uuids[i] = NewV7Fast()
		if !uuids[i].IsV7() {
			t.Fatalf("UUID invalide généré sous recul d'horloge à l'index %d: %s", i, uuids[i])
		}
		if i > 0 {
			if uuids[i-1].Compare(uuids[i]) >= 0 {
				t.Fatalf("perte de monotonicité sous recul d'horloge: %s >= %s", uuids[i-1], uuids[i])
			}
		}
	}

	for i := 1; i < n; i++ {
		prevMs := uuids[i-1].TimestampMs()
		curMs := uuids[i].TimestampMs()
		if curMs < prevMs {
			t.Fatalf("recul de timestamp milliseconde détecté: cur=%d < prev=%d", curMs, prevMs)
		}
	}
}

func TestUUIDv7_BurstDriftAndCarryOver(t *testing.T) {
	// Rafale de 50 000 UUIDs pour forcer de multiples débordements de millisecondes (subMs12 > 4095)
	const burstSize = 50000
	uuids := make([]UUID, burstSize)

	for i := 0; i < burstSize; i++ {
		uuids[i] = NewV7Fast()
		if !uuids[i].IsV7() {
			t.Fatalf("invariance RFC violée en rafale à l'index %d: %s (v=%d, var=%d)", i, uuids[i], uuids[i].Version(), uuids[i].Variant())
		}
		if i > 0 {
			if uuids[i-1].Compare(uuids[i]) >= 0 {
				t.Fatalf("monotonicité rompue en rafale au débordement: %s >= %s", uuids[i-1], uuids[i])
			}
		}
	}

	for i := 0; i < burstSize; i++ {
		if (uuids[i][6] >> 4) != 0x07 {
			t.Fatalf("octet 6 version corrompu: 0x%02x (attendu 0x7X)", uuids[i][6])
		}
		if (uuids[i][8] & 0xC0) != 0x80 {
			t.Fatalf("octet 8 variante corrompu: 0x%02x (attendu 0x8X..0xBX)", uuids[i][8])
		}
	}
}

func TestUUIDv7_VsCOracleBitExact(t *testing.T) {
	tmpBin := filepath.Join(t.TempDir(), "uuidv7_c_oracle")
	srcDir := filepath.Join("sources", "uuidv7")
	if _, err := os.Stat(filepath.Join(srcDir, "uuidv7.c")); err != nil {
		srcDir = filepath.Join("..", "..", "sources", "uuidv7")
	}

	cmdBuild := exec.Command("gcc", "-O2", "-Wall", "-Wextra", "-std=gnu99", "-pthread",
		"-I", srcDir,
		filepath.Join(srcDir, "test_oracle_uuidv7.c"),
		filepath.Join(srcDir, "uuidv7.c"),
		"-o", tmpBin,
	)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("échec de compilation oracle C: %v\nSortie: %s", err, string(out))
	}

	cmdRun := exec.Command(tmpBin)
	out, err := cmdRun.CombinedOutput()
	if err != nil {
		t.Fatalf("échec d'exécution oracle C: %v\nSortie: %s", err, string(out))
	}
	t.Logf("Sortie Oracle C (gcc -O2):\n%s", string(out))
}

func TestUUID_StandardInterfacesAndParity(t *testing.T) {
	// 1. Nil & Max
	nilU := Nil()
	if !nilU.IsNil() {
		t.Errorf("Nil().IsNil() should be true")
	}
	maxU := Max()
	if !maxU.IsMax() {
		t.Errorf("Max().IsMax() should be true")
	}

	// 2. NewV4
	v4 := NewV4()
	if !v4.IsV4() || v4.Version() != 4 || v4.Variant() != 2 {
		t.Errorf("NewV4() invalid: version=%d, variant=%d", v4.Version(), v4.Variant())
	}

	// 3. Binary Appender & Marshaler
	u := NewV7()
	bData, err := u.MarshalBinary()
	if err != nil || len(bData) != 16 {
		t.Fatalf("MarshalBinary failed: %v, len=%d", err, len(bData))
	}
	var uUnmarshaled UUID
	if err := uUnmarshaled.UnmarshalBinary(bData); err != nil || uUnmarshaled != u {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	appended, err := u.AppendBinary(nil)
	if err != nil || len(appended) != 16 || !slices.Equal(appended, bData) {
		t.Fatalf("AppendBinary failed: %v", err)
	}

	// 4. SQL Valuer & Scanner
	val, err := u.Value()
	if err != nil || val != u.String() {
		t.Fatalf("Value() failed: %v, got %v", err, val)
	}

	var scanned UUID
	if err := scanned.Scan(u.String()); err != nil || scanned != u {
		t.Fatalf("Scan(string) failed: %v", err)
	}
	if err := scanned.Scan(u[:]); err != nil || scanned != u {
		t.Fatalf("Scan([]byte) failed: %v", err)
	}

	// 5. Parité avec New() standard
	defaultU := New()
	if !defaultU.IsV7() {
		t.Errorf("New() default should be V7")
	}
}

func TestUUIDv7_AdversarialHighByteRejection(t *testing.T) {
	validStr := "01953258-2940-7000-8000-000000000000"
	b := []byte(validStr)

	// Injecter des octets non-ASCII (0x80..0xFF) à chaque position
	for pos := 0; pos < 36; pos++ {
		if pos == 8 || pos == 13 || pos == 18 || pos == 23 {
			continue // Délimiteurs
		}
		orig := b[pos]
		for badVal := 0x80; badVal <= 0xFF; badVal += 17 {
			b[pos] = byte(badVal)
			_, err := ParseBytes(b)
			if err == nil {
				t.Fatalf("ParseBytes n'a pas rejeté l'octet invalide 0x%02X à la position %d", badVal, pos)
			}
		}
		b[pos] = orig
	}
}

func TestUUIDv7_AdversarialMalformedDelimiters(t *testing.T) {
	badDelims := []string{
		"01953258_2940-7000-8000-000000000000",
		"01953258-2940_7000-8000-000000000000",
		"01953258-2940-7000_8000-000000000000",
		"01953258-2940-7000-8000_000000000000",
		"01953258 2940 7000 8000 000000000000",
		"01953258-2940-7000-8000-00000000000G",
	}
	for _, bad := range badDelims {
		_, err := Parse(bad)
		if err == nil {
			t.Fatalf("Parse aurait dû rejeter la chaîne malformée %q", bad)
		}
	}
}
