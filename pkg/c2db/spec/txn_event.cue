package spec

// Schéma du journal d'événements de transaction (txnlog).
//
// Le journal est append-only, indépendant du WAL, et donc insensible aux
// troncatures de reprise de ce dernier. Chaque mutation, compaction, élagage,
// archivage ou refus produit un enregistrement de disposition binaire FIXE :
// l'en-tête est à champs scalaires positionnés par décalage, la charge utile
// reste OPAQUE (aucune interprétation par ce codec). Ce schéma est la source
// de vérité des décalages et tailles ; le codec Go `txnlog.go` doit coïncider
// avec lui, ce qu'un test de parité vérifie mécaniquement.

#C2TxnEventKind: "mutation" | "compaction" | "prune" | "archive" | "refusal"

#C2TxnEventKindCode: close({
	mutation:   1
	compaction: 2
	prune:      3
	archive:    4
	refusal:    5
})

#C2TxnEvent: close({
	kind:       #C2TxnEventKind
	shard:      uint16
	seq:        uint64
	timestamp:  uint64
	version_id: bytes
	watermark:  uint64
	key:        bytes
	payload:    bytes
})

#C2TxnEventLayout: close({
	magic_value:     0x434C4F47
	version_schema:  1
	header_size:     64
	max_key_len:     65536
	max_payload_len: 16777216

	off_magic:       0
	off_schema:      4
	off_kind:        6
	off_flags:       7
	off_shard:       8
	off_reserved:    10
	off_seq:         12
	off_timestamp:   20
	off_version:     28
	off_watermark:   44
	off_key_len:     52
	off_payload_len: 56
	off_crc:         60

	sz_magic:       4
	sz_schema:      2
	sz_kind:        1
	sz_flags:       1
	sz_shard:       2
	sz_reserved:    2
	sz_seq:         8
	sz_timestamp:   8
	sz_version:     16
	sz_watermark:   8
	sz_key_len:     4
	sz_payload_len: 4
	sz_crc:         4

	kind_codes: #C2TxnEventKindCode
	fields: ["kind", "shard", "seq", "timestamp", "version_id", "watermark", "key", "payload"]
})

txn_event_layout: #C2TxnEventLayout
