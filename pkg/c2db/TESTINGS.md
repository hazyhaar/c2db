# Manuel de qualification — pont VFS et moteur c2db

Fichier d’autorité : `/devhoros/c2simd/c2pkg/c2db/TESTINGS.md`.
Paquet sous test : `/devhoros/c2simd/c2pkg/c2db`.
Toolchain : `GOTOOLCHAIN=go1.27.0`, `GOEXPERIMENT=simd`.
Répertoire d’isolement disque : `DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures` (repli identique dans le harnais hostile si la variable est vide).

Ce manuel décrit ce que les suites exécutent, les oracles qu’elles opposent, et les commandes qui les reproduisent. Les chiffres proviennent des constantes du source et des campagnes mesurées sous ThreadSanitizer. Un résultat qui n’est pas ancré à un fichier de test ou à une campagne datée n’a pas sa place ici.

---

## 1. Doctrine et principes de qualification c2db

### 1.1 Ciblage strict des tests

L’exécution massive `go test ./...` depuis la racine du module ou du workspace est interdite. Les tests de ce manuel s’exécutent exclusivement sur le paquet ciblé :

```
./c2pkg/c2db
```

depuis `/devhoros/c2simd`. Les sous-paquets `./c2pkg/c2db/benchcmp`, `./c2pkg/c2db/ci` et `./c2pkg/c2db/cmd/c2db-verify` ne sont pas convoqués par une commande de qualification VFS/c2db : chacun a son propre banc.

Un filtre `-run` borne encore le sous-ensemble. La qualification complète du paquet (section 4.4) reste un `go test` **du paquet**, pas une récursion de module.

### 1.2 Anti-factive et intégrité expérimentale

Aucun test de ce paquet n’est autorisé à conclure sur des tuples de complaisance (`randint` décoratif, bruit blanc, constante recopiée dans l’assertion). Les transitions doivent traverser le moteur :

- **c2db** : `OpenDB` / `OpenShard`, `Put` / `PutBatch` / `Get` / `GetAsOf`, `Commit` / `Flush`, replay WAL, compaction `rebuildLatestLocked`.
- **SQLite sur VFS** : `sql.Open("sqlite", "file:…?vfs=…")`, `BEGIN` / `COMMIT` / `ROLLBACK`, `SELECT` / `UPDATE` / `INSERT`, puis `PRAGMA integrity_check`.

Les oracles chiffrés sont explicites :

| Oracle | Seuil | Ancrage |
| :--- | :--- | :--- |
| Conservation de la masse monétaire du banc hostile | `delta == 0.000000e+00` (tolérance de comparaison `1e-4` dans le source, log `%.6f` / `%e`) | `vfs_integration_fixture_test.go` |
| Intégrité B-tree SQLite | `PRAGMA integrity_check = ok` | mêmes fixtures d’intégration |
| Colocalisation Voie C | `shards observés == 1` | même banc, après fermeture du pool |
| Soldes individuels | égalité compte par compte contre le journal d’audit mutexé | même banc |
| Parité bit-exacte noyaux transpilés | égalité octet à octet contre `gcc -O2` | `Test*VsCOracle` |

Le générateur `math/rand/v2` du banc hostile n’invente pas le résultat : il choisit uniquement le couple (compte source, compte destination, montant 1–20) d’un **virement SQL réel**. L’oracle est le journal d’audit des commits réussis, pas la graine.

### 1.3 Sanitizer et environnement d’exécution

Toute qualification de ce paquet s’exécute sous ThreadSanitizer :

```
DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures \
GOTOOLCHAIN=go1.27.0 \
GOEXPERIMENT=simd \
go test -race -count=1 ./c2pkg/c2db
```

`-count=1` interdit le cache de résultats. `DEVHOROS_TMPDIR` isole les images `data.img` / `wal.img` hors de `/tmp` système. Le banc hostile crée `fuzz_<pid>_<nano>` sous cette racine et le détruit en `defer`.

Go 1.26 est hors contrat : le codegen scalaire y régresse et invalide tout chiffre SIMD (doctrine `/devhoros/c2simd/CLAUDE.md`).

---

## 2. Cartographie des suites de tests c2db et VFS

