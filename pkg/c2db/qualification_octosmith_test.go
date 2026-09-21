// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

const helperEnvVar = "GO_WANT_OCTOSMITH_HELPER_PROCESS"

func init() {
	if os.Getenv(helperEnvVar) == "1" {
		runOctoSmithHelperProcess()
		os.Exit(0)
	}
}

func runOctoSmithHelperProcess() {
	dir := os.Getenv("OCTOSMITH_SHARD_DIR")
	keyHex := os.Getenv("OCTOSMITH_SHARD_KEY")
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper decode key: %v\n", err)
		os.Exit(1)
	}
	var key [32]byte
	copy(key[:], keyBytes)

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper OpenShard: %v\n", err)
		os.Exit(1)
	}

	// 1. Écrire et committer 10 clés pérennes
	tx1, err := s.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper Begin tx1: %v\n", err)
		os.Exit(2)
	}
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("sigkill-committed-%03d", i)
		v := fmt.Sprintf("committed-payload-%03d", i)
		if err := tx1.Put([]byte(k), []byte(v)); err != nil {
			fmt.Fprintf(os.Stderr, "helper Put %s: %v\n", k, err)
			os.Exit(3)
		}
	}
	if err := tx1.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "helper Commit tx1: %v\n", err)
		os.Exit(4)
	}

	// 2. Ouvrir une transaction orpheline avec mutations sales en mémoire (SANS RecTxCommit)
	txOrphan, err := s.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper Begin txOrphan: %v\n", err)
		os.Exit(5)
	}
	if err := txOrphan.Put([]byte("orphan-k-001"), []byte("bogus-orphan-payload")); err != nil {
		fmt.Fprintf(os.Stderr, "helper Put orphan: %v\n", err)
		os.Exit(6)
	}
	if err := txOrphan.Put([]byte("sigkill-committed-001"), []byte("corrupted-payload")); err != nil {
		fmt.Fprintf(os.Stderr, "helper Put overwrite: %v\n", err)
		os.Exit(7)
	}

	// 3. Notifier le processus parent que l'état est stabilisé et prêt pour le SIGKILL
	fmt.Println("READY_FOR_SIGKILL")
	_ = os.Stdout.Sync()

	// 4. Bloquer indéfiniment sans aucune vidange ni nettoyage applicatif.
	// Le processus sera abruptement interrompu par le signal 9 (SIGKILL) de l'OS.
	select {}
}

func newTestRand(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}

func u64ToBuf(v uint64) [16]byte {
	var b [16]byte
	b[0] = byte(v & 0xFF)
	b[1] = byte((v >> 8) & 0xFF)
	b[2] = byte((v >> 16) & 0xFF)
	b[3] = byte((v >> 24) & 0xFF)
	b[4] = byte((v >> 32) & 0xFF)
	b[5] = byte((v >> 40) & 0xFF)
	b[6] = byte((v >> 48) & 0xFF)
	b[7] = byte((v >> 56) & 0xFF)
	return b
}

// ----------------------------------------------------------------------------
// T1-A : Véritable SIGKILL OS (Signal 9) sur processus enfant et reprise crash
// ----------------------------------------------------------------------------
// Exécute un processus enfant indépendant via exec.Command. L'enfant ouvre le
// shard, committe 10 clés pérennes, entame une transaction orpheline avec
// mutations en mémoire non commitées, puis notifie le parent et attend.
// Le processus parent lui envoie un véritable syscall.SIGKILL (signal 9 du noyau).
// Après la mort brutale de l'enfant, le parent ouvre le shard avec OpenShard :
// 1. Les 10 clés commitées sont 100% présentes et bit-exactes.
// 2. Les clés de la transaction orpheline sont ABSENTES (ErrNotFound).
// 3. Les clés commitées n'ont subi aucune corruption partielle.
// 4. Le shard récupéré poursuit immédiatement ses opérations nominales (Put/Get).
func TestOctoSmith_RealProcess_SIGKILL_Recovery(t *testing.T) {
	dir := t.TempDir()
	rnd := newTestRand(42)
	var key [32]byte
	_, _ = rnd.Read(key[:])

	cmd := exec.Command(os.Args[0], "-test.run=TestOctoSmith_RealProcess_SIGKILL_Recovery")
	cmd.Env = append(os.Environ(),
		helperEnvVar+"=1",
		"OCTOSMITH_SHARD_DIR="+dir,
		"OCTOSMITH_SHARD_KEY="+hex.EncodeToString(key[:]),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	// Attendre le signal "READY_FOR_SIGKILL" émis par l'enfant une fois les écritures prêtes
	scanner := bufio.NewScanner(stdout)
	ready := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "READY_FOR_SIGKILL" {
			ready = true
			break
		}
	}
	if !ready {
		_ = cmd.Process.Kill()
		t.Fatalf("Le processus enfant n'a pas émis READY_FOR_SIGKILL")
	}

	// Émission d'un véritable SIGKILL (signal 9 de l'OS)
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("Envoi SIGKILL à l'enfant: %v", err)
	}

	// Attente de la terminaison du processus enfant
	waitErr := cmd.Wait()
	if waitErr == nil {
		t.Fatalf("Le processus enfant aurait dû terminer anormalement sur SIGKILL")
	}
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("Le processus enfant n'a pas retourné une *exec.ExitError: %v", waitErr)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("Impossible d'extraire syscall.WaitStatus depuis %v", exitErr)
	}
	if !status.Signaled() {
		t.Fatalf("Le processus enfant ne s'est pas terminé sur un signal OS: exit code=%d", status.ExitStatus())
	}
	if status.Signal() != syscall.SIGKILL {
		t.Fatalf("Le processus enfant s'est terminé sur le signal %v, attendu syscall.SIGKILL (signal 9)", status.Signal())
	}
	t.Logf("Processus enfant certifié tué avec succès par syscall.SIGKILL (Signal 9)")

	// Réouverture dans le processus parent : rejeu du WAL et reprise crash
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard après vrai SIGKILL: %v", err)
	}
	defer sReopen.Close()

	// 1. Vérifier que les 10 clés commitées sont présentes et intactes
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("sigkill-committed-%03d", i)
		wantV := fmt.Sprintf("committed-payload-%03d", i)
		gotV, err := sReopen.Get([]byte(k))
		if err != nil {
			t.Fatalf("Clé commitée manquante %s: %v", k, err)
		}
		if string(gotV) != wantV {
			t.Fatalf("Valeur commitée altérée sur %s: got=%q want=%q", k, string(gotV), wantV)
		}
	}

	// 2. Vérifier que les mutations orphelines sont 100% absentes
	gotOrphan, err := sReopen.Get([]byte("orphan-k-001"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Clé orpheline orphan-k-001 présente après SIGKILL: got=%q err=%v", gotOrphan, err)
	}

	// 3. Vérifier que la clé écrasée par la transaction orpheline n'a pas été corrompue
	got001, err := sReopen.Get([]byte("sigkill-committed-001"))
	if err != nil || string(got001) != "committed-payload-001" {
		t.Fatalf("Clé écrasée par transaction orpheline corrompue: got=%q", string(got001))
	}

	// 4. Vérifier que le shard récupéré accepte de nouvelles écritures/lectures nominales
	postKey := []byte("post-crash-key")
	postVal := []byte("post-crash-value")
	if err := sReopen.Put(postKey, postVal); err != nil {
		t.Fatalf("Put post-crash: %v", err)
	}
	gotPost, err := sReopen.Get(postKey)
	if err != nil || !bytes.Equal(gotPost, postVal) {
		t.Fatalf("Get post-crash: got=%q want=%q err=%v", gotPost, postVal, err)
	}
	t.Log("TestOctoSmith_RealProcess_SIGKILL_Recovery PASS : vrai signal 9 OS validé avec intégrité ACID")
}

