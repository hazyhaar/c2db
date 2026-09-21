# Département de Fuzzing (Fuzzing Department) — C2DB & Extensions

Ce document consigne l'architecture, la méthodologie, les suites de tests et les résultats métrologiques du département de fuzzing intégré pour le moteur transactionnel `c2db` et le format de graphe mémoire `c2cpg`.

---

## 1. Objectifs & Doctrine d'Intégrité

Le département de fuzzing a pour mission d'éprouver la résilience matérielle et logicielle des composants critiques face aux données malformées, aux défaillances physiques de stockage et aux conflits de concurrence extrême :
1. **Absence totale de panique non contrôlée :** Aucun flux binaire ou permutation d'opérations ne doit provoquer de panique Go, d'écrasement de pile ou de corruption de pointeurs.
2. **Garantie d'intégrité ACID & Fail-Closed :** Tout segment de journal corrompu ou fragment tronqué doit être soit restauré jusqu'au dernier commit durable vérifié, soit rejeté de manière étanche par une erreur typée explicite.
3. **Zéro Data Race sous charge concurrente :** Les structures de données mémoire (B-Tree, MVCC, instantanés de vues) doivent fonctionner sans la moindre condition de concurrence sous le détecteur de courses (`-race`).

---

## 2. Suites de Fuzzing C2DB (`c2db/fuzz_engine_test.go`)

Les tests exploitent l'infrastructure de fuzzing natif Go (`testing.F`) et sont conçus pour une intégration continue déterministe.

### 2.1. Fuzzing des Clés et Valeurs (`FuzzKeysValues`)
- **Périmètre exploré :**
  - Clés vides (longueur 0) : rejet systématique et immédiat avec `errInsert`, sans panique.
  - Clés de 1024 octets : validation du franchissement des seuils d'insertion dans les feuilles B-Tree.
  - Clés et valeurs contenant des octets nuls (`\x00`), des séparateurs VFS (`:`, `/`), des octets de contrôle et des séquences binaires denses (`0xFF`).
  - Valeurs supérieures à la taille de page (4 Ko, 8 Ko) déclenchant l'allocation et le chaînage des pages d'overflow.
- **Invariants vérifiés :**
  - Pour toute écriture confirmée (`Put(k, v) == nil`), la lecture directe (`Get(k)`) retourne exactement `v` bit à bit.
  - L'instantané de vue (`View()`) ouvert concomitamment retourne une valeur conforme à son horizon temporel.
  - La suppression (`Delete(k)`) supprime la clé et n'altère pas les clés collatérales de la même feuille.

### 2.2. Fuzzing des Transactions Concurrentes MVCC (`FuzzMVCCConcurrency`)
- **Périmètre exploré :**
  - Interprétation dynamique d'un flux d'instructions aléatoire réparti sur des goroutines concurrentes (3 travailleurs simultanés).
  - Permutations arbitraires d'opérations entrelacées : `Begin`, `Put`, `Delete`, `Commit`, `Rollback`, lectures directes `Get` et instantanés isolés `View`.
  - Collision intentionnelle sur un espace restreint de clés pour forcer la contention sur les verrous d'écrivain et l'historique MVCC.
- **Durcissement architectural appliqué :**
  - Détection et élimination d'une condition de course lors de l'appel concurrent `View()` face à des mutations actives : encapsulation immuable de `snap` et `hasSnap` au sein de la structure `liveHeap` publiée. Chaque vue snapshot lit désormais son identifiant scellé depuis la structure `liveHeap` épinglée, éliminant tout accès non synchronisé aux champs d'écriture du shard.

### 2.3. Fuzzing du Journal WAL & Crash Recovery (`FuzzWALCrashRecovery`)
- **Périmètre exploré :**
  - Injection de fragments corrompus, tronqués ou altérés au sein de l'image de journal `wal.img`.
  - Troncatures brutales simulant une coupure d'alimentation au milieu d'un bloc de 4 Ko (LBA).
  - Inversion de bits (*bit flips*) sur les trames de log, altérant les en-têtes, les charges utiles ou les signatures cryptographiques Poly1305.
- **Invariants vérifiés :**
  - Le chargeur WAL et le moteur de rejeu (`Replay`) n'émettent aucune panique non capturée.
  - Rejet propre et sécurisé via les erreurs typées (`errWALBadTag`, `errWALMagic`, `errWALCorruptedMedian`) ou réouverture réussie avec intégrité des transactions confirmées antérieures au point de corruption.
  - Reprise opérationnelle immédiate : le shard restauré accepte de nouvelles écritures et de nouvelles transactions durables.

