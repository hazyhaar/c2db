package c2db

import (
	"bytes"
	"testing"
)

// ouvrirShardTest ouvre un shard neuf dans un répertoire temporaire.
func ouvrirShardTest(t *testing.T, dir string) *Shard {
	t.Helper()
	var key [32]byte
	copy(key[:], "codemap_test/v1/checkpoint")
	sh, err := OpenShard(dir, key, 1)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	return sh
}

// ouvrirShardTestGrand ouvre un shard avec les options qu'emploie c2store :
// 65536 pages (1 Gio) et un WAL de 64 Mio.
func ouvrirShardTestGrand(t *testing.T, dir string) *Shard {
	t.Helper()
	var key [32]byte
	copy(key[:], "codemap_test/v1/checkpoint")
	sh, err := OpenShard(dir, key, 1, WithHeapPages(65536), WithWALBytes(64*1024*1024))
	if err != nil {
		t.Fatalf("OpenShard grand: %v", err)
	}
	return sh
}

// TestCheckpointPutPersistanceGrandShard reproduit l'échec observé via c2store,
// où le shard est ouvert avec 1 Gio de tas.
func TestCheckpointPutPersistanceGrandShard(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTestGrand(t, dir)
	if err := sh.Put([]byte("cle"), []byte("valeur")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTestGrand(t, dir)
	defer func() { _ = sh2.Close() }()
	got, err := sh2.Get([]byte("cle"))
	if err != nil {
		t.Fatalf("Get après réouverture: %v", err)
	}
	if !bytes.Equal(got, []byte("valeur")) {
		t.Fatalf("valeur perdue après pointage grand shard: %q", got)
	}
}

// TestCheckpointPrefixGrand reproduit le schéma de clés de c2store (symbolKey
// + index inverse) et une lecture par préfixe après pointage.
func TestCheckpointPrefixGrand(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTestGrand(t, dir)
	pairs := [][2][]byte{
		{[]byte("symbols/Alpha/f1/s1"), []byte{1, 2, 3}},
		{[]byte("filesyms/f1/Alpha/s1"), []byte{1}},
	}
	if err := sh.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTestGrand(t, dir)
	defer func() { _ = sh2.Close() }()
	if got, err := sh2.Get([]byte("symbols/Alpha/f1/s1")); err != nil {
		t.Fatalf("Get direct après pointage: %v", err)
	} else if !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("valeur altérée après pointage: %v", got)
	}
	q, err := sh2.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	ents, err := q.Prefix([]byte("symbols/Alpha/")).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("préfixe vide après pointage : %d entrées", len(ents))
	}
}

// TestCheckpointDoublePrefix exerce un second pointage, qui peut déclencher le
// recyclage et la troncature du journal, puis un parcours de préfixe.
func TestCheckpointDoublePrefix(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTestGrand(t, dir)
	if err := sh.Put([]byte("symbols/A/f1/s1"), []byte("un")); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint 1: %v", err)
	}
	if err := sh.Put([]byte("symbols/A/f1/s2"), []byte("deux")); err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint 2: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTestGrand(t, dir)
	defer func() { _ = sh2.Close() }()
	q, _ := sh2.Query()
	ents, err := q.Prefix([]byte("symbols/A/")).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ents) != 2 {
		t.Fatalf("préfixe après double pointage : %d entrées, attendu 2", len(ents))
	}
}