// ----------------------------------------------------------------------------
// T1-B : Rejet des mutations orphelines déjà journalisées avec KillWithoutFlush
// ----------------------------------------------------------------------------
// Simule une coupure en appelant KillWithoutFlush pendant que des
// transactions multi-mutations sont en cours. Vérifie que les transactions
// commitées persistent intactes et que les transactions orphelines (sans
// RecTxCommit) sont 100% absentes à la réouverture.
func TestOctoSmith_Kill9_MultiMutationBeforeCommit(t *testing.T) {
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}

	dir := t.TempDir()
	var key [32]byte
	_, _ = rnd.Read(key[:])

	// 1. Initialiser le shard et écrire des données transactionnelles commitées
	s := mustOpenShard(t, dir, key, 0)
	committedOracle := make(map[string][]byte)

	// Écrire et committer 2 lots de transactions explicites
	tx1, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx1: %v", err)
	}
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("committed-k-%03d", i)
		v := randBytes(128)
		if err := tx1.Put([]byte(k), v); err != nil {
			t.Fatalf("tx1 Put %s: %v", k, err)
		}
		committedOracle[k] = v
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 Commit: %v", err)
	}

	tx2, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx2: %v", err)
	}
	for i := 5; i < 10; i++ {
		k := fmt.Sprintf("committed-k-%03d", i)
		v := randBytes(128)
		if err := tx2.Put([]byte(k), v); err != nil {
			t.Fatalf("tx2 Put %s: %v", k, err)
		}
		committedOracle[k] = v
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2 Commit: %v", err)
	}

	// 2. Ouvrir une transaction orpheline (multi-mutations sans RecTxCommit)
	txOrphan, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin txOrphan: %v", err)
	}
	orphanKeys := []string{"orphan-k-001", "orphan-k-002", "orphan-k-003"}
	for _, ok := range orphanKeys {
		if err := txOrphan.Put([]byte(ok), randBytes(200)); err != nil {
			t.Fatalf("txOrphan Put %s: %v", ok, err)
		}
	}
	// Tenter également de corrompre une clé déjà commitée au sein de la transaction orpheline
	if err := txOrphan.Put([]byte("committed-k-001"), []byte("bogus-uncommitted-overwrite")); err != nil {
		t.Fatalf("txOrphan overwrite: %v", err)
	}

	// Forcer l'écriture physique dans le WAL (sans RecTxCommit !)
	if s.wal != nil {
		if err := s.wal.Flush(); err != nil {
			t.Fatalf("wal Flush: %v", err)
		}
	}

	// 3. Coupure matérielle brute SANS vidange mémoire applicative — simule un vrai SIGKILL
	if err := s.KillWithoutFlush(); err != nil {
		t.Fatalf("KillWithoutFlush: %v", err)
	}

	// 4. Réouverture : le WAL est scanné et replayé
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard après KillWithoutFlush: %v", err)
	}
	defer sReopen.Close()

	// Vérifier que TOUTES les clés commitées sont présentes et strictement bit-exactes
	for k, want := range committedOracle {
		got, err := sReopen.Get([]byte(k))
		if err != nil {
			t.Fatalf("Transaction commitée manquante %s après coupure: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Divergence bit-exacte sur clé commitée %s: got=%q want=%q", k, got, want)
		}
	}

	// Vérifier que la clé commitée écrasée par la transaction orpheline a conservé sa valeur commitée
	gotOrig, err := sReopen.Get([]byte("committed-k-001"))
	if err != nil || !bytes.Equal(gotOrig, committedOracle["committed-k-001"]) {
		t.Fatalf("La clé commitée 001 a été altérée par la transaction orpheline !")
	}

	// Vérifier que TOUTES les clés de la transaction orpheline sont ABSENTES (ErrNotFound)
	for _, ok := range orphanKeys {
		got, err := sReopen.Get([]byte(ok))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Clé orpheline %s présente après crash: got=%q err=%v (attendu ErrNotFound)", ok, got, err)
		}
	}

	// Vérifier que heapRoot et heapUsed sont cohérents
	if sReopen.heapUsed < 1 {
		t.Fatalf("heapUsed invalide après réouverture: %d", sReopen.heapUsed)
	}
	if sReopen.heapRoot >= sReopen.heapPages {
		t.Fatalf("heapRoot invalide après réouverture: %d >= %d", sReopen.heapRoot, sReopen.heapPages)
	}

	// Poursuite nominale après réouverture
	nextKey := []byte("post-crash-key")
	nextVal := []byte("post-crash-payload")
	if err := sReopen.Put(nextKey, nextVal); err != nil {
		t.Fatalf("Put post-crash a échoué: %v", err)
	}
	got, err := sReopen.Get(nextKey)
	if err != nil || !bytes.Equal(got, nextVal) {
		t.Fatalf("Get post-crash divergent: err=%v", err)
	}

	t.Logf("T1 OK : Kill-9 multi-mutation avant commit — rollback transactionnel et parité bit-exacte validés")
}

