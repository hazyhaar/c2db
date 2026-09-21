// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"github.com/hazyhaar/c2db/pkg/cihook55"
	"golang.org/x/sys/unix"
)

// TestQual_01_TornWritesBTree valide le comportement face à un torn-write mi-page
// sur data.img : rejet strict à l'ouverture, comptabilisation en PagesSkipped
// et lisibilité des données nominales via replay WAL en mode normal.
func TestQual_01_TornWritesBTree(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// 1. Initialiser un Shard et insérer 50 enregistrements
	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard initial: %v", err)
	}

	const recordCount = 50
	keys := make([][]byte, recordCount)
	vals := make([][]byte, recordCount)
	for i := 0; i < recordCount; i++ {
		keys[i] = []byte(fmt.Sprintf("qual-btree-key-%04d", i))
		vals[i] = []byte(fmt.Sprintf("qual-btree-val-%04d-payload-anti-corruption-data", i))
		if err := s.Put(keys[i], vals[i]); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	if err := s.flushPages(); err != nil {
		t.Fatalf("flushPages: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close initial: %v", err)
	}

	// 2. Simuler une écriture tronquée (torn-write mi-page) sur data.img :
	// écraser les 8192 derniers octets de la page 0 (16 Ko) avec du bruit.
	dataPath := filepath.Join(dir, "data.img")
	dev, err := Open(dataPath)
	if err != nil {
		t.Fatalf("Open data.img pour torn-write: %v", err)
	}

	const tornSize = 8192
	tornBuf := mmapAligned(t, tornSize)
	for i := range tornBuf {
		tornBuf[i] = byte(0xAA ^ (i & 0xFF))
	}

	// Page 0 : LBAs 0..3. Les 8192 derniers octets correspondent aux LBAs 2 et 3.
	const tornLBA = 2
	if err := dev.Write(tornLBA, tornBuf); err != nil {
		_ = dev.Close()
		t.Fatalf("dev.Write torn-write: %v", err)
	}
	if err := dev.Flush(); err != nil {
		_ = dev.Close()
		t.Fatalf("dev.Flush torn-write: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("dev.Close torn-write: %v", err)
	}

	// 3. Rouvrir le Shard avec OpenShardStrict : prouver que l'ouverture est rejetée avec erreur.
	sStrict, err := OpenShardStrict(dir, key, 0)
	if err == nil {
		_ = sStrict.Close()
		t.Fatalf("OpenShardStrict avec page corrompue aurait dû échouer")
	}
	if !errors.Is(err, errPageSeal) && !bytes.Contains([]byte(err.Error()), []byte("corrupted")) {
		t.Fatalf("OpenShardStrict: erreur inattendue: %v", err)
	}

	// 4. Rouvrir en mode normal : prouver que la page altérée est comptabilisée
	// dans s.PagesSkipped() et que la base reste lisible pour les autres données valides.
	sNormal, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard normal a échoué: %v", err)
	}
	defer func() { _ = sNormal.Close() }()

	if sNormal.PagesSkipped() == 0 {
		t.Fatalf("sNormal.PagesSkipped() = %d, attendu >= 1", sNormal.PagesSkipped())
	}

	for i := 0; i < recordCount; i++ {
		got, err := sNormal.Get(keys[i])
		if err != nil {
			t.Fatalf("Get %s après reprise WAL: %v", keys[i], err)
		}
		if !bytes.Equal(got, vals[i]) {
			t.Fatalf("Donnée divergente pour %s: got %q, want %q", keys[i], got, vals[i])
		}
	}
}

// TestQual_02_TornWritesWALSector simule une panne de courant coupant le 2e secteur
// d'un bloc de 8 Ko dans un WAL sous O_DIRECT. Rejet immédiat par taille/tag sans panique,
// et garantie de restitution intégrale des transactions antérieures validées.
func TestQual_02_TornWritesWALSector(t *testing.T) {
	path, key := walCrashImage(t)
	const walSizeLBAs = 16
	w, err := CreateWAL(path, walSizeLBAs*LBASize, key)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// 1. Écrire des enregistrements antérieurs validés (durable)
	const durableCount = 3
	durable := make([]Record, durableCount)
	for i := 0; i < durableCount; i++ {
		durable[i] = walCrashRecord(i, []byte(fmt.Sprintf("durable-tx-payload-%04d", i)))
		if err := w.Append(durable[i]); err != nil {
			t.Fatalf("Append durable %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush durable: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close wal: %v", err)
	}

	// 2. Simuler une panne de courant lors de l'écriture d'un enregistrement de 8 Ko
	// (2 secteurs de 4 Ko). Seul le premier secteur (LBA durableCount) est écrit.
	tornLBA := uint64(durableCount)
	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open WAL pour torn write: %v", err)
	}

	sector1 := mmapAligned(t, LBASize)
	// Structuration du 1er secteur : Magic valide, longueur déclarée = 8192 (8 Ko), ID, Type
	binary.LittleEndian.PutUint32(sector1[0:4], WALMagic)
	binary.LittleEndian.PutUint32(sector1[4:8], 8192) // 8 Ko > walMaxPayload (4055)
	id, err := NewID(uint64(durableCount+10), 0, uint64(durableCount+10))
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	copy(sector1[8:24], id[:])
	sector1[24] = byte(RecPut)
	for i := 25; i < LBASize; i++ {
		sector1[i] = byte(0x55 ^ (i & 0xFF))
	}

	// Écriture directe du premier secteur uniquement (panne coupant le 2e secteur)
	if err := dev.Write(tornLBA, sector1); err != nil {
		_ = dev.Close()
		t.Fatalf("dev.Write 1er secteur: %v", err)
	}
	if err := dev.Flush(); err != nil {
		_ = dev.Close()
		t.Fatalf("dev.Flush torn: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("dev.Close: %v", err)
	}

	// 3. Ouvrir le WAL après torn write : prouver que scanTip tronque de manière autonome
	// l'en-tête incomplet au filigrane durable (wTorn.next == durableCount) sans manipulation manuelle.
	wTorn, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL après torn write: %v", err)
	}
	defer func() { _ = wTorn.Close() }()

	if wTorn.next != durableCount {
		t.Fatalf("filigrane après troncature autonome: got %d, want %d", wTorn.next, durableCount)
	}

	var replayErr error
	var recs []Record
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		recs, replayErr = wTorn.Replay()
	}()

	if didPanic {
		t.Fatalf("ÉCHEC: wal.Replay() a paniqué sur torn write de secteur")
	}
	if replayErr != nil {
		t.Fatalf("Replay après troncature autonome: %v", replayErr)
	}
	if len(recs) != durableCount {
		t.Fatalf("recs doit contenir exactement les transactions durables: got %d, want %d", len(recs), durableCount)
	}
	for i := 0; i < durableCount; i++ {
		assertRecordEqual(t, recs[i], durable[i], i)
	}

	// 4. Preuve fail-closed : altérer un octet dans le premier bloc durable et vérifier
	// que OpenWAL échoue immédiatement avec errWALBadTag sans tronquer silencieusement.
	xfer, checkCanaries := mmapXferWithCanaries(t)
	flipWALBlock(t, path, 0, walPayloadOff, xfer)
	checkCanaries()

	wCorrupt, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL inattendu: %v", err)
	}
	defer func() { _ = wCorrupt.Close() }()
	_, replayErr = wCorrupt.Replay()
	if replayErr == nil {
		t.Fatalf("Replay aurait dû échouer avec errWALBadTag sur bloc durable altéré")
	}
	if !errors.Is(replayErr, errWALBadTag) {
		t.Fatalf("Attendu errWALBadTag, obtenu: %v", replayErr)
	}
}

