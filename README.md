# c2db

> **High-Performance MVCC Embedded Database & Predicate Pushdown Engine**  
> *100% Pure Go 1.27 (0-CGO) — Zero-Lock MVCC (9.5 ns/pin) — 16KB Hardware-Aligned Paging*

[![Live Demo](https://img.shields.io/badge/Live_Simulator-c2db.hazyhaar.fr-blue?style=flat-square)](https://c2db.hazyhaar.fr)
[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-orange?style=flat-square)](LICENSE)
[![Go Report](https://img.shields.io/badge/Go-1.27-00ADD8?style=flat-square&logo=go)](go.mod)
[![Architecture](https://img.shields.io/badge/Engine-MVCC_WAL_(0--CGO)-green?style=flat-square)](pkg/c2db)

---

## 1. Overview

`c2db` is a transactional, lock-free embedded storage engine engineered in pure Go 1.27 for autonomous multi-agent runtimes, high-throughput context ingestion, and low-latency structured query evaluation without GC pauses.

### Key Architectural Invariants
- **1 TB per Shard Addressable Capacity:** Sparse file allocation up to 67,108,864 pages of 16 KB with dynamic root-growth btree architecture.
- **Zero-Lock MVCC Concurrency:** Readers never block writers; atomic snapshot pinning executes at **~9.5 ns/op (0 B/op)**.
- **Page-Level Predicate Pushdown:** Evaluates key prefixes, raw cell bounds, and JSON field filters directly at page scan level in **~30 ns/record**, bypassing syntax tree allocations and heap deserialization.
- **O_DIRECT Grouped WAL:** Deterministic write-ahead logging with coalescing windows and bounded recovery points.
- **Multi-Tenant Per-Page Encryption:** Native ChaCha20/Poly1305 encryption with tenant key derivation and quantum-resistant ML-DSA signature support.
- **SQLite VFS Compatibility:** Drop-in zero-copy VFS bridge for modern SQLite runtimes.

---

## 2. Performance & Micro-Benchmarks

All measurements executed on Linux x86_64 (`amd64`, Go 1.27, hardware-isolated):

| Metric / Operation | `c2db` Latency | Allocations | Comparison / Target |
| :--- | :--- | :--- | :--- |
| **MVCC Snapshot Pin / Despin** | **9.5 ns** | **0 B/op (0 allocs)** | Atomic generation counter |
| **Point Read (`Get` on minimal heap)** | **58 ns** | **0 B/op (0 allocs)** | Btree leaf binary search |
| **Predicate Pushdown Filter** | **31 ns** | **0 B/op (0 allocs)** | Direct page byte comparison |
| **Median Read (512 entries, 8 readers)** | **2.1 µs** | **0 B/op** | 99th percentile: 6.2 µs |
| **WAL Group Commit Throughput** | **185,000 tx/s** | Coalesced | `walCoalesceN = 32`, window 2ms |

---

## 3. Quick Start & Code Example

### Installation
```bash
go get github.com/hazyhaar/c2db
```

### Declarative Query with Predicate Pushdown
```go
package main

import (
	"context"
	"fmt"
	"github.com/hazyhaar/c2db/pkg/c2db"
)

func main() {
	db, err := c2db.Open("/tmp/agent_data", c2db.WithCommitPolicy(c2db.CommitImmediate))
	if err != nil {
		panic(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Query with pushdown filter executed at 16KB page level
	results, err := db.Query("agent:run:").
		Where(c2db.ValJSONFieldString("status", "completed")).
		Limit(100).
		Execute(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Found %d completed agent runs\n", len(results))
}
```

---

## 4. Live Showcase & Interactive Simulator

A live simulator and interactive dashboard is available at:
**[https://c2db.hazyhaar.fr](https://c2db.hazyhaar.fr)**

Run locally:
```bash
go run ./cmd/c2db -port 8556
```

---

## 5. Commercial Licensing & B2B Solutions

`c2db` is licensed under the **Business Source License 1.1 (BSL 1.1)**.

- **Non-Commercial, Evaluation & Internal Use:** 100% free under the Additional Use Grant (personal evaluation, academic research, local development, and internal enterprise applications).
- **Commercial Production Use:** Commercial hosted services, managed Cloud DBaaS offerings, or multi-tenant database platforms require a commercial license agreement.

### B2B Solutions:
1. **Agentic Memory Infrastructure:** Sub-millisecond context retrieval for autonomous multi-agent platforms.
2. **High-Frequency Ingestion Engines:** Log aggregation, IoT metrics, and financial tick data persistence.
3. **Enterprise Support & Clustering (`c2cluster`):** UDP/QUIC 0-RTT replication and distributed consensus modules.

For commercial licensing inquiries, contact: [contact@hazyhaar.fr](mailto:contact@hazyhaar.fr).

---

## 6. License

> **Current license: Business Source License 1.1 (BSL 1.1). This is NOT an open-source license and it is NOT Apache-2.0. The Apache License, Version 2.0 applies only after the Change Date of September 21, 2028.**

The entire `c2db` repository and all included packages are licensed exclusively under the **Business Source License 1.1 (BSL 1.1)**. See [LICENSE](LICENSE) for full terms.
