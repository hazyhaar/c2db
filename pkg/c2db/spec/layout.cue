package spec

#UuidLayout: close({
	unix_ms_bits:  48
	version_bits:  4
	seq_bits:      12
	variant_bits:  2
	shard_bits:    10
	counter_bits:  52
	total_bits:    128
	version_value: 7
	variant_value: 2
	overlay:       "c2uuidv7.Compose"
	locations:     "offset"
	cas:           "blake3"
	total_bits:    unix_ms_bits + version_bits + seq_bits + variant_bits + shard_bits + counter_bits
})

#HdrOff: close({
	page_id: 0
	hlc:     8
	topo:    16
	type:    20
	flags:   21
	nslots:  22
	free_lo: 24
	free_hi: 26
	crc:     28
	body:    64
})

#WalLayout: close({
	magic_size: 4
	len_size:   4
	id_size:    16
	type_size:  1
	tag_size:   16
	block_size: 4096
	seal:       "poly1305"
	varint:     "quic_2bit_msb"
	logical:    "variable"
	disk:       "block_padded"
})

#IoLayout: close({
	open:     "o_direct"
	write:    "pwrite"
	read:     "pread"
	allocate: "fallocate"
	flush:    "fdatasync"
	forbid: ["mmap_map_shared_write", "io_uring_v1", "ioctl_nvme", "cgo"]
})

#TxLayout: close({
	inhibit_flush_in_group: true
	commit_order:           "durability_barrier_before_publish"
	multiblock_seal:        "tx_commit_record"
	rollback_recovery:      "converge_dirty_from_pub"
})

#Poly1305KdfLayout: close({
	algo:              "blake3_derive_key"
	page_seal_context: "c2db-page-seal/v1"
	page_seal_inputs:  "base_key_shard_pageidx_blake3page"
	wal_seal_context:  "c2db-wal-seal/v1"
	wal_seal_inputs:   "base_key_canonical_block"
	zero_tag_policy:   "unallocated_page_only"
	constant_time_cmp: true
})

#CellLayout: close({
	kind_inline:       0
	kind_overflow:     1
	overflow_desc_len: 13
	overflow_magic:    false
})

#RecoveryLayout: close({
	durable_watermark: "commit_watermark"
	torn_tail_policy:  "truncate_incomplete_headers_only"
	bad_mac_policy:    "fail_closed_error"
})

#DirtyLayout: close({
	dense_list_capacity: 65536
	dense_list_cost:     "O_D"
	bitmap_fallback:     "O_N_div_64_plus_D"
	bytes_equal_scan:    false
})

#CsgLayout: close({
	tree_desc: "by_value"
	page:      "uint8_t_ptr_plus_offset"
	gabarit:   "vmm_virtio_vring.c"
})

#Layout: close({
	page_size:         16384
	lba_size:          4096
	lba_per_page:      4
	header_size:       64
	body_size:         16320
	wal_block:         4096
	shards:            1024
	slot_bytes:        16
	writers_per_shard: 1
	inter_shard_tx:    false
	page_check:        "crc32c"
	page_size:         lba_size * lba_per_page
	page_size:         header_size + body_size
	hdr:               #HdrOff
	uuid:              #UuidLayout
	wal:               #WalLayout
	io:                #IoLayout
	csg:               #CsgLayout
	tx:                #TxLayout
	kdf:               #Poly1305KdfLayout
	cell:              #CellLayout
	recovery:          #RecoveryLayout
	dirty:             #DirtyLayout
})

layout: #Layout