// TestQual_06_BitRotDetection scelle une page B-Tree avec CRC32-C pleine page
// et sceau Poly1305, mute un seul bit à l'offset 100 et vérifie l'échec immédiat.
func TestQual_06_BitRotDetection(t *testing.T) {
	page := mmapAligned(t, int(pageN))
	st := Db_bt_leaf_init(page, pageN)
	if st.Ok != 1 {
		t.Fatalf("Db_bt_leaf_init: st.Ok = %d", st.Ok)
	}

	// Insérer une charge utile dans la page pour matérialiser une page feuille active
	for i := 64; i < 2048; i++ {
		page[i] = byte((i * 13) ^ 0x37)
	}

	// 1. Scellement avec PageCRC32CFullStore (CRC pleine page masquant les octets 28..31)
	if ok := PageCRC32CFullStore(page); !ok {
		t.Fatalf("PageCRC32CFullStore a échoué")
	}
	crcStored := binary.LittleEndian.Uint32(page[28:32])
	crcInitial := PageCRC32CFull(page)
	if crcInitial != crcStored {
		t.Fatalf("Incohérence initiale CRC: calculé %#08x != stocké %#08x", crcInitial, crcStored)
	}

	// 2. Scellement avec Poly1305
	var sealKey [32]byte
	if _, err := rand.Read(sealKey[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	keyInitial := DerivePageSealKey(sealKey, 0, 0, page)
	var tagInitial [16]byte
	c2poly1305.Crypto_poly1305(tagInitial[:], page, pageN, keyInitial[:])

	// Configuration Pager opposable
	dir := t.TempDir()
	devPath := filepath.Join(dir, "data_rot.img")
	dev, err := Create(devPath, 4*pageN)
	if err != nil {
		t.Fatalf("Create dev: %v", err)
	}
	defer func() { _ = dev.Close() }()

	tagsPath := filepath.Join(dir, "tags_rot.img")
	tagsDev, err := Create(tagsPath, uint64(tagsFileLBAs)*LBASize)
	if err != nil {
		t.Fatalf("Create tags: %v", err)
	}
	defer func() { _ = tagsDev.Close() }()

	pager, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	if err := pager.SetSeal(sealKey, tagsDev, 0); err != nil {
		t.Fatalf("SetSeal: %v", err)
	}
	if err := pager.PutPage(0, page); err != nil {
		t.Fatalf("PutPage: %v", err)
	}
	if _, err := pager.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}

	// 3. Muter un seul bit (flip bit) à l'offset 100 de la page
	const rotOffset = 100
	const rotBit = 3
	page[rotOffset] ^= (1 << rotBit)

	// 4. Vérifier que PageCRC32CFull échoue immédiatement
	crcAfterRot := PageCRC32CFull(page)
	if crcAfterRot == crcInitial {
		t.Fatalf("ÉCHEC: PageCRC32CFull n'a pas détecté l'inversion d'un bit à l'offset %d (CRC: %#08x)", rotOffset, crcAfterRot)
	}
	if crcAfterRot == crcStored {
		t.Fatalf("ÉCHEC: CRC après mutation égale au CRC stocké")
	}

	// 5. Vérifier que la validation de sceau Poly1305 échoue immédiatement
	var tagAfterRot [16]byte
	keyAfterRot := DerivePageSealKey(sealKey, 0, 0, page)
	c2poly1305.Crypto_poly1305(tagAfterRot[:], page, pageN, keyAfterRot[:])
	if subtle.ConstantTimeCompare(tagAfterRot[:], tagInitial[:]) == 1 {
		t.Fatalf("ÉCHEC: Tag Poly1305 identique après mutation d'un bit")
	}

	// 6. Vérifier que pager.verifyPage échoue immédiatement avec errPageSeal
	if err := pager.verifyPage(0, page); !errors.Is(err, errPageSeal) {
		t.Fatalf("pager.verifyPage: attendu errPageSeal, obtenu: %v", err)
	}

	// 7. Rétablir le bit et vérifier le retour nominal
	page[rotOffset] ^= (1 << rotBit)
	if PageCRC32CFull(page) != crcInitial {
		t.Fatalf("Le CRC32-C ne correspond pas après rétablissement du bit")
	}
	if err := pager.verifyPage(0, page); err != nil {
		t.Fatalf("verifyPage échoue après rétablissement du bit: %v", err)
	}
}

// TestQual_10_WALWrapAroundEpoch instancie un WAL de taille bornée, provoque des
// checkpoints et compactages successifs avec rotation d'époque et réutilisation
// physique des LBAs, tout en garantissant la stricte monotonie croissante des LSN
// et la continuité absolue des transactions.
func TestQual_10_WALWrapAroundEpoch(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "bounded_wal.img")
	const walCapLBAs = 16
	walBytes := uint64(walCapLBAs) * LBASize

	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	w, err := CreateWAL(walPath, walBytes, key)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}
	defer func() {
		if w.dev != nil {
			_ = w.Close()
		}
	}()

	var allRecordedLSNs []uint64
	trackLSN := func(label string) {
		currentLSN := w.lsn
		if len(allRecordedLSNs) > 0 {
			prev := allRecordedLSNs[len(allRecordedLSNs)-1]
			if currentLSN <= prev {
				t.Fatalf("Rupture de monotonie LSN [%s]: current %d <= prev %d", label, currentLSN, prev)
			}
		}
		allRecordedLSNs = append(allRecordedLSNs, currentLSN)
	}

	// Époque 1 : Écriture d'un lot d'enregistrements
	const ep1Count = 6
	ep1Recs := make([]Record, ep1Count)
	for i := 0; i < ep1Count; i++ {
		ep1Recs[i] = walCrashRecord(i, []byte(fmt.Sprintf("epoch-1-tx-%d", i)))
		if err := w.Append(ep1Recs[i]); err != nil {
			t.Fatalf("Append ep1 %d: %v", i, err)
		}
		trackLSN(fmt.Sprintf("ep1-tx-%d", i))
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush ep1: %v", err)
	}

	// Checkpoint et Compactage (Rotation vers Époque 2)
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint ep1: %v", err)
	}
	trackLSN("ep1-checkpoint")

	arch1 := filepath.Join(dir, "wal_epoch1.arch")
	if err := w.CompactTo(arch1); err != nil {
		t.Fatalf("CompactTo ep1: %v", err)
	}
	if err := VerifyArchive(arch1, key); err != nil {
		t.Fatalf("VerifyArchive arch1: %v", err)
	}

	// Vérifier que le pointeur physique LBA a bouclé (wrap around) vers le début
	if w.next > 2 {
		t.Fatalf("Pointeur physique next n'a pas bouclé après compactage: got %d, want <= 2", w.next)
	}

	// Époque 2 : Nouvelles écritures réutilisant les secteurs LBA physiques
	const ep2Count = 6
	ep2Recs := make([]Record, ep2Count)
	for i := 0; i < ep2Count; i++ {
		ep2Recs[i] = walCrashRecord(100+i, []byte(fmt.Sprintf("epoch-2-tx-%d", i)))
		if err := w.Append(ep2Recs[i]); err != nil {
			t.Fatalf("Append ep2 %d: %v", i, err)
		}
		trackLSN(fmt.Sprintf("ep2-tx-%d", i))
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush ep2: %v", err)
	}

	// Checkpoint et Compactage (Rotation vers Époque 3)
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint ep2: %v", err)
	}
	trackLSN("ep2-checkpoint")

	arch2 := filepath.Join(dir, "wal_epoch2.arch")
	if err := w.CompactTo(arch2); err != nil {
		t.Fatalf("CompactTo ep2: %v", err)
	}
	if err := VerifyArchive(arch2, key); err != nil {
		t.Fatalf("VerifyArchive arch2: %v", err)
	}

	// Époque 3 : Écritures subséquentes
	const ep3Count = 4
	ep3Recs := make([]Record, ep3Count)
	for i := 0; i < ep3Count; i++ {
		ep3Recs[i] = walCrashRecord(200+i, []byte(fmt.Sprintf("epoch-3-tx-%d", i)))
		if err := w.Append(ep3Recs[i]); err != nil {
			t.Fatalf("Append ep3 %d: %v", i, err)
		}
		trackLSN(fmt.Sprintf("ep3-tx-%d", i))
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush ep3: %v", err)
	}

	// Vérification de la stricte monotonie globale des LSN
	if len(allRecordedLSNs) != ep1Count+1+ep2Count+1+ep3Count {
		t.Fatalf("Nombre de pas LSN capturés: %d, attendu %d", len(allRecordedLSNs), ep1Count+1+ep2Count+1+ep3Count)
	}
	for i := 1; i < len(allRecordedLSNs); i++ {
		if allRecordedLSNs[i] <= allRecordedLSNs[i-1] {
			t.Fatalf("LSN non strictement croissant à l'index %d: %d <= %d", i, allRecordedLSNs[i], allRecordedLSNs[i-1])
		}
	}

	// Continuité des transactions : relecture active de l'époque courante
	activeRecs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay actif époque 3: %v", err)
	}

	// Vérifier la présence des transactions de l'époque 3 dans le rejeu
	ep3Found := 0
	for _, rec := range activeRecs {
		for _, want := range ep3Recs {
			if rec.Type == want.Type && rec.ID == want.ID && bytes.Equal(rec.Payload, want.Payload) {
				ep3Found++
			}
		}
	}
	if ep3Found != ep3Count {
		t.Fatalf("Transactions époque 3 retrouvées: %d, attendu %d", ep3Found, ep3Count)
	}
}

