// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/binary"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

const (
	// TenantMACContext est le contexte de domaine des clés de chiffrement des
	// données du moteur c2db. Il est réservé au scellement des pages et du
	// journal (voir PageSealContext et WALSealContext) et ne doit jamais servir
	// à l'identité de session.
	TenantMACContext = "c2db/tenant-mac/v1"

	// SessionMACContext est le contexte de domaine de l'identité de session du
	// cluster, employé pour la poignée de main mTLS. Il est délibérément
	// distinct de TenantMACContext : la séparation des domaines garantit qu'une
	// clé d'identité de session compromise n'expose pas le chiffrement des
	// données, et réciproquement.
	SessionMACContext = "c2cluster/tenant-mac/v1"

	PageSealContext = "c2db-page-seal/v1"
	WALSealContext  = "c2db-wal-seal/v1"
	epochRingCap    = 8
)

// deriveTenantMAC construit le matériel de dérivation commun (clé maîtresse,
// identifiant de locataire, époque) puis applique le contexte de domaine
// fourni. Zéro allocation : le tampon de dérivation réside sur la pile.
func deriveTenantMAC(domain string, master [32]byte, tenant uint16, epoch c2uuidv7.UUID) [32]byte {
	var mat [50]byte
	copy(mat[:32], master[:])
	binary.LittleEndian.PutUint16(mat[32:34], tenant)
	copy(mat[34:], epoch[:])
	return blake3archtsim.DeriveKey(domain, mat[:])
}

// DeriveTenantMAC dérive la clé de chiffrement des données du locataire dans le
// domaine TenantMACContext. Sa valeur est immuable : toute modification
// romprait la lecture des données existantes.
func DeriveTenantMAC(master [32]byte, tenant uint16, epoch c2uuidv7.UUID) [32]byte {
	return deriveTenantMAC(TenantMACContext, master, tenant, epoch)
}

// DeriveSessionMAC dérive la graine d'identité de session du locataire dans le
// domaine SessionMACContext. Cette graine unique est partagée par le serveur
// (c2cluster/c2net) et par les clients (c2client et c2cluster/c2client), de
// sorte que les certificats Ed25519 des deux extrémités de la poignée de main
// mTLS se correspondent. Elle ne doit jamais alimenter le chiffrement des
// données, qui relève de DeriveTenantMAC.
func DeriveSessionMAC(master [32]byte, tenant uint16, epoch c2uuidv7.UUID) [32]byte {
	return deriveTenantMAC(SessionMACContext, master, tenant, epoch)
}

// DerivePageSealKey dérive une clé Poly1305 à usage unique pour une page de données
// à partir de la clé maîtresse, du shard, de l'index de page et du hachage de son contenu.
// Zéro allocation : le tampon matériel de dérivation est alloué sur la pile.
func DerivePageSealKey(master [32]byte, shard uint16, pageIdx uint64, pageContent []byte) [32]byte {
	var mat [74]byte
	copy(mat[:32], master[:])
	binary.LittleEndian.PutUint16(mat[32:34], shard)
	binary.LittleEndian.PutUint64(mat[34:42], pageIdx)
	h := blake3archtsim.Sum256(pageContent)
	copy(mat[42:74], h[:])
	return blake3archtsim.DeriveKey(PageSealContext, mat[:])
}

// DeriveWALSealKey dérive une clé Poly1305 à usage unique pour un bloc de journal WAL
// à partir de la clé maîtresse et du bloc canonique complet (hors tag final).
// Zéro allocation : le tampon matériel de dérivation est alloué sur la pile.
func DeriveWALSealKey(base [32]byte, canonicalMsg []byte) [32]byte {
	var mat [32 + (walTagOff - walLenOff)]byte
	copy(mat[:32], base[:])
	copy(mat[32:], canonicalMsg)
	return blake3archtsim.DeriveKey(WALSealContext, mat[:])
}

type epochSlot struct {
	epoch c2uuidv7.UUID
	gen   uint32
	mac   [32]byte
	live  bool
}

type EpochRing struct {
	slots [epochRingCap]epochSlot
	gens  [epochRingCap]uint32
	n     uint8
	cur   uint8
}

func (r *EpochRing) Install(epoch c2uuidv7.UUID, mac [32]byte) {
	var i uint8
	if r.n < epochRingCap {
		i = r.n
		r.n++
	} else {
		i = (r.cur + 1) % epochRingCap
	}
	r.gens[i]++
	r.slots[i] = epochSlot{epoch: epoch, gen: r.gens[i], mac: mac, live: true}
	r.cur = i
}

func (r *EpochRing) Current() (mac [32]byte, epoch c2uuidv7.UUID, gen uint32, ok bool) {
	if r.n == 0 {
		return
	}
	s := r.slots[r.cur]
	if !s.live {
		return
	}
	return s.mac, s.epoch, s.gen, true
}

func (r *EpochRing) Lookup(epoch c2uuidv7.UUID) (mac [32]byte, gen uint32, ok bool) {
	for i := uint8(0); i < r.n; i++ {
		s := r.slots[i]
		if s.live && s.epoch == epoch {
			return s.mac, s.gen, true
		}
	}
	return
}

func (r *EpochRing) Revoke(epoch c2uuidv7.UUID) bool {
	for i := uint8(0); i < r.n; i++ {
		if r.slots[i].live && r.slots[i].epoch == epoch {
			r.slots[i].live = false
			r.slots[i].mac = [32]byte{}
			r.gens[i]++
			r.slots[i].gen = r.gens[i]
			return true
		}
	}
	return false
}