### 2.1 Pont VFS et fichiers virtuels

#### `vfs_file_test.go` — 27 tests unitaires

Granularité : `BlockSize = 4096`. Tampon sale : `dirtyFlushThreshold = 256` pages, soit 1 Mio. Masquage zéro-bloc : noyau `Page4k_is_zero_avx2` avec repli scalaire 0-alloc par mots de 8 octets (`isZeroBlock4K` dans `vfs_file.go`).

| Test | Contrat prouvé |
| :--- | :--- |
| `TestVFSFile_BlockBoundaries` | Écritures chevauchant 1, 2 et 3 frontières 4 Ko (offset 4090 / 20 octets, puis 2 et 3 blocs). |
| `TestVFSFile_PartialWriteNeighbors` | RMW partiel au milieu d’un bloc : plage 1000–1199 mutée en `'B'`, voisins `'A'` intacts ; queue 4000–4095 en `'C'`. |
| `TestVFSFile_ShortRead` | Lecture sparse / courte : `ErrShortRead`, `n` exact, reliquat du tampon hostile (`0xFF` / `0xFE` / `0xAA`) écrasé à `0x00`. |
| `TestVFSFile_TruncateReExtend` | Truncate puis ré-extension : aucun octet fantôme ressuscité. |
| `TestVFSFile_Concurrency` | 8 goroutines × 50 itérations d’écriture/lecture sur un même handle. |
| `TestVFSFile_Generation` | Génération initiale = 1 ; `Delete` + `Create` incrémente ; l’ancien handle rend `ErrStaleHandle`. |
| `TestVFSFile_SparseZeroBlock` | Bloc entièrement nul : `Delete` du `BlockKey`, relecture = 4096 zéros. |
| `TestVFSFile_RealC2DB` | Cycle de vie complet adossé à un `*DB` disque réel (`OpenDB` + `O_DIRECT`). |
| `TestVFSFile_MultiHandleSharedMutex` | Deux handles du même `(storage, tenant, fileID)` partagent `sharedFileState` ; le dernier `Close` déréférence le registre. |
| `TestVFSFile_ArithmeticOverflow` | Offsets négatifs et `off + len > math.MaxInt64` → `ErrInvalidOffset` / `ErrInvalidSize` (forme soustractive anti-wrap). |
| `TestVFSFile_ColocatedShard` | Adaptateur `ShardColocatedStorage` : fichier virtuel et blocs sur un seul fragment d’un `*DB` réel. |
| `TestVFSFile_KeyInjectivityAndCollisionResistance` | `MetaKey` / `BlockKey` injectifs pour les couples de tenants/fileID collés (`a:b`/`c` vs `a`/`b:c`). |
| `TestVFSFile_SyncContract` | `Sync` invoque le `Syncer` sous-jacent, incrémente le compteur, propage l’erreur d’I/O. |
| `TestVFSFile_MaxInt64Boundary` | 1 octet à `math.MaxInt64-1` accepté ; 2 octets au même offset → `ErrInvalidOffset`. |
| `TestVFSFile_DirtyBufferDefersPut` | `WriteAt` d’une page 4 Ko ne persiste pas avant `Sync` (`ErrNotFound` sur le magasin). |
| `TestVFSFile_DirtyBufferPartialRMW` | RMW partiel servi depuis `dirtyBlocks`, sans `Put` prématuré. |
| `TestVFSFile_DirtyBufferFlushZeroDeletes` | Flush d’un bloc devenu zéro → `Delete`, pas `Put` de 4096 nuls. |
| `TestVFSFile_DirtyBufferThresholdFlush` | 255 pages : aucun flush ; la 256ᵉ déclenche la persistance automatique (1 Mio). |
| `TestVFSFile_DirtyBufferTruncateDrops` | `Truncate` jette les pages sales au-delà de la nouvelle taille. |
| `TestVFSFile_DirtyBufferCloseFlushes` | `Close` persiste le tampon ; réouverture relit le payload. |
| `TestVFSFile_CloseStalePreservesNewGeneration` | `Close` d’un handle périmé n’écrit pas sous l’ancienne génération (préservation COW). |
| `TestVFSFile_StorageIsolation` | Deux `memStorage` distincts, même `(tenant, fileID)` : registres et `dirtyBlocks` disjoints. |
| `TestVFSFile_GenerationRaceFree` | 32 goroutines × 40 itérations Create/Open/Delete : générations monotones, pas de course. |
| `TestVFSFile_MetaDirtyRetryOnFailure` | Échec I/O sur `Put` méta : `metaDirty` reste vrai, handle non `closed` ; second `Close` réussit. |
| `TestVFSFile_ConcurrentCreateIncrementsMonotonically` | Créations concurrentes : génération strictement croissante. |
| `TestVFSFile_CloseStorageErrorPreservesDirty` | Échec I/O au `Close` : `dirtyBlocks` conservés, handle resté ouvert, retry ensuite vert. |
| `TestVFSFile_CreateFailurePreservesOldData` | Échec de `Create` : les 3 blocs de l’ancienne génération restent lisibles (COW). |