// TestQual_05_MedianCorruptedHeader_FailClosed prouve que si un bloc médian durable
// a son en-tête altéré (ex: len > 4055), scanTip refuse d'ouvrir le WAL avec errWALCorruptedMedian
// plutôt que d'amputer silencieusement ce bloc et les blocs durables subséquents.
func TestQual_05_MedianCorruptedHeader_FailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal_median.img")
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	w, err := CreateWAL(path, 1<<20, key)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// Écriture de 3 blocs durables
	for i := 0; i < 3; i++ {
		rec := walCrashRecord(i+1, []byte(fmt.Sprintf("durable-block-%d", i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append block %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Altération de l'en-tête du bloc 1 (médian) : len_field = 5000 (> 4055 max payload)
	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open dev: %v", err)
	}
	buf := mmapAligned(t, LBASize)
	if err := dev.Read(1, buf); err != nil {
		_ = dev.Close()
		t.Fatalf("Read block 1: %v", err)
	}
	binary.LittleEndian.PutUint32(buf[walLenOff:walLenOff+4], 5000)
	if err := dev.Write(1, buf); err != nil {
		_ = dev.Close()
		t.Fatalf("Write block 1: %v", err)
	}
	if err := dev.Flush(); err != nil {
		_ = dev.Close()
		t.Fatalf("Flush dev: %v", err)
	}
	_ = dev.Close()

	// Tentative d'ouverture : doit échouer avec errWALCorruptedMedian
	wReopen, err := OpenWAL(path, key)
	if err == nil {
		_ = wReopen.Close()
		t.Fatalf("ÉCHEC CRITIQUE: OpenWAL a réussi en amputant silencieusement les blocs 1 et 2 !")
	}
	if !errors.Is(err, errWALCorruptedMedian) {
		t.Fatalf("Attendu errWALCorruptedMedian, obtenu: %v", err)
	}
}

// TestQual_06_MultiblockTornTransaction_RecoveryCycle vérifie le cycle complet :
// 1. Transaction interrompue ne laissant qu'UNE SEULE mutation survivante sans commit : prouver son rejet strict.
// 2. Transaction franchissant le tampon quantique de 32 blocs sans commit : prouver son rejet strict.
// 3. Nouvelle transaction valide committée.
// 4. Réouverture : intégrité totale, données valides présentes, données interrompues absentes.
func TestQual_06_MultiblockTornTransaction_RecoveryCycle(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// Étape 1 : Shard initial avec données de base stables
	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Put([]byte("base-key"), []byte("base-val")); err != nil {
		t.Fatalf("Put base: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close base: %v", err)
	}

	// Étape 2 : Simulation d'une transaction coupée ne laissant qu'UNE SEULE mutation survivante (B1)
	walPath := filepath.Join(dir, "wal.img")
	w, err := OpenWAL(walPath, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	// Écrire une mutation transactionnelle unique (flag = txFlagTransactional) SANS RecTxCommit
	orphanTxID := [16]byte{0xEE, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}
	orphanPayload := packTxKV([]byte("single-orphan-tx-key"), []byte("single-orphan-tx-val"))
	if err := w.Append(Record{ID: orphanTxID, Type: RecPut, Payload: orphanPayload}); err != nil {
		t.Fatalf("Append orphan single tx: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush orphan single tx: %v", err)
	}
	_ = w.Close()

	// Réouverture du Shard : prouver que l'unique mutation transactionnelle sans commit est REJETÉE
	sReopen1, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard après coupure single mutation: %v", err)
	}
	if _, err := sReopen1.Get([]byte("single-orphan-tx-key")); !errors.Is(err, ErrNotFound) {
		_ = sReopen1.Close()
		t.Fatalf("ÉCHEC B1: mutation transactionnelle unique sans commit appliquée au replay ! err=%v", err)
	}
	_ = sReopen1.Close()

	// Étape 3 : Transaction franchissant le tampon quantique de 32 blocs sans commit
	sReopen2, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard pour débordement 32 blocs: %v", err)
	}
	txOverflow, err := sReopen2.Begin()
	if err != nil {
		t.Fatalf("Begin txOverflow: %v", err)
	}
	// 100 enregistrements inline de 2 000 octets (< maxInlineValLen=2048)
	// Chaque enregistrement nécessite son propre bloc WAL (2 050 octets > 2 027 octets résiduels du LBA).
	// 100 enregistrements génèrent donc au moins 100 blocs WAL, franchissant largement
	// le tampon quantique de 32 blocs (128 Ko).
	for i := 0; i < 100; i++ {
		k := []byte(fmt.Sprintf("quantum-overflow-k-%03d", i))
		v := make([]byte, 2000)
		for j := range v {
			v[j] = byte(i ^ j)
		}
		if err := txOverflow.Put(k, v); err != nil {
			t.Fatalf("Put overflow %d: %v", i, err)
		}
	}
	// Forcer l'écriture matérielle dans le fichier WAL sans commit
	if err := sReopen2.wal.Flush(); err != nil {
		t.Fatalf("Flush overflow tx: %v", err)
	}
	// Assertion FORMELLE et MESURÉE que le tampon de 32 blocs a été franchi
	if sReopen2.wal.next <= 32 {
		t.Fatalf("CONDITION DU TEST NON REMPLIE : wal.next=%d <= 32, le tampon quantique n'a pas été franchi !", sReopen2.wal.next)
	}
	t.Logf("Tampon quantique de 32 blocs franchi avec succès : wal.next = %d blocs réels", sReopen2.wal.next)

	// Simulation d'une coupure brutale : fermeture directe des descripteurs bas niveau sans vidage propre
	_ = sReopen2.wal.dev.Close()
	_ = sReopen2.data.Close()
	_ = unix.Munmap(sReopen2.pub)
	_ = unix.Munmap(sReopen2.dirty)
	_ = unix.Close(sReopen2.lockFd)

	// Étape 4 : Réouverture et vérification d'absence totale des clés du tampon débordé
	sReopen3, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard après débordement quantum: %v", err)
	}
	for i := 0; i < 100; i++ {
		k := []byte(fmt.Sprintf("quantum-overflow-k-%03d", i))
		if _, err := sReopen3.Get(k); !errors.Is(err, ErrNotFound) {
			_ = sReopen3.Close()
			t.Fatalf("Clé de transaction débordée non committée présente: %s", k)
		}
	}

	// Étape 5 : Nouvelle transaction valide et scellée avec Commit()
	txValid, err := sReopen3.Begin()
	if err != nil {
		_ = sReopen3.Close()
		t.Fatalf("Begin txValid: %v", err)
	}
	for i := 0; i < 20; i++ {
		k := []byte(fmt.Sprintf("valid-final-k-%03d", i))
		v := []byte(fmt.Sprintf("valid-final-v-%03d", i))
		if err := txValid.Put(k, v); err != nil {
			_ = sReopen3.Close()
			t.Fatalf("txValid Put %d: %v", i, err)
		}
	}
	if err := txValid.Commit(); err != nil {
		_ = sReopen3.Close()
		t.Fatalf("txValid Commit: %v", err)
	}
	if err := sReopen3.Close(); err != nil {
		t.Fatalf("Close sReopen3: %v", err)
	}

	// Étape 6 : Seconde réouverture et contrôle d'intégrité intégrale
	sFinal, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard sFinal: %v", err)
	}
	defer sFinal.Close()

	// Les clés valides doivent être présentes
	for i := 0; i < 20; i++ {
		k := []byte(fmt.Sprintf("valid-final-k-%03d", i))
		exp := []byte(fmt.Sprintf("valid-final-v-%03d", i))
		got, err := sFinal.Get(k)
		if err != nil || !bytes.Equal(got, exp) {
			t.Fatalf("Clé valide altérée: %s err=%v", k, err)
		}
	}
	// Les clés de base initiales doivent être présentes
	gotBase, err := sFinal.Get([]byte("base-key"))
	if err != nil || !bytes.Equal(gotBase, []byte("base-val")) {
		t.Fatalf("Clé de base altérée: err=%v", err)
	}
	// Toutes les clés d'anciennes transactions interrompues doivent être absentes
	if _, err := sFinal.Get([]byte("single-orphan-tx-key")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Résurrection de la transaction orpheline !")
	}
	for i := 0; i < 100; i++ {
		k := []byte(fmt.Sprintf("quantum-overflow-k-%03d", i))
		if _, err := sFinal.Get(k); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Résurrection de clé débordée: %s", k)
		}
	}
}

