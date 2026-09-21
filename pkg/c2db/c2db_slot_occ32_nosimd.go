//go:build !amd64 || !goexperiment.simd || js || wasm

package c2db

func C2db_slot_occ32_avx2(p []byte) uint32 {
	return C2db_slot_occ32(p)
}