#### `vfs_bridge_test.go` — mapping d’erreurs

`TestVFSBusyOrMapsHeapFull` : `vfsBusyOr` traduit `ErrHeapFull`, `errInsert`, `ErrWriterBusy`, `ErrViewHeld`, `ErrBusy` en `sqlite3.SQLITE_BUSY`. Une erreur non classée conserve le code de repli `778`. `isVFSBusy` est vrai pour `ErrHeapFull` et `errInsert`.

L’enregistrement `modernc.org/sqlite` (`RegisterC2DBVFS`) et le cycle `xOpen` / `xWrite` / `xSync` / `xClose` ne sont pas un test unitaire isolé de ce fichier : ils sont exercés par les fixtures d’intégration (`sql.Open("sqlite", "file:…?vfs="+vfsName)`). Un trou sparse `ErrNotFound` se lit comme 0 octet utile / 4096 zéros côté `VFSFile.ReadAt` (`TestVFSFile_SparseZeroBlock`, `TestVFSFile_ShortRead`).

`RegisterC2DBVFS`, si le stockage est un `*DB` dont `busyTimeout <= 0`, pose `SetBusyTimeout(5 * time.Second)` (`vfs_bridge.go`). Le banc hostile **annule** ce défaut immédiatement après l’enregistrement.

#### `vfs_locks_test.go` — échelle POSIX mémoire (12 tests)

États : `NONE` → `SHARED` → `RESERVED` → `PENDING` → `EXCLUSIVE`.

| Test | Contrat |
| :--- | :--- |
| `TestVFSLocksNominalSequence` | Séquence nominale complète, `CheckReservedLock` vrai dès `RESERVED`. |
| `TestVFSLocksRejectDoubleReserved` | Second `RESERVED` → `ErrBusy` ; le concurrent reste `SHARED`. |
| `TestVFSLocksRejectSharedWhilePending` | Nouveau `SHARED` sous `PENDING` → `ErrBusy` ; `EXCLUSIVE` bloqué tant qu’un lecteur survit. |
| `TestVFSLocksIllegalSequence` | `NONE→RESERVED` et `SHARED→PENDING` → `ErrLockSequence`. |
| `TestVFSLocksDirectSharedToExclusive` | Raccourci crash-recovery : `SHARED→EXCLUSIVE` licite si seul holder. |
| `TestVFSLocksDirectReservedToExclusive` | `RESERVED→EXCLUSIVE` via `PENDING`. |
| `TestVFSLocksIdempotence` | Re-lock au niveau courant = no-op. |
| `TestVFSLocksTorture` | Transitions aléatoires, aucun état illégal retenu. |
| `TestVFSLocks_DoublePendingRejection` | Deux `PENDING` concurrent → rejet. |
| `TestVFSLocks_WriterStarvationPrevention` | Un écrivain n’est pas affamé indéfiniment par des `SHARED` tardifs une fois `PENDING` posé. |
| `TestVFSLocks_ExtremeConcurrentFuzz` | Fuzz concurrent des transitions. |
| `TestVFSLocks_RejectIllegalReservedDowngrade` | Rétrogradation illicite `RESERVED→…` rejetée. |

#### `vfs_colocation_test.go` — Voie C

`TestRoute_VFSColocation` :

