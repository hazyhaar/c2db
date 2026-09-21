TEXT github.com/hazyhaar/c2pkg/c2db.(*Device).pwriteAll(SB) /devhoros/c2simd/c2pkg/c2db/device.go
  device.go:227		0x898900		493b6610		CMPQ SP, 0x10(R14)						
  device.go:227		0x898904		0f86af010000		JBE 0x898ab9							
  device.go:227		0x89890a		55			PUSHQ BP							
  device.go:227		0x89890b		4889e5			MOVQ SP, BP							
  device.go:227		0x89890e		4883ec70		SUBQ $0x70, SP							
  device.go:227		0x898912		48899c2488000000	MOVQ BX, 0x88(SP)						
  device.go:228		0x89891a		4889842480000000	MOVQ AX, 0x80(SP)						
  device.go:228		0x898922		eb09			JMP 0x89892d							
  device.go:229		0x898924		4c89c0			MOVQ R8, AX							
  device.go:228		0x898927		4889d1			MOVQ DX, CX							
  device.go:228		0x89892a		4c89cb			MOVQ R9, BX							
  device.go:228		0x89892d		4885c9			TESTQ CX, CX							
  device.go:228		0x898930		0f8473010000		JE 0x898aa9							
  device.go:228		0x898936		48897c2440		MOVQ DI, 0x40(SP)						
  device.go:228		0x89893b		48894c2438		MOVQ CX, 0x38(SP)						
  device.go:228		0x898940		48895c2460		MOVQ BX, 0x60(SP)						
  device.go:228		0x898945		4889742448		MOVQ SI, 0x48(SP)						
  device.go:229		0x89894a		488b00			MOVQ 0(AX), AX							
  syscall_unix.go:207	0x89894d		e8ce5fdbff		CALL golang.org/x/sys/unix.pwrite(SB)				
  device.go:230		0x898952		4885c0			TESTQ AX, AX							
  device.go:230		0x898955		7e4d			JLE 0x8989a4							
  device.go:231		0x898957		90			NOPL								
  type.go:195		0x898958		ba01000000		MOVL $0x1, DX							
  type.go:195		0x89895d		4c8b842480000000	MOVQ 0x80(SP), R8						
  type.go:195		0x898965		f0490fc15018		LOCK XADDQ DX, 0x18(R8)						
  device.go:232		0x89896b		488b542438		MOVQ 0x38(SP), DX						
  device.go:232		0x898970		4839d0			CMPQ AX, DX							
  device.go:232		0x898973		0f873a010000		JA 0x898ab3							
  device.go:232		0x898979		488b7c2440		MOVQ 0x40(SP), DI						
  device.go:232		0x89897e		4829c7			SUBQ AX, DI							
  device.go:232		0x898981		4989f9			MOVQ DI, R9							
  device.go:232		0x898984		49f7d9			NEGQ R9								
  device.go:232		0x898987		49c1f93f		SARQ $0x3f, R9							
  device.go:232		0x89898b		4921c1			ANDQ AX, R9							
  device.go:232		0x89898e		4829c2			SUBQ AX, DX							
  device.go:232		0x898991		4c8b542460		MOVQ 0x60(SP), R10						
  device.go:232		0x898996		4d01d1			ADDQ R10, R9							
  device.go:233		0x898999		4c8b542448		MOVQ 0x48(SP), R10						
  device.go:233		0x89899e		498d3402		LEAQ 0(R10)(AX*1), SI						
  device.go:233		0x8989a2		eb1c			JMP 0x8989c0							
  device.go:229		0x8989a4		4c8b842480000000	MOVQ 0x80(SP), R8						
  device.go:235		0x8989ac		488b742448		MOVQ 0x48(SP), SI						
  device.go:235		0x8989b1		488b7c2440		MOVQ 0x40(SP), DI						
  device.go:235		0x8989b6		488b542438		MOVQ 0x38(SP), DX						
  device.go:235		0x8989bb		4c8b4c2460		MOVQ 0x60(SP), R9						
  device.go:235		0x8989c0		4885db			TESTQ BX, BX							
  device.go:235		0x8989c3		7568			JNE 0x898a2d							
  device.go:230		0x8989c5		4885c0			TESTQ AX, AX							
  device.go:240		0x8989c8		0f8556ffffff		JNE 0x898924							
  device.go:241		0x8989ce		90			NOPL								
  type.go:22		0x8989cf		ba01000000		MOVL $0x1, DX							
  type.go:22		0x8989d4		41875020		XCHGL DX, 0x20(R8)						
  errors.go:26		0x8989d8		488d0520063b00		LEAQ 0x3b0620(IP), AX						
  errors.go:26		0x8989df		bb12000000		MOVL $0x12, BX							
  errors.go:26		0x8989e4		31c9			XORL CX, CX							
  errors.go:26		0x8989e6		31ff			XORL DI, DI							
  errors.go:26		0x8989e8		89fe			MOVL DI, SI							
  errors.go:26		0x8989ea		e831b9c5ff		CALL fmt.errorf(SB)						
  errors.go:26		0x8989ef		4885c0			TESTQ AX, AX							
  errors.go:26		0x8989f2		7533			JNE 0x898a27							
  errors.go:31		0x8989f4		90			NOPL								
  errors.go:65		0x8989f5		b810000000		MOVL $0x10, AX							
  errors.go:65		0x8989fa		488d1d47a4a000		LEAQ 0xa0a447(IP), BX						
  errors.go:65		0x898a01		b901000000		MOVL $0x1, CX							
  errors.go:65		0x898a06		e8f596b8ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)			
  errors.go:65		0x898a0b		48c7400812000000	MOVQ $0x12, 0x8(AX)						
  errors.go:65		0x898a13		488d15e5053b00		LEAQ 0x3b05e5(IP), DX						
  errors.go:65		0x898a1a		488910			MOVQ DX, 0(AX)							
  device.go:242		0x898a1d		4889c3			MOVQ AX, BX							
  device.go:242		0x898a20		488d050989a500		LEAQ 0xa58909(IP), AX						
  device.go:242		0x898a27		4883c470		ADDQ $0x70, SP							
  device.go:242		0x898a2b		5d			POPQ BP								
  device.go:242		0x898a2c		c3			RET								
  syscall_unix.go:207	0x898a2d		48895c2450		MOVQ BX, 0x50(SP)						
  syscall_unix.go:207	0x898a32		48894c2468		MOVQ CX, 0x68(SP)						
  device.go:237		0x898a37		4889f2			MOVQ SI, DX							
  device.go:237		0x898a3a		48c1fe3f		SARQ $0x3f, SI							
  device.go:237		0x898a3e		48c1ee34		SHRQ $0x34, SI							
  device.go:237		0x898a42		4801f2			ADDQ SI, DX							
  device.go:237		0x898a45		48c1fa0c		SARQ $0xc, DX							
  device.go:237		0x898a49		4889542458		MOVQ DX, 0x58(SP)						
  device.go:236		0x898a4e		90			NOPL								
  type.go:22		0x898a4f		ba01000000		MOVL $0x1, DX							
  type.go:22		0x898a54		41875020		XCHGL DX, 0x20(R8)						
  device.go:237		0x898a58		488b5318		MOVQ 0x18(BX), DX						
  device.go:237		0x898a5c		4889c8			MOVQ CX, AX							
  device.go:237		0x898a5f		90			NOPL								
  device.go:237		0x898a60		ffd2			CALL DX								
  device.go:237		0x898a62		b90e000000		MOVL $0xe, CX							
  device.go:237		0x898a67		4889c7			MOVQ AX, DI							
  device.go:237		0x898a6a		4889de			MOVQ BX, SI							
  device.go:237		0x898a6d		31c0			XORL AX, AX							
  device.go:237		0x898a6f		488d1d10ba3a00		LEAQ 0x3aba10(IP), BX						
  device.go:237		0x898a76		e8a547bdff		CALL runtime.concatstring2(SB)					
  device.go:237		0x898a7b		488b4c2458		MOVQ 0x58(SP), CX						
  device.go:237		0x898a80		31ff			XORL DI, DI							
  device.go:237		0x898a82		31f6			XORL SI, SI							
  device.go:237		0x898a84		4531c0			XORL R8, R8							
  device.go:237		0x898a87		4989c1			MOVQ AX, R9							
  device.go:237		0x898a8a		4989da			MOVQ BX, R10							
  device.go:237		0x898a8d		31c0			XORL AX, AX							
  device.go:237		0x898a8f		bb04000000		MOVL $0x4, BX							
  device.go:237		0x898a94		e847df0100		CALL github.com/hazyhaar/c2pkg/c2db.probeEmit(SB)		
  device.go:238		0x898a99		488b442450		MOVQ 0x50(SP), AX						
  device.go:238		0x898a9e		488b5c2468		MOVQ 0x68(SP), BX						
  device.go:238		0x898aa3		4883c470		ADDQ $0x70, SP							
  device.go:238		0x898aa7		5d			POPQ BP								
  device.go:238		0x898aa8		c3			RET								
  device.go:245		0x898aa9		31c0			XORL AX, AX							
  device.go:245		0x898aab		31db			XORL BX, BX							
  device.go:245		0x898aad		4883c470		ADDQ $0x70, SP							
  device.go:245		0x898ab1		5d			POPQ BP								
  device.go:245		0x898ab2		c3			RET								
  device.go:232		0x898ab3		e828b4bfff		CALL runtime.panicBounds(SB)					
  device.go:232		0x898ab8		90			NOPL								
  device.go:227		0x898ab9		4889442408		MOVQ AX, 0x8(SP)						
  device.go:227		0x898abe		48895c2410		MOVQ BX, 0x10(SP)						
  device.go:227		0x898ac3		48894c2418		MOVQ CX, 0x18(SP)						
  device.go:227		0x898ac8		48897c2420		MOVQ DI, 0x20(SP)						
  device.go:227		0x898acd		4889742428		MOVQ SI, 0x28(SP)						
  device.go:227		0x898ad2		e86996bfff		CALL runtime.morestack_noctxt.abi0(SB)				
  device.go:227		0x898ad7		488b442408		MOVQ 0x8(SP), AX						
  device.go:227		0x898adc		488b5c2410		MOVQ 0x10(SP), BX						
  device.go:227		0x898ae1		488b4c2418		MOVQ 0x18(SP), CX						
  device.go:227		0x898ae6		488b7c2420		MOVQ 0x20(SP), DI						
  device.go:227		0x898aeb		488b742428		MOVQ 0x28(SP), SI						
  device.go:227		0x898af0		e90bfeffff		JMP github.com/hazyhaar/c2pkg/c2db.(*Device).pwriteAll(SB)	
