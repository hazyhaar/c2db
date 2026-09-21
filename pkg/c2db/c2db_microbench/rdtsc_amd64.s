// SPDX-License-Identifier: Apache-2.0 OR MIT
//go:build amd64

#include "textflag.h"

// func rdtsc() uint64
TEXT ·rdtsc(SB), NOSPLIT, $0-8
	BYTE $0x0F; BYTE $0x31 // RDTSC: EDX:EAX <- TSC
	SHLQ $32, DX
	ORQ DX, AX
	MOVQ AX, ret+0(FP)
	RET

// func rdtscp() (tsc uint64, aux uint32)
TEXT ·rdtscp(SB), NOSPLIT, $0-12
	BYTE $0x0F; BYTE $0x01; BYTE $0xF9 // RDTSCP: EDX:EAX <- TSC, ECX <- IA32_TSC_AUX
	SHLQ $32, DX
	ORQ DX, AX
	MOVQ AX, tsc+0(FP)
	MOVL CX, aux+8(FP)
	RET