- `Route(BlockKey(tenant, fileID, gen, blockNo))` identique à `Route(MetaKey(tenant, fileID))` pour `blockNo` de 0 à **1000** (1001 blocs).
- Même shard pour `test.db`, `test.db-journal`, `test.db-wal`, `test.db-shm`.
- Huit `fileID` distincts `dbN.db` touchent **au moins 2** shards (les bases indépendantes ne collapsent pas).
- Une clé hors préfixe `vfs:` (`users:123`) suit le hachage BLAKE3 intégral (`Sum256` puis `Uint16>>6`).

### 2.2 Routage, scatter-gather et compaction

#### `route_test.go`

| Test | Contrat |
| :--- | :--- |
| `TestNumShardsConst` | `NumShards == 1024`. |
| `TestRouteStable` | Même clé → même shard ; 256 clés d’un octet ne collapsent pas toutes sur 0. |
| `TestRouteCoverage` | 4096 clés de 2 octets touchent au moins 16 fragments. |
| `TestDBTwoShards` | Deux clés de shards distincts, `Put`/`Get` isolés. |
| `TestDBScanPrefixMerge` | Fusion de scan préfixe multi-fragments. |
| `TestOpenDBLazyShards` | Ouverture paresseuse : un fragment absent n’est pas créé tant qu’aucune clé n’y tombe. |

Le préfixe VFS `vfsRoutingPrefix` et le parseur `parseUintAscii` (garde `n > (math.MaxInt-9)/10` avant `n*10+digit`) sont éprouvés par `TestRoute_MalformedVFSKeysNoPanic` dans `engine_test.go` : clés `vfs:9223372036854775808:`, `vfs:999999999999999999999:x`, formes tronquées, `nil` — aucune panique, repli sur le hachage intégral.

#### `engine_test.go` — fast-path, scatter-gather, versions, crochet

Extraits pertinents pour le pont et la compaction :

| Test | Contrat |
| :--- | :--- |
| `TestDB_PutBatch_ScatterGatherMultiShards` | 500 paires, ≥ 2 shards ouverts, relecture concurrente bit-exacte de chaque valeur. |
| `TestDB_PutBatch_ConcurrentParallel` | 8 goroutines × (32 clés disjointes + 8 clés recouvrantes), `SetBusyTimeout(5s)`. |
| `TestDB_PutBatch_ReplicationHookCalled` | `replHook` invoqué **une fois par paire**, `recType == RecPut`, shard = `Route(key)`, valeur identique — fast-path mono-shard **et** scatter-gather multi-shards. |
| `TestEngine_AutoCompactPreservesVersionIDs` | Trois `Put` → trois identifiants de 16 octets distincts ; `rebuildLatestLocked` préserve `lastID` et `GetAsOf(k, id2) == v2`. |
| `TestEngineAutoCompactOnHeapFull` | `ErrHeapFull` déclenche la compaction, le `Put` reprend. |
| `TestEnginePutBatch1000` | Lot de 1000 paires sur un fragment. |
| `TestShardCycleComplete` / `TestEngineMVCCTwoVersions` / `TestEngineGroupCommit` | Cycle shard, deux versions MVCC, group-commit. |
| `TestWALCrashWriteFlushBuffer` (+ fuzz, heap crash, overflow, scan after overflow) | Crash-write, tampon non flushé, page tamponnée, heap plein. |

Les autres tests de `engine_test.go` (heap, delete, elevate root, capacité 256 Mo, `AsOf` après compaction de feuille) restent dans le même paquet et s’exécutent avec la commande de qualification complète.

### 2.3 Qualification et moteur c2db

#### `vfs_integration_fixture_test.go` — 4 tests

| Test | Charge | Oracle |
| :--- | :--- | :--- |
| `TestVFSIntegration_FullRelationalCycle` | Table `users`, 100 lignes, agrégats, sous-requêtes, UPDATE/DELETE | `PRAGMA integrity_check = ok` |
| `TestVFSIntegration_TransactionRollback` | `INSERT` dans une tx puis `Rollback` | id=2 absent, id=1 intact, integrity `ok` |
| `TestVFSIntegration_ColdReopenDurability` | 500 lignes, `Close` SQLite + VFS + `*DB`, réouverture à froid d’une **nouvelle** instance | relecture bit-à-bit des 500 lignes, integrity `ok` |
| `TestVFSIntegration_ExtremeConcurrentFuzzing` | voir section 3 | masse 10000.000000, shards=1, integrity `ok`, 0 retry épuisé |