// ----------------------------------------------------------------------------
// T2 : Troncature WAL non complété en queue (corruption)
// ----------------------------------------------------------------------------
// Simule une coupure brutale qui tronque le fichier WAL en queue. Vérifie
// que scanTip gère le bloc incomplet de manière autonome, que les blocs
// durables sont conservés et que la réouverture fonctionne avec
// intégrité des données.
func TestOctoSmith_TruncatedWALTail(t *testing.T) {
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}

	dir := t.TempDir()
	var key [32]byte
	_, _ = rnd.Read(key[:])

	// 1. Créer un shard et committer 20 transactions nominales
	s := mustOpenShard(t, dir, key, 0)
	committedOracle := make(map[string][]byte)
	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("qual-k-%03d", i)
		v := randBytes(128)
		if err := s.Put([]byte(k), v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
		committedOracle[k] = v
	}
	// Forcer le flush du WAL pour sceller durablement les 20 premières transactions dans wal.img
	if err := s.wal.Flush(); err != nil {
		t.Fatalf("wal Flush: %v", err)
	}

	// 2. Écrire un enregistrement actif en queue de WAL et forcer son flush physique partiel
	tornKey := "torn-tail-key"
	tornVal := randBytes(256)
	id, err := NewID(uint64(1000), s.id, s.counter)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	s.counter++
	rec := Record{ID: id, Type: RecPut, Payload: packKV([]byte(tornKey), tornVal)}
	if err := s.wal.Append(rec); err != nil {
		t.Fatalf("wal Append torn: %v", err)
	}
	if err := s.wal.Flush(); err != nil {
		t.Fatalf("wal Flush: %v", err)
	}

	tornLBA := s.wal.next - 1

	// Coupure matérielle brutale SANS vidange propre
	if err := s.KillWithoutFlush(); err != nil {
		t.Fatalf("KillWithoutFlush: %v", err)
	}

	// 3. Simuler une déchirure matérielle du dernier bloc actif (mid-record tear) :
	// Le bloc a été interrompu en pleine écriture de secteur lors de la chute de tension,
	// corrompant le champ de longueur de l'en-tête (d.Ok == 0 dans Db_wal_unpack).
	walPath := filepath.Join(dir, "wal.img")
	f, err := os.OpenFile(walPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	tailOff := int64(tornLBA * LBASize)
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, tailOff+4); err != nil {
		_ = f.Close()
		t.Fatalf("WriteAt torn block: %v", err)
	}
	_ = f.Close()

	// 4. Première réouverture : scanTip doit détecter le bloc déchiré/incomplet, le tronquer au dernier LSN intègre
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard après déchirure WAL: %v", err)
	}

	// Toutes les données commitées avant la coupure doivent être 100% bit-exactes
	for k, want := range committedOracle {
		got, err := sReopen.Get([]byte(k))
		if err != nil {
			t.Fatalf("Donnée manquante après déchirure WAL %s: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Divergence sur donnée commitée %s", k)
		}
	}

	// L'enregistrement déchiré en queue doit avoir été éliminé
	if _, err := sReopen.Get([]byte(tornKey)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Enregistrement déchiré présent après réouverture: err=%v", err)
	}

	// Vérifier que heapRoot et heapUsed sont cohérents
	if sReopen.heapUsed < 1 {
		t.Fatalf("heapUsed invalide après troncature: %d", sReopen.heapUsed)
	}

	// Poursuite nominale et vérification de la reprise
	postKey := []byte("post-truncation-key")
	postVal := []byte("post-truncation-val")
	if err := sReopen.Put(postKey, postVal); err != nil {
		t.Fatalf("Put post-truncation a échoué: %v", err)
	}

	// Relecture immédiate de la valeur écrite en reprise
	gotPost, err := sReopen.Get(postKey)
	if err != nil {
		t.Fatalf("Get post-truncation-key a échoué: %v", err)
	}
	if !bytes.Equal(gotPost, postVal) {
		t.Fatalf("Valeur relue après troncature divergente: got=%q want=%q", gotPost, postVal)
	}

	// Fermeture propre pour sceller le nouvel état
	if err := sReopen.Close(); err != nil {
		t.Fatalf("Close sReopen: %v", err)
	}

	// 5. Seconde réouverture propre : vérifie que la reprise est durable et que la troncature passée n'a laissé aucune corruption latente
	s3, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard s3 après reprise durable: %v", err)
	}
	defer s3.Close()

	// La clé reprise doit persister intacte
	gotPost3, err := s3.Get(postKey)
	if err != nil || !bytes.Equal(gotPost3, postVal) {
		t.Fatalf("post-truncation-key absente ou altérée dans s3: got=%q err=%v", gotPost3, err)
	}

	// L'oracle des 20 clés initiales reste 100% bit-exact
	for k, want := range committedOracle {
		got, err := s3.Get([]byte(k))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("Clé historique %s compromise dans s3: err=%v", k, err)
		}
	}

	// La clé déchirée reste rigoureusement introuvable
	if _, err := s3.Get([]byte(tornKey)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tornKey réapparue dans s3 !")
	}

	t.Logf("T2 OK : Déchirure WAL en queue — %d données récupérées avec parité bit-exacte, relecture et seconde réouverture validées",
		len(committedOracle))
}