// TestQual_07_TxRollback_RootSplitRestoration valide que si une transaction provoque
// une division de racine du tas (root split), un Rollback restaure exactement heapRoot
// et heapUsed et que l'écriture suivante n'a aucune divergence.
func TestQual_07_TxRollback_RootSplitRestoration(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Insérer 10 clés initiales stables
	for i := 0; i < 10; i++ {
		k := []byte(fmt.Sprintf("base-init-k-%03d", i))
		v := []byte(fmt.Sprintf("base-init-v-%03d", i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put base %d: %v", i, err)
		}
	}

	preRoot := s.heapRoot
	preUsed := s.heapUsed

	// Démarrer une transaction et insérer suffisamment de données pour forcer un ROOT SPLIT
	// Chaque entrée = 20 octets clé + 300 octets valeur = ~340 octets
	// 80 entrées = ~27 200 octets > capacité d'une page de 16 Ko (16 384 octets)
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for i := 0; i < 80; i++ {
		k := []byte(fmt.Sprintf("split-tx-key-%04d", i))
		v := make([]byte, 300)
		for j := range v {
			v[j] = byte((i + j) & 0xFF)
		}
		if err := tx.Put(k, v); err != nil {
			t.Fatalf("tx Put %d: %v", i, err)
		}
	}

	// Assertion formelle que la racine A EFFECTIVEMENT DIVISÉ (root split)
	if s.heapRoot == preRoot {
		t.Fatalf("CONDITION DU TEST NON REMPLIE : heapRoot n'a pas changé (%d == %d), la racine n'a pas éclaté !", s.heapRoot, preRoot)
	}
	if s.heapUsed <= preUsed {
		t.Fatalf("CONDITION DU TEST NON REMPLIE : heapUsed (%d) n'a pas dépassé preUsed (%d)", s.heapUsed, preUsed)
	}
	t.Logf("Root split effectif constaté : heapRoot=%d (était %d), heapUsed=%d (était %d)", s.heapRoot, preRoot, s.heapUsed, preUsed)

	// Annulation de la transaction
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// Assertion de restauration bit-exacte de la racine et de l'espace alloué
	if s.heapRoot != preRoot {
		t.Fatalf("heapRoot non restauré après division de racine: got %d want %d", s.heapRoot, preRoot)
	}
	if s.heapUsed != preUsed {
		t.Fatalf("heapUsed non restauré après division de racine: got %d want %d", s.heapUsed, preUsed)
	}

	// Vérification de la lisibilité des données de base
	for i := 0; i < 10; i++ {
		k := []byte(fmt.Sprintf("base-init-k-%03d", i))
		exp := []byte(fmt.Sprintf("base-init-v-%03d", i))
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, exp) {
			t.Fatalf("Donnée de base altérée après rollback: key=%s err=%v", k, err)
		}
	}

	// Écriture suivante nominale
	if err := s.Put([]byte("post-split-rollback-key"), []byte("post-split-rollback-val")); err != nil {
		t.Fatalf("Put post-rollback: %v", err)
	}
	got, err := s.Get([]byte("post-split-rollback-key"))
	if err != nil || !bytes.Equal(got, []byte("post-split-rollback-val")) {
		t.Fatalf("Get post-rollback: got %q, want post-split-rollback-val", got)
	}

	// 5. Fermeture complète et réouverture du Shard pour prouver la persistance et l'absence des 80 clés annulées
	if err := s.Close(); err != nil {
		t.Fatalf("Close s: %v", err)
	}

	s2, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard s2 après rollback root split: %v", err)
	}

	// Vérification de la présence des 10 clés de base
	for i := 0; i < 10; i++ {
		k := []byte(fmt.Sprintf("base-init-k-%03d", i))
		exp := []byte(fmt.Sprintf("base-init-v-%03d", i))
		got, err := s2.Get(k)
		if err != nil || !bytes.Equal(got, exp) {
			_ = s2.Close()
			t.Fatalf("Donnée de base absente ou altérée après réouverture: key=%s err=%v", k, err)
		}
	}

	// Vérification stricte que TOUTES les 80 clés de la transaction annulée sont ABSENTES (ErrNotFound)
	for i := 0; i < 80; i++ {
		k := []byte(fmt.Sprintf("split-tx-key-%04d", i))
		if _, err := s2.Get(k); !errors.Is(err, ErrNotFound) {
			_ = s2.Close()
			t.Fatalf("Clé annulée après root-split ressurgie après réouverture ! key=%s err=%v", k, err)
		}
	}

	// Vérification de la clé post-rollback
	gotPost, err := s2.Get([]byte("post-split-rollback-key"))
	if err != nil || !bytes.Equal(gotPost, []byte("post-split-rollback-val")) {
		_ = s2.Close()
		t.Fatalf("Clé post-rollback absente après réouverture: err=%v", err)
	}

	// 6. Nouvelle transaction explicite committée sur le Shard réouvert
	tx2, err := s2.Begin()
	if err != nil {
		_ = s2.Close()
		t.Fatalf("Begin tx2: %v", err)
	}
	for i := 0; i < 15; i++ {
		k := []byte(fmt.Sprintf("tx2-post-reopen-k-%03d", i))
		v := []byte(fmt.Sprintf("tx2-post-reopen-v-%03d", i))
		if err := tx2.Put(k, v); err != nil {
			_ = s2.Close()
			t.Fatalf("tx2 Put %d: %v", i, err)
		}
	}
	if err := tx2.Commit(); err != nil {
		_ = s2.Close()
		t.Fatalf("tx2 Commit: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close s2: %v", err)
	}

	// 7. Seconde réouverture et assertion intégrale de parité
	s3, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard s3: %v", err)
	}
	defer s3.Close()

	for i := 0; i < 15; i++ {
		k := []byte(fmt.Sprintf("tx2-post-reopen-k-%03d", i))
		exp := []byte(fmt.Sprintf("tx2-post-reopen-v-%03d", i))
		got, err := s3.Get(k)
		if err != nil || !bytes.Equal(got, exp) {
			t.Fatalf("tx2 clé absente sur s3: key=%s err=%v", k, err)
		}
	}
	for i := 0; i < 80; i++ {
		k := []byte(fmt.Sprintf("split-tx-key-%04d", i))
		if _, err := s3.Get(k); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Clé annulée encore présente sur s3: %s", k)
		}
	}
	// Vérification de la présence et de l'intégrité des 10 clés initiales de base sur s3
	for i := 0; i < 10; i++ {
		k := []byte(fmt.Sprintf("base-init-k-%03d", i))
		exp := []byte(fmt.Sprintf("base-init-v-%03d", i))
		got, err := s3.Get(k)
		if err != nil || !bytes.Equal(got, exp) {
			t.Fatalf("base clé absente ou altérée sur s3: key=%s err=%v", k, err)
		}
	}
}

// TestQual_08_ReplayAllOrNothing_RollbackOnPartialError prouve que si une transaction committée
// dans le journal contient une mutation qui échoue à l'application après qu'une première mutation a été
// appliquée dans le tampon dirty, le rejeu Tout-ou-Rien restaure intégralement l'état pré-groupe
// sans laisser aucun préfixe de mutation dans pub ni dirty.
func TestQual_08_ReplayAllOrNothing_RollbackOnPartialError(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// 1. Base stable
	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Put([]byte("base-stable-k"), []byte("base-stable-v")); err != nil {
		t.Fatalf("Put base: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close base: %v", err)
	}

	// 2. Écriture directe dans le WAL d'un groupe transactionnel avec commit.
	// La mutation 1 est un RecPut valide (sera effectivement appliquée à s.dirty).
	// La mutation 2 est un RecMut avec JSON syntaxiquement VALIDE (passe validateTxGroup),
	// mais portant une opération sémantiquement inconnue (op=99) qui échouera à l'exécution dans applyMutOps.
	walPath := filepath.Join(dir, "wal.img")
	w, err := OpenWAL(walPath, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	txID := [16]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C}

	// Mutation 1 : RecPut valide contenant un document JSON valide (appliqué en premier dans s.dirty)
	p1 := packTxKV([]byte("tx-prefix-key"), []byte(`{"field1":"init-val"}`))
	if err := w.Append(Record{ID: txID, Type: RecPut, Payload: p1}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}
	// Mutation 2 : RecMut avec JSON syntaxiquement valide mais op sémantiquement invalide (op=99)
	// json.Unmarshal réussit dans validateTxGroup(), puis applyMutOps() échoue à l'exécution !
	badOpsJSON := `[{"f":"field1","op":99,"v":"1"}]`
	p2 := packTxKV([]byte("tx-prefix-key"), []byte(badOpsJSON))
	if err := w.Append(Record{ID: txID, Type: RecMut, Payload: p2}); err != nil {
		t.Fatalf("Append m2: %v", err)
	}
	// Commit du groupe
	if err := w.Append(Record{ID: txID, Type: RecTxCommit}); err != nil {
		t.Fatalf("Append commit: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush WAL: %v", err)
	}
	_ = w.Close()

	// 3. Réouverture en mode normal : s.replay() applique m1, subit l'échec de m2,
	// déclenche la restauration intégrale pré-groupe (s.convergeDirtyFromPub())
	// et écarte la transaction corrompue sans laisser aucun préfixe de m1 dans pub ni dirty.
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	// Preuve formelle : tx-prefix-key DOIT être absente (ErrNotFound), prouvant que m1 a été ROLLED BACK
	if _, err := sReopen.Get([]byte("tx-prefix-key")); !errors.Is(err, ErrNotFound) {
		_ = sReopen.Close()
		t.Fatalf("ÉCHEC TOUT-OU-RIEN : le préfixe tx-prefix-key a survécu malgré l'échec d'application de m2 !")
	}
	// La clé de base antérieure doit être 100% intacte
	got, err := sReopen.Get([]byte("base-stable-k"))
	if err != nil || !bytes.Equal(got, []byte("base-stable-v")) {
		_ = sReopen.Close()
		t.Fatalf("Clé de base altérée après rollback de rejeu: err=%v", err)
	}

	// Nouvelle écriture nominale post-reprise
	if err := sReopen.Put([]byte("tx-post-replay-k"), []byte("tx-post-replay-v")); err != nil {
		_ = sReopen.Close()
		t.Fatalf("Put post-replay: %v", err)
	}
	gotNew, err := sReopen.Get([]byte("tx-post-replay-k"))
	if err != nil || !bytes.Equal(gotNew, []byte("tx-post-replay-v")) {
		_ = sReopen.Close()
		t.Fatalf("Get post-replay: got %q, want tx-post-replay-v", gotNew)
	}
	_ = sReopen.Close()

	// 4. Preuve en mode strict : OpenShardStrict doit explicitement refuser le shard
	_, errStrict := OpenShardStrict(dir, key, 0)
	if errStrict == nil {
		t.Fatalf("OpenShardStrict aurait dû refuser d'ouvrir le shard avec un échec de rejeu transactionnel")
	}
	if !strings.Contains(errStrict.Error(), "replay tx") {
		t.Fatalf("OpenShardStrict erreur attendue 'replay tx', obtenu: %v", errStrict)
	}
}