#### Suites de qualification moteur

| Fichier | Tests | Objet |
| :--- | :--- | :--- |
| `qualification_concurrency_test.go` | `TestQual_03_BTreeSplitCascades` (6000 clés triées), `TestQual_04_HeapSaturationRollback`, `TestQual_05_MVCCSnapshotExpiration`, `TestQual_08_OFDLockSentinelIsolation` (`SetBusyTimeout(0)` sur deux shards) | Splits en cascade, saturation tas, expiration de vue, isolation OFD |
| `qualification_hostile_crash_test.go` | Helper SIGKILL + `TestHostileCrash_MultiProcessSIGKILL`, torn middle-chunk multi-LBA, `RepackHeap` power-loss, overflow torn + durabilité | Crash réel multi-processus, journal témoin atomique |
| `qualification_torn_test.go` | `TestQual_01` torn B-tree, `TestQual_02` torn WAL secteur, bit-rot, WAL wrap epoch, header médian corrompu, tx multi-blocs, rollback split racine, replay all-or-nothing, lecteur concurrent pendant publish, poison post-commit, overflow chain, `TestQual_12` franchissement **256** slots pager (≥ 300 pages sales) | Cycle de vie complet après corruption : rejet **puis** reprise nominale |
| `qualification_overflow_test.go` | Roundtrip/streaming 100 o → 2 Mio ; discriminant et collision | Chaînes overflow 1, 2, 4, ~15, ~126 pages |
| `qualification_synctest_test.go` | χ² d’uniformité N=100 000 clés / K=1024 shards (E=97.65625, garde χ² ∈ [850, 1200]) ; `TestQual_11_Synctest_10kSimulatedAccesses` | Partitionnement et horloge `testing/synctest` |
| `qualification_endurance_test.go` | Compactage continu, soak concurrent, RSS `MaxHeapPages` | Enveloppe mémoire bornée (`-short` saute l’endurance) |
| `qualification_octosmith_test.go` | SIGKILL process réel, WAL tronqué, 50 cycles oracle, parité bit-exacte, métadonnées heap corrompues fail-closed | Oracle de reprise après mort brutale |
| `pager_seal_test.go` | `TestCI55_PageSealReject`, `TestCI55_PageSealZeroTagPolicy` | Tag Poly1305 corrompu → `errPageSeal` ; politique tag zéro |
| `csgguard_test.go` | Tables de saut C2 (`SwitchDense`/`Holes`/`Sparse`/`TooFew`), commutateur WAL `RecType` 0..7, `TestCrc32c_zeroalloc` = **0 allocs/op** | Garde compilateur + zéro allocation CRC32C page 16 Ko |
| `zz_exploitation_fixes_test.go` | Course table de shards, WAL plein refuse l’écriture, plafond de vues, poison device, sink de sondes, open strict, réemploi slot pager | Régressions d’exploitation figées |
| `ub_pinning_test.go` | `cellOff` hors bornes, `cellSize` hors page, limite 16320, `chunkLen` overflow, offsets stream corrompus | Pinning anti-UB : rejet, pas de slice hors page |

Les oracles C (`Test*VsCOracle` : `db_btree_oracle_test.go`, `db_page_oracle_test.go`, `db_wal_oracle_test.go`, `page4k_is_zero_oracle_test.go`, `c2db_*_test.go`) restent la porte de parité bit-exacte des noyaux transpilés. Ils ne se substituent pas aux fixtures SQL du pont VFS.

---

## 3. Autopsie du banc de fuzzing concurrent

Référence : `TestVFSIntegration_ExtremeConcurrentFuzzing` dans `/devhoros/c2simd/c2pkg/c2db/vfs_integration_fixture_test.go` (ligne 439). Architecture de file : `/devhoros/c2simd/c2pkg/c2db/VFS_ARCHITECTURE.md` §5.

### 3.1 Spécifications de la charge (source)