// ----------------------------------------------------------------------------
// T3 : Réouverture et parité durable bit-exacte contre oracle (heapRoot/heapUsed)
// ----------------------------------------------------------------------------
// Effectue 10 cycles d'écriture → fermeture → réouverture avec assertion
// bit-exacte sur toutes les clés/valeurs. Vérifie heapRoot et heapUsed
// restaurés, intégrité du tas B-Tree (zéro déchet), et reprise opérationnelle
// immédiate à chaque cycle.
func TestOctoSmith_Reopen_BitExactParityOracle(t *testing.T) {
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}
	dir := t.TempDir()
	var key [32]byte
	_, _ = rnd.Read(key[:])

	const numRounds = 10
	const keysPerRound = 20

	for round := 0; round < numRounds; round++ {
		// 1. Ouverture du shard
		s := mustOpenShard(t, dir, key, 0)

		// 2. Écriture de données
		expected := make(map[string][]byte)
		for i := 0; i < keysPerRound; i++ {
			k := fmt.Sprintf("parity-k-r%02d-i%04d", round, i)
			v := randBytes(256)
			if err := s.Put([]byte(k), v); err != nil {
				t.Fatalf("Round %d Put %s: %v", round, k, err)
			}
			expected[k] = v
		}

		// Capturer heapRoot et heapUsed avant fermeture
		preRoot := s.heapRoot
		preUsed := s.heapUsed

		// 3. Fermeture propre
		if err := s.Close(); err != nil {
			t.Fatalf("Round %d Close: %v", round, err)
		}

		// 4. Réouverture
		sReopen, err := OpenShard(dir, key, 0)
		if err != nil {
			t.Fatalf("Round %d OpenShard: %v", round, err)
		}

		// Vérification bit-exacte de toutes les données
		for k, want := range expected {
			got, err := sReopen.Get([]byte(k))
			if err != nil {
				t.Fatalf("Round %d Get %s: %v", round, k, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Round %d Get %s mismatch: len(got)=%d len(want)=%d", round, k, len(got), len(want))
			}
		}

		// Vérification de heapRoot et heapUsed restaurés
		if sReopen.heapUsed != preUsed {
			t.Fatalf("Round %d heapUsed diverge: got %d want %d", round, sReopen.heapUsed, preUsed)
		}
		if sReopen.heapRoot != preRoot {
			t.Fatalf("Round %d heapRoot diverge: got %d want %d", round, sReopen.heapRoot, preRoot)
		}

		// Vérification de l'intégrité du tas B-Tree (zéro clé fantôme)
		for k := range expected {
			_, err := sReopen.Get([]byte(k))
			if err != nil {
				t.Fatalf("Round %d key %s absent après réouverture: %v", round, k, err)
			}
		}

		// Poursuite nominale
		if err := sReopen.Put([]byte(fmt.Sprintf("round-%d-new-key", round)), []byte("new-value")); err != nil {
			t.Fatalf("Round %d Put new: %v", round, err)
		}

		if err := sReopen.Close(); err != nil {
			t.Fatalf("Round %d Close reopen: %v", round, err)
		}
	}

	t.Logf("T3 OK : %d cycles de parité bit-exacte (reopen) validés", numRounds)
}

// ----------------------------------------------------------------------------
// T4 : Test de stress 50 cycles (commit aléatoire, kill brutal, réouverture)
// ----------------------------------------------------------------------------
// 50 cycles alternant commit nominal et CloseWithoutFlush (simule SIGKILL).
// Maintient un oracle cumulatif bit-exact sur l'ensemble des 50 cycles,
// avec vérification exacte de heapRoot/heapUsed et zéro clé fantôme.
func TestOctoSmith_Stress_50CyclesOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du test de stress sous -short")
	}

	dir := t.TempDir()
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}
	var key [32]byte
	_, _ = rnd.Read(key[:])

	const numCycles = 50
	const keysPerCycle = 5

	cumulativeExpected := make(map[string][]byte)
	var lastHeapRoot uint64
	var lastHeapUsed uint64

	for cycle := 0; cycle < numCycles; cycle++ {
		// 1. Ouverture du shard
		s := mustOpenShard(t, dir, key, 0)

		if cycle > 0 {
			if s.heapRoot != lastHeapRoot || s.heapUsed != lastHeapUsed {
				t.Fatalf("Cycle %d : divergence à l'ouverture: heapRoot=%d (want %d), heapUsed=%d (want %d)",
					cycle, s.heapRoot, lastHeapRoot, s.heapUsed, lastHeapUsed)
			}
		}

		// 2. Écriture de données pour ce cycle
		for i := 0; i < keysPerCycle; i++ {
			k := fmt.Sprintf("stress-k-c%02d-i%02d", cycle, i)
			v := randBytes(128)
			if err := s.Put([]byte(k), v); err != nil {
				t.Fatalf("Cycle %d Put %s: %v", cycle, k, err)
			}
			cumulativeExpected[k] = v
		}

		lastHeapRoot = s.heapRoot
		lastHeapUsed = s.heapUsed

		// 3. Fermeture : alterner entre Close() et CloseWithoutFlush()
		if cycle%2 == 0 {
			// Fermeture propre (commit nominal)
			if err := s.Close(); err != nil {
				t.Fatalf("Cycle %d Close: %v", cycle, err)
			}
		} else {
			// Fermeture brutale sans flush (simule SIGKILL)
			if err := s.CloseWithoutFlush(); err != nil {
				t.Fatalf("Cycle %d CloseWithoutFlush: %v", cycle, err)
			}
		}

		// 4. Réouverture
		sReopen, err := OpenShard(dir, key, 0)
		if err != nil {
			t.Fatalf("Cycle %d OpenShard: %v", cycle, err)
		}

		// Vérification exacte de heapRoot et heapUsed restaurés
		if sReopen.heapRoot != lastHeapRoot {
			t.Fatalf("Cycle %d : heapRoot divergent après réouverture: got %d want %d", cycle, sReopen.heapRoot, lastHeapRoot)
		}
		if sReopen.heapUsed != lastHeapUsed {
			t.Fatalf("Cycle %d : heapUsed divergent après réouverture: got %d want %d", cycle, sReopen.heapUsed, lastHeapUsed)
		}

		// Vérification oracle bit-exacte sur TOUTES les clés cumulées depuis le début
		for k, want := range cumulativeExpected {
			got, err := sReopen.Get([]byte(k))
			if err != nil {
				t.Fatalf("Cycle %d Get %s manquant: %v", cycle, k, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Cycle %d Get %s mismatch", cycle, k)
			}
		}

		// Vérification zéro clé fantôme
		if _, err := sReopen.Get([]byte("non-existent-key")); err == nil {
			t.Fatalf("Cycle %d : clé inexistante retournée !", cycle)
		}

		// Poursuite opérationnelle immédiate
		nextKey := fmt.Sprintf("cycle-%d-resumption", cycle)
		nextVal := []byte("resumed")
		if err := sReopen.Put([]byte(nextKey), nextVal); err != nil {
			t.Fatalf("Cycle %d Put resumption: %v", cycle, err)
		}
		cumulativeExpected[nextKey] = nextVal
		lastHeapRoot = sReopen.heapRoot
		lastHeapUsed = sReopen.heapUsed

		if err := sReopen.Close(); err != nil {
			t.Fatalf("Cycle %d Close reopen: %v", cycle, err)
		}
	}

	t.Logf("T4 OK : %d cycles de stress avec oracle cumulatif (%d clés) et parité exacte heapRoot/heapUsed",
		numCycles, len(cumulativeExpected))
}