// TestCheckpointVersionsPrefix vérifie que le parcours voit la version la plus
// récente d'une clé réécrite, après pointage.
func TestCheckpointVersionsPrefix(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTestGrand(t, dir)
	if err := sh.Put([]byte("symbols/A/f1/s1"), []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := sh.Put([]byte("symbols/A/f1/s1"), []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTestGrand(t, dir)
	defer func() { _ = sh2.Close() }()
	got, err := sh2.Get([]byte("symbols/A/f1/s1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("version courante = %q, attendu v2", got)
	}
	q, _ := sh2.Query()
	ents, err := q.Prefix([]byte("symbols/A/")).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("préfixe après versions : %d entrées, attendu 1", len(ents))
	}
}

// TestCheckpointDebordementPrefix exerce une valeur assez grande pour tenir sur
// plusieurs pages (débordement), puis pointage et parcours de préfixe.
func TestCheckpointDebordementPrefix(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTestGrand(t, dir)
	gros := bytes.Repeat([]byte("x"), 120000)
	if err := sh.Put([]byte("symbols/A/f1/gros"), gros); err != nil {
		t.Fatalf("Put débordement: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTestGrand(t, dir)
	defer func() { _ = sh2.Close() }()
	got, err := sh2.Get([]byte("symbols/A/f1/gros"))
	if err != nil {
		t.Fatalf("Get débordement: %v", err)
	}
	if !bytes.Equal(got, gros) {
		t.Fatalf("valeur de débordement altérée: %d octets", len(got))
	}
	q, _ := sh2.Query()
	ents, err := q.Prefix([]byte("symbols/A/")).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("préfixe après débordement : %d entrées, attendu 1", len(ents))
	}
}

// TestCheckpointPutPersistance : une écriture suivie d'un pointage doit
// survivre à la réouverture. Le pointage pointait jusqu'ici le WAL sans que la
// racine de page 0 soit garantie durable, si bien qu'une réouverture rendait
// une valeur absente.
func TestCheckpointPutPersistance(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTest(t, dir)
	if err := sh.Put([]byte("cle"), []byte("valeur")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTest(t, dir)
	defer func() { _ = sh2.Close() }()
	got, err := sh2.Get([]byte("cle"))
	if err != nil {
		t.Fatalf("Get après réouverture: %v", err)
	}
	if !bytes.Equal(got, []byte("valeur")) {
		t.Fatalf("valeur perdue après pointage: %q", got)
	}
}

// TestCheckpointBatchPersistance : même exigence pour un lot, seul chemin
// employé par c2store pour les fichiers et les symboles.
func TestCheckpointBatchPersistance(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTest(t, dir)
	pairs := [][2][]byte{
		{[]byte("a"), []byte("1")},
		{[]byte("b"), []byte("2")},
		{[]byte("c"), []byte("3")},
	}
	if err := sh.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTest(t, dir)
	defer func() { _ = sh2.Close() }()
	for _, p := range pairs {
		got, err := sh2.Get(p[0])
		if err != nil {
			t.Fatalf("Get(%s) après réouverture: %v", p[0], err)
		}
		if !bytes.Equal(got, p[1]) {
			t.Fatalf("%s perdu après pointage: %q", p[0], got)
		}
	}
}

// TestCheckpointDeletePersistance : une suppression pointée doit rester
// effective après réouverture.
func TestCheckpointDeletePersistance(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTest(t, dir)
	if err := sh.Put([]byte("cle"), []byte("valeur")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := sh.Delete([]byte("cle")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := sh.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTest(t, dir)
	defer func() { _ = sh2.Close() }()
	if _, err := sh2.Get([]byte("cle")); err == nil {
		t.Fatalf("clé supprimée réapparue après pointage")
	}
}

// TestCheckpointSansPointage : contrôle de non-vacuité, la réouverture sans
// pointage doit déjà préserver l'écriture par rejeu du WAL.
func TestCheckpointSansPointage(t *testing.T) {
	dir := t.TempDir()
	sh := ouvrirShardTest(t, dir)
	if err := sh.Put([]byte("cle"), []byte("valeur")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sh2 := ouvrirShardTest(t, dir)
	defer func() { _ = sh2.Close() }()
	got, err := sh2.Get([]byte("cle"))
	if err != nil {
		t.Fatalf("Get sans pointage: %v", err)
	}
	if !bytes.Equal(got, []byte("valeur")) {
		t.Fatalf("valeur perdue sans pointage: %q", got)
	}
}