| Paramètre | Valeur | Origine |
| :--- | :--- | :--- |
| Goroutines | **32** | `numGoroutines = 32` |
| Opérations par goroutine | **20** | `opsPerGoroutine = 20` |
| Opérations planifiées | **640** | 32 × 20 |
| Mix | 70 % virements, 30 % lectures `SUM(balance)` | `r.Float64() < 0.30` |
| Transactions d’écriture / lecture (mix 70/30 de 640) | **≈ 450 virements, ≈ 190 lectures** | 0,70 × 640 = 448 ; 0,30 × 640 = 192 |
| Comptes | **10**, solde initial **1000.0** chacun | `numAccounts`, `initialBalancePerAccount` |
| Masse attendue | **10000.0** | `expectedTotalMoney` |
| Pool SQLite | `SetMaxOpenConns(50)`, `SetMaxIdleConns(50)` | forcer 50 connexions physiques |
| DSN | `_busy_timeout=150&_pragma=busy_timeout(150)&_txlock=immediate` | plus `PRAGMA busy_timeout = 150` |
| c2db | `c2dbInstance.SetBusyTimeout(0)` **après** `RegisterC2DBVFS` | annule le défaut 5 s du pont |
| Backoff Go | base **1 ms**, plafond **`maxBackoff = 50 ms`**, jitter `Int64N(backoff/2+1)`, **`maxRetries = 5000`** | boucle interne |
| Journal d’audit | slice mutexée, une entrée **par COMMIT réussi** | oracle lost-update |
| Magasin | `OpenDB(dir, masterKey)` disque réel sous `DEVHOROS_TMPDIR` | pas de `memStorage` |

Chaque virement est une transaction SQL réelle : `SELECT balance`, `UPDATE … − amt`, `UPDATE … + amt`, `COMMIT`. Un solde insuffisant n’est pas une écriture : rollback, opération comptée réussie sans entrée d’audit. Les lectures sont des `BeginTx(ReadOnly)` + `SELECT SUM(balance)` avec tolérance `1e-4` en cours de course.

### 3.2 Goulot initial (BTO = 5 s, > 25 min)

SQLite n’admet **qu’un écrivain physique** par base. `RegisterC2DBVFS` pose `SetBusyTimeout(5 * time.Second)` sur le `*DB`. Sous 32 goroutines et ThreadSanitizer, chaque collision s’endort dans le busy-handler C `_sqliteDefaultBusyCallback` jusqu’à 5 s.

Sans coalescence, chaque `xWrite` d’une page 4 Ko traversait le moteur (`Put`/`Get` fragment). Combiné au BTO de 5 s, le temps wall dépasse **25 minutes** : le test « passe » ou saute le budget CI, la file est inerte.

### 3.3 Expérience BTO = 0 ms

`SQLITE_BUSY` immédiat. Le client Go réessaie en boucle serrée. Observation : **tempête d’environ 50 000 retries**, starvation, famine CPU. Les goroutines se volent le verrou sans jamais céder assez longtemps pour qu’un `COMMIT` + `xSync` se termine. Zéro progrès utile.

### 3.4 Expérience BTO = 50 ms

Le temps wall descend vers **202 s**. Sous charge machine, starvation stochastique : **6 goroutines** épuisent un plafond de **2000 retries**. 50 ms côté SQLite ne couvrent pas toujours un `xSync` + commit c2db instrumenté par `-race`.

### 3.5 Calibration finale validée (Phase 3)

Triplet retenu dans le source actuel :

1. SQLite `busy_timeout = 150 ms` (DSN `_busy_timeout=150` **et** `PRAGMA busy_timeout = 150`).
2. `c2dbInstance.SetBusyTimeout(0)` : le fragment rend `ErrWriterBusy` tout de suite ; `vfsBusyOr` mappe en `SQLITE_BUSY` sans retenir le moteur 5 s.
3. Backoff applicatif Go : 1 ms → 50 ms, jitter, **5000** tentatives.

L’attente courte est en C (là où SQLite sait réessayer `xLock`). L’attente longue et le jitter sont en Go (là où l’on peut abandonner). Le tampon sale 256 pages (1 Mio) coalesce les `xWrite` : le journal c2db n’avance qu’au `xSync`, au `Close`, ou au seuil.

### 3.6 Résultats réels mesurés (opposables)

