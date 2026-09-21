package spec

#KernelStatus: "proposed" | "reuse" | "out_of_schema" | "landed"

#Kernel: close({
	id:        string
	status:    #KernelStatus
	is_kernel: bool
	kind:      "new_couple" | "table" | "reuse_existing" | "excluded"
	depends_on_emit?: [...string]
	reuses?: [...string]
	varint?:            string
	nibble_factorable?: bool
	reason?:            string
})

kernels: [...#Kernel] & [
	{
		id:        "c2db_slot_occ32"
		status:    "landed"
		is_kernel: true
		kind:      "new_couple"
		depends_on_emit: ["cmpeq", "movemask"]
		reason: "couple sgoiter : _mm256_cmpeq_epi8.Equal + _mm256_movemask_epi8.ToBits, repli scalaire"
	},
	{
		id:        "c2db_wal_class32"
		status:    "landed"
		is_kernel: true
		kind:      "table"
		reuses: ["vint_lens32", "lut16"]
		varint: layout.wal.varint
		reason: "varint 2 bits MSB (QUIC RFC 9000 §16) ; table lut16 sgoiterée, KAT + oracle gcc"
	},
	{
		id:        "c2db_key_class32"
		status:    "landed"
		is_kernel: true
		kind:      "table"
		reuses: ["lut16"]
		nibble_factorable: true
		reason:            "nibble 0-9 classe 1, 10-15 classe 2 ; table lut16 sgoiterée"
	},
	{
		id:        "c2db_id_prefix16"
		status:    "reuse"
		is_kernel: false
		kind:      "reuse_existing"
		reuses: ["hex_encode16"]
		reason: "réemploi hex_encode16, pas un noyau"
	},
	{
		id:        "c2db_delta_mode32"
		status:    "out_of_schema"
		is_kernel: false
		kind:      "excluded"
		reason:    "reduction_de_bloc"
	},
]