// TestQual_09_ConcurrentReader_DuringPublish prouve l'arrivée contrôlée d'un lecteur concurrent
// EXACTEMENT entre la réservation de capacité / point durable et la publication mémoire,
// et vérifie formellement l'isolation de son ancien instantané directement sur readerLive.buf.
func TestQual_09_ConcurrentReader_DuringPublish(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Clé initiale
	if err := s.Put([]byte("reader-k1"), []byte("reader-v1")); err != nil {
		t.Fatalf("Put init: %v", err)
	}

	// Démarrer une transaction
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Put([]byte("reader-tx-k"), []byte("reader-tx-v")); err != nil {
		t.Fatalf("Put tx: %v", err)
	}

	// Arrivée contrôlée du lecteur concurrent EXACTEMENT entre la réservation/point durable et la publication mémoire.
	// Le hook before-publish s'exécute au tout début de publish(), APRES preparePublish() et APRES wal.Flush() de RecTxCommit !
	var readerLive *liveHeap
	cihook55.Set("before-publish", func() {
		readerLive = s.pinLive()
	})
	defer cihook55.Set("before-publish", nil)

	// Le Commit() DOIT réussir sans ErrViewHeld car preparePublish a dimensionné et réservé les ressources
	if err := tx.Commit(); err != nil {
		if readerLive != nil {
			s.unpinLive(readerLive)
		}
		t.Fatalf("tx.Commit a échoué malgré la réservation de capacité: %v", err)
	}

	if readerLive == nil {
		t.Fatalf("Le hook before-publish n'a pas été déclenché")
	}
	defer s.unpinLive(readerLive)

	// Preuve formelle d'isolation du snapshot du lecteur :
	// Lecture DIRECTE sur readerLive.buf avec readerLive.root
	var maxSnap [16]byte
	for i := range maxSnap {
		maxSnap[i] = 0xFF
	}
	probeTx := Db_bt_get_as_of_heap(readerLive.buf, uint64(len(readerLive.buf)), uint64(len(readerLive.buf))/pageN, readerLive.root, []byte("reader-tx-k"), uint64(len("reader-tx-k")), maxSnap[:], nil, 0)
	if probeTx.Found != 0 {
		t.Fatalf("ISOLATION DU SNAPSHOT ROMPUE : le lecteur épinglé voit la clé de la nouvelle transaction dans readerLive.buf !")
	}
	probeBase := Db_bt_get_as_of_heap(readerLive.buf, uint64(len(readerLive.buf)), uint64(len(readerLive.buf))/pageN, readerLive.root, []byte("reader-k1"), uint64(len("reader-k1")), maxSnap[:], nil, 0)
	if probeBase.Found == 0 {
		t.Fatalf("Le lecteur épinglé ne voit pas la clé de base dans readerLive.buf !")
	}

	// Les NOUVELLES vues ouvertes après le commit voient bien la valeur committée
	valCommitted, err := s.Get([]byte("reader-tx-k"))
	if err != nil || !bytes.Equal(valCommitted, []byte("reader-tx-v")) {
		t.Fatalf("Nouvelle vue ne voit pas la valeur committée: %v", err)
	}

	// Désépinglage et vérification du nettoyage de holdHeaps
	s.unpinLive(readerLive)
	readerLive = nil
	s.reapHolds()
	if len(s.holdHeaps) != 0 {
		t.Fatalf("holdHeaps non purgé après unpin: len=%d", len(s.holdHeaps))
	}
}

// TestQual_10_PostCommitFailure_PoisonAndRecoveryCycle prouve qu'en cas de défaillance
// post-engagement durable, le shard en mémoire passe en état fail-closed empoisonné (ErrPostCommit),
// puis que sa fermeture et réouverture restaure intégralement l'état nominal avec la transaction scellée dans le WAL.
func TestQual_10_PostCommitFailure_PoisonAndRecoveryCycle(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	if err := s.Put([]byte("base-pre-crash-k"), []byte("base-pre-crash-v")); err != nil {
		t.Fatalf("Put base: %v", err)
	}

	// Démarrer une transaction
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Put([]byte("durable-tx-k"), []byte("durable-tx-v")); err != nil {
		t.Fatalf("Put tx: %v", err)
	}

	// Injecter une faute de publication post-engagement durable
	s.SetPublishFaultInjection(true)

	commitErr := tx.Commit()
	if commitErr == nil {
		t.Fatalf("tx.Commit aurait dû échouer avec ErrPostCommit lors de la faute injectée")
	}
	if !errors.Is(commitErr, ErrPostCommit) {
		t.Fatalf("Attendu erreur enveloppant ErrPostCommit, obtenu: %v", commitErr)
	}

	// Preuve formelle que le shard courant est POISONNÉ (fail-closed strict) :
	// Toutes les opérations subséquentes sur cette instance en mémoire doivent être rejetées
	if _, err := s.Get([]byte("base-pre-crash-k")); !errors.Is(err, ErrPostCommit) {
		t.Fatalf("Get sur shard empoisonné aurait dû échouer avec ErrPostCommit: got %v", err)
	}
	if err := s.Put([]byte("blocked-k"), []byte("blocked-v")); !errors.Is(err, ErrPostCommit) {
		t.Fatalf("Put sur shard empoisonné aurait dû échouer avec ErrPostCommit: got %v", err)
	}
	if _, err := s.Begin(); !errors.Is(err, ErrPostCommit) {
		t.Fatalf("Begin sur shard empoisonné aurait dû échouer avec ErrPostCommit: got %v", err)
	}

	// Fermeture de l'instance empoisonnée
	_ = s.Close()

	// Réouverture propre : l'état nominal est 100% restauré et la transaction scellée dans le WAL est présente !
	s2, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard s2 après crash post-commit: %v", err)
	}
	defer s2.Close()

	// Vérification de la présence de la transaction engagée durablement dans le WAL avant le crash
	gotDurable, err := s2.Get([]byte("durable-tx-k"))
	if err != nil || !bytes.Equal(gotDurable, []byte("durable-tx-v")) {
		t.Fatalf("Transaction durable non restaurée au rejeu après crash post-commit: err=%v got=%q", err, gotDurable)
	}
	// Vérification de la clé antérieure
	gotBase, err := s2.Get([]byte("base-pre-crash-k"))
	if err != nil || !bytes.Equal(gotBase, []byte("base-pre-crash-v")) {
		t.Fatalf("Donnée de base altérée sur s2: err=%v", err)
	}

	// Reprise opérationnelle nominale : écritures et lectures fonctionnent sans aucune dégradation
	if err := s2.Put([]byte("nominal-resume-k"), []byte("nominal-resume-v")); err != nil {
		t.Fatalf("Put sur shard restauré: %v", err)
	}
	gotResume, err := s2.Get([]byte("nominal-resume-k"))
	if err != nil || !bytes.Equal(gotResume, []byte("nominal-resume-v")) {
		t.Fatalf("Get sur shard restauré: got %q, want nominal-resume-v", gotResume)
	}
}