Campagnes sous `GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd go test -race -count=1 -timeout 20m -run TestVFSIntegration_ExtremeConcurrentFuzzing ./c2pkg/c2db` :

| Grandeur | Valeur mesurée |
| :--- | :--- |
| Durée wall sous `-race` | **139 s à 251 s** |
| Accélération vs blocage initial (> 25 min) | **> 7,5× à 10×** |
| Shards touchés | **strictement 1** (`t.Fatalf` si `n != 1`) |
| Masse monétaire | `solde=10000.000000 attendu=10000.000000 delta=0.000000e+00` |
| Intégrité | `PRAGMA integrity_check = ok` |
| Retries échoués (plafond 5000 atteint) | **0** |
| Lost update | aucun : chaque compte égal au replay de l’audit |

L’oracle n’est pas « absence de `SQLITE_BUSY` ». L’oracle est la conservation de la masse, l’égalité compte par compte contre l’audit, un seul fragment, et l’intégrité B-tree SQLite, **après** épuisement de la file.

---

## 4. Protocoles de reproduction et commandes officielles

Toutes les commandes se lancent depuis `/devhoros/c2simd`.

### 4.1 Exécution unitaire VFS

Couvre `TestVFSFile_*`, `TestVFSLocks*`, `TestVFSBusyOrMapsHeapFull`, `TestRoute_VFSColocation`, `TestVFSIntegration_*` (y compris le banc hostile).

```
DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd go test -race -count=1 -run "TestVFS" ./c2pkg/c2db
```

### 4.2 Banc hostile extrême

Budget 20 minutes : la fourchette mesurée 139–251 s tient largement ; le plafond protège une régression BTO.

```
DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd go test -race -count=1 -timeout 20m -run "TestVFSIntegration_ExtremeConcurrentFuzzing" ./c2pkg/c2db
```

Journaux attendus en fin de course :

```
Conservation monétaire validée : solde=10000.000000 attendu=10000.000000 delta=0.000000e+00
Intégrité SQLite validée : PRAGMA integrity_check = ok
shards observés: 1
```

Tout `Échec de l'opération … après retries maximales` est un échec de qualification (retries échoués ≠ 0).

### 4.3 Scatter-gather et compaction

```
DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd go test -race -count=1 -run "TestDB_PutBatch" ./c2pkg/c2db
```

Exécute `TestDB_PutBatch_ScatterGatherMultiShards`, `TestDB_PutBatch_ConcurrentParallel`, `TestDB_PutBatch_ReplicationHookCalled`. Pour y joindre la préservation des identifiants de version :

```
DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd go test -race -count=1 -run "TestDB_PutBatch|TestEngine_AutoCompactPreservesVersionIDs|TestRoute_MalformedVFSKeysNoPanic" ./c2pkg/c2db
```

### 4.4 Qualification complète du paquet

```
DEVHOROS_TMPDIR=/devhoros/.tmp_fixtures GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd go test -race -count=1 ./c2pkg/c2db
```

Cette commande est le plafond licite : un paquet, pas `./...`. Les bancs `benchcmp`, `ci` et `cmd/c2db-verify` restent hors de cette ligne.

Un `unix.EINVAL` à l’ouverture (`O_DIRECT` absent du système de fichiers) est un `t.Skip` documenté, pas un vert déguisé : relancer sur un volume qui honore `O_DIRECT`.

---

## 5. Invariants de lecture du manuel

1. Recalibrer le BTO SQLite ou le `SetBusyTimeout` du fragment **sans** rejouer `TestVFSIntegration_ExtremeConcurrentFuzzing` sous `-race` invalide la section 3.
2. Modifier `dirtyFlushThreshold` (256) ou `BlockSize` (4096) sans `TestVFSFile_DirtyBufferThresholdFlush` et le cycle d’intégration casse le contrat 1 Mio / page 4 Ko.
3. Une compaction qui alloue un nouvel identifiant de version au lieu de recopier les 16 octets casse `TestEngine_AutoCompactPreservesVersionIDs` et toute lecture `GetAsOf`.
4. Un routage qui hache le `blockNo` ou le suffixe `-wal` casse `TestRoute_VFSColocation` et le `shards=1` du banc hostile.