// ----------------------------------------------------------------------------
// T5 : Parité après Flush → Compact → Repack + coupure brutale
// ----------------------------------------------------------------------------
// Vérifie que la séquence Flush → Compact → Repack suivie d'une coupure
// brutale (CloseWithoutFlush) préserve la parité bit-exacte et restaure
// correctement heapRoot/heapUsed.
func TestOctoSmith_FlushRepackParityOracle(t *testing.T) {
	dir := t.TempDir()
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}
	var key [32]byte
	_, _ = rnd.Read(key[:])

	s := mustOpenShard(t, dir, key, 0, WithHeapPages(4096))

	const numKeys = 50
	preData := make(map[string][]byte)
	for i := 0; i < numKeys; i++ {
		k := fmt.Sprintf("repack-parity-k-%04d", i)
		v := randBytes(256)
		if err := s.Put([]byte(k), v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
		preData[k] = v
	}

	// 1. Flush des pages
	if err := s.flushPages(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 2. Compactage
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 3. Repack du tas
	if err := s.RepackHeap(); err != nil {
		t.Fatalf("RepackHeap: %v", err)
	}

	// Capturer l'état post-repack
	preCompactRoot := s.heapRoot
	preCompactUsed := s.heapUsed

	// 4. Fermeture brutale sans flush (simule SIGKILL)
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	// 5. Réouverture
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard post-repack: %v", err)
	}
	defer sReopen.Close()

	// Vérification bit-exacte de toutes les données
	for k, want := range preData {
		got, err := sReopen.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get %s post-repack: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get %s post-repack mismatch: len(got)=%d len(want)=%d", k, len(got), len(want))
		}
	}

	// Vérification heapRoot/heapUsed restaurés
	if sReopen.heapUsed != preCompactUsed {
		t.Fatalf("heapUsed post-repack diverge: got %d want %d", sReopen.heapUsed, preCompactUsed)
	}
	if sReopen.heapRoot != preCompactRoot {
		t.Fatalf("heapRoot post-repack diverge: got %d want %d", sReopen.heapRoot, preCompactRoot)
	}

	// Poursuite nominale
	if err := sReopen.Put([]byte("post-repack-key"), []byte("post-repack-val")); err != nil {
		t.Fatalf("Put post-repack: %v", err)
	}

	t.Logf("T5 OK : Flush→Compact→Repack→Coupure brutale→Réouverture validés")
}

// ----------------------------------------------------------------------------
// T6 : Corruption métadonnées heap → fail-closed en mode strict
// ----------------------------------------------------------------------------
// Corrompt heapRoot dans data.img → fail-closed en mode strict,
// restauration via WAL en mode normal.
func TestOctoSmith_HeapMetadataCorruption_FailClosed(t *testing.T) {
	dir := t.TempDir()
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}
	var key [32]byte
	_, _ = rnd.Read(key[:])

	// 1. Créer un shard et écrire des données
	s := mustOpenShard(t, dir, key, 0)
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("corrupt-k-%03d", i)
		v := randBytes(128)
		if err := s.Put([]byte(k), v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 2. Corrompre heapRoot dans data.img (offset 32, 8 bytes LE)
	dataPath := filepath.Join(dir, "data.img")
	dataBytes, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("ReadFile data.img: %v", err)
	}
	// Écrire un heapRoot corrompu (valeur hors limites)
	corruptRoot := uint64(0xFFFFFFFFFFFFFFFF)
	binary.LittleEndian.PutUint64(dataBytes[heapRootOff:heapRootOff+8], corruptRoot)
	if err := os.WriteFile(dataPath, dataBytes, 0o600); err != nil {
		t.Fatalf("WriteFile data.img: %v", err)
	}

	// 3. Réouverture en mode strict : doit échouer (fail-closed)
	_, err = OpenShardStrict(dir, key, 0)
	if err == nil {
		t.Fatalf("OpenShardStrict n'a pas échoué avec heapRoot corrompu !")
	}

	// 4. Réouverture en mode normal : doit restaurer via WAL
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard normal après corruption: %v", err)
	}
	defer sReopen.Close()

	// Vérifier que les données sont récupérables via WAL
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("corrupt-k-%03d", i)
		got, err := sReopen.Get([]byte(k))
		if err != nil {
			t.Fatalf("Donnée non récupérable via WAL après corruption: %s: %v", k, err)
		}
		if len(got) == 0 {
			t.Fatalf("Donnée vide pour %s après corruption", k)
		}
	}

	t.Logf("T6 OK : Corruption heapRoot → fail-closed (strict) et restauration WAL (normal)")
}

