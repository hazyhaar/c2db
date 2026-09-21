//go:build !amd64 || !goexperiment.simd || js || wasm

package c2db

func Page4k_is_zero_avx2(page []byte, n uint64) byte {
	if page == nil || n != 4096 || uint64(len(page)) < 4096 {
		return 0
	}
	for i := 0; i < 4096; i++ {
		if page[i] != 0 {
			return 0
		}
	}
	return 1
}
