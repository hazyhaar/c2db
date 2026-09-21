TEXT github.com/hazyhaar/c2pkg/c2db.(*WAL).writeOne(SB) /devhoros/c2simd/c2pkg/c2db/wal.go
  wal.go:417		0x8d4360		4c8d6424d0		LEAQ -0x30(SP), R12						
  wal.go:417		0x8d4365		4d3b6610		CMPQ R12, 0x10(R14)						
  wal.go:417		0x8d4369		0f867c020000		JBE 0x8d45eb							
  wal.go:417		0x8d436f		55			PUSHQ BP							
  wal.go:417		0x8d4370		4889e5			MOVQ SP, BP							
  wal.go:417		0x8d4373		4881eca8000000		SUBQ $0xa8, SP							
  wal.go:419		0x8d437a		48898424e8000000	MOVQ AX, 0xe8(SP)						
  wal.go:418		0x8d4382		488b10			MOVQ 0(AX), DX							
  wal.go:418		0x8d4385		488b5208		MOVQ 0x8(DX), DX						
  wal.go:418		0x8d4389		48c1ea0c		SHRQ $0xc, DX							
  wal.go:419		0x8d438d		48395038		CMPQ 0x38(AX), DX						
  wal.go:419		0x8d4391		0f82dc000000		JB 0x8d4473							
  wal.go:420		0x8d4397		80786800		CMPB 0x68(AX), $0x0						
  wal.go:420		0x8d439b		742e			JE 0x8d43cb							
  wal.go:418		0x8d439d		4889942488000000	MOVQ DX, 0x88(SP)						
  wal.go:611		0x8d43a5		31db			XORL BX, BX							
  wal.go:611		0x8d43a7		31c9			XORL CX, CX							
  wal.go:611		0x8d43a9		31ff			XORL DI, DI							
  wal.go:611		0x8d43ab		31f6			XORL SI, SI							
  wal.go:611		0x8d43ad		e8ee100000		CALL github.com/hazyhaar/c2pkg/c2db.(*WAL).compactTo(SB)	
  wal.go:421		0x8d43b2		4885c0			TESTQ AX, AX							
  wal.go:421		0x8d43b5		0f85af000000		JNE 0x8d446a							
  wal.go:425		0x8d43bb		488b8424e8000000	MOVQ 0xe8(SP), AX						
  wal.go:425		0x8d43c3		488b942488000000	MOVQ 0x88(SP), DX						
  wal.go:425		0x8d43cb		4c8b6038		MOVQ 0x38(AX), R12						
  wal.go:425		0x8d43cf		4939d4			CMPQ R12, DX							
  wal.go:425		0x8d43d2		0f829b000000		JB 0x8d4473							
  wal.go:426		0x8d43d8		440f11bc2498000000	MOVUPS X15, 0x98(SP)						
  wal.go:426		0x8d43e1		4c89e0			MOVQ R12, AX							
  wal.go:426		0x8d43e4		e87759bbff		CALL runtime.convT64(SB)					
  wal.go:426		0x8d43e9		488d0dd0ff9b00		LEAQ 0x9bffd0(IP), CX						
  wal.go:426		0x8d43f0		48898c2498000000	MOVQ CX, 0x98(SP)						
  wal.go:426		0x8d43f8		48898424a0000000	MOVQ AX, 0xa0(SP)						
  errors.go:26		0x8d4400		488d0526b43700		LEAQ 0x37b426(IP), AX						
  errors.go:26		0x8d4407		bb17000000		MOVL $0x17, BX							
  errors.go:26		0x8d440c		488d8c2498000000	LEAQ 0x98(SP), CX						
  errors.go:26		0x8d4414		bf01000000		MOVL $0x1, DI							
  errors.go:26		0x8d4419		89fe			MOVL DI, SI							
  errors.go:26		0x8d441b		0f1f440000		NOPL 0(AX)(AX*1)						
  errors.go:26		0x8d4420		e8fbfec1ff		CALL fmt.errorf(SB)						
  errors.go:26		0x8d4425		4885c0			TESTQ AX, AX							
  errors.go:26		0x8d4428		7537			JNE 0x8d4461							
  errors.go:31		0x8d442a		90			NOPL								
  errors.go:65		0x8d442b		b810000000		MOVL $0x10, AX							
  errors.go:65		0x8d4430		488d1d11ea9c00		LEAQ 0x9cea11(IP), BX						
  errors.go:65		0x8d4437		b901000000		MOVL $0x1, CX							
  errors.go:65		0x8d443c		0f1f4000		NOPL 0(AX)							
  errors.go:65		0x8d4440		e8bbdcb4ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)			
  errors.go:65		0x8d4445		48c7400817000000	MOVQ $0x17, 0x8(AX)						
  errors.go:65		0x8d444d		488d15d9b33700		LEAQ 0x37b3d9(IP), DX						
  errors.go:65		0x8d4454		488910			MOVQ DX, 0(AX)							
  wal.go:426		0x8d4457		4889c3			MOVQ AX, BX							
  wal.go:426		0x8d445a		488d05cfcea100		LEAQ 0xa1cecf(IP), AX						
  wal.go:426		0x8d4461		4881c4a8000000		ADDQ $0xa8, SP							
  wal.go:426		0x8d4468		5d			POPQ BP								
  wal.go:426		0x8d4469		c3			RET								
  wal.go:422		0x8d446a		4881c4a8000000		ADDQ $0xa8, SP							
  wal.go:422		0x8d4471		5d			POPQ BP								
  wal.go:422		0x8d4472		c3			RET								
  wal.go:429		0x8d4473		4883b8b800000000	CMPQ 0xb8(AX), $0x0						
  wal.go:429		0x8d447b		750b			JNE 0x8d4488							
  wal.go:430		0x8d447d		488b5038		MOVQ 0x38(AX), DX						
  wal.go:430		0x8d4481		488990b0000000		MOVQ DX, 0xb0(AX)						
  wal.go:432		0x8d4488		488b90b8000000		MOVQ 0xb8(AX), DX						
  wal.go:432		0x8d448f		48c1e20c		SHLQ $0xc, DX							
  wal.go:433		0x8d4493		488b88a8000000		MOVQ 0xa8(AX), CX						
  wal.go:433		0x8d449a		4c8da200100000		LEAQ 0x1000(DX), R12						
  wal.go:433		0x8d44a1		4c39e1			CMPQ CX, R12							
  wal.go:433		0x8d44a4		0f823b010000		JB 0x8d45e5							
  wal.go:433		0x8d44aa		4c39e2			CMPQ DX, R12							
  wal.go:433		0x8d44ad		0f872c010000		JA 0x8d45df							
  wal.go:433		0x8d44b3		4c8ba098000000		MOVQ 0x98(AX), R12						
  wal.go:433		0x8d44ba		4829d1			SUBQ DX, CX							
  wal.go:433		0x8d44bd		48898c2480000000	MOVQ CX, 0x80(SP)						
  wal.go:433		0x8d44c5		498d0414		LEAQ 0(R12)(DX*1), AX						
  wal.go:433		0x8d44c9		4889842490000000	MOVQ AX, 0x90(SP)						
  wal.go:434		0x8d44d1		440fb69424c8000000	MOVZX 0xc8(SP), R10						
  wal.go:434		0x8d44da		4c8b9c24d8000000	MOVQ 0xd8(SP), R11						
  wal.go:434		0x8d44e2		488b9424d0000000	MOVQ 0xd0(SP), DX						
  wal.go:434		0x8d44ea		4c8ba424e0000000	MOVQ 0xe0(SP), R12						
  wal.go:434		0x8d44f2		48891424		MOVQ DX, 0(SP)							
  wal.go:434		0x8d44f6		4c895c2408		MOVQ R11, 0x8(SP)						
  wal.go:434		0x8d44fb		4c89642410		MOVQ R12, 0x10(SP)						
  wal.go:434		0x8d4500		bb00100000		MOVL $0x1000, BX						
  wal.go:434		0x8d4505		bf00100000		MOVL $0x1000, DI						
  wal.go:434		0x8d450a		488db424b8000000	LEAQ 0xb8(SP), SI						
  wal.go:434		0x8d4512		41b810000000		MOVL $0x10, R8							
  wal.go:434		0x8d4518		4589c1			MOVL R8, R9							
  wal.go:434		0x8d451b		0f1f440000		NOPL 0(AX)(AX*1)						
  wal.go:434		0x8d4520		e89b22fcff		CALL github.com/hazyhaar/c2pkg/c2db.Db_wal_pack(SB)		
  wal.go:434		0x8d4525		84c0			TESTL AL, AL							
  wal.go:434		0x8d4527		0f849b000000		JE 0x8d45c8							
  wal.go:437		0x8d452d		488b9424e8000000	MOVQ 0xe8(SP), DX						
  wal.go:437		0x8d4535		4883c218		ADDQ $0x18, DX							
  wal.go:437		0x8d4539		488d742460		LEAQ 0x60(SP), SI						
  wal.go:437		0x8d453e		440f1032		MOVUPS 0(DX), X14						
  wal.go:437		0x8d4542		440f1136		MOVUPS X14, 0(SI)						
  wal.go:437		0x8d4546		440f107210		MOVUPS 0x10(DX), X14						
  wal.go:437		0x8d454b		440f117610		MOVUPS X14, 0x10(SI)						
  wal.go:437		0x8d4550		4889e2			MOVQ SP, DX							
  wal.go:437		0x8d4553		440f1036		MOVUPS 0(SI), X14						
  wal.go:437		0x8d4557		440f1132		MOVUPS X14, 0(DX)						
  wal.go:437		0x8d455b		440f107610		MOVUPS 0x10(SI), X14						
  wal.go:437		0x8d4560		440f117210		MOVUPS X14, 0x10(DX)						
  wal.go:437		0x8d4565		488b842490000000	MOVQ 0x90(SP), AX						
  wal.go:437		0x8d456d		bb00100000		MOVL $0x1000, BX						
  wal.go:437		0x8d4572		488b8c2480000000	MOVQ 0x80(SP), CX						
  wal.go:437		0x8d457a		e861380000		CALL github.com/hazyhaar/c2pkg/c2db.sealWAL(SB)			
  wal.go:438		0x8d457f		488b8424e8000000	MOVQ 0xe8(SP), AX						
  wal.go:438		0x8d4587		488b90b8000000		MOVQ 0xb8(AX), DX						
  wal.go:438		0x8d458e		48ffc2			INCQ DX								
  wal.go:438		0x8d4591		488990b8000000		MOVQ DX, 0xb8(AX)						
  wal.go:439		0x8d4598		48ff4038		INCQ 0x38(AX)							
  wal.go:440		0x8d459c		48ff4040		INCQ 0x40(AX)							
  wal.go:441		0x8d45a0		c680d800000000		MOVB $0x0, 0xd8(AX)						
  wal.go:442		0x8d45a7		4883fa20		CMPQ DX, $0x20							
  wal.go:442		0x8d45ab		750e			JNE 0x8d45bb							
  wal.go:443		0x8d45ad		e8eefcffff		CALL github.com/hazyhaar/c2pkg/c2db.(*WAL).flushQuantum(SB)	
  wal.go:443		0x8d45b2		4881c4a8000000		ADDQ $0xa8, SP							
  wal.go:443		0x8d45b9		5d			POPQ BP								
  wal.go:443		0x8d45ba		c3			RET								
  wal.go:445		0x8d45bb		31c0			XORL AX, AX							
  wal.go:445		0x8d45bd		31db			XORL BX, BX							
  wal.go:445		0x8d45bf		4881c4a8000000		ADDQ $0xa8, SP							
  wal.go:445		0x8d45c6		5d			POPQ BP								
  wal.go:445		0x8d45c7		c3			RET								
  wal.go:435		0x8d45c8		488b050178a500		MOVQ github.com/hazyhaar/c2pkg/c2db.errWALTooLong(SB), AX	
  wal.go:435		0x8d45cf		488b1d0278a500		MOVQ github.com/hazyhaar/c2pkg/c2db.errWALTooLong+8(SB), BX	
  wal.go:435		0x8d45d6		4881c4a8000000		ADDQ $0xa8, SP							
  wal.go:435		0x8d45dd		5d			POPQ BP								
  wal.go:435		0x8d45de		c3			RET								
  wal.go:433		0x8d45df		90			NOPL								
  wal.go:433		0x8d45e0		e8fbf8bbff		CALL runtime.panicBounds(SB)					
  wal.go:433		0x8d45e5		e8f6f8bbff		CALL runtime.panicBounds(SB)					
  wal.go:433		0x8d45ea		90			NOPL								
  wal.go:417		0x8d45eb		4889442438		MOVQ AX, 0x38(SP)						
  wal.go:417		0x8d45f0		e84bdbbbff		CALL runtime.morestack_noctxt.abi0(SB)				
  wal.go:417		0x8d45f5		488b442438		MOVQ 0x38(SP), AX						
  wal.go:417		0x8d45fa		e961fdffff		JMP github.com/hazyhaar/c2pkg/c2db.(*WAL).writeOne(SB)		