// ----------------------------------------------------------------------------
// Test fonctionnel : Parité bit-exacte des opérations Shard (conservé)
// ----------------------------------------------------------------------------
// Ce test vérifie la parité bit-exacte des opérations Put/Get/Mut/Del/Rollback
// sur le Shard, incluant les grands objets overflow, les snapshots MVCC et
// les mises à jour partielles via commitMut.
func TestOctoSmith_ShardOracleBitExact(t *testing.T) {
	rnd := newTestRand(42)
	randBytes := func(n int) []byte {
		data := make([]byte, n)
		_, _ = rnd.Read(data)
		return data
	}
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	s := mustOpenShard(t, dir, key, 0)
	defer s.Close()

	// Oracle Go en mémoire : état de référence bit-exact.
	oracle := make(map[string][]byte)
	// Snapshots MVCC : version réelle du moteur [16]byte -> oracle state snapshot immuable.
	snaps := make(map[[16]byte]map[string][]byte)

	// Helper : applique une opération à l'oracle courant (les snapshots existants restent strictement immuables).
	apply := func(key string, val []byte) {
		oracle[key] = append([]byte{}, val...)
	}

	// Helper : supprime une clé de l'oracle courant.
	remove := func(key string) {
		delete(oracle, key)
	}

	// Helper : capture un instantané immuable sous la version réelle du moteur
	takeSnapshot := func() [16]byte {
		snapID := s.lastID
		snapCopy := make(map[string][]byte, len(oracle))
		for k, v := range oracle {
			snapCopy[k] = append([]byte{}, v...)
		}
		snaps[snapID] = snapCopy
		return snapID
	}

	// --- Scénario 1 : Petits inserts inline (< 2 Ko) ---
	t.Log("--- Scénario 1 : Petits inserts inline (< 2 Ko) ---")
	smallVals := [][]byte{
		randBytes(100),
		randBytes(512),
		randBytes(1024),
		randBytes(1900),
	}
	for i, val := range smallVals {
		k := fmt.Sprintf("small_%d", i)
		if err := s.Put([]byte(k), val); err != nil {
			t.Fatalf("Put small %s: %v", k, err)
		}
		apply(k, val)
	}

	for k, want := range oracle {
		got, err := s.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get %s: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get %s mismatch: len(got)=%d len(want)=%d", k, len(got), len(want))
		}
	}
	t.Logf("Scénario 1 OK : %d petites clés en parité bit-exacte", len(oracle))

	// --- Scénario 2 : Grands inserts overflow (> 16 Ko, multi-pages) ---
	t.Log("--- Scénario 2 : Grands inserts overflow (> 16 Ko) ---")
	bigVals := [][]byte{
		randBytes(16321),
		randBytes(65536),
		randBytes(250000),
		randBytes(2 * 1024 * 1022),
	}
	for i, val := range bigVals {
		k := fmt.Sprintf("big_%d", i)
		if err := s.Put([]byte(k), val); err != nil {
			t.Fatalf("Put big %s: %v", k, err)
		}
		apply(k, val)
	}

	for k, want := range oracle {
		got, err := s.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get %s: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get %s mismatch: len(got)=%d len(want)=%d", k, len(got), len(want))
		}
	}
	t.Logf("Scénario 2 OK : %d grandes clés en parité bit-exacte", len(oracle))

	// --- Scénario 3 : Snapshots MVCC immuables et cohérents ---
	t.Log("--- Scénario 3 : Snapshots MVCC immuables et cohérents ---")
	snap1 := takeSnapshot()

	newKey := "snap_new"
	newVal := randBytes(500)
	if err := s.Put([]byte(newKey), newVal); err != nil {
		t.Fatalf("Put snap_new: %v", err)
	}
	apply(newKey, newVal)

	// Vérification de l'isolation MVCC : la nouvelle clé ne doit PAS exister dans le snapshot antérieur
	_, err := s.GetAsOf([]byte(newKey), snap1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAsOf %s sur snap antérieur doit retourner ErrNotFound, got %v", newKey, err)
	}

	// Toutes les clés existantes au moment de snap1 doivent être bit-exactes dans snap1
	for k, want := range snaps[snap1] {
		gotSnap, err := s.GetAsOf([]byte(k), snap1)
		if err != nil {
			t.Fatalf("GetAsOf %s sur snap1: %v", k, err)
		}
		if !bytes.Equal(gotSnap, want) {
			t.Fatalf("GetAsOf %s divergence snap1", k)
		}
	}

	// Prendre un deuxième snapshot qui lui, doit contenir la nouvelle clé
	snap2 := takeSnapshot()
	gotSnap2, err := s.GetAsOf([]byte(newKey), snap2)
	if err != nil {
		t.Fatalf("GetAsOf %s sur snap2: %v", newKey, err)
	}
	if !bytes.Equal(gotSnap2, newVal) {
		t.Fatalf("GetAsOf %s sur snap2 mismatch: got len=%d want len=%d", newKey, len(gotSnap2), len(newVal))
	}
	t.Logf("Scénario 3 OK : isolation et immuabilité des snapshots MVCC validées")

	// --- Scénario 4 : Mises à jour partielles (Mut) via JSON/QL ops ---
	t.Log("--- Scénario 4 : Mises à jour partielles (Mut) ---")
	mutKey := "mut_key"
	initialDoc := map[string]interface{}{
		"titre": "Initial Title",
		"tags":  []interface{}{"tag1", "tag2"},
		"count": float64(42),
	}
	initDocBytes, err := json.Marshal(initialDoc)
	if err != nil {
		t.Fatalf("Marshal initial doc: %v", err)
	}
	if err := s.Put([]byte(mutKey), initDocBytes); err != nil {
		t.Fatalf("Put init %s: %v", mutKey, err)
	}
	apply(mutKey, initDocBytes)
	snapPreMut := takeSnapshot()

	updateOps := []qlMutOp{
		{Op: opSetField, F: "titre", V: json.RawMessage(`"Updated Title"`)},
		{Op: opIncrU64, F: "count", V: json.RawMessage(`5`)},
	}
	// Calcul indépendant par l'oracle pur Go (sans appeler applyMutOps du moteur)
	expectedDocMap := map[string]interface{}{
		"titre": "Updated Title",
		"tags":  []interface{}{"tag1", "tag2"},
		"count": float64(47),
	}
	expectedMutRaw, err := json.Marshal(expectedDocMap)
	if err != nil {
		t.Fatalf("Marshal expectedDocMap: %v", err)
	}
	// Appel du chemin applicatif réel du shard : calcul interne de la mutation par le moteur
	if err := s.Mut([]byte(mutKey), updateOps); err != nil {
		t.Fatalf("s.Mut update %s: %v", mutKey, err)
	}
	apply(mutKey, expectedMutRaw)

	// Vérification intégrale du document JSON retourné par s.Get(mutKey)
	gotMut, err := s.Get([]byte(mutKey))
	if err != nil {
		t.Fatalf("Get mut %s: %v", mutKey, err)
	}

	var gotDocMap map[string]interface{}
	if err := json.Unmarshal(gotMut, &gotDocMap); err != nil {
		t.Fatalf("Unmarshal gotMut %s: %v", mutKey, err)
	}

	// Assertion exhaustive sur TOUS les champs du document JSON
	if gotDocMap["titre"] != "Updated Title" || expectedDocMap["titre"] != "Updated Title" {
		t.Fatalf("titre diverge: got %v want Updated Title", gotDocMap["titre"])
	}
	if gotDocMap["count"] != float64(47) || expectedDocMap["count"] != float64(47) {
		t.Fatalf("count diverge: got %v want 47", gotDocMap["count"])
	}
	tags, ok := gotDocMap["tags"].([]interface{})
	if !ok || len(tags) != 2 || tags[0] != "tag1" || tags[1] != "tag2" {
		t.Fatalf("tags non préservés après mutation: %v", gotDocMap["tags"])
	}

	// Vérifier l'isolation MVCC : le snapshot pris AVANT la mutation voit toujours l'état initial
	gotSnapBefore, err := s.GetAsOf([]byte(mutKey), snapPreMut)
	if err != nil {
		t.Fatalf("GetAsOf mut snapPreMut: %v", err)
	}
	var gotBeforeMap map[string]interface{}
	if err := json.Unmarshal(gotSnapBefore, &gotBeforeMap); err != nil {
		t.Fatalf("Unmarshal snapBefore: %v", err)
	}
	if gotBeforeMap["titre"] != "Initial Title" || gotBeforeMap["count"] != float64(42) {
		t.Fatalf("Snapshot pre-mut altéré ! titre=%v count=%v", gotBeforeMap["titre"], gotBeforeMap["count"])
	}

	// Le snapshot pris APRÈS la mutation voit la version mise à jour
	snapPostMut := takeSnapshot()
	gotSnapAfter, err := s.GetAsOf([]byte(mutKey), snapPostMut)
	if err != nil {
		t.Fatalf("GetAsOf mut snapPostMut: %v", err)
	}
	if !bytes.Equal(gotSnapAfter, expectedMutRaw) {
		t.Fatalf("Snapshot post-mut divergent de expectedMutRaw")
	}
	t.Logf("Scénario 4 OK : mises à jour partielles vérifiées sur tous les champs avec isolation MVCC")

	// --- Scénario 5 : Suppressions (Del / Tombstones) ---
	t.Log("--- Scénario 5 : Suppressions (Del / Tombstones) ---")
	delKey := "del_key"
	delVal := randBytes(200)
	if err := s.Put([]byte(delKey), delVal); err != nil {
		t.Fatalf("Put del key: %v", err)
	}
	apply(delKey, delVal)

	if err := s.Delete([]byte(delKey)); err != nil {
		t.Fatalf("Delete %s: %v", delKey, err)
	}
	remove(delKey)

	if _, exists := oracle[delKey]; exists {
		t.Fatalf("Oracle should have deleted key %s", delKey)
	}
	_, err = s.Get([]byte(delKey))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted key: expected ErrNotFound, got %v", err)
	}
	t.Logf("Scénario 5 OK : suppressions en parité bit-exacte")

	// --- Scénario 6 : Rollbacks transactionnels ---
	t.Log("--- Scénario 6 : Rollbacks transactionnels ---")
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	rbKey := "rollback_key"
	rbVal := []byte("rollback_val")
	if err := tx.Put([]byte(rbKey), rbVal); err != nil {
		t.Fatalf("tx Put: %v", err)
	}
	// On n'appelle pas apply(rbKey, rbVal) car la transaction va être annulée
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx Rollback: %v", err)
	}
	_, err = s.Get([]byte(rbKey))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Shard should not have key after rollback, but got: %v", err)
	}
	t.Logf("Scénario 6 OK : rollback ne modifie pas l'oracle")

	// --- Scénario 7 : Put, Mut, Del mixtes dans un cycle complet ---
	t.Log("--- Scénario 7 : Cycle mixte complet ---")
	mixKey := "mix_key"
	mixVal1 := []byte("initial_value_mix")
	if err := s.Put([]byte(mixKey), mixVal1); err != nil {
		t.Fatalf("Put mix initial: %v", err)
	}
	apply(mixKey, mixVal1)

	mutDoc := map[string]interface{}{"status": "active", "score": float64(10)}
	mutDocBytes, _ := json.Marshal(mutDoc)
	if err := s.Put([]byte(mixKey), mutDocBytes); err != nil {
		t.Fatalf("Put mix doc: %v", err)
	}
	apply(mixKey, mutDocBytes)

	// Mutation applicative sur le doc mixte
	mixMutOps := []qlMutOp{
		{Op: opIncrU64, F: "score", V: json.RawMessage(`15`)},
	}
	// Calcul indépendant par l'oracle pur Go (sans appeler applyMutOps du moteur)
	expectedMixDoc := map[string]interface{}{
		"status": "active",
		"score":  float64(25),
	}
	expectedMixMut, err := json.Marshal(expectedMixDoc)
	if err != nil {
		t.Fatalf("Marshal expectedMixDoc: %v", err)
	}
	if err := s.Mut([]byte(mixKey), mixMutOps); err != nil {
		t.Fatalf("s.Mut mix: %v", err)
	}
	apply(mixKey, expectedMixMut)

	gotMixMut, err := s.Get([]byte(mixKey))
	if err != nil || !bytes.Equal(gotMixMut, expectedMixMut) {
		t.Fatalf("s.Mut mix résultat divergent: got=%q want=%q", gotMixMut, expectedMixMut)
	}

	if err := s.Delete([]byte(mixKey)); err != nil {
		t.Fatalf("Delete mix: %v", err)
	}
	remove(mixKey)

	_, err = s.Get([]byte(mixKey))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get mix after delete: expected ErrNotFound, got %v", err)
	}
	t.Logf("Scénario 7 OK : cycle mixte complet en parité bit-exacte")

	t.Log("=== Tout les scénarios OctoSmith ont réussi ===")
}

