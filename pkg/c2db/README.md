# c2db — Moteur de Persistance MVCC Agent-First & Haute Performance

`c2db` est un moteur de stockage transactionnel embarqué conçu pour les architectures multi-agents autonomes, l'ingestion massive de contextes IA et le traitement de données volumineuses sans verrouillage en lecture.

---

## 1. Caractéristiques Clés & Invariants Matériels

- **Capacité déclarée de 1 Téraoctet par Shard (borne d'adressage) :** Le noyau borne l'espace de pages d'un shard à `maxHeapPages = 67 108 864` pages de 16 Ko, soit 1 Tio d'espace d'adressage par fichier clairsemé (*sparse file*), et 1 Pio sur une grappe de 1024 shards. Cette valeur est un plafond d'adressage, non un volume démontré : la qualification `TestCapacity_1TerabyteShard` n'a atteint que 1 048 576 pages (16 Gio) sur l'hôte de mesure, dont le noyau applique `vm.overcommit_memory=0` (repli documenté par le test). La capacité effectivement remplissable dépend en outre de la forme de l'arbre ; le plafond de profondeur qui bornait auparavant la croissance est levé par la croissance de racine (M7), prouvée à la profondeur 5 sur 256 pages (`TestDbBtree_RootGrowDepth4`). Aucune passe ne remplit 1 Tio.
- **Concurrence MVCC sans verrouillage en lecture :** La lecture ne prend aucun verrou ; elle épingle un instantané par compteur atomique. Les coûts mesurés sont de ~9,5 ns/op (0 allocation) pour le pin/despin MVCC, ~60 ns/op pour un `Get` sur un tas minimal, et ~2,8 µs/op pour un `Get` sur un corpus réaliste de 512 entrées (clés de 36 octets, valeurs de 220 octets). Sous huit lecteurs et deux écrivains saturants, la latence individuelle médiane du `Get` est de ~2 µs (p99 ~6 µs). L'affirmation « inférieure à 100 nanosecondes » ne vaut donc que pour le pin MVCC et le `Get` d'un tas minimal, pas pour une lecture sur corpus réel.
- **Refoulement de prédicats (*Predicate Pushdown*) :** Le moteur de requêtes déclaratif ([`query.go`](query.go)) évalue les prédicats de clés, de cellules brutes et de champs JSON directement au niveau des pages, sans désérialisation d'arbres syntaxiques ni chargement des débordements, ce qui réduit la matérialisation : sur 1 000 enregistrements réels, une requête filtrée par champ JSON alloue ~1 011 fois (29,7 Ko) contre ~2 011 fois (265 Ko) pour le même parcours sans prédicat. L'évaluation marginale du prédicat coûte ~30 ns par enregistrement balayé. La requête complète n'est pas zéro-allocation : elle coûte ~200 µs pour 1 000 enregistrements réels. Le chiffre public « 54 ns/op zéro-allocation » qualifiait l'évaluation d'un prédicat, non la requête entière.
- **Journal WAL & reprise déterministe :** Journal de transactions en écriture anticipée sous `O_DIRECT`, scellé par bloc et relu au dernier enregistrement durable. Le point de reprise et la fenêtre de perte sont fixés par le contrat de durabilité (section 5). La reprise rejoue la queue non pointée du journal, bornée par la politique de commit ; elle n'est donc pas « instantanée » par construction.
- **Chiffrement Natif Multi-Tenants :** Chiffrement par page de 16 Ko adossé à la dérivation de clés de tenant ([`DeriveTenantMAC`](engine.go)).

---

## 2. API Déclarative de Requêtage (Agent-First)

```go
import "github.com/hazyhaar/c2pkg/c2db"

// Requête déclarative avec refoulement de prédicats (Predicate Pushdown)
results, err := db.Query("agent:context:").
    WhereKey(func(k []byte) bool { return bytes.HasSuffix(k, []byte(":active")) }).
    Where(c2db.ValJSONFieldString("status", "completed")).
    Limit(50).
    Execute(ctx)
```

---

## 3. Cartographie de l'Écosystème `c2pkg`

`c2db` constitue le socle de persistance d'une suite modulaire assemblable à la compilation :

| Extension | Rôle Métier |
| :--- | :--- |
| [`c2cluster`](../c2cluster) | Clustering réseau UDP/QUIC 0-RTT, mTLS souverain Zero-CA et réplication active WAL. |
| [`c2quorum`](../c2quorum) | Consensus distribué, élection de chef et baux de verrouillage linéaires (alternative à `etcd`). |
| [`c2ranked`](../c2ranked) | Ensembles ordonnés (Ranked Sets) avec skiplist à spans $O(\log N)$ et files de priorité. |
| [`c2events`](../c2events) | Flux d'événements, groupes de consommateurs avec reprise sur panne et courtage Pub/Sub. |

---

## 4. Stratégie de Licence & Commercialisation

- **Noyau `c2db` :** Sous licence permissive **Apache 2.0** (libre pour intégration in-process locale, développement et usage interne).
- **Extensions Distribuées (`c2cluster`, `c2quorum`, etc.) :** Sous licence **BSL 1.1** (Business Source License) protégeant le projet contre l'exploitation en service managé concurrent par les fournisseurs de cloud, avec conversion automatique en open-source après 3 ans.

---

## 5. Contrat de Durabilité

La durabilité de `c2db` est régie par `CommitPolicy` ([`route.go`](route.go)), composée d'un mode `CommitMode` et d'une fenêtre `Window`. Deux régimes locaux et un régime répliqué cohabitent.

- **`CommitImmediate` (régime par défaut) :** Le journal est pointé par groupe de `walCoalesceN = 32` écritures ([`engine.go`](engine.go)), borné par la fenêtre par défaut `defaultCommitWindow = 2 ms`. La fenêtre de perte est explicitement bornée : une écriture non pointée devient rejouable après coupure dès que la fenêtre s'est écoulée depuis la première écriture non pointée du groupe, ou dès que 32 écritures ont été groupées, selon ce qui survient en premier. Le délai borne l'ajout au groupe, non la durée du `fdatasync` lui-même. Une coupure à l'intérieur de la fenêtre perd les écritures non encore pointées.
- **`CommitWindowed(d)` :** Même mécanisme, avec une fenêtre explicite `d` fixée à l'ouverture (`WithCommitPolicy`) ou par appel (`WithDurability`). La fenêtre de perte vaut alors exactement `d`.
- **`CommitReplicated` :** La durabilité locale n'est pas requise et le committer ne pointe plus selon la fenêtre. La garantie appartient à la couche de réplication/quorum. Quand un `ReplicationSink` est branché (`SetReplicationSink`), le moteur publie chaque enregistrement WAL (`Publish`, qui retourne une séquence monotone) et `DB.Sync` attend l'acquittement de quorum (`WaitAck`) de la dernière séquence publiée par chaque shard. Le pointage local (`fdatasync`) n'est alors qu'une optimisation de vitesse de reprise. Un quorum non atteint avant l'annulation du contexte retourne `ErrReplicationQuorum`. Sans sink branché, `DB.Sync` se replie sur la barrière locale.
- **`DB.Sync(ctx)` :** Barrière de durabilité explicite. Sous les régimes locaux, elle force le pointage de tous les shards ayant des écritures non pointées et attend leur `fdatasync` ; le verrou écrivain de chaque shard sérialise la barrière avec les écritures en cours, si bien qu'au retour toute écriture acquittée avant l'appel est rejouable. Un contexte annulé interrompt la barrière entre deux shards ou pendant l'attente d'acquittement.
- **`WithDurability(p)` :** Attache une politique à un appel d'écriture précis, sans rompre les appelants existants (sans option, la politique d'ouverture s'applique). Pour `CommitImmediate`, le pointage du shard est forcé avant le retour ; pour `CommitWindowed(d)`, l'écriture est enregistrée auprès du committer central avec la fenêtre `d` ; pour `CommitReplicated`, l'appel attend l'acquittement de quorum du sink branché.
- **`PutBatch` :** Le chemin de lot pointe localement par construction (un seul pack WAL fermé puis vidage du pager). `CommitImmediate` y est donc déjà satisfait et `CommitWindowed` a fortiori, le lot étant durable avant l'échéance. `CommitReplicated` y ajoute l'attente d'acquittement de quorum, seule condition qui déclare le lot durable sous ce régime.

**Frontière entre durabilité locale et réplication :** le `fdatasync` local garantit la rejouabilité sur la machine locale, jamais la survie à la perte du support. La durabilité répliquée n'est acquise qu'après acquittement de quorum ; sous `CommitReplicated`, c'est cette condition seule qui déclare l'écriture durable. Le noyau n'expose que l'interface `ReplicationSink` ([`replication.go`](replication.go)) ; l'implémentation réelle réside dans la couche de réplication (`c2repl`), ce qui évite toute dépendance circulaire.

## 6. Qualification & Vérification

Exécution du banc de tests complet avec détection de concurrence sous Go 1.27 :

```bash
go test -race -count=1 ./c2db
```
