// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

// PageCRC32CFull calcule le CRC32-C pleine page (16 Ko) masquant les octets 28..31.
func PageCRC32CFull(page []byte) uint32 {
	return C2db_crc32c_fullpage(page, uint64(len(page)))
}

// PageCRC32CFullStore calcule et écrit le CRC32-C pleine page dans les octets 28..31.
func PageCRC32CFullStore(page []byte) bool {
	return C2db_crc32c_fullpage_store(page, uint64(len(page))) == 1
}

// SearchPrefixInLeaf utilise le noyau vectoriel transpilé pour localiser
// par recherche binaire les slots correspondant au préfixe dans une page feuille.
func SearchPrefixInLeaf(page []byte, prefix []byte) (slotIdx, count uint64, found bool) {
	if len(page) != int(pageN) || len(prefix) == 0 {
		return 0, 0, false
	}
	res := C2db_btree_prefix_search(page, pageN, prefix, uint64(len(prefix)))
	return res.Slot_idx, res.Count, res.Found != 0
}

// CompactPageLeaf défragmente une page feuille et purge les tombstones in-place.
func CompactPageLeaf(page, scratch []byte, idLen uint64, dropTombstones bool) (slotsBefore, slotsAfter, bytesFreed uint64, ok bool) {
	drop := byte(0)
	if dropTombstones {
		drop = 1
	}
	res := C2db_slot_pack_compact(page, uint64(len(page)), scratch, uint64(len(scratch)), idLen, drop)
	if res.Ok == 1 {
		_ = C2db_crc32c_fullpage_store(page, uint64(len(page)))
	}
	return res.Slots_before, res.Slots_after, res.Bytes_freed, res.Ok == 1
}

// Zipper3Way effectue la réconciliation sans allocation entre Base, Ours et Theirs.
func Zipper3Way(base, ours, theirs, out []C2db_zip_entry_t) (merged, conflicts uint64, ok bool) {
	res := C2db_zipper_3way(base, uint64(len(base)), ours, uint64(len(ours)), theirs, uint64(len(theirs)), out, uint64(len(out)))
	return res.Merged_count, res.Conflicts_count, res.Ok == 1
}