// TestQual_11_ReplayAllOrNothing_OverflowChainRollback prouve que si une transaction
// contenant une valeur overflow échoue lors d'une mutation ultérieure, aucune page d'overflow
// ne subsiste dans pub et la clé overflow est strictement absente au rejeu.
func TestQual_11_ReplayAllOrNothing_OverflowChainRollback(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// 1. Shard avec base stable et SCELLEMENT CRYPTOGRAPHIQUE ACTIF
	s, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Put([]byte("ofl-base-k"), []byte("ofl-base-v")); err != nil {
		t.Fatalf("Put base: %v", err)
	}
	preRoot := s.heapRoot
	preUsed := s.heapUsed
	headPage := preUsed + 1

	// Document JSON valide de 50 000 octets (4 pages réelles > OverflowChunkCapacity=16320)
	// structuré pour passer le décodage initial de document dans applyMutOps et déclencher op=99
	prefix := `{"field1":"init","pad":"`
	suffix := `"}`
	padLen := 50000 - len(prefix) - len(suffix)
	var sb strings.Builder
	sb.Grow(50000)
	sb.WriteString(prefix)
	for i := 0; i < padLen; i++ {
		sb.WriteByte(byte('A' + (i % 26)))
	}
	sb.WriteString(suffix)
	oflVal := []byte(sb.String())
	if len(oflVal) != 50000 {
		t.Fatalf("len(oflVal)=%d, attendu 50000", len(oflVal))
	}

	// Écriture des 4 pages d'overflow DIRECTEMENT via s.pager qui possède le scellement Poly1305 actif (tags.img)
	offset := 0
	curr := headPage
	numOflPages := uint64(0)
	for seq := uint32(0); offset < len(oflVal); seq++ {
		numOflPages++
		chunk := len(oflVal) - offset
		if uint64(chunk) > OverflowChunkCapacity {
			chunk = int(OverflowChunkCapacity)
		}
		pgBuf := make([]byte, pageN)
		pgBuf[BT_TypeOffset] = TypeOverflow
		var nextPg uint64
		if offset+chunk < len(oflVal) {
			nextPg = curr + 1
		} else {
			nextPg = 0
		}
		binary.LittleEndian.PutUint64(pgBuf[32:40], nextPg)
		binary.LittleEndian.PutUint32(pgBuf[40:44], uint32(chunk))
		binary.LittleEndian.PutUint32(pgBuf[44:48], seq)
		// Offset canonique 64 (conforme à allocOverflowPages / overflow.go:124)
		copy(pgBuf[64:64+chunk], oflVal[offset:offset+chunk])
		crc := C2db_crc32c_fullpage(pgBuf, pageN)
		binary.LittleEndian.PutUint32(pgBuf[28:32], crc)

		if err := s.pager.PutPage(curr*pageLBAs, pgBuf); err != nil {
			t.Fatalf("PutPage overflow %d: %v", curr, err)
		}
		offset += chunk
		curr = nextPg
	}
	if numOflPages != 4 {
		t.Fatalf("numOflPages=%d, attendu 4 pages pour 50 Ko", numOflPages)
	}

	// Vidage vers data.img et calcul scellé des tags Poly1305 vers tags.img
	if _, err := s.pager.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty overflow: %v", err)
	}
	// Fermeture propre de s pour purger tout cache mémoire
	if err := s.Close(); err != nil {
		t.Fatalf("Close s: %v", err)
	}

	// TÉMOIN NOMINAL FORMEL À FROID SUR PAGER SCELLÉ :
	// Réouverture d'une instance témoin sans aucun cache préalable (slots vides).
	// Démontre l'acceptation cryptographique intégrale par lecture disque et verifyPage()
	// (Poly1305 contre tags.img) ainsi que la restitution bit-exacte des 50 000 octets via readOverflowChain().
	sWitness, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard sWitness: %v", err)
	}
	if !sWitness.replayValidateOverflowChain(headPage, 50000) {
		_ = sWitness.Close()
		t.Fatalf("TÉMOIN NOMINAL À FROID ÉCHOUÉ : la chaîne de 4 pages scellée sur disque a été rejetée par verifyPage() !")
	}
	witnessVal, err := readOverflowChain(sWitness.dirty, sWitness.heapPages, headPage, 50000)
	if err != nil {
		_ = sWitness.Close()
		t.Fatalf("TÉMOIN NOMINAL À FROID ÉCHOUÉ : readOverflowChain: %v", err)
	}
	if !bytes.Equal(witnessVal, oflVal) {
		_ = sWitness.Close()
		t.Fatalf("TÉMOIN NOMINAL À FROID ÉCHOUÉ : restitution corrompue de la valeur lue du disque !")
	}
	if err := sWitness.Close(); err != nil {
		t.Fatalf("Close sWitness: %v", err)
	}

	// 2. Créer une transaction WAL contenant :
	// - Mutation 1 : RecPut de la valeur overflow de 50 000 octets (validée et appliquée dans s.dirty)
	// - Mutation 2 : RecMut avec JSON syntaxiquement valide mais op sémantiquement inconnue (op=99)
	// - RecTxCommit
	walPath := filepath.Join(dir, "wal.img")
	w, err := OpenWAL(walPath, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	txID := [16]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C}

	desc := make([]byte, 13)
	desc[0] = 1 // isOverflow
	binary.LittleEndian.PutUint32(desc[1:5], uint32(len(oflVal)))
	binary.LittleEndian.PutUint64(desc[5:13], headPage)

	p1 := packTxKV([]byte("ofl-tx-key"), desc)
	if err := w.Append(Record{ID: txID, Type: RecPut, Payload: p1}); err != nil {
		t.Fatalf("Append ofl m1: %v", err)
	}

	// Mutation 2 : RecMut avec JSON valide ciblant ofl-tx-key, déclenchant op=99 dans applyMutOps
	badOps := `[{"f":"field1","op":99,"v":"1"}]`
	p2 := packTxKV([]byte("ofl-tx-key"), []byte(badOps))
	if err := w.Append(Record{ID: txID, Type: RecMut, Payload: p2}); err != nil {
		t.Fatalf("Append ofl m2: %v", err)
	}
	// Commit
	if err := w.Append(Record{ID: txID, Type: RecTxCommit}); err != nil {
		t.Fatalf("Append ofl commit: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush WAL: %v", err)
	}
	_ = w.Close()

	// 3. Interception par hooks :
	// a) Observation de la précondition : la mutation 0 (RecPut ofl-tx-key) DOIT être appliquée dans s.dirty
	//    avant que la mutation 1 (op 99) n'échoue !
	var mut0Applied bool
	var mut0KeyFoundInDirty bool
	var mut0ValMatchesInDirty bool
	cihook55.Set("replay-tx-mutation-applied", func() {
		curS := ActiveReplayShard()
		if curS == nil {
			return
		}
		if curS.ReplayLastMutIdx == 0 && curS.ReplayLastMutKey == "ofl-tx-key" {
			mut0Applied = true
			var maxSnap [16]byte
			for j := range maxSnap {
				maxSnap[j] = 0xFF
			}
			probe := Db_bt_get_as_of_heap(curS.dirty, uint64(len(curS.dirty)), uint64(len(curS.dirty))/pageN, curS.heapRoot, []byte("ofl-tx-key"), uint64(len("ofl-tx-key")), maxSnap[:], nil, 0)
			if probe.Found != 0 {
				mut0KeyFoundInDirty = true
			}
			val, err := readOverflowChain(curS.dirty, curS.heapPages, headPage, 50000)
			if err == nil && bytes.Equal(val, oflVal) {
				mut0ValMatchesInDirty = true
			}
		}
	})
	defer func() { cihook55.Set("replay-tx-mutation-applied", nil) }()

	// b) Observation de l'échec ciblé sur la mutation 1 (op 99) :
	var mut1FailedExpectedly bool
	cihook55.Set("replay-tx-mutation-failed", func() {
		curS := ActiveReplayShard()
		if curS == nil {
			return
		}
		if curS.ReplayLastMutIdx == 1 && curS.ReplayLastMutKey == "ofl-tx-key" {
			if curS.ReplayLastMutErr != nil && (errors.Is(curS.ReplayLastMutErr, errQLOpInconnue) || strings.Contains(curS.ReplayLastMutErr.Error(), "op_inconnue")) {
				mut1FailedExpectedly = true
			}
		}
	})
	defer func() { cihook55.Set("replay-tx-mutation-failed", nil) }()

	// c) Observation de la restauration immédiate après le rollback et AVANT syncDirtyFull() :
	// Exécuté immédiatement après convergeDirtyFromPub() lors de l'échec de la mutation 2 au rejeu.
	// Prouve formellement la restauration intégrale de s.dirty avant que syncDirtyFull() ne s'exécute !
	var hookFired bool
	var preSyncHeapRoot uint64
	var preSyncHeapUsed uint64
	var preSyncDirtyMatchesPub bool
	var preSyncOflKeyAbsent bool
	cihook55.Set("replay-tx-rolled-back", func() {
		curS := ActiveReplayShard()
		if curS == nil {
			return
		}
		hookFired = true
		preSyncHeapRoot = curS.heapRoot
		preSyncHeapUsed = curS.heapUsed
		// Vérification sur TOUTES les pages touchées, y compris les pages d'overflow au-delà de preUsed :
		// curS.dirty DOIT être 100% identique à curS.pub sur toute la plage jusqu'à headPage+numOflPages !
		endPage := headPage + numOflPages
		if endPage > curS.heapPages {
			endPage = curS.heapPages
		}
		preSyncDirtyMatchesPub = bytes.Equal(curS.dirty[:endPage*pageN], curS.pub[:endPage*pageN])

		var maxSnap [16]byte
		for j := range maxSnap {
			maxSnap[j] = 0xFF
		}
		probe := Db_bt_get_as_of_heap(curS.dirty, uint64(len(curS.dirty)), uint64(len(curS.dirty))/pageN, curS.heapRoot, []byte("ofl-tx-key"), uint64(len("ofl-tx-key")), maxSnap[:], nil, 0)
		preSyncOflKeyAbsent = (probe.Found == 0)
	})
	defer cihook55.Set("replay-tx-rolled-back", nil)

	// Réouverture du Shard : rejoue le WAL scellé
	sReopen, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	// 4. Assertions formelles des préconditions et de la restauration AVANT syncDirtyFull()
	if !mut0Applied || !mut0KeyFoundInDirty || !mut0ValMatchesInDirty {
		_ = sReopen.Close()
		t.Fatalf("PRÉCONDITION NON SATISFAITE : la première mutation n'a pas été constatée appliquée dans s.dirty (applied=%v, found=%v, valMatch=%v) !",
			mut0Applied, mut0KeyFoundInDirty, mut0ValMatchesInDirty)
	}
	if !mut1FailedExpectedly {
		_ = sReopen.Close()
		t.Fatalf("PRÉCONDITION NON SATISFAITE : l'échec de la seconde mutation sur op: 99 n'a pas été identifié !")
	}
	if !hookFired {
		_ = sReopen.Close()
		t.Fatalf("Le hook replay-tx-rolled-back n'a pas été déclenché : la transaction n'a pas atteint l'annulation au rejeu !")
	}
	if preSyncHeapRoot != preRoot {
		_ = sReopen.Close()
		t.Fatalf("preSyncHeapRoot altéré: got %d, want %d", preSyncHeapRoot, preRoot)
	}
	if preSyncHeapUsed != preUsed {
		_ = sReopen.Close()
		t.Fatalf("preSyncHeapUsed altéré: got %d, want %d", preSyncHeapUsed, preUsed)
	}
	if !preSyncDirtyMatchesPub {
		_ = sReopen.Close()
		t.Fatalf("Divergence pub vs dirty détectée AVANT syncDirtyFull sur les pages d'overflow !")
	}
	if !preSyncOflKeyAbsent {
		_ = sReopen.Close()
		t.Fatalf("ofl-tx-key encore présente dans dirty AVANT syncDirtyFull !")
	}

	// Vérifications sur l'instance réouverte
	if _, err := sReopen.Get([]byte("ofl-tx-key")); !errors.Is(err, ErrNotFound) {
		_ = sReopen.Close()
		t.Fatalf("ÉCHEC : clé overflow présente sur shard réouvert !")
	}
	gotBase, err := sReopen.Get([]byte("ofl-base-k"))
	if err != nil || !bytes.Equal(gotBase, []byte("ofl-base-v")) {
		_ = sReopen.Close()
		t.Fatalf("Clé de base altérée: err=%v", err)
	}

	// 5. Poursuite jusqu'à une nouvelle écriture nominale, fermeture et seconde réouverture
	if err := sReopen.Put([]byte("ofl-post-k"), []byte("ofl-post-v")); err != nil {
		_ = sReopen.Close()
		t.Fatalf("Put post-rollback overflow: %v", err)
	}
	gotPost, err := sReopen.Get([]byte("ofl-post-k"))
	if err != nil || !bytes.Equal(gotPost, []byte("ofl-post-v")) {
		_ = sReopen.Close()
		t.Fatalf("Get post-rollback overflow: got %q, want ofl-post-v", gotPost)
	}
	if err := sReopen.Close(); err != nil {
		t.Fatalf("Close sReopen: %v", err)
	}

	// Seconde réouverture : contrôle de durabilité de la reprise opérationnelle
	sReopen2, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard sReopen2: %v", err)
	}
	defer sReopen2.Close()

	if _, err := sReopen2.Get([]byte("ofl-tx-key")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Clé overflow annulée réapparue sur sReopen2 !")
	}
	gotPost2, err := sReopen2.Get([]byte("ofl-post-k"))
	if err != nil || !bytes.Equal(gotPost2, []byte("ofl-post-v")) {
		t.Fatalf("Clé post-rollback absente sur sReopen2: err=%v", err)
	}
	gotBase2, err := sReopen2.Get([]byte("ofl-base-k"))
	if err != nil || !bytes.Equal(gotBase2, []byte("ofl-base-v")) {
		t.Fatalf("Clé de base altérée sur sReopen2: err=%v", err)
	}
}

