# Rapport de Micro-Métrologie Silicium c2db (NVMe & CPU)

- **Date d'exécution :** 2026-09-04T12:25:48Z
- **Processeur hôte :** amd64 (32 vCPUs)
- **Fréquence TSC étalonnée :** 3.187 GHz (résolution 1 cycle = 0.314 ns)
- **Système de fichiers sous-jacent :** NVMe PCIe Gen4 x4 monté sur `/devhoros`

## 1. Pénalité de Cache-Line Split (Alignement L1D 64 octets)

| Décalage (Octets) | Cycles P50 | Latence P50 (ns) | Latence P99 (ns) | Pénalité vs Aligné |
| :--- | :--- | :--- | :--- | :--- |
| +00 B | 379.0 | 118.9 ns | 134.0 ns | +0.0% |
| +01 B | 379.0 | 118.9 ns | 120.8 ns | +0.0% |
| +08 B | 378.0 | 118.6 ns | 119.5 ns | -0.3% |
| +16 B | 379.0 | 118.9 ns | 120.5 ns | +0.0% |
| +31 B | 379.0 | 118.9 ns | 119.5 ns | +0.0% |
| +32 B | 379.0 | 118.9 ns | 119.5 ns | +0.0% |
| +63 B | 379.0 | 118.9 ns | 120.5 ns | +0.0% |

## 2. Quantum de Lecture NVMe (O_DIRECT)

| Taille de bloc | P50 (µs) | P90 (µs) | P99 (µs) | Débit (Mo/s) | Coût (ns / Kio) | Jitter (P99/P50) |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 512 B | 9.28 µs | 9.63 µs | 30.34 µs | 52.6 Mo/s | 18553.73 ns | 3.27x |
| 4 Ko | 10.84 µs | 11.65 µs | 49.70 µs | 360.5 Mo/s | 2709.15 ns | 4.59x |
| 8 Ko | 12.20 µs | 30.17 µs | 62.37 µs | 640.1 Mo/s | 1525.54 ns | 5.11x |
| 16 Ko | 14.31 µs | 16.37 µs | 65.83 µs | 1091.8 Mo/s | 894.44 ns | 4.60x |
| 32 Ko | 17.95 µs | 19.35 µs | 80.28 µs | 1741.3 Mo/s | 560.81 ns | 4.47x |
| 64 Ko | 32.99 µs | 41.49 µs | 108.95 µs | 1894.4 Mo/s | 515.51 ns | 3.30x |
| 128 Ko | 51.33 µs | 123.46 µs | 146.20 µs | 2435.4 Mo/s | 400.99 ns | 2.85x |
| 256 Ko | 159.59 µs | 165.70 µs | 167.70 µs | 1566.5 Mo/s | 623.39 ns | 1.05x |

## 3. Quantum d'Écriture NVMe (O_DIRECT + fdatasync)

| Taille de bloc | Pwrite P50 (µs) | Fsync P50 (µs) | Total P50 (µs) | Débit effectif (Mo/s) | IOPS | Jitter (P99/P50) |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 4 Ko | 15.47 µs | 374.86 µs | 390.88 µs | 10.0 Mo/s | 2558.3 ops | 1.70x |
| 8 Ko | 11.34 µs | 348.02 µs | 359.55 µs | 21.7 Mo/s | 2781.2 ops | 1.82x |
| 16 Ko | 13.23 µs | 346.18 µs | 359.54 µs | 43.5 Mo/s | 2781.4 ops | 1.09x |
| 32 Ko | 16.10 µs | 344.58 µs | 360.75 µs | 86.6 Mo/s | 2772.0 ops | 1.12x |
| 64 Ko | 21.45 µs | 371.55 µs | 393.13 µs | 159.0 Mo/s | 2543.7 ops | 1.67x |
| 128 Ko | 32.25 µs | 368.70 µs | 401.16 µs | 311.6 Mo/s | 2492.8 ops | 1.05x |
| 256 Ko | 53.53 µs | 366.89 µs | 420.62 µs | 594.4 Mo/s | 2377.5 ops | 1.84x |

## 4. Comparatif CoW : Recopie Globale vs Masque SIMD

### Recopie intégrale du tas (comportement d'origine)

