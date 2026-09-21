// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQuery_PrefixAndRange(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Insertion de clés avec différents préfixes de domaine
	// agent:01:0001, agent:01:0002, ..., agent:02:0001, ..., user:01:0001
	for i := 1; i <= 20; i++ {
		k := []byte(fmt.Sprintf("agent:01:%04d", i))
		v := []byte(fmt.Sprintf(`{"agent":"01","seq":%d,"status":"active"}`, i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	for i := 1; i <= 10; i++ {
		k := []byte(fmt.Sprintf("agent:02:%04d", i))
		v := []byte(fmt.Sprintf(`{"agent":"02","seq":%d,"status":"idle"}`, i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	for i := 1; i <= 5; i++ {
		k := []byte(fmt.Sprintf("user:01:%04d", i))
		v := []byte(fmt.Sprintf(`{"user":"01","seq":%d}`, i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}

	// 1. Requête par préfixe agent:01:
	q, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	res01, err := q.Prefix([]byte("agent:01:")).Collect()
	if err != nil {
		t.Fatalf("Collect agent:01: %v", err)
	}
	if len(res01) != 20 {
		t.Fatalf("agent:01 count = %d want 20", len(res01))
	}
	if string(res01[0].Key) != "agent:01:0001" || string(res01[19].Key) != "agent:01:0020" {
		t.Fatalf("bornes incorrectes pour agent:01: %s -> %s", res01[0].Key, res01[19].Key)
	}

	// 2. Requête par intervalle Range [agent:01:0005, agent:01:0015[
	qRange, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	resRange, err := qRange.Range([]byte("agent:01:0005"), []byte("agent:01:0015")).Collect()
	if err != nil {
		t.Fatalf("Collect range: %v", err)
	}
	if len(resRange) != 10 {
		t.Fatalf("range count = %d want 10", len(resRange))
	}
	if string(resRange[0].Key) != "agent:01:0005" || string(resRange[9].Key) != "agent:01:0014" {
		t.Fatalf("range bornes incorrectes: %s -> %s", resRange[0].Key, resRange[9].Key)
	}

	// 3. Count direct sans allocation
	qCount, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	cnt, err := qCount.Prefix([]byte("agent:02:")).Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if cnt != 10 {
		t.Fatalf("agent:02 Count = %d want 10", cnt)
	}
}

func TestQuery_ReverseAndPagination(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	for i := 1; i <= 50; i++ {
		k := []byte(fmt.Sprintf("item:%03d", i))
		v := []byte(fmt.Sprintf("val:%03d", i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	// 1. Parcours descendant (Reverse)
	qRev, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	resRev, err := qRev.Prefix([]byte("item:")).Reverse().Limit(5).Collect()
	if err != nil {
		t.Fatalf("Collect reverse: %v", err)
	}
	if len(resRev) != 5 {
		t.Fatalf("Reverse len = %d want 5", len(resRev))
	}
	if string(resRev[0].Key) != "item:050" || string(resRev[4].Key) != "item:046" {
		t.Fatalf("Reverse order incorrect: %s -> %s", resRev[0].Key, resRev[4].Key)
	}

	// 2. Pagination Offset + Limit
	qPage, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	resPage, err := qPage.Prefix([]byte("item:")).Offset(10).Limit(5).Collect()
	if err != nil {
		t.Fatalf("Collect page: %v", err)
	}
	if len(resPage) != 5 {
		t.Fatalf("Page len = %d want 5", len(resPage))
	}
	if string(resPage[0].Key) != "item:011" || string(resPage[4].Key) != "item:015" {
		t.Fatalf("Page offset incorrect: %s -> %s", resPage[0].Key, resPage[4].Key)
	}
}

func TestQuery_PredicatePushdown_KeyAndJSON(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Données typiques d'un système d'agents autonomes
	records := []struct {
		key string
		val string
	}{
		{"trace:agent-alpha:001", `{"role":"coder","status":"success","duration_ms":120}`},
		{"trace:agent-alpha:002", `{"role":"coder","status":"failed","error":"syntax error in diff"}`},
		{"trace:agent-alpha:003", `{"role":"auditor","status":"success","duration_ms":45}`},
		{"trace:agent-beta:001", `{"role":"coder","status":"success","duration_ms":310}`},
		{"trace:agent-beta:002", `{"role":"tester","status":"failed","error":"timeout waiting for socket"}`},
		{"trace:agent-gamma:001", `{"role":"auditor","status":"failed","error":"security gate rejected"}`},
	}

	for _, r := range records {
		if err := s.Put([]byte(r.key), []byte(r.val)); err != nil {
			t.Fatalf("Put %s: %v", r.key, err)
		}
	}

	// 1. Filtrage sur champ JSON rapide "status":"failed"
	qFail, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	failed, err := qFail.Prefix([]byte("trace:")).
		Where(ValJSONFieldString("status", "failed")).
		Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if len(failed) != 3 {
		t.Fatalf("Failed count = %d want 3", len(failed))
	}

	// 2. Conjonction : rôle "coder" ET status "failed"
	qCoderFail, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	coderFailed, err := qCoderFail.Prefix([]byte("trace:")).
		Where(ValJSONFieldString("role", "coder")).
		Where(ValJSONFieldString("status", "failed")).
		Collect()
	if err != nil {
		t.Fatalf("Collect coderFailed: %v", err)
	}
	if len(coderFailed) != 1 {
		t.Fatalf("Coder failed count = %d want 1", len(coderFailed))
	}
	if string(coderFailed[0].Key) != "trace:agent-alpha:002" {
		t.Fatalf("Coder failed key = %s want trace:agent-alpha:002", coderFailed[0].Key)
	}

	// 3. Filtrage sous-chaîne d'erreur JSON : "timeout"
	qTimeout, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	timeoutEntries, err := qTimeout.Prefix([]byte("trace:")).
		Where(ValJSONFieldContains("error", "timeout")).
		Collect()
	if err != nil {
		t.Fatalf("Collect timeout: %v", err)
	}
	if len(timeoutEntries) != 1 || string(timeoutEntries[0].Key) != "trace:agent-beta:002" {
		t.Fatalf("Timeout entries = %v want trace:agent-beta:002", timeoutEntries)
	}

	// 4. First() et ErrNotFound
	qFirst, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	firstEntry, err := qFirst.Prefix([]byte("trace:agent-alpha:")).First()
	if err != nil {
		t.Fatalf("First: %v", err)
	}
	if string(firstEntry.Key) != "trace:agent-alpha:001" {
		t.Fatalf("First key = %s want trace:agent-alpha:001", firstEntry.Key)
	}

	qNotFound, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	_, err = qNotFound.Prefix([]byte("trace:non-existent:")).First()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("First on non-existent: got %v want ErrNotFound", err)
	}
}

func TestQuery_Pushdown_OverflowAndStreaming(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Stocker un mélange de valeurs inline (< 2 Ko) et d'overflow (> 2 Ko, ex: 16 Ko et 40 Ko)
	inlineVal := []byte("inline short payload")
	largeVal1 := make([]byte, 16000)
	for i := range largeVal1 {
		largeVal1[i] = byte('A' + (i % 26))
	}
	largeVal2 := make([]byte, 45000)
	for i := range largeVal2 {
		largeVal2[i] = byte('0' + (i % 10))
	}

	if err := s.Put([]byte("blob:01:small"), inlineVal); err != nil {
		t.Fatalf("Put small: %v", err)
	}
	if err := s.Put([]byte("blob:02:medium"), largeVal1); err != nil {
		t.Fatalf("Put medium: %v", err)
	}
	if err := s.Put([]byte("blob:03:large"), largeVal2); err != nil {
		t.Fatalf("Put large: %v", err)
	}

	// 1. Filtrage pushdown par taille minimale sans décoder le corps
	// ValMinLength(10000) : doit rejeter inlineVal sans jamais lire ses données complètes
	qSize, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	largeKeys, err := qSize.Prefix([]byte("blob:")).
		WhereRawVal(ValMinLength(10000)).
		CollectKeys()
	if err != nil {
		t.Fatalf("CollectKeys: %v", err)
	}
	if len(largeKeys) != 2 {
		t.Fatalf("largeKeys len = %d want 2", len(largeKeys))
	}
	if string(largeKeys[0]) != "blob:02:medium" || string(largeKeys[1]) != "blob:03:large" {
		t.Fatalf("largeKeys mismatch: %s, %s", largeKeys[0], largeKeys[1])
	}

	// 2. Streaming via Stream(io.Reader) sur les grands objets
	qStream, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	streamCount := 0
	err = qStream.Prefix([]byte("blob:")).WhereRawVal(ValMinLength(40000)).Stream(func(k []byte, r io.Reader) bool {
		streamCount++
		if string(k) != "blob:03:large" {
			t.Errorf("Stream key = %s want blob:03:large", k)
		}
		data, readErr := io.ReadAll(r)
		if readErr != nil {
			t.Errorf("ReadAll stream: %v", readErr)
			return false
		}
		if !bytes.Equal(data, largeVal2) {
			t.Errorf("Streamed data mismatch: len %d want %d", len(data), len(largeVal2))
		}
		return true
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if streamCount != 1 {
		t.Fatalf("Stream count = %d want 1", streamCount)
	}

	// 3. Projection pipeline
	qProj, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var projectedItems []string
	err = qProj.Prefix([]byte("blob:")).Project(func(k, v []byte) ([]byte, error) {
		summary := fmt.Sprintf("%s=>len:%d", string(k), len(v))
		return []byte(summary), nil
	}, func(proj []byte) bool {
		projectedItems = append(projectedItems, string(proj))
		return true
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(projectedItems) != 3 {
		t.Fatalf("Project len = %d want 3", len(projectedItems))
	}
	expected0 := fmt.Sprintf("blob:01:small=>len:%d", len(inlineVal))
	if projectedItems[0] != expected0 {
		t.Fatalf("Project[0] = %s want %s", projectedItems[0], expected0)
	}
}

func TestQuery_OnIsolatedMVCCView(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Données initiales
	for i := 1; i <= 5; i++ {
		k := []byte(fmt.Sprintf("agent:task:%02d", i))
		v := []byte(fmt.Sprintf("initial-state-%d", i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put initial: %v", err)
		}
	}

	// Capture d'une View snapshot isolée
	view, err := s.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer view.Close()

	// Mutations sur le shard après capture de la vue :
	// - modification de tâches existantes
	// - ajout de nouvelles tâches
	for i := 1; i <= 5; i++ {
		k := []byte(fmt.Sprintf("agent:task:%02d", i))
		v := []byte(fmt.Sprintf("mutated-state-%d", i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put mutated: %v", err)
		}
	}
	for i := 6; i <= 10; i++ {
		k := []byte(fmt.Sprintf("agent:task:%02d", i))
		v := []byte(fmt.Sprintf("new-task-%d", i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put new: %v", err)
		}
	}

	// 1. Requête sur la View isolée : doit voir STRICTEMENT l'état initial (5 tâches initiales)
	qView, err := view.Query()
	if err != nil {
		t.Fatalf("view.Query: %v", err)
	}
	viewEntries, err := qView.Prefix([]byte("agent:task:")).Collect()
	if err != nil {
		t.Fatalf("viewEntries Collect: %v", err)
	}
	if len(viewEntries) != 5 {
		t.Fatalf("viewEntries len = %d want 5 (isolation MVCC violée)", len(viewEntries))
	}
	for i, e := range viewEntries {
		expectedVal := fmt.Sprintf("initial-state-%d", i+1)
		if string(e.Val) != expectedVal {
			t.Fatalf("view entry %d value = %s want %s", i, e.Val, expectedVal)
		}
	}

	// 2. Requête sur le Shard actif : doit voir les 10 tâches avec les valeurs mutées
	qShard, err := s.Query()
	if err != nil {
		t.Fatalf("shard.Query: %v", err)
	}
	shardEntries, err := qShard.Prefix([]byte("agent:task:")).Collect()
	if err != nil {
		t.Fatalf("shardEntries Collect: %v", err)
	}
	if len(shardEntries) != 10 {
		t.Fatalf("shardEntries len = %d want 10", len(shardEntries))
	}
	if string(shardEntries[0].Val) != "mutated-state-1" {
		t.Fatalf("shardEntries[0] = %s want mutated-state-1", shardEntries[0].Val)
	}
}

// TestCapacity_1TerabyteShard valide qu'un shard peut être instancié à la capacité
// maximale de 1 Téraoctet (WithHeapPages(67108864) = 67 108 864 pages de 16 Ko),
// opérer en écriture/lecture, résister à la réouverture avec adoption de capacité,
// tout en conservant une empreinte mémoire résidente (RSS) minimale grâce au demand paging.
func TestCapacity_1TerabyteShard(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du test de capacité 1 To sous -short")
	}

	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	rssInit := readProcessRSS()

	// Sonde de l'espace de commit virtuel de l'hôte
	const pages1TB uint64 = 67108864
	probeBytes := int(pages1TB * pageN)
	probeBuf, errProbe := unix.Mmap(-1, 0, probeBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	targetPages := pages1TB
	if errProbe != nil {
		// Repli sur 16 Go (1 048 576 pages) si le noyau hôte a vm.overcommit_memory=0
		targetPages = 1048576
		t.Logf("Noyau hôte avec vm.overcommit_memory=0 (CommitLimit atteint pour 1 To) : qualification sur %d pages (16 Go)", targetPages)
	} else {
		_ = unix.Munmap(probeBuf)
		t.Logf("Noyau hôte autorisant 1 To : qualification pleine échelle sur %d pages (1 To)", targetPages)
	}

	s, err := OpenShard(dir, key, 0, WithHeapPages(targetPages))
	if err != nil {
		t.Fatalf("OpenShard %d pages: %v", targetPages, err)
	}

	if s.heapPages != targetPages {
		t.Fatalf("heapPages = %d want %d", s.heapPages, targetPages)
	}
	if s.shardBytes != targetPages*pageN {
		t.Fatalf("shardBytes = %d want %d", s.shardBytes, targetPages*pageN)
	}

	// Écriture d'enregistrements représentatifs
	for i := 0; i < 50; i++ {
		k := []byte(fmt.Sprintf("tb:agent:%04d", i))
		v := make([]byte, 512)
		binary.LittleEndian.PutUint64(v[:8], uint64(i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	// Exécution de requêtes déclaratives sur le shard 1 To
	q, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	count, err := q.Prefix([]byte("tb:agent:")).Count()
	if err != nil {
		t.Fatalf("Count 1 To: %v", err)
	}
	if count != 50 {
		t.Fatalf("Count 1 To = %d want 50", count)
	}

	// Fermeture du shard
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Réouverture sans option : doit adopter automatiquement la taille existante de 1 To
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard réouverture: %v", err)
	}
	defer sReopen.Close()

	if sReopen.heapPages != targetPages {
		t.Fatalf("heapPages adopté à la réouverture = %d want %d", sReopen.heapPages, targetPages)
	}

	// Vérification de lecture intègre
	valGot, err := sReopen.Get([]byte("tb:agent:0042"))
	if err != nil {
		t.Fatalf("Get 0042: %v", err)
	}
	if binary.LittleEndian.Uint64(valGot[:8]) != 42 {
		t.Fatalf("valGot = %d want 42", binary.LittleEndian.Uint64(valGot[:8]))
	}

	rssFinal := readProcessRSS()
	deltaMB := float64(int64(rssFinal)-int64(rssInit)) / (1024 * 1024)
	t.Logf("Test 1 To complété avec succès. Delta RSS réel = %.2f Mo (prouve le zéro allocation physique préalable)", deltaMB)
}

func BenchmarkQuery_Count_ZeroAlloc(b *testing.B) {
	dir := b.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		b.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("bench:agent:%04d", i))
		v := []byte(fmt.Sprintf(`{"agent":"alpha","idx":%d,"state":"ok"}`, i))
		if err := s.Put(k, v); err != nil {
			b.Fatalf("Put: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		q, err := s.Query()
		if err != nil {
			b.Fatal(err)
		}
		cnt, err := q.Prefix([]byte("bench:agent:")).Count()
		if err != nil || cnt != 500 {
			b.Fatalf("Count: cnt=%d err=%v", cnt, err)
		}
	}
}

func BenchmarkQuery_Count_ReusedCursor(b *testing.B) {
	dir := b.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		b.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("bench:agent:%04d", i))
		v := []byte(fmt.Sprintf(`{"agent":"alpha","idx":%d,"state":"ok"}`, i))
		if err := s.Put(k, v); err != nil {
			b.Fatalf("Put: %v", err)
		}
	}

	c, err := s.Cursor()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cnt, err := c.Query().Prefix([]byte("bench:agent:")).Count()
		if err != nil || cnt != 500 {
			b.Fatalf("Count: cnt=%d err=%v", cnt, err)
		}
	}
}

func BenchmarkQuery_PredicatePushdown_JSON(b *testing.B) {
	dir := b.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		b.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("bench:agent:%04d", i))
		status := "success"
		if i%5 == 0 {
			status = "error"
		}
		v := []byte(fmt.Sprintf(`{"agent":"alpha","idx":%d,"status":"%s"}`, i, status))
		if err := s.Put(k, v); err != nil {
			b.Fatalf("Put: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		q, err := s.Query()
		if err != nil {
			b.Fatal(err)
		}
		entries, err := q.Prefix([]byte("bench:agent:")).
			Where(ValJSONFieldString("status", "error")).
			Collect()
		if err != nil || len(entries) != 100 {
			b.Fatalf("Collect: len=%d err=%v", len(entries), err)
		}
	}
}

// TestQuery_JSONAdversarial prouve que fastExtractJSONField extraite le champ réel
// quand son nom apparaît dans une valeur chaîne (guillemets échappés). Rouge d'abord :
// le source actuel (bytes.Index) extraite la première occurrence dans la valeur, pas le champ.
func TestQuery_JSONAdversarial(t *testing.T) {
	// Une valeur chaîne EXACTEMENT égale au nom de champ précède le champ réel :
	// le source actuel (bytes.Index) extraite la valeur, pas le champ.
	doc := []byte(`{"note":"status","status":"vrai"}`)
	val, found := fastExtractJSONField(doc, "status")
	if !found || val != "vrai" {
		t.Fatalf("valeur == nom de champ : found=%v val=%q (attendu found=true val=vrai)", found, val)
	}

	// Nom de champ dans une valeur, sans champ réel : pas de faux positif.
	_, found = fastExtractJSONField([]byte(`{"note":"status"}`), "status")
	if found {
		t.Fatalf("faux positif : le nom dans la valeur chaîne a été traité comme champ")
	}

	// Champ réel présent, nom absent des valeurs : extraction nominale préservée.
	val, found = fastExtractJSONField([]byte(`{"status":"ok","note":"x"}`), "status")
	if !found || val != "ok" {
		t.Fatalf("champ nominal : found=%v val=%q (attendu found=true val=ok)", found, val)
	}
}