// TestQual_11b_ReplayAllOrNothing_OverflowValidationFailureRollback prouve qu'en cas d'échec
// de validation du groupe (mutation 2 avec JSON syntaxiquement invalide) survenant APRÈS
// qu'une première chaîne d'overflow a été validée et a copié ses pages dans s.dirty,
// la restauration immédiate pré-groupe (convergeDirtyFromPub) annule intégralement les modifications
// apportées à s.dirty sans laisser aucun résidu partiel.
func TestQual_11b_ReplayAllOrNothing_OverflowValidationFailureRollback(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// 1. Shard avec base stable et scellement actif
	s, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Put([]byte("base-k-11b"), []byte("base-v-11b")); err != nil {
		t.Fatalf("Put base: %v", err)
	}
	preRoot := s.heapRoot
	preUsed := s.heapUsed
	headPage := preUsed + 1

	// Écriture de 4 pages d'overflow scellées
	oflVal := make([]byte, 50000)
	for i := range oflVal {
		oflVal[i] = byte(i*11 + 3)
	}
	offset := 0
	curr := headPage
	numOflPages := uint64(0)
	for seq := uint32(0); offset < len(oflVal); seq++ {
		numOflPages++
		chunk := len(oflVal) - offset
		if uint64(chunk) > OverflowChunkCapacity {
			chunk = int(OverflowChunkCapacity)
		}
		pgBuf := make([]byte, pageN)
		pgBuf[BT_TypeOffset] = TypeOverflow
		var nextPg uint64
		if offset+chunk < len(oflVal) {
			nextPg = curr + 1
		} else {
			nextPg = 0
		}
		binary.LittleEndian.PutUint64(pgBuf[32:40], nextPg)
		binary.LittleEndian.PutUint32(pgBuf[40:44], uint32(chunk))
		binary.LittleEndian.PutUint32(pgBuf[44:48], seq)
		copy(pgBuf[64:64+chunk], oflVal[offset:offset+chunk])
		crc := C2db_crc32c_fullpage(pgBuf, pageN)
		binary.LittleEndian.PutUint32(pgBuf[28:32], crc)

		if err := s.pager.PutPage(curr*pageLBAs, pgBuf); err != nil {
			t.Fatalf("PutPage overflow %d: %v", curr, err)
		}
		offset += chunk
		curr = nextPg
	}
	if _, err := s.pager.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty overflow: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close base: %v", err)
	}

	// TÉMOIN NOMINAL FORMEL À FROID SUR PAGER SCELLÉ :
	// Réouverture d'une instance témoin sans aucun cache préalable (slots vides).
	// Démontre l'acceptation cryptographique intégrale par lecture disque et verifyPage()
	// (Poly1305 contre tags.img) ainsi que la restitution bit-exacte des 50 000 octets.
	sWitness, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard sWitness (11b): %v", err)
	}
	if !sWitness.replayValidateOverflowChain(headPage, 50000) {
		_ = sWitness.Close()
		t.Fatalf("TÉMOIN NOMINAL À FROID ÉCHOUÉ (11b) : chaîne de 4 pages rejetée !")
	}
	witnessVal, err := readOverflowChain(sWitness.dirty, sWitness.heapPages, headPage, 50000)
	if err != nil || !bytes.Equal(witnessVal, oflVal) {
		_ = sWitness.Close()
		t.Fatalf("TÉMOIN NOMINAL À FROID ÉCHOUÉ (11b) : restitution corrompue de l'overflow !")
	}
	if err := sWitness.Close(); err != nil {
		t.Fatalf("Close sWitness (11b): %v", err)
	}

	// 2. Écriture WAL :
	// - Mutation 1 : RecPut de la chaîne d'overflow (sera validée et chargera ses pages dans s.dirty)
	// - Mutation 2 : RecMut avec JSON syntaxiquement MALFORMÉ (provoque l'échec de validateTxGroup)
	// - RecTxCommit
	walPath := filepath.Join(dir, "wal.img")
	w, err := OpenWAL(walPath, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	txID := [16]byte{0xBA, 0xAD, 0xF0, 0x0D, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C}

	desc := make([]byte, 13)
	desc[0] = 1
	binary.LittleEndian.PutUint32(desc[1:5], uint32(len(oflVal)))
	binary.LittleEndian.PutUint64(desc[5:13], headPage)

	p1 := packTxKV([]byte("ofl-fail-k"), desc)
	if err := w.Append(Record{ID: txID, Type: RecPut, Payload: p1}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}

	// Mutation 2 : JSON syntaxiquement invalide
	p2 := packTxKV([]byte("ofl-fail-k"), []byte("MALFORMED_JSON_SYNTAX{{{"))
	if err := w.Append(Record{ID: txID, Type: RecMut, Payload: p2}); err != nil {
		t.Fatalf("Append m2: %v", err)
	}
	if err := w.Append(Record{ID: txID, Type: RecTxCommit}); err != nil {
		t.Fatalf("Append commit: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush WAL: %v", err)
	}
	_ = w.Close()

	// 3. Interception par hooks :
	// a) Observation de la précondition : constatation du chargement effectif des 4 pages dans s.dirty et de la divergence avec s.pub
	var overflowLoadedObserved bool
	var dirtyDivergedFromPub bool
	cihook55.Set("replay-tx-overflow-loaded", func() {
		curS := ActiveReplayShard()
		if curS == nil {
			return
		}
		overflowLoadedObserved = true
		endPage := headPage + numOflPages
		if endPage > curS.heapPages {
			endPage = curS.heapPages
		}
		dirtyDivergedFromPub = !bytes.Equal(curS.dirty[:endPage*pageN], curS.pub[:endPage*pageN])
	})
	defer func() { cihook55.Set("replay-tx-overflow-loaded", nil) }()

	// b) Observation de l'échec ciblé sur le second enregistrement
	var valStep1Failed bool
	cihook55.Set("replay-tx-validation-step-failed", func() {
		curS := ActiveReplayShard()
		if curS == nil {
			return
		}
		if curS.ReplayValStep == 1 {
			valStep1Failed = true
		}
	})
	defer func() { cihook55.Set("replay-tx-validation-step-failed", nil) }()

	// c) Observation de la restauration après annulation et AVANT syncDirtyFull()
	var valHookFired bool
	var valPreSyncDirtyMatchesPub bool
	var valHeapRoot uint64
	var valHeapUsed uint64
	cihook55.Set("replay-tx-validation-failed", func() {
		curS := ActiveReplayShard()
		if curS == nil {
			return
		}
		valHookFired = true
		valHeapRoot = curS.heapRoot
		valHeapUsed = curS.heapUsed
		endPage := headPage + numOflPages
		if endPage > curS.heapPages {
			endPage = curS.heapPages
		}
		valPreSyncDirtyMatchesPub = bytes.Equal(curS.dirty[:endPage*pageN], curS.pub[:endPage*pageN])
	})
	defer cihook55.Set("replay-tx-validation-failed", nil)

	// 4. Réouverture : le rejeu valide m1 (charge les pages d'overflow dans s.dirty),
	// puis échoue sur m2 dans validateTxGroup(), restaurant immédiatement s.dirty depuis s.pub
	sReopen, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	if !overflowLoadedObserved || !dirtyDivergedFromPub {
		_ = sReopen.Close()
		t.Fatalf("PRÉCONDITION NON SATISFAITE (11b) : chargement et divergence de s.dirty non constatés (loaded=%v, diverged=%v) !",
			overflowLoadedObserved, dirtyDivergedFromPub)
	}
	if !valStep1Failed {
		_ = sReopen.Close()
		t.Fatalf("PRÉCONDITION NON SATISFAITE (11b) : échec de validation non identifié sur le second enregistrement !")
	}
	if !valHookFired {
		_ = sReopen.Close()
		t.Fatalf("Le hook replay-tx-validation-failed n'a pas été déclenché !")
	}
	if valHeapRoot != preRoot {
		_ = sReopen.Close()
		t.Fatalf("valHeapRoot = %d, want %d", valHeapRoot, preRoot)
	}
	if valHeapUsed != preUsed {
		_ = sReopen.Close()
		t.Fatalf("valHeapUsed = %d, want %d", valHeapUsed, preUsed)
	}
	if !valPreSyncDirtyMatchesPub {
		_ = sReopen.Close()
		t.Fatalf("Divergence pub vs dirty détectée après annulation de validation d'overflow !")
	}

	if _, err := sReopen.Get([]byte("ofl-fail-k")); !errors.Is(err, ErrNotFound) {
		_ = sReopen.Close()
		t.Fatalf("ofl-fail-k encore présente malgré l'échec de validation au rejeu !")
	}
	gotBase, err := sReopen.Get([]byte("base-k-11b"))
	if err != nil || !bytes.Equal(gotBase, []byte("base-v-11b")) {
		_ = sReopen.Close()
		t.Fatalf("Clé de base altérée: err=%v", err)
	}

	// Écriture nominale post-reprise
	if err := sReopen.Put([]byte("nominal-post-11b"), []byte("nominal-val-11b")); err != nil {
		_ = sReopen.Close()
		t.Fatalf("Put post-validation-failure: %v", err)
	}
	gotPost, err := sReopen.Get([]byte("nominal-post-11b"))
	if err != nil || !bytes.Equal(gotPost, []byte("nominal-val-11b")) {
		_ = sReopen.Close()
		t.Fatalf("Get nominal-post-11b altéré: got %q", gotPost)
	}

	// Fermeture contrôlée de sReopen
	if err := sReopen.Close(); err != nil {
		t.Fatalf("Close sReopen: %v", err)
	}

	// Seconde réouverture avec contrôle de durabilité
	sReopen2, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard sReopen2 (11b): %v", err)
	}
	defer sReopen2.Close()

	if _, err := sReopen2.Get([]byte("ofl-fail-k")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ofl-fail-k réapparue sur sReopen2 !")
	}
	gotBase2, err := sReopen2.Get([]byte("base-k-11b"))
	if err != nil || !bytes.Equal(gotBase2, []byte("base-v-11b")) {
		t.Fatalf("base-k-11b altérée sur sReopen2: err=%v", err)
	}
	gotPost2, err := sReopen2.Get([]byte("nominal-post-11b"))
	if err != nil || !bytes.Equal(gotPost2, []byte("nominal-val-11b")) {
		t.Fatalf("nominal-post-11b non persistée sur sReopen2: got %q", gotPost2)
	}
}