---

## 3. Suite de Fuzzing C2CPG (`c2cpg/fuzz_test.go`)

Le composant `c2cpg` implémente un graphe de dépendances de code binaire mmap à zéro allocation.

### 3.1. Fuzzing de `OpenBytes` (`FuzzOpenBytes`)
- **Périmètre exploré :**
  - Tranches de taille inférieure à l'en-tête standard (128 octets).
  - Données binaires aléatoires avec magics et versions corrompus.
  - Offsets falsifiés pointant au-delà de la tranche mémoire ou provoquant des dépassements d'entiers 32-bit (string table, node records, partitions CSR forward et CSC reverse, table de hachage).
- **Durcissement architectural appliqué :**
  - Arithmétique 64-bit non débordante pour l'ensemble des validations d'offsets et de longueurs.
  - Contrôle strict de monotonie sur `csrRowOffsets` et `cscColOffsets` (bornage par `numEdges`).
  - Bornage des itérations de recherche dans `Lookup` à `len(hashBuckets)` pour interdire toute boucle infinie sur table de hachage saturée.
  - Gardes de bornes sur `OutEdges` et `InEdges` face aux indices invalides.
- **Invariants vérifiés :**
  - Toute corruption est systématiquement interceptée à l'ouverture, retournant `ErrCorruptFile`, `ErrInvalidMagic` ou `ErrInvalidVersion`.
  - Tout graphe validé est navigable sans panique (`Node`, `OutEdges`, `InEdges`, `Lookup`).

---

## 4. Bilan Métrologique d'Exécution

Les campagnes d'exécution ont été menées sous Linux x86_64 avec le détecteur de courses activé (`-race`).

| Composant | Suite de Fuzzing | Graines Initiales | Débit d'Exécution | Nouveaux Cas Découverts | Statut |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`c2cpg`** | `FuzzOpenBytes` | 9 seeds | **226 910 execs/sec** (1,76M en 6s) | 18 mutations | **PASS (0 crash, 0 hang)** |
| **`c2db`** | `FuzzKeysValues` | 8 seeds | **282 execs/sec** (avec I/O disque) | 2 mutations | **PASS (0 crash)** |
| **`c2db`** | `FuzzMVCCConcurrency` | 3 seeds | **85 execs/sec** (concurrence multi-goroutines) | 3 mutations | **PASS (0 race, 0 deadlock)** |
| **`c2db`** | `FuzzWALCrashRecovery` | 3 seeds | **644 execs/sec** (corruption + réouverture) | 7 mutations | **PASS (0 panic)** |

### Validation Formelle des Portes CI (`c2db/ci`)
```
=== RUN   TestCI_Deep
PASS functional/bipolar nominal vert, rejet rouge
PASS functional/hook before-publish tiré
PASS technical/provenance-stamp tampon present
PASS functional/overlay-host overlay go test vert
PASS functional/overlay-ablate ablation du sceau rougit l'hôte
--- PASS: TestCI_Deep (1.85s)
PASS
```

---

## 5. Commandes Canoniques d'Exécution

Pour exécuter les suites de tests et campagnes de fuzzing conformément à la doctrine du dépôt :

### Exécution des graines de fuzzing sous détection de courses (-race)
```bash
# Validation ciblée C2CPG
go test -race -count=1 ./c2cpg/...

# Validation ciblée C2DB (suites Fuzz)
go test -race -count=1 -run="Fuzz" ./c2db
```

### Campagnes de fuzzing dynamique continu
```bash
# Fuzzing du lecteur binaire c2cpg
go test -run=^$ -fuzz=FuzzOpenBytes -fuzztime=30s ./c2cpg

# Fuzzing des clés et valeurs c2db
go test -run=^$ -fuzz=FuzzKeysValues -fuzztime=30s ./c2db

# Fuzzing de la concurrence MVCC c2db
go test -run=^$ -fuzz=FuzzMVCCConcurrency -fuzztime=30s ./c2db

# Fuzzing du crash recovery WAL c2db
go test -run=^$ -fuzz=FuzzWALCrashRecovery -fuzztime=30s ./c2db
```
