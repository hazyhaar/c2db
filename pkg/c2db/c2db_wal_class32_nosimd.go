//go:build !amd64 || !goexperiment.simd || js || wasm

package c2db

// handwrite: C2db_wal_class32_avx2 appelle le scalaire émis (intrinsèque absente hors amd64+simd).

func C2db_wal_class32_avx2(in []byte, out []byte) {
	C2db_wal_class32(in, out)
}
