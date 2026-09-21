# Banc opposable — débit d'écriture concurrente SQLite face à c2db

Date : 2026-09-19T23:21:18+02:00. Machine : linux/amd64, 32 CPU, go1.27.0-X:simd. Dépôt : `/devhoros/c2simd`.

## Matière

wcard_execution vide (0 ligne constatée) dans /devhoros/horos55/data/wf55.db ; substitution déclarée par paires réelles {wcard x 72, step_run x 22} associées en tourniquet, octets des valeurs intégralement issus de la base.

Chaque écriture porte une clé unique (`opposed/<compteur>/<ancre réelle>`) et une valeur JSON réelle ; les clés sont retenues par échantillonnage sur le routage Blake3 de production (`c2db.Route`), de sorte que chaque palier n'écrit que dans son ensemble borné de shards. Aucune valeur n'est synthétisée.

## Protocole loyal

- SQLite (modernc, sans CGO) : `PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`, `PRAGMA busy_timeout=5000` posé sur chaque connexion de travailleur, table `kv(k BLOB PRIMARY KEY, v BLOB)`, `INSERT OR REPLACE`, une connexion par goroutine, départ sur barrière commune, durée = ingestion seule. Quand le verrou rend SQLITE_BUSY au-delà du busy_timeout, l'insertion est rejouée côté Go (plafond 60 s, attente incluse dans la mesure) et chaque refus rejoué est compté dans la colonne dédiée : ces refus sont la sérialisation observée, non un artefact.
- c2db : `c2db.DB` sous `O_DIRECT`, ensemble borné à 16 shards (paliers 4, 16) puis 64 shards (palier 64), `SetBusyTimeout(5s)` sur chaque écrivain de shard, `Put` direct, départ sur barrière commune, durée = ingestion seule (pré-ouverture des shards hors chronomètre).
- Vérification : relecture et vérification bit-exacte à 100% de l'ensemble exhaustif des clés côté SQLite et côté c2db, comptage `COUNT(*)` côté SQLite et dénombrement des shards distincts touchés côté c2db. Tout écart de parité ou d'exhaustivité échoue immédiatement le banc.

## Résultats

| P goroutines | N / goroutine | Total | Shards c2db | SQLite ops/s | SQLite s | SQLite refus busy rejoués | c2db ops/s | c2db s | S = c2db/sqlite | Shards touchés |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 500 | 2000 | 16 | 30489 | 0.07 | 0 | 11183 | 0.18 | 0.37 | 16 |
| 16 | 500 | 8000 | 16 | 22668 | 0.35 | 0 | 14828 | 0.54 | 0.65 | 16 |
| 64 | 500 | 32000 | 64 | 23791 | 1.35 | 0 | 44573 | 0.72 | 1.87 | 64 |

## Lecture

SQLite sérialise l'ensemble des écritures derrière son verrou WAL unique sous forte concurrence. c2db partitionne les écritures par routage Blake3 sur des shards indépendants dotés de leurs propres verrous d'écrivain sous O_DIRECT. Sur un support de stockage NVMe unique avec barrière de synchronisation matérielle directe, le débit global reflète le compromis entre l'indépendance des verrous de shards et le coût unitaire de l'E/S directe non mise en cache.

## Limites déclarées

- Mesure sur un seul poste, un seul volume ; la latence de `fdatasync` du volume porte les deux moteurs.
- Charge totale du banc : 42000 écritures ; valeurs de quelques centaines d'octets.
- Si la jambe c2db est marquée IGNORÉE, le volume ne porte pas `O_DIRECT` et seul le motif est consigné, sans chiffre inventé.
