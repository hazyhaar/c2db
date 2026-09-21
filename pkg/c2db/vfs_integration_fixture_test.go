// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestVFSIntegration_FullRelationalCycle teste un cycle complet relationnel SQLite sur VFS c2db :
// création de table, insertions volumineuses, agrégations complexes, sous-requêtes,
// mises à jour ciblées, suppressions et contrôle d'intégrité B-tree.
func TestVFSIntegration_FullRelationalCycle(t *testing.T) {
	storage := newMemStorage()
	vfsName := fmt.Sprintf("c2db_vfs_cycle_%d", time.Now().UnixNano())
	unregister, err := RegisterC2DBVFS(vfsName, storage)
	if err != nil {
		t.Fatalf("RegisterC2DBVFS: %v", err)
	}
	defer unregister()

	db, err := sql.Open("sqlite", "file:fixture.db?vfs="+vfsName)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// 1. Création de la table users
	_, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		balance REAL NOT NULL,
		role TEXT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE users: %v", err)
	}

	// 2. Insertion de 100 lignes variées
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	stmt, err := tx.Prepare("INSERT INTO users (id, name, balance, role) VALUES (?, ?, ?, ?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer stmt.Close()

	type userRow struct {
		id      int
		name    string
		balance float64
		role    string
	}
	expected := make([]userRow, 100)

	for i := 1; i <= 100; i++ {
		name := fmt.Sprintf("user_%d", i)
		bal := float64(i) * 1.5
		role := "user"
		if i%10 == 0 {
			role = "admin"
		} else if i%3 == 0 {
			role = "guest"
		}
		expected[i-1] = userRow{id: i, name: name, balance: bal, role: role}
		if _, err := stmt.Exec(i, name, bal, role); err != nil {
			t.Fatalf("stmt.Exec(%d): %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// 3. Requêtes relationnelles complexes
	// Calcul théorique pour balance > 50 :
	var wantCount int
	var wantSum float64
	for _, u := range expected {
		if u.balance > 50.0 {
			wantCount++
			wantSum += u.balance
		}
	}
	wantAvg := wantSum / float64(wantCount)

	var gotCount int
	var gotSum, gotAvg float64
	err = db.QueryRow("SELECT COUNT(*), SUM(balance), AVG(balance) FROM users WHERE balance > 50").Scan(&gotCount, &gotSum, &gotAvg)
	if err != nil {
		t.Fatalf("SELECT aggregate: %v", err)
	}
	if gotCount != wantCount {
		t.Errorf("COUNT(*) = %d, want %d", gotCount, wantCount)
	}
	if math.Abs(gotSum-wantSum) > 1e-4 {
		t.Errorf("SUM(balance) = %f, want %f", gotSum, wantSum)
	}
	if math.Abs(gotAvg-wantAvg) > 1e-4 {
		t.Errorf("AVG(balance) = %f, want %f", gotAvg, wantAvg)
	}

	// Jointure ou sous-requête :
	var maxUser string
	err = db.QueryRow("SELECT u1.name FROM users u1 WHERE u1.balance = (SELECT MAX(u2.balance) FROM users u2)").Scan(&maxUser)
	if err != nil {
		t.Fatalf("SELECT subquery: %v", err)
	}
	if maxUser != "user_100" {
		t.Errorf("maxUser = %q, want user_100", maxUser)
	}

	// 4. UPDATE de lignes avec vérification des valeurs mises à jour
	res, err := db.Exec("UPDATE users SET balance = balance + 10 WHERE role = 'admin'")
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if affected != 10 {
		t.Errorf("RowsAffected = %d, want 10", affected)
	}

	// Vérifier la valeur mise à jour pour user_10 (10 * 1.5 + 10 = 25.0)
	var bal10 float64
	err = db.QueryRow("SELECT balance FROM users WHERE id = 10").Scan(&bal10)
	if err != nil || math.Abs(bal10-25.0) > 1e-4 {
		t.Errorf("balance user_10 = %f, want 25.0 (err=%v)", bal10, err)
	}

	// 5. DELETE de certaines lignes avec vérification du décompte
	res, err = db.Exec("DELETE FROM users WHERE role = 'guest'")
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	delAffected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if delAffected <= 0 {
		t.Errorf("delAffected = %d, attendu > 0", delAffected)
	}

	var guestCount int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'guest'").Scan(&guestCount)
	if err != nil || guestCount != 0 {
		t.Errorf("guestCount après DELETE = %d, want 0 (err=%v)", guestCount, err)
	}

	// 6. PRAGMA integrity_check -> validation stricte que le résultat est "ok"
	var integrityResult string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil {
		t.Fatalf("PRAGMA integrity_check: %v", err)
	}
	if integrityResult != "ok" {
		t.Fatalf("PRAGMA integrity_check = %q, want 'ok'", integrityResult)
	}
}

// TestVFSIntegration_TransactionRollback teste l'atomicité et l'annulation stricte
// des écritures via le rollback journal virtuel c2db.
func TestVFSIntegration_TransactionRollback(t *testing.T) {
	storage := newMemStorage()
	vfsName := fmt.Sprintf("c2db_vfs_rollback_%d", time.Now().UnixNano())
	unregister, err := RegisterC2DBVFS(vfsName, storage)
	if err != nil {
		t.Fatalf("RegisterC2DBVFS: %v", err)
	}
	defer unregister()

	db, err := sql.Open("sqlite", "file:rollback.db?vfs="+vfsName)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE ledger (id INTEGER PRIMARY KEY, note TEXT);")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	_, err = db.Exec("INSERT INTO ledger (id, note) VALUES (1, 'initial');")
	if err != nil {
		t.Fatalf("INSERT initial: %v", err)
	}

	// Début de transaction explicite
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Insère un enregistrement clé
	_, err = tx.Exec("INSERT INTO ledger (id, note) VALUES (2, 'phantom_record');")
	if err != nil {
		t.Fatalf("tx.Exec insert: %v", err)
	}

	// Exécute tx.Rollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}

	// Vérifie par un SELECT que l'enregistrement n'existe pas
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM ledger WHERE id = 2").Scan(&count)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if count != 0 {
		t.Fatalf("Enregistrement 2 présent après Rollback ! count=%d", count)
	}

	// Vérifie que l'enregistrement initial est intact
	var note1 string
	err = db.QueryRow("SELECT note FROM ledger WHERE id = 1").Scan(&note1)
	if err != nil || note1 != "initial" {
		t.Fatalf("Enregistrement 1 altéré: got=%q err=%v", note1, err)
	}

	// Validation d'intégrité
	var integrityResult string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil || integrityResult != "ok" {
		t.Fatalf("PRAGMA integrity_check: %q (err=%v)", integrityResult, err)
	}
}

// TestVFSIntegration_ColdReopenDurability teste la persistance et la réouverture à froid :
// 1. Création d'une instance c2db réelle sur disque via OpenDB(t.TempDir(), ...).
// 2. Enregistrement du VFS c2db.
// 3. Insertion de 500 enregistrements, COMMIT.
// 4. Fermeture complète de la connexion SQLite.
// 5. Désenregistrement du VFS.
// 6. FERMETURE COMPLÈTE de l'instance c2db sous-jacente (c2dbInstance.Close()).
// 7. Réouverture à froid d'une NOUVELLE instance c2db (OpenDB) depuis le disque.
// 8. Enregistrement à nouveau du VFS sur la nouvelle instance c2db.
// 9. Ouverture d'une toute nouvelle connexion SQLite sql.Open.
// 10. Relecture intégrale des 500 enregistrements avec comparaison bit-à-bit.
// 11. Validation stricte PRAGMA integrity_check == "ok".
// 12. Fermeture propre de SQLite et de la nouvelle instance c2db.
func TestVFSIntegration_ColdReopenDurability(t *testing.T) {
	tempDir := t.TempDir()
	var masterKey [32]byte
	for i := range masterKey {
		masterKey[i] = byte(i + 1)
	}

	// 1. Créer une instance c2db réelle sur disque
	c2dbInstance1, err := OpenDB(tempDir, masterKey)
	if err != nil {
		t.Fatalf("OpenDB (phase 1): %v", err)
	}

	// 2. Enregistrer le VFS c2db
	vfsName := fmt.Sprintf("c2db_vfs_durability_%d", time.Now().UnixNano())
	unregister1, err := RegisterC2DBVFS(vfsName, c2dbInstance1)
	if err != nil {
		_ = c2dbInstance1.Close()
		t.Fatalf("RegisterC2DBVFS (phase 1): %v", err)
	}

	// 3. Ouvrir la connexion SQLite, insérer 500 enregistrements, COMMIT
	db1, err := sql.Open("sqlite", "file:durability.db?vfs="+vfsName)
	if err != nil {
		unregister1()
		_ = c2dbInstance1.Close()
		t.Fatalf("sql.Open db1: %v", err)
	}

	_, err = db1.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY, key TEXT, val BLOB);")
	if err != nil {
		_ = db1.Close()
		unregister1()
		_ = c2dbInstance1.Close()
		t.Fatalf("CREATE TABLE: %v", err)
	}

	tx, err := db1.Begin()
	if err != nil {
		_ = db1.Close()
		unregister1()
		_ = c2dbInstance1.Close()
		t.Fatalf("Begin: %v", err)
	}
	stmt, err := tx.Prepare("INSERT INTO records (id, key, val) VALUES (?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		_ = db1.Close()
		unregister1()
		_ = c2dbInstance1.Close()
		t.Fatalf("Prepare: %v", err)
	}

	type recItem struct {
		id  int
		key string
		val []byte
	}
	testRecords := make([]recItem, 500)
	for i := 0; i < 500; i++ {
		r := recItem{
			id:  i + 1,
			key: fmt.Sprintf("key_%04d", i+1),
			val: []byte(fmt.Sprintf("payload_content_%d_verified_bytes", i+1)),
		}
		testRecords[i] = r
		if _, err := stmt.Exec(r.id, r.key, r.val); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			_ = db1.Close()
			unregister1()
			_ = c2dbInstance1.Close()
			t.Fatalf("stmt.Exec(%d): %v", i+1, err)
		}
	}
	_ = stmt.Close()

	if err := tx.Commit(); err != nil {
		_ = db1.Close()
		unregister1()
		_ = c2dbInstance1.Close()
		t.Fatalf("Commit: %v", err)
	}

	// 4. Fermer la connexion SQLite (db.Close())
	if err := db1.Close(); err != nil {
		unregister1()
		_ = c2dbInstance1.Close()
		t.Fatalf("db1.Close: %v", err)
	}

	// 5. Désenregistrer le VFS
	unregister1()

	// 6. FERMER COMPLÈTEMENT l'instance c2db sous-jacente
	if err := c2dbInstance1.Close(); err != nil {
		t.Fatalf("c2dbInstance1.Close: %v", err)
	}

	// 7. Réouvrir à froid une NOUVELLE instance c2db (OpenDB(même répertoire, ...)) depuis le disque
	c2dbInstance2, err := OpenDB(tempDir, masterKey)
	if err != nil {
		t.Fatalf("OpenDB à froid (phase 2): %v", err)
	}
	defer func() {
		if c2dbInstance2 != nil {
			_ = c2dbInstance2.Close()
		}
	}()

	// 8. Enregistrer à nouveau le VFS sur la nouvelle instance c2db
	unregister2, err := RegisterC2DBVFS(vfsName, c2dbInstance2)
	if err != nil {
		t.Fatalf("RegisterC2DBVFS (phase 2): %v", err)
	}
	defer unregister2()

	// 9. Ouvrir une toute nouvelle connexion SQLite sql.Open
	db2, err := sql.Open("sqlite", "file:durability.db?vfs="+vfsName)
	if err != nil {
		t.Fatalf("sql.Open db2: %v", err)
	}
	defer db2.Close()

	// 10. Relire l'intégralité des 500 enregistrements, comparer bit-à-bit avec les données attendues
	rows, err := db2.Query("SELECT id, key, val FROM records ORDER BY id ASC")
	if err != nil {
		t.Fatalf("Query db2: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var key string
		var val []byte
		if err := rows.Scan(&id, &key, &val); err != nil {
			t.Fatalf("Scan row %d: %v", count+1, err)
		}
		if count >= len(testRecords) {
			t.Fatalf("Lignes excédentaires: %d > %d", count+1, len(testRecords))
		}
		want := testRecords[count]
		if id != want.id || key != want.key || !bytes.Equal(val, want.val) {
			t.Fatalf("Incohérence à la ligne %d: got=(%d, %q, %s), want=(%d, %q, %s)",
				count+1, id, key, val, want.id, want.key, want.val)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if count != 500 {
		t.Fatalf("Nombre total relu = %d, attendu 500", count)
	}

	// 11. Exécuter PRAGMA integrity_check -> valider que le résultat est "ok"
	var integrityResult string
	err = db2.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil || integrityResult != "ok" {
		t.Fatalf("PRAGMA integrity_check db2: %q (err=%v)", integrityResult, err)
	}

	// 12. Fermer proprement SQLite et la nouvelle instance c2db
	if err := db2.Close(); err != nil {
		t.Fatalf("db2.Close: %v", err)
	}
	unregister2()
	if err := c2dbInstance2.Close(); err != nil {
		t.Fatalf("c2dbInstance2.Close: %v", err)
	}
	c2dbInstance2 = nil
}

// TestVFSIntegration_ExtremeConcurrentFuzzing fuzze concurremment le VFS c2db sur une VRAIE
// instance c2db disque sous race detector :
// 32 goroutines exécutant 20 opérations financières (30% lectures, 70% virements).
// Applique un backoff exponentiel court avec jitter sur contention SQLITE_BUSY / locked.
// Consigne chaque virement commité dans un journal d'audit indépendant protégé par mutex.
// Valide le solde exact de CHAQUE compte individuellement d'après l'audit (absence de lost update),
// la masse monétaire globale et l'intégrité B-tree finale.
func TestVFSIntegration_ExtremeConcurrentFuzzing(t *testing.T) {
	base := os.Getenv("DEVHOROS_TMPDIR")
	if base == "" {
		base = "/devhoros/.tmp_fixtures"
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("MkdirAll base: %v", err)
	}
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "fuzz_") {
				_ = os.RemoveAll(filepath.Join(base, e.Name()))
			}
		}
	}
	dir := filepath.Join(base, fmt.Sprintf("fuzz_%d_%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll dir: %v", err)
	}
	defer os.RemoveAll(dir)

	var masterKey [32]byte
	for i := range masterKey {
		masterKey[i] = byte(i + 0x42)
	}

	// 1. Exécuter le test sur une VRAIE instance c2db sur disque
	c2dbInstance, err := OpenDB(dir, masterKey)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() {
		if c2dbInstance != nil {
			_ = c2dbInstance.Close()
		}
	}()

	vfsName := fmt.Sprintf("c2db_vfs_fuzz_%d", time.Now().UnixNano())
	unregister, err := RegisterC2DBVFS(vfsName, c2dbInstance)
	if err != nil {
		t.Fatalf("RegisterC2DBVFS: %v", err)
	}
	defer unregister()
	c2dbInstance.SetBusyTimeout(0)

	db, err := sql.Open("sqlite", fmt.Sprintf("file:fuzz.db?vfs=%s&_busy_timeout=150&_pragma=busy_timeout(150)&_txlock=immediate", vfsName))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Limites de pool pour forcer l'ouverture simultanée de nombreuses connexions physiques SQLite
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(50)

	// Configuration du busy_timeout SQLite
	if _, err := db.Exec("PRAGMA busy_timeout = 150;"); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}

	// Initialisation des tables
	_, err = db.Exec("CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance REAL NOT NULL);")
	if err != nil {
		t.Fatalf("CREATE TABLE accounts: %v", err)
	}

	const numAccounts = 10
	const initialBalancePerAccount = 1000.0
	const expectedTotalMoney = float64(numAccounts) * initialBalancePerAccount // 10000.0

	initTx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin init: %v", err)
	}
	for i := 1; i <= numAccounts; i++ {
		if _, err := initTx.Exec("INSERT INTO accounts (id, balance) VALUES (?, ?)", i, initialBalancePerAccount); err != nil {
			_ = initTx.Rollback()
			t.Fatalf("Insert account %d: %v", i, err)
		}
	}
	if err := initTx.Commit(); err != nil {
		t.Fatalf("Commit init: %v", err)
	}

	// 3. Journal d'audit indépendant en mémoire protégé par son propre sync.Mutex
	type auditTransfer struct {
		fromID int
		toID   int
		amount float64
	}
	var (
		auditMu  sync.Mutex
		auditLog []auditTransfer
	)

	const numGoroutines = 32
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	isLockedOrBusy := func(err error) bool {
		if err == nil {
			return false
		}
		msg := strings.ToLower(err.Error())
		return strings.Contains(msg, "busy") ||
			strings.Contains(msg, "locked") ||
			strings.Contains(msg, "sqlite_busy") ||
			strings.Contains(msg, "errbusy") ||
			strings.Contains(msg, "writer busy") ||
			strings.Contains(msg, "(5)") ||
			strings.Contains(msg, "heap full") ||
			strings.Contains(msg, "view held") ||
			strings.Contains(msg, "insert rejected") ||
			strings.Contains(msg, "778")
	}

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(gid+1), uint64(time.Now().UnixNano())))

			for op := 0; op < opsPerGoroutine; op++ {
				// 30% lectures globales, 70% virements
				isReadOp := r.Float64() < 0.30

				const maxRetries = 5000
				success := false

				backoff := 1 * time.Millisecond
				const maxBackoff = 50 * time.Millisecond

				var lastErr error
				for retry := 0; retry < maxRetries; retry++ {
					sleepBackoff := func() {
						jitter := time.Duration(r.Int64N(int64(backoff)/2 + 1))
						time.Sleep(backoff + jitter)
						backoff *= 2
						if backoff > maxBackoff {
							backoff = maxBackoff
						}
					}

					if isReadOp {
						tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
						if err != nil {
							lastErr = err
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] Begin read: %v", gid, err)
							return
						}

						var currentTotal float64
						err = tx.QueryRow("SELECT SUM(balance) FROM accounts").Scan(&currentTotal)
						if err != nil {
							lastErr = err
							_ = tx.Rollback()
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] Read SUM: %v", gid, err)
							return
						}
						_ = tx.Rollback()

						if math.Abs(currentTotal-expectedTotalMoney) > 1e-4 {
							t.Errorf("[G%d] Détection anomalie masse monétaire en lecture: got=%f, want=%f",
								gid, currentTotal, expectedTotalMoney)
						}
						success = true
						break
					} else {
						// Transaction de virement
						fromID := 1 + r.IntN(numAccounts)
						toID := 1 + r.IntN(numAccounts)
						for toID == fromID {
							toID = 1 + r.IntN(numAccounts)
						}
						amt := float64(1 + r.IntN(20))

						tx, err := db.Begin()
						if err != nil {
							lastErr = err
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] Begin tx virement: %v", gid, err)
							return
						}

						var fromBal float64
						err = tx.QueryRow("SELECT balance FROM accounts WHERE id = ?", fromID).Scan(&fromBal)
						if err != nil {
							lastErr = err
							_ = tx.Rollback()
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] Query balance: %v", gid, err)
							return
						}

						if fromBal < amt {
							_ = tx.Rollback()
							success = true
							break
						}

						if _, err = tx.Exec("UPDATE accounts SET balance = balance - ? WHERE id = ?", amt, fromID); err != nil {
							lastErr = err
							_ = tx.Rollback()
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] UPDATE from: %v", gid, err)
							return
						}

						if _, err = tx.Exec("UPDATE accounts SET balance = balance + ? WHERE id = ?", amt, toID); err != nil {
							lastErr = err
							_ = tx.Rollback()
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] UPDATE to: %v", gid, err)
							return
						}

						if err = tx.Commit(); err != nil {
							lastErr = err
							_ = tx.Rollback()
							if isLockedOrBusy(err) {
								sleepBackoff()
								continue
							}
							t.Errorf("[G%d] Commit virement: %v", gid, err)
							return
						}

						// Virement confirmé avec succès -> consignation dans le journal d'audit
						auditMu.Lock()
						auditLog = append(auditLog, auditTransfer{
							fromID: fromID,
							toID:   toID,
							amount: amt,
						})
						auditMu.Unlock()

						success = true
						break
					}
				}

				if !success {
					t.Errorf("[G%d] Échec de l'opération %d après retries maximales: lastErr=%v", gid, op, lastErr)
				}
			}
		}(g)
	}

	wg.Wait()

	// 4. Validation finale :
	// a) Recalculer le solde attendu de CHAQUE compte individuellement d'après l'état initial et l'audit
	expectedBalances := make(map[int]float64, numAccounts)
	for i := 1; i <= numAccounts; i++ {
		expectedBalances[i] = initialBalancePerAccount
	}
	auditMu.Lock()
	for _, entry := range auditLog {
		expectedBalances[entry.fromID] -= entry.amount
		expectedBalances[entry.toID] += entry.amount
	}
	auditMu.Unlock()

	// b) Relire le solde de chaque compte individuel depuis SQLite et vérifier l'égalité exacte compte par compte
	for i := 1; i <= numAccounts; i++ {
		var gotBal float64
		err := db.QueryRow("SELECT balance FROM accounts WHERE id = ?", i).Scan(&gotBal)
		if err != nil {
			t.Fatalf("SELECT balance account %d: %v", i, err)
		}
		wantBal := expectedBalances[i]
		if math.Abs(gotBal-wantBal) > 1e-4 {
			t.Fatalf("Divergence de solde pour le compte %d: got=%f, want=%f (delta=%f)",
				i, gotBal, wantBal, gotBal-wantBal)
		}
	}

	// c) Vérification globale de la conservation de la masse monétaire
	var finalTotal float64
	err = db.QueryRow("SELECT SUM(balance) FROM accounts").Scan(&finalTotal)
	if err != nil {
		t.Fatalf("SELECT final SUM: %v", err)
	}
	t.Logf("Conservation monétaire validée : solde=%.6f attendu=%.6f delta=%e", finalTotal, expectedTotalMoney, math.Abs(finalTotal-expectedTotalMoney))
	if math.Abs(finalTotal-expectedTotalMoney) > 1e-4 {
		t.Fatalf("PERTE OU CRÉATION MONÉTAIRE: got=%f, want=%f", finalTotal, expectedTotalMoney)
	}

	// d) Exécution de PRAGMA integrity_check -> doit retourner "ok"
	var integrityResult string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil {
		t.Fatalf("PRAGMA integrity_check final: %v", err)
	}
	t.Logf("Intégrité SQLite validée : PRAGMA integrity_check = %s", integrityResult)
	if integrityResult != "ok" {
		t.Fatalf("CORRUPTION B-TREE DÉTECTÉE: integrity_check = %q, want 'ok'", integrityResult)
	}

	n := -1
	if c2dbInstance != nil {
		c2dbInstance.mu.RLock()
		if c2dbInstance.shards != nil {
			n = len(c2dbInstance.shards)
		}
		c2dbInstance.mu.RUnlock()
	}
	if n < 0 {
		n = 0
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir %s: %v", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() || len(e.Name()) != 4 {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "data.img")); err != nil {
				continue
			}
			n++
		}
	}
	t.Logf("shards observés: %d", n)
	if n != 1 {
		t.Fatalf("shards=%d, want 1", n)
	}

	// f) Fermer SQLite et l'instance c2db
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	unregister()
	if err := c2dbInstance.Close(); err != nil {
		t.Fatalf("c2dbInstance.Close: %v", err)
	}
	c2dbInstance = nil
}
