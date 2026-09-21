# DIRECTIVES — c2pkg/c2db (jalon M0)

Le paquet `c2db` est un moteur de pages persistantes, sans CGO. Les formats, le banc M8 et les noyaux vectoriels proposés vivent sous [`spec/`](file:///devhoros/c2simd/c2pkg/c2db/spec). Le tas B-tree, le journal scellé et c2QL sont livrés dans ce paquet. Les noyaux SIMD d’occupation de slots restent au statut proposé tant que l’émetteur n’offre pas la comparaison vectorielle et le masque de bits.

## 1. Page et unité d’adressage

Une page occupe 16 Kio, soit quatre LBA NVMe de 4 Kio. L’en-tête tient sur 64 octets, une ligne de cache. Le corps est slotté ; un emplacement est un décalage, jamais un UUID. Un contrôle CRC32-C peut porter sur la page en mémoire. Ce contrôle n’est pas le sceau d’écriture.

## 2. Journal d’écriture anticipée

L’enregistrement logique est de longueur variable. Toute écriture disque est un multiple de 4096 octets, obtenu par remplissage. L’ordre des champs est : magique sur 32 bits, longueur, identifiant UUIDv7 de 16 octets, type dense, charge utile, remplissage, étiquette Poly1305 de 16 octets. Le sceau cryptographique est Poly1305. CRC32-C n’est pas ce sceau.

La longueur variable du journal emploie le varint QUIC (RFC 9000 §16) : les deux bits de poids fort de l’octet de tête donnent 1, 2, 4 ou 8 octets. Ce choix réemploie `vint_lens32`.

## 3. Identifiant

L’identifiant est un recouvrement de `c2uuidv7.Compose` : 48 bits d’horodatage en millisecondes, 4 bits de version, 12 bits de séquence, 2 bits de variante, 10 bits de fragment, 52 bits de compteur. L’adressage de contenu emploie BLAKE3.

## 4. Fragments

Le moteur compte 1024 fragments. Chaque fragment admet un seul écrivain. Aucune transaction inter-fragments n’est définie au jalon v1.

## 5. Entrées-sorties

Les lectures et écritures passent par `pread` et `pwrite` sous `O_DIRECT`. Le fichier est préalloué par `fallocate`. Le vidage demandé par Flush est `fdatasync`. Sont interdits : l’écriture par `mmap` en `MAP_SHARED`, `io_uring` au jalon v1, les `ioctl` NVMe, et CGO.

## 6. Contraintes de transpilation

Un descripteur d’arbre se transmet par valeur. Une page se désigne par un pointeur `uint8_t *` et un décalage. Le gabarit d’écriture C est [`vmm_virtio_vring.c`](file:///devhoros/c2simd/c2pkg/c2vmm/c_src/vmm_virtio_vring.c).

## 7. Noyaux et banc

Les noyaux du jalon M0 restent au statut proposé ([`spec/kernels.cue`](file:///devhoros/c2simd/c2pkg/c2db/spec/kernels.cue)). Aucune fiche `#ArchtimeSimdKernel` landée, aucun accélération fictive. Le banc ([`spec/bench_m8.cue`](file:///devhoros/c2simd/c2pkg/c2db/spec/bench_m8.cue)) est rédigé avant le moteur. Aucun chiffre de performance n’est figé ici.

La porte d’homologation des formats est `cue vet` sur [`spec/`](file:///devhoros/c2simd/c2pkg/c2db/spec).
