package spec

#Phase: "pre_engine" | "engine"

#Fixture: close({
	id:          string
	kind:        "nominal" | "limite" | "erreur" | "adversarial"
	description: string
})

#Scenario: close({
	id:     string
	intent: string
	phase:  #Phase
	fixtures: [...string]
	metrics: [...string]
	proves: string
})

fixtures: [...#Fixture] & [
	{id: "snap_lsn_n", kind: "nominal", description: "lecture d'un instantané figé à un LSN donné"},
	{id: "snap_page_warm", kind: "nominal", description: "relecture de la même page déjà présente en tampon"},
	{id: "cow_overlap", kind: "limite", description: "écriture sur clé déjà partagée, vecteur capable de rompre le CoW"},
	{id: "commit_batch", kind: "nominal", description: "plusieurs enregistrements WAL groupés avant un seul Flush"},
	{id: "torn_unflushed", kind: "erreur", description: "crash entre pwrite et fdatasync, bloc présent sans sceau durable"},
	{id: "poly1305_bad_tag", kind: "adversarial", description: "étiquette Poly1305 corrompue d'un octet, rejet du bloc"},
	{id: "shards_1024_empty", kind: "nominal", description: "ouverture des 1024 fragments sans charge utile"},
	{id: "wal_prefix_reclaim", kind: "nominal", description: "récupération du préfixe WAL jusqu'au dernier point de reprise"},
]

scenarios: [...#Scenario] & [
	{
		id:     "snap_read"
		intent: "lecture snapshot"
		phase:  "engine"
		fixtures: ["snap_lsn_n", "snap_page_warm"]
		metrics: ["pages_pread", "bytes_pread", "crc32c_mismatch"]
		proves: "la page relue après instantané conserve le corps slotté et le CRC32-C mémoire"
	},
	{
		id:     "hot_write"
		intent: "écriture chaude"
		phase:  "engine"
		fixtures: ["cow_overlap"]
		metrics: ["cow_breaks", "pages_copied", "bytes_pwrite"]
		proves: "une écriture qui recouvre une page partagée rompt le CoW puis relit la charge utile écrite"
	},
	{
		id:     "group_commit"
		intent: "commit groupé"
		phase:  "engine"
		fixtures: ["commit_batch"]
		metrics: ["records_batched", "wal_blocks_issued", "fdatasync_calls"]
		proves: "N enregistrements logiques tiennent en K blocs 4096 et un seul fdatasync les rend durables"
	},
	{
		id:     "crash_write_flush"
		intent: "crash entre write et flush"
		phase:  "engine"
		fixtures: ["torn_unflushed", "poly1305_bad_tag"]
		metrics: ["records_durable", "records_lost", "poly1305_reject"]
		proves: "reprise jusqu'à l'état durable : fdatasync relus, non flushés absents ou rejetés par Poly1305"
	},
	{
		id:     "shards_empty"
		intent: "1024 shards à vide"
		phase:  "engine"
		fixtures: ["shards_1024_empty"]
		metrics: ["shards_opened", "bytes_fallocate"]
		proves: "les 1024 fragments s'ouvrent sur fichiers préalloués, un écrivain chacun, zéro transaction croisée"
	},
	{
		id:     "gc_prefix"
		intent: "GC prefix"
		phase:  "engine"
		fixtures: ["wal_prefix_reclaim"]
		metrics: ["prefix_bytes_reclaimed", "wal_blocks_kept"]
		proves: "le préfixe antérieur au point de reprise disparaît ; les blocs conservés se relisent bit-exact"
	},
]