| Pages copiées | Volume brut | P50 Latence (µs) | P99 Latence (µs) | Débit mémoire DRAM |
| :--- | :--- | :--- | :--- | :--- |
| 16 pages | 256 Ko | 3.42 µs | 7.73 µs | 71.3 Go/s |
| 64 pages | 1 Mo | 19.74 µs | 25.77 µs | 49.5 Go/s |
| 256 pages | 4 Mo | 116.85 µs | 360.84 µs | 33.4 Go/s |
| 1024 pages | 16 Mo | 564.38 µs | 980.09 µs | 27.7 Go/s |
| 4096 pages | 64 Mo | 4482.97 µs | 5870.38 µs | 13.9 Go/s |

### Recopie sélective par Masque Dirty SIMD (tzcnt / blsr)

| Pages modifiées | Volume effectif | P50 Latence (µs) | P99 Latence (µs) | Accélération vs 64 Mo |
| :--- | :--- | :--- | :--- | :--- |
| 1 pages | 16 Ko | 0.10 µs | 0.11 µs | 42777.1x |
| 2 pages | 32 Ko | 0.58 µs | 0.60 µs | 7765.0x |
| 4 pages | 64 Ko | 0.91 µs | 0.93 µs | 4914.9x |
| 8 pages | 128 Ko | 1.79 µs | 1.89 µs | 2505.7x |
| 16 pages | 256 Ko | 3.55 µs | 3.84 µs | 1263.7x |
| 32 pages | 512 Ko | 7.14 µs | 11.50 µs | 627.6x |

## 5. Contexte Transactionnel : goid() vs *Tx explicite

| Approche | Cycles P50 | Latence P50 (ns) | Latence P99 (ns) | Coût relatif |
| :--- | :--- | :--- | :--- | :--- |
| goid_runtime_stack | 2314.0 | 726.06 ns | 4989.54 ns | 100.6x |
| explicit_tx_struct | 23.0 | 7.22 ns | 7.84 ns | 1.0x |

## 6. Balayage Fin des Frontières (64 Ko à 320 Ko et Désalignements)

| Épreuve / Cas Frontière | Pwrite P50 (µs) | Fsync P50 (µs) | Total P50 (µs) | Débit (Mo/s) | Jitter (P99/P50) | Scission Noyau ? |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 64K (Quantum standard) | 40.67 µs | 375.25 µs | 415.51 µs | 150.4 Mo/s | 2.16x | Non |
| 96K (Intermédiaire 6 canaux) | 27.09 µs | 366.98 µs | 394.09 µs | 237.9 Mo/s | 1.33x | Non |
| 124K (128K - 4K) | 31.77 µs | 399.90 µs | 431.75 µs | 280.5 Mo/s | 1.08x | Non |
| 128K (Quantum 8 canaux pile) | 32.13 µs | 367.39 µs | 399.78 µs | 312.7 Mo/s | 1.14x | Non |
| 132K (128K + 4K débordement) | 32.76 µs | 366.46 µs | 399.60 µs | 322.6 Mo/s | 1.04x | Non |
| 160K (10 canaux) | 37.37 µs | 357.11 µs | 394.60 µs | 396.0 Mo/s | 1.07x | Non |
| 192K (12 canaux) | 47.13 µs | 139.17 µs | 187.13 µs | 1002.0 Mo/s | 5.81x | Non |
| 224K (14 canaux) | 52.99 µs | 193.26 µs | 245.88 µs | 889.7 Mo/s | 1.81x | Non |
| 252K (256K - 4K) | 52.66 µs | 185.89 µs | 238.47 µs | 1032.0 Mo/s | 2.05x | Non |
| 256K (MDTS frontière matérielle) | 53.34 µs | 138.07 µs | 191.45 µs | 1305.9 Mo/s | 2.62x | Non |
| 260K (256K + 4K scission noyau) | 54.35 µs | 199.99 µs | 254.33 µs | 998.3 Mo/s | 2.30x | **OUI (> 256 Ko)** |
| 288K (Dépassement MDTS) | 58.98 µs | 192.57 µs | 251.50 µs | 1118.3 Mo/s | 3.10x | **OUI (> 256 Ko)** |
| 320K (Dépassement 2 x 160K) | 64.20 µs | 153.07 µs | 217.11 µs | 1439.4 Mo/s | 2.66x | **OUI (> 256 Ko)** |
| 128K aligné sur 16K (Optimal) | 32.15 µs | 371.62 µs | 404.09 µs | 309.3 Mo/s | 1.60x | Non |
| 128K décalé de 512B (Désalignement secteur) | 81.30 µs | 381.91 µs | 463.34 µs | 269.8 Mo/s | 1.05x | Non |
| 128K décalé de 4K (Désalignement page) | 32.14 µs | 371.54 µs | 403.91 µs | 309.5 Mo/s | 3.39x | Non |
