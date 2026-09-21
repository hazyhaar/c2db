// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2uuidv7

import (
	"testing"
	"uuid" // Paquet officiel Go 1.27
)

// 1. BENCHMARK : GÉNÉRATION MONOTHREAD
// Justification : Débit pur d'émission d'identifiants par cœur CPU.
// Conscience supprimée : Engorgement CPU dans les boucles de traitement haute fréquence (> 1M/s).

func BenchmarkNewV7Fast_C2(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewV7Fast()
	}
}

func BenchmarkNewV7_C2_Crypto(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewV7()
	}
}

func BenchmarkNewV7_Stdlib(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = uuid.NewV7()
	}
}

// 2. BENCHMARK : GÉNÉRATION MULTITHREAD SOUS HAUTE CONCURRENCE (RunParallel)
// Justification : Mesure de la contention sur le verrou global sync.Mutex de la lib standard.
// Conséquence supprimée : Effondrement catastrophique de latence (Tail Latency P99) et sérialisation des goroutines.

func BenchmarkNewV7Fast_C2_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = NewV7Fast()
		}
	})
}

func BenchmarkNewV7_Stdlib_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = uuid.NewV7()
		}
	})
}

// 3. BENCHMARK : SÉRIALISATION & FORMATAGE HEXADÉCIMAL 36 OCTETS
// Justification : Encodage direct in-place par table ARCHTIME vs 5 sous-appels hex.Encode standard.
// Conséquence supprimée : Pression GC et fragmentation mémoire lors de l'export JSON / logs télémétriques.

func BenchmarkFormat_C2_EncodeHex(b *testing.B) {
	u := NewV7Fast()
	var buf [36]byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u.EncodeHex(&buf)
	}
}

func BenchmarkFormat_Stdlib_AppendText(b *testing.B) {
	u := uuid.NewV7()
	var buf [36]byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = u.AppendText(buf[:0])
	}
}

func BenchmarkFormat_C2_String(b *testing.B) {
	u := NewV7Fast()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = u.String()
	}
}

func BenchmarkFormat_Stdlib_String(b *testing.B) {
	u := uuid.NewV7()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = u.String()
	}
}

// 4. BENCHMARK : ANALYSE & DÉCODAGE (PARSING)
// Justification : Décodage en passe unique branchless vs 5 tranches hex.Decode standard.
// Conséquence supprimée : Surcoût de validation d'entrée dans les routeurs RPC / filtres réseau.

func BenchmarkParse_C2_ParseBytes(b *testing.B) {
	raw := []byte("018f3a5b-7c8d-7e9f-a012-3456789abcde")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseBytes(raw)
	}
}

func BenchmarkParse_Stdlib_UnmarshalText(b *testing.B) {
	raw := []byte("018f3a5b-7c8d-7e9f-a012-3456789abcde")
	var u uuid.UUID
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = u.UnmarshalText(raw)
	}
}
