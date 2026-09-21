# Banc double — fonctionnel et mutation, assembleur et I/O

## Assembleur (go tool objdump)

| Symbole | CALL | SYSCALL | Fichier |
| :--- | ---: | ---: | :--- |
| `publish.s` | 59 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/publish.s` |
| `putBody.s` | 19 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/putBody.s` |
| `GetAsOf.s` | 6 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/GetAsOf.s` |
| `pinLive.s` | 0 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/pinLive.s` |
| `writeOne.s` | 10 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/writeOne.s` |
| `verifyPage.s` | 6 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/verifyPage.s` |
| `pwriteAll.s` | 8 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/pwriteAll.s` |
| `lockWriter.s` | 6 | 0 | `/devhoros/c2simd/c2pkg/c2db/bench_asm/lockWriter.s` |

## A. Strates fonctionnelles

| Nom | Moteur | ns/op | IPC | write sysc | write octets | Preuve |
| :--- | :--- | ---: | ---: | ---: | ---: | :--- |
| insert | c2db | 98827 | 1.72 | 5 | 20480 | GetDoc==Insert bit-exact |
| insert | sqlite | 621489 | 0.62 | 528 | 1667072 | INSERT kv |
| get | c2db | 3027 | 4.71 | 0 | 0 | pinLive+GetAsOf |
| get | sqlite | 5919 | 2.20 | 0 | 0 | SELECT by key |
| scan_filter | c2db | 790739 | 2.74 | 3 | 24576 | filter eq status=open N>0 |
| cas | c2db | 776986 | 2.62 | 3 | 24576 | rejet expect puis RecMut |
| lot | c2db | 385555 | 2.68 | 3 | 24576 | 2 ins un fdatasync |
| view | c2db | 58511 | 2.02 | 0 | 0 | View gelé v1 pendant Put v2 |

## B. Strates mutation de donnée

| Nom | ns/op | IPC | L1D miss | write octets | Preuve |
| :--- | ---: | ---: | ---: | ---: | :--- |
| json_large | 46821 | 2.26 | 9834 | 0 | Insert 512B |
| uuid_overlay | 62540 | 2.18 | 38389 | 8192 | KindOf==IDKindPut |
| recmut | 1456535 | 4.03 | 3959 | 49152 | cas op=3 incr |
| cow_grow | 67549 | 2.39 | 156003 | 8192 | heapUsed croît |

Logs assembleur : `/devhoros/c2simd/c2pkg/c2db/bench_asm`.
