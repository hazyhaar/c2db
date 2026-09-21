package c2poly1305

import (
	"bytes"
	"golang.org/x/crypto/poly1305"
	"math/big"
	"testing"
)

func genMathLimitVectors() []struct {
	name     string
	key, msg []byte
} {
	vectors := []struct {
		name     string
		key, msg []byte
	}{}

	p := new(big.Int)
	p.SetString("3fffffffffffffffffffffffffffffffb", 16) // 2^130 - 5

	pMinus1 := new(big.Int).Sub(p, big.NewInt(1))
	pPlus1 := new(big.Int).Add(p, big.NewInt(1))
	bound2_130_6 := new(big.Int).Sub(p, big.NewInt(1)) // = 2^130 - 6
	bound2_130_6.SetString("3fffffffffffffffffffffffffffffffa", 16)

	encodeLE16 := func(n *big.Int) []byte {
		b := n.Bytes()
		res := make([]byte, 16)
		for i := 0; i < len(b) && i < 16; i++ {
			res[i] = b[len(b)-1-i]
		}
		return res
	}

	maxClampKey := make([]byte, 32)
	for i := 0; i < 16; i++ {
		maxClampKey[i] = 0xff
	}
	maxClampKey[3] &= 0x0f
	maxClampKey[4] &= 0xfc
	maxClampKey[7] &= 0x0f
	maxClampKey[8] &= 0xfc
	maxClampKey[11] &= 0x0f
	maxClampKey[12] &= 0xfc
	for i := 16; i < 32; i++ {
		maxClampKey[i] = 0xff
	}

	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Math_P", maxClampKey, encodeLE16(p)})
	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Math_P_Minus_1", maxClampKey, encodeLE16(pMinus1)})
	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Math_P_Plus_1", maxClampKey, encodeLE16(pPlus1)})
	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Math_2_130_Minus_6", maxClampKey, encodeLE16(bound2_130_6)})
	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Max_Clamp_Key_Zero_Msg", maxClampKey, bytes.Repeat([]byte{0}, 16)})
	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Max_Clamp_Key_FF_Msg", maxClampKey, bytes.Repeat([]byte{0xff}, 16)})

	// Test de débordement 26-bitx5 avec maxClampKey
	carryMsg := bytes.Repeat([]byte{0xff, 0xff, 0xff, 0xff}, 4) // 16 bytes de 0xff
	// Chaîne longue pour accumuler
	carryMsgLong := bytes.Repeat(carryMsg, 16)
	vectors = append(vectors, struct {
		name     string
		key, msg []byte
	}{"Carry_Overflow_Long", maxClampKey, carryMsgLong})

	return vectors
}

func TestAdversarialVectors(t *testing.T) {
	vectors := genMathLimitVectors()

	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			var got, want [16]byte
			var k [32]byte
			copy(k[:], tc.key)

			poly1305.Sum(&want, tc.msg, &k)
			Crypto_poly1305(got[:], tc.msg, uint64(len(tc.msg)), tc.key)

			if !bytes.Equal(got[:], want[:]) {
				t.Fatalf("Vector %s failed: got %x, want %x", tc.name, got, want)
			}
		})
	}
}
