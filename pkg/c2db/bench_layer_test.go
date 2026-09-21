// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"testing"
)

func BenchmarkL2_BtreeInsertVerHeap(b *testing.B) {
	const np = uint64(16)
	nbytes := np * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok == 0 {
		b.Fatal("leaf_init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	id := make([]byte, 16)
	key := []byte("layer2-key")
	val := []byte("layer2-val-0123456789")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id[15] = byte(i)
		id[14] = byte(i >> 8)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, np, &hst, key, uint64(len(key)), id, val, uint64(len(val)))
		if got.Ok == 0 {
			copy(pub, dirty)
			hst = Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
			st = Db_bt_leaf_init(pub, pageN)
			copy(dirty, pub)
			continue
		}
		hst = got
		copy(pub, dirty)
	}
}
