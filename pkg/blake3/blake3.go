package blake3archtsim

import (
	"encoding/binary"
)

// La compression d'un bloc n'est plus portée par du Go manuscrit : elle est
// transpilée depuis /devhoros/c2simd/sources/blake3archtsim.c et vit dans
// blake3archtsim_gen.go (branche vectorielle sous goexperiment.simd && amd64)
// et blake3archtsim_nosimd_gen.go (repli scalaire). Le présent fichier ne
// garde que l'arbre de hachage, qui n'a pas de forme vectorielle.
//
// Les constantes IV et de permutation du message vivent elles aussi dans
// l'artefact émis, sous les noms BLAKE3ARCHTSIM_IV et
// BLAKE3ARCHTSIM_MSG_PERMUTATION.

const (
	BlockSize = 64
	ChunkSize = 1024
	OutLen    = 32
	KeyLen    = 32

	ChunkStart        = 1 << 0
	ChunkEnd          = 1 << 1
	Parent            = 1 << 2
	Root              = 1 << 3
	KeyedHash         = 1 << 4
	DeriveKeyContext  = 1 << 5
	DeriveKeyMaterial = 1 << 6
)

// Digest represents an active BLAKE3 hashing instance.
type Digest struct {
	keyWords    [8]uint32
	hasherFlags uint8
	stack       [54][8]uint32
	stackLen    uint8
	chunk       chunkState
}

type chunkState struct {
	cv               [8]uint32
	chunkCounter     uint64
	buf              [BlockSize]byte
	bufLen           uint8
	blocksCompressed uint8
	flags            uint8
}

// New returns an initialized BLAKE3 hasher.
func New() *Digest {
	d := new(Digest)
	d.Reset()
	return d
}

func wordsFromKey(key [32]byte) (w [8]uint32) {
	for i := 0; i < 8; i++ {
		w[i] = binary.LittleEndian.Uint32(key[i*4 : (i+1)*4])
	}
	return w
}

func (d *Digest) init(keyWords [8]uint32, flags uint8) {
	d.keyWords = keyWords
	d.hasherFlags = flags
	d.stackLen = 0
	d.chunk.cv = keyWords
	d.chunk.chunkCounter = 0
	d.chunk.bufLen = 0
	d.chunk.blocksCompressed = 0
	d.chunk.flags = flags
	d.chunk.buf = [BlockSize]byte{}
}

func (d *Digest) Reset() {
	d.init(BLAKE3ARCHTSIM_IV, 0)
}

func DeriveKey(context string, keyMaterial []byte) [32]byte {
	var ctx Digest
	ctx.init(BLAKE3ARCHTSIM_IV, DeriveKeyContext)
	_, _ = ctx.Write([]byte(context))
	ck := ctx.SumFinal()
	var mat Digest
	mat.init(wordsFromKey(ck), DeriveKeyMaterial)
	_, _ = mat.Write(keyMaterial)
	return mat.SumFinal()
}

// Write absorbs data.
func (d *Digest) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		if d.chunk.bufLen == BlockSize {
			if d.chunk.blocksCompressed == 15 {
				chunkOut := d.chunk.output()
				d.pushStack(chunkOut)
				d.chunk.chunkCounter++
				d.chunk.cv = d.keyWords
				d.chunk.bufLen = 0
				d.chunk.blocksCompressed = 0
				d.chunk.flags = d.hasherFlags
				d.chunk.buf = [BlockSize]byte{}
			} else {
				var out [16]uint32
				flags := d.chunk.flags
				if d.chunk.blocksCompressed == 0 {
					flags |= ChunkStart
				}
				Blake3archtsim_compress(d.chunk.cv[:], d.chunk.buf[:], BlockSize, d.chunk.chunkCounter, flags, out[:])
				copy(d.chunk.cv[:], out[:8])
				d.chunk.blocksCompressed++
				d.chunk.bufLen = 0
				d.chunk.buf = [BlockSize]byte{}
			}
		}

		want := BlockSize - int(d.chunk.bufLen)
		if want > len(p) {
			want = len(p)
		}
		copy(d.chunk.buf[d.chunk.bufLen:], p[:want])
		d.chunk.bufLen += uint8(want)
		p = p[want:]
	}
	return total, nil
}

func (c *chunkState) output() [8]uint32 {
	flags := c.flags | ChunkEnd
	if c.blocksCompressed == 0 {
		flags |= ChunkStart
	}
	var out [16]uint32
	Blake3archtsim_compress(c.cv[:], c.buf[:], c.bufLen, c.chunkCounter, flags, out[:])
	var cv [8]uint32
	copy(cv[:], out[:8])
	return cv
}

func (d *Digest) pushStack(cv [8]uint32) {
	totalChunks := d.chunk.chunkCounter + 1
	for (totalChunks & 1) == 0 {
		parentBlock := makeParentBlock(d.stack[d.stackLen-1], cv)
		d.stackLen--
		var parentOut [16]uint32
		Blake3archtsim_compress(d.keyWords[:], parentBlock[:], BlockSize, 0, Parent|d.hasherFlags, parentOut[:])
		copy(cv[:], parentOut[:8])
		totalChunks >>= 1
	}
	d.stack[d.stackLen] = cv
	d.stackLen++
}

func makeParentBlock(l, r [8]uint32) [64]byte {
	var b [64]byte
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(b[i*4:(i+1)*4], l[i])
		binary.LittleEndian.PutUint32(b[32+i*4:32+(i+1)*4], r[i])
	}
	return b
}

// Sum256 computes BLAKE3 hash in one pass without heap allocation.
func Sum256(data []byte) [32]byte {
	var d Digest
	d.Reset()
	_, _ = d.Write(data)
	return d.SumFinal()
}

// SumFinal finishes hash calculation.
func (d *Digest) SumFinal() [32]byte {
	var out [32]byte
	if d.stackLen == 0 {
		// Single chunk root
		flags := d.chunk.flags | ChunkEnd | Root
		if d.chunk.blocksCompressed == 0 {
			flags |= ChunkStart
		}
		var out16 [16]uint32
		Blake3archtsim_compress(d.chunk.cv[:], d.chunk.buf[:], d.chunk.bufLen, d.chunk.chunkCounter, flags, out16[:])
		for i := 0; i < 8; i++ {
			binary.LittleEndian.PutUint32(out[i*4:(i+1)*4], out16[i])
		}
		return out
	}

	currentCV := d.chunk.output()
	for d.stackLen > 0 {
		parentBlock := makeParentBlock(d.stack[d.stackLen-1], currentCV)
		d.stackLen--
		flags := uint8(Parent) | d.hasherFlags
		if d.stackLen == 0 {
			flags |= Root
		}
		var parentOut [16]uint32
		Blake3archtsim_compress(d.keyWords[:], parentBlock[:], BlockSize, 0, flags, parentOut[:])
		copy(currentCV[:], parentOut[:8])
	}

	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(out[i*4:(i+1)*4], currentCV[i])
	}
	return out
}
