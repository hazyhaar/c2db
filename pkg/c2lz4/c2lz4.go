package c2lz4

// Decompress décompresse un bloc LZ4 vers dst de façon sécurisée.
func Decompress(src []byte, dst []byte) (int, bool) {
	if len(src) == 0 || len(dst) == 0 {
		return 0, false
	}
	n := Lz4_decompress_safe(src, len(src), dst, len(dst))
	if n < 0 {
		return 0, false
	}
	return n, true
}

// Compress compresse un bloc src vers dst avec LZ4 rapide.
func Compress(src []byte, dst []byte) int {
	if len(src) == 0 || len(dst) == 0 {
		return 0
	}
	return Lz4_compress_fast(src, len(src), dst, len(dst))
}