// TestQual_12_PagerCapacity_Franchissement256Slots prouve formellement le franchissement
// de la capacité initiale du cache de pages (PagerSlots = 256), la réservation dimensionnée
// de slots supplémentaires dans le pager AVANT l'engagement durable, et l'absence stricte
// de toute écriture disque / éviction faillible pendant publish().
func TestQual_12_PagerCapacity_Franchissement256Slots(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// Shard avec capacité 1024 pages (16 Mo) pour accueillir largement > 256 pages
	s, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Vérification de la capacité initiale du pager
	if s.pager.SlotsCount() != PagerSlots {
		t.Fatalf("SlotsCount initial = %d, attendu %d", s.pager.SlotsCount(), PagerSlots)
	}

	// Clé de base initiale
	if err := s.Put([]byte("base-k-12"), []byte("base-v-12")); err != nil {
		t.Fatalf("Put base: %v", err)
	}

	// Création d'une transaction générant > 256 pages sales
	// 300 pages d'overflow = 300 * 16320 octets = 4 896 000 octets
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	largeVal := make([]byte, 300*int(OverflowChunkCapacity))
	for i := range largeVal {
		largeVal[i] = byte((i * 13) ^ 0x5A)
	}

	if err := tx.Put([]byte("large-tx-300p"), largeVal); err != nil {
		t.Fatalf("tx.Put large: %v", err)
	}

	// Vérification que le nombre de pages sales dépasse formellement la capacité initiale du pager (256)
	dirtyCount := s.dirtyPagesCount()
	if dirtyCount < 300 {
		t.Fatalf("dirtyPagesCount = %d, attendu >= 300 pages sales", dirtyCount)
	}
	t.Logf("Transaction large préparée : %d pages sales (> PagerSlots=256)", dirtyCount)

	// Interception via before-publish et after-publish :
	// Vérifie que :
	// 1. preparePublish a étendu la capacité du pager (SlotsCount >= dirtyCount)
	// 2. Mesure le compteur d'évictions/vidages disque du pager PENDANT la publication mémoire
	var postCommitPagerSlots int
	var pagerFlushesStart uint64
	var pagerFlushesEnd uint64
	cihook55.Set("before-publish", func() {
		postCommitPagerSlots = s.pager.SlotsCount()
		pagerFlushesStart = s.pager.FlushesCount()
	})
	defer cihook55.Set("before-publish", nil)

	cihook55.Set("after-publish", func() {
		pagerFlushesEnd = s.pager.FlushesCount()
	})
	defer cihook55.Set("after-publish", nil)

	// Commit de la transaction : DOIT réussir sans aucune écriture faillible post-engagement
	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit a échoué: %v", err)
	}

	// 1. Assertion formelle que la capacité du pager a bien été étendue avant/pendant publish
	if postCommitPagerSlots < dirtyCount {
		t.Fatalf("Le pager n'a pas été dimensionné: postCommitPagerSlots=%d < dirtyCount=%d", postCommitPagerSlots, dirtyCount)
	}

	// 2. Preuve formelle d'absence d'écriture faillible pendant publish() :
	// Le compteur d'évictions/vidages matériels du pager n'a pas bougé pendant publish() !
	if pagerFlushesEnd != pagerFlushesStart {
		t.Fatalf("ÉCHEC : FlushDirty matériel détecté PENDANT publish() ! flushesEnd=%d, flushesStart=%d", pagerFlushesEnd, pagerFlushesStart)
	}
	t.Logf("Preuve acquise : 0 FlushDirty pendant publish() sur %d pages transférées (capacité pager=%d)", dirtyCount, postCommitPagerSlots)

	// Vérification de lecture immédiate en mémoire
	got, err := s.Get([]byte("large-tx-300p"))
	if err != nil || !bytes.Equal(got, largeVal) {
		t.Fatalf("Lecture post-commit de la clé 300 pages altérée: len(got)=%d, len(exp)=%d", len(got), len(largeVal))
	}

	// Fermeture propre avec FlushDirty de toutes les pages vers data.img
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Réouverture et vérification de la persistance intégrale des 300 pages
	sReopen, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard réouverture: %v", err)
	}
	defer sReopen.Close()

	gotReopen, err := sReopen.Get([]byte("large-tx-300p"))
	if err != nil || !bytes.Equal(gotReopen, largeVal) {
		t.Fatalf("Clé 300 pages corrompue après réouverture: err=%v", err)
	}
	gotBase, err := sReopen.Get([]byte("base-k-12"))
	if err != nil || !bytes.Equal(gotBase, []byte("base-v-12")) {
		t.Fatalf("Clé de base altérée après réouverture: err=%v", err)
	}

	// Écriture nominale post-reprise
	if err := sReopen.Put([]byte("resume-k-12"), []byte("resume-v-12")); err != nil {
		t.Fatalf("Put post-reprise: %v", err)
	}
	gotResume, err := sReopen.Get([]byte("resume-k-12"))
	if err != nil || !bytes.Equal(gotResume, []byte("resume-v-12")) {
		t.Fatalf("Get post-reprise altéré: got %q", gotResume)
	}
}