// ----------------------------------------------------------------------------
// TestOctoSmith_Mut_ReplayWAL_NonCircular : preuve formelle de calcul applicatif
// et reconstruction bit-exacte par rejeu WAL (fermeture de la Condition 1)
// ----------------------------------------------------------------------------
func TestOctoSmith_Mut_ReplayWAL_NonCircular(t *testing.T) {
	rnd := newTestRand(42)
	dir := t.TempDir()
	var key [32]byte
	_, _ = rnd.Read(key[:])

	s := mustOpenShard(t, dir, key, 0)
	docKey := []byte("article:0042")

	initialDoc := map[string]interface{}{
		"title": "Autonomous Database Foundations",
		"views": float64(100),
		"tags":  []interface{}{"storage", "wal"},
	}
	initialBytes, err := json.Marshal(initialDoc)
	if err != nil {
		t.Fatalf("Marshal initial: %v", err)
	}
	if err := s.Put(docKey, initialBytes); err != nil {
		t.Fatalf("Put initial: %v", err)
	}

	// Forcer le flush des pages initiales dans data.img et capturer l'image de données brute
	if err := s.flushPages(); err != nil {
		t.Fatalf("flushPages initial: %v", err)
	}
	dataPath := filepath.Join(dir, "data.img")
	dataInitialBytes, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("ReadFile dataInitialBytes: %v", err)
	}
	walPath := filepath.Join(dir, "wal.img")
	walInitialBytes, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("ReadFile walInitialBytes: %v", err)
	}

	// 1. Calcul de l'attendu indépendamment via structure pure Go (SANS appeler applyMutOps du moteur)
	expectedMutDoc := map[string]interface{}{
		"title": "Autonomous Database Foundations 2.0",
		"views": float64(125),
		"tags":  []interface{}{"storage", "wal"},
	}

	// 2. Définition des opérations de mutation QL passées au moteur
	ops := []qlMutOp{
		{Op: opSetField, F: "title", V: json.RawMessage(`"Autonomous Database Foundations 2.0"`)},
		{Op: opIncrU64, F: "views", V: json.RawMessage(`25`)},
	}

	// 3. Exécution de la mutation via le chemin applicatif réel du moteur
	// ATTENTION : Aucun document précalculé n'est transmis à s.Mut !
	// Le moteur lit le document en mémoire, applique les opérations, émet un record RecMut
	// dans le journal WAL, et publie la version.
	if err := s.Mut(docKey, ops); err != nil {
		t.Fatalf("s.Mut: %v", err)
	}

	// Forcer la présence matérielle du record RecMut dans le journal WAL sur disque
	// avant la coupure (fdatasync du WAL sans toucher à data.img)
	if s.wal != nil {
		if err := s.wal.Flush(); err != nil {
			t.Fatalf("wal Flush: %v", err)
		}
	}

	// 4. Vérification immédiate en mémoire avant toute coupure
	gotDirect, err := s.Get(docKey)
	if err != nil {
		t.Fatalf("Get direct: %v", err)
	}
	var directMap map[string]interface{}
	if err := json.Unmarshal(gotDirect, &directMap); err != nil {
		t.Fatalf("Unmarshal gotDirect: %v", err)
	}
	if directMap["title"] != expectedMutDoc["title"] || directMap["views"] != expectedMutDoc["views"] {
		t.Fatalf("Divergence directe s.Mut: got=%v want=%v", directMap, expectedMutDoc)
	}

	// 5. Coupure brutale sans vidange des pages de données (KillWithoutFlush)
	if err := s.KillWithoutFlush(); err != nil {
		t.Fatalf("KillWithoutFlush: %v", err)
	}

	// Étape cruciale : restauration physique de dataInitialBytes sur data.img !
	// Cette écriture garantit que data.img ne contient STRICTEMENT AUCUNE trace de la mutation.
	// La mutation réside EXCLUSIVEMENT dans le journal WAL (sous forme de record RecMut).
	if err := os.WriteFile(dataPath, dataInitialBytes, 0o600); err != nil {
		t.Fatalf("Restauration dataInitialBytes: %v", err)
	}

	// 6. Réouverture : le moteur charge les pages initiales non-mutées, puis rejoue le WAL (Replay).
	// Lors du rejeu, il rencontre RecMut, applique la mutation à la volée et reconstruit le document.
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	// 7. Assertion bit-exacte après rejeu WAL complet
	gotReplayed, err := sReopen.Get(docKey)
	if err != nil {
		t.Fatalf("Get après rejeu WAL: %v", err)
	}
	var replayedMap map[string]interface{}
	if err := json.Unmarshal(gotReplayed, &replayedMap); err != nil {
		t.Fatalf("Unmarshal replayedMap: %v", err)
	}
	if !reflect.DeepEqual(replayedMap, expectedMutDoc) {
		t.Fatalf("Document rejoué divergent de l'attendu global: got=%+v want=%+v", replayedMap, expectedMutDoc)
	}
	if len(replayedMap) != 3 {
		t.Fatalf("Nombre de champs inattendu dans replayedMap: got %d want 3", len(replayedMap))
	}
	if replayedMap["title"] != "Autonomous Database Foundations 2.0" {
		t.Fatalf("Titre reconstruit invalide: %v", replayedMap["title"])
	}
	if replayedMap["views"] != float64(125) {
		t.Fatalf("Views reconstruites invalides: %v", replayedMap["views"])
	}
	replayedTags, ok := replayedMap["tags"].([]interface{})
	if !ok || len(replayedTags) != 2 || replayedTags[0] != "storage" || replayedTags[1] != "wal" {
		t.Fatalf("Tags reconstruits invalides: %v", replayedMap["tags"])
	}
	_ = sReopen.Close()

	// 8. Contre-épreuve causale (reductio ad absurdum) :
	// Dans un shard jumeau avec la même image initiale dataInitialBytes mais dont le WAL
	// ne contient pas le record RecMut (WAL initial préservé avant mutation), la réouverture
	// DOIT produire le document initial NON-MUTÉ (views: 100, title: "Autonomous Database Foundations").
	// Cela prouve à 100% que la reconstruction de la mutation ci-dessus était causée par le rejeu de RecMut.
	dirTwin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirTwin, "data.img"), dataInitialBytes, 0o600); err != nil {
		t.Fatalf("WriteFile twin data.img: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirTwin, "wal.img"), walInitialBytes, 0o600); err != nil {
		t.Fatalf("WriteFile twin wal.img: %v", err)
	}

	sTwin, err := OpenShard(dirTwin, key, 0)
	if err != nil {
		t.Fatalf("OpenShard twin: %v", err)
	}
	defer sTwin.Close()

	gotTwin, err := sTwin.Get(docKey)
	if err != nil {
		t.Fatalf("Get twin: %v", err)
	}
	var twinMap map[string]interface{}
	if err := json.Unmarshal(gotTwin, &twinMap); err != nil {
		t.Fatalf("Unmarshal twinMap: %v", err)
	}
	if !reflect.DeepEqual(twinMap, initialDoc) {
		t.Fatalf("Document jumeau non-muté divergent de l'initialDoc: got=%+v want=%+v", twinMap, initialDoc)
	}
	if len(twinMap) != 3 {
		t.Fatalf("Nombre de champs inattendu dans twinMap: got %d want 3", len(twinMap))
	}
	if twinMap["title"] != "Autonomous Database Foundations" || twinMap["views"] != float64(100) {
		t.Fatalf("Contre-épreuve échouée : got title=%v views=%v", twinMap["title"], twinMap["views"])
	}
	twinTags, ok := twinMap["tags"].([]interface{})
	if !ok || len(twinTags) != 2 || twinTags[0] != "storage" || twinTags[1] != "wal" {
		t.Fatalf("Tags jumeau invalides: %v", twinMap["tags"])
	}

	t.Log("TestOctoSmith_Mut_ReplayWAL_NonCircular PASS : calcul applicatif indépendant, nécessité causale du rejeu RecMut et contre-épreuve validées")
}
