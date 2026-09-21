// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	primaryRealDB   = "/devhoros/horos55/data/sources.db"
	secondaryRealDB = "/devhoros/horos55/data/k_rules.db"
)

// loadRealBenchmarkKV charge n couples (clé, valeur) exclusivement extraits
// de bases de données réelles de production (sources.db, k_rules.db).
// Conformément à la doctrine anti-factive et aux exigences d'homologation d'Astra (R2.3),
// toute fabrication synthétique ou pseudo-aléatoire est formellement prohibée.
func loadRealBenchmarkKV(t testing.TB, n int) (keys [][]byte, vals [][]byte, provenance string) {
	t.Helper()
	keys, vals, prov, err := loadRealBenchmarkKVInternal(n)
	if err != nil {
		t.Fatalf("chargement corpus réel impossible (règle anti-factive): %v", err)
	}
	return keys, vals, prov
}

func loadRealBenchmarkKVInternal(n int) (keys [][]byte, vals [][]byte, provenance string, err error) {
	keys = make([][]byte, 0, n)
	vals = make([][]byte, 0, n)

	// 1. Tenter d'abord la base de sources d'acquisition EverySprings (sources.db : ~9600 enregistrements réels)
	sourcesLoaded := 0
	if _, statErr := os.Stat(primaryRealDB); statErr == nil {
		db, dbErr := sql.Open("sqlite", "file:"+primaryRealDB+"?mode=ro")
		if dbErr == nil {
			defer db.Close()
			rows, qErr := db.Query(`SELECT id, label, remote_url, config_json FROM source ORDER BY id LIMIT ?`, n)
			if qErr == nil {
				defer rows.Close()
				for rows.Next() {
					var id, label, remoteURL, configJSON string
					if scanErr := rows.Scan(&id, &label, &remoteURL, &configJSON); scanErr == nil {
						payload, mErr := json.Marshal(map[string]string{
							"label":       label,
							"remote_url":  remoteURL,
							"config_json": configJSON,
						})
						if mErr == nil {
							keys = append(keys, []byte(id))
							vals = append(vals, payload)
							sourcesLoaded++
							if len(keys) >= n {
								break
							}
						}
					}
				}
			}
		}
	}

	// 2. Si besoin de plus de paires, compléter avec les règles réelles (k_rules.db : 259 règles)
	rulesLoaded := 0
	if len(keys) < n {
		if _, statErr := os.Stat(secondaryRealDB); statErr == nil {
			db, dbErr := sql.Open("sqlite", "file:"+secondaryRealDB+"?mode=ro")
			if dbErr == nil {
				defer db.Close()
				remaining := n - len(keys)
				rows, qErr := db.Query(`SELECT slug, title, description, remediation_prompt FROM k_rules ORDER BY id LIMIT ?`, remaining)
				if qErr == nil {
					defer rows.Close()
					for rows.Next() {
						var slug, title, descr, prompt string
						if scanErr := rows.Scan(&slug, &title, &descr, &prompt); scanErr == nil {
							payload, mErr := json.Marshal(map[string]string{
								"title":              title,
								"description":        descr,
								"remediation_prompt": prompt,
							})
							if mErr == nil {
								keys = append(keys, []byte(slug))
								vals = append(vals, payload)
								rulesLoaded++
								if len(keys) >= n {
									break
								}
							}
						}
					}
				}
			}
		}
	}

	if len(keys) < n {
		return nil, nil, "", fmt.Errorf("matière réelle insuffisante : %d couples obtenus sur %d requis (sources=%d, k_rules=%d)",
			len(keys), n, sourcesLoaded, rulesLoaded)
	}

	provenance = fmt.Sprintf("Données réelles authentiques : %d lignes de %s et %d lignes de %s (total=%d paires réelles)",
		sourcesLoaded, primaryRealDB, rulesLoaded, secondaryRealDB, len(keys))
	return keys, vals, provenance, nil
}
