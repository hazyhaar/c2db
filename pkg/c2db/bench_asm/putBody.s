TEXT github.com/hazyhaar/c2pkg/c2db.(*Shard).putBody(SB) /devhoros/c2simd/c2pkg/c2db/engine.go
  engine.go:832		0x89c020		4c8da42418ffffff	LEAQ 0xffffff18(SP), R12						
  engine.go:832		0x89c028		4d3b6610		CMPQ R12, 0x10(R14)							
  engine.go:832		0x89c02c		0f86bd050000		JBE 0x89c5ef								
  engine.go:832		0x89c032		55			PUSHQ BP								
  engine.go:832		0x89c033		4889e5			MOVQ SP, BP								
  engine.go:832		0x89c036		4881ec60010000		SUBQ $0x160, SP								
  engine.go:2455	0x89c03d		4c898c24a0010000	MOVQ R9, 0x1a0(SP)							
  engine.go:2455	0x89c045		4c89842498010000	MOVQ R8, 0x198(SP)							
  engine.go:2455	0x89c04d		4889b42490010000	MOVQ SI, 0x190(SP)							
  engine.go:2455	0x89c055		48898c2480010000	MOVQ CX, 0x180(SP)							
  engine.go:2455	0x89c05d		48899c2478010000	MOVQ BX, 0x178(SP)							
  engine.go:2455	0x89c065		4889bc2488010000	MOVQ DI, 0x188(SP)							
  engine.go:2455	0x89c06d		488b5010		MOVQ 0x10(AX), DX							
  engine.go:833		0x89c071		90			NOPL									
  engine.go:2455	0x89c072		4885d2			TESTQ DX, DX								
  engine.go:2455	0x89c075		7410			JE 0x89c087								
  engine.go:2455	0x89c077		48837a7000		CMPQ 0x70(DX), $0x0							
  engine.go:2455	0x89c07c		7509			JNE 0x89c087								
  engine.go:2456	0x89c07e		90			NOPL									
  wal.go:144		0x89c07f		48c7427001000000	MOVQ $0x1, 0x70(DX)							
  engine.go:2455	0x89c087		4889842470010000	MOVQ AX, 0x170(SP)							
  engine.go:834		0x89c08f		e88c030100		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).preparePublish(SB)		
  engine.go:834		0x89c094		4885c0			TESTQ AX, AX								
  engine.go:834		0x89c097		0f8581010000		JNE 0x89c21e								
  engine.go:837		0x89c09d		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:837		0x89c0a5		488b5048		MOVQ 0x48(AX), DX							
  engine.go:837		0x89c0a9		4889542478		MOVQ DX, 0x78(SP)							
  engine.go:838		0x89c0ae		488b5050		MOVQ 0x50(AX), DX							
  engine.go:838		0x89c0b2		4889542470		MOVQ DX, 0x70(SP)							
  engine.go:845		0x89c0b7		488b9c2490010000	MOVQ 0x190(SP), BX							
  engine.go:845		0x89c0bf		488b8c2498010000	MOVQ 0x198(SP), CX							
  engine.go:845		0x89c0c7		488bbc24a0010000	MOVQ 0x1a0(SP), DI							
  engine.go:845		0x89c0cf		e82c670100		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).encodeValue(SB)		
  engine.go:846		0x89c0d4		4d85c9			TESTQ R9, R9								
  engine.go:846		0x89c0d7		0f85f4000000		JNE 0x89c1d1								
  engine.go:845		0x89c0dd		48898c24b8000000	MOVQ CX, 0xb8(SP)							
  engine.go:845		0x89c0e5		48899c24b0000000	MOVQ BX, 0xb0(SP)							
  engine.go:845		0x89c0ed		4889842428010000	MOVQ AX, 0x128(SP)							
  engine.go:845		0x89c0f5		4084ff			TESTL DI, DI								
  engine.go:849		0x89c0f8		0f8429010000		JE 0x89c227								
  engine.go:850		0x89c0fe		488b942470010000	MOVQ 0x170(SP), DX							
  engine.go:850		0x89c106		4c8b4a08		MOVQ 0x8(DX), R9							
  engine.go:850		0x89c10a		4d85c9			TESTQ R9, R9								
  engine.go:850		0x89c10d		0f84ae000000		JE 0x89c1c1								
  engine.go:850		0x89c113		4d8d5010		LEAQ 0x10(R8), R10							
  engine.go:850		0x89c117		660f1f840000000000	NOPW 0(AX)(AX*1)							
  engine.go:850		0x89c120		4d395110		CMPQ 0x10(R9), R10							
  engine.go:850		0x89c124		0f8d97000000		JGE 0x89c1c1								
  engine.go:845		0x89c12a		4889b42480000000	MOVQ SI, 0x80(SP)							
  engine.go:845		0x89c132		4c89442468		MOVQ R8, 0x68(SP)							
  engine.go:851		0x89c137		4c89c8			MOVQ R9, AX								
  engine.go:851		0x89c13a		4c89d3			MOVQ R10, BX								
  engine.go:851		0x89c13d		0f1f00			NOPL 0(AX)								
  engine.go:851		0x89c140		e89b930100		CALL github.com/hazyhaar/c2pkg/c2db.(*Pager).Reserve(SB)		
  engine.go:851		0x89c145		4885c0			TESTQ AX, AX								
  engine.go:851		0x89c148		752f			JNE 0x89c179								
  engine.go:865		0x89c14a		488b842428010000	MOVQ 0x128(SP), AX							
  engine.go:865		0x89c152		488b8c24b8000000	MOVQ 0xb8(SP), CX							
  engine.go:857		0x89c15a		488b942470010000	MOVQ 0x170(SP), DX							
  engine.go:865		0x89c162		488b9c24b0000000	MOVQ 0xb0(SP), BX							
  engine.go:855		0x89c16a		488bb42480000000	MOVQ 0x80(SP), SI							
  engine.go:855		0x89c172		4c8b442468		MOVQ 0x68(SP), R8							
  engine.go:851		0x89c177		eb48			JMP 0x89c1c1								
  engine.go:851		0x89c179		48899c2418010000	MOVQ BX, 0x118(SP)							
  engine.go:851		0x89c181		48898424a0000000	MOVQ AX, 0xa0(SP)							
  engine.go:840		0x89c189		488b4c2478		MOVQ 0x78(SP), CX							
  engine.go:840		0x89c18e		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:840		0x89c196		48894848		MOVQ CX, 0x48(AX)							
  engine.go:841		0x89c19a		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:841		0x89c19f		48894850		MOVQ CX, 0x50(AX)							
  engine.go:842		0x89c1a3		e818e20000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:852		0x89c1a8		488b8424a0000000	MOVQ 0xa0(SP), AX							
  engine.go:852		0x89c1b0		488b9c2418010000	MOVQ 0x118(SP), BX							
  engine.go:852		0x89c1b8		4881c460010000		ADDQ $0x160, SP								
  engine.go:852		0x89c1bf		5d			POPQ BP									
  engine.go:852		0x89c1c0		c3			RET									
  engine.go:855		0x89c1c1		4901f0			ADDQ SI, R8								
  engine.go:855		0x89c1c4		4c898424c8000000	MOVQ R8, 0xc8(SP)							
  engine.go:855		0x89c1cc		e9ef020000		JMP 0x89c4c0								
  engine.go:845		0x89c1d1		4c89942420010000	MOVQ R10, 0x120(SP)							
  engine.go:845		0x89c1d9		4c898c24a8000000	MOVQ R9, 0xa8(SP)							
  engine.go:840		0x89c1e1		488b4c2478		MOVQ 0x78(SP), CX							
  engine.go:840		0x89c1e6		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:840		0x89c1ee		48894848		MOVQ CX, 0x48(AX)							
  engine.go:841		0x89c1f2		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:841		0x89c1f7		48894850		MOVQ CX, 0x50(AX)							
  engine.go:842		0x89c1fb		0f1f440000		NOPL 0(AX)(AX*1)							
  engine.go:842		0x89c200		e8bbe10000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:847		0x89c205		488b8424a8000000	MOVQ 0xa8(SP), AX							
  engine.go:847		0x89c20d		488b9c2420010000	MOVQ 0x120(SP), BX							
  engine.go:847		0x89c215		4881c460010000		ADDQ $0x160, SP								
  engine.go:847		0x89c21c		5d			POPQ BP									
  engine.go:847		0x89c21d		c3			RET									
  engine.go:835		0x89c21e		4881c460010000		ADDQ $0x160, SP								
  engine.go:835		0x89c225		5d			POPQ BP									
  engine.go:835		0x89c226		c3			RET									
  engine.go:865		0x89c227		488d942430010000	LEAQ 0x130(SP), DX							
  engine.go:865		0x89c22f		440f113a		MOVUPS X15, 0(DX)							
  engine.go:865		0x89c233		440f117a10		MOVUPS X15, 0x10(DX)							
  engine.go:865		0x89c238		440f117a20		MOVUPS X15, 0x20(DX)							
  engine.go:865		0x89c23d		488bbc2488010000	MOVQ 0x188(SP), DI							
  engine.go:865		0x89c245		4889c6			MOVQ AX, SI								
  engine.go:865		0x89c248		4989d8			MOVQ BX, R8								
  engine.go:865		0x89c24b		4989c9			MOVQ CX, R9								
  engine.go:865		0x89c24e		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:865		0x89c256		488b9c2478010000	MOVQ 0x178(SP), BX							
  engine.go:865		0x89c25e		488b8c2480010000	MOVQ 0x180(SP), CX							
  engine.go:865		0x89c266		e895560000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).journalPut(SB)		
  engine.go:865		0x89c26b		488d942430010000	LEAQ 0x130(SP), DX							
  engine.go:865		0x89c273		4989e2			MOVQ SP, R10								
  engine.go:865		0x89c276		450f1032		MOVUPS 0(R10), X14							
  engine.go:865		0x89c27a		440f1132		MOVUPS X14, 0(DX)							
  engine.go:865		0x89c27e		450f107210		MOVUPS 0x10(R10), X14							
  engine.go:865		0x89c283		440f117210		MOVUPS X14, 0x10(DX)							
  engine.go:865		0x89c288		450f107220		MOVUPS 0x20(R10), X14							
  engine.go:865		0x89c28d		440f117220		MOVUPS X14, 0x20(DX)							
  engine.go:865		0x89c292		4c8d9c24d0000000	LEAQ 0xd0(SP), R11							
  engine.go:865		0x89c29a		440f1032		MOVUPS 0(DX), X14							
  engine.go:865		0x89c29e		450f1133		MOVUPS X14, 0(R11)							
  engine.go:865		0x89c2a2		440f107210		MOVUPS 0x10(DX), X14							
  engine.go:865		0x89c2a7		450f117310		MOVUPS X14, 0x10(R11)							
  engine.go:865		0x89c2ac		440f107220		MOVUPS 0x20(DX), X14							
  engine.go:865		0x89c2b1		450f117320		MOVUPS X14, 0x20(R11)							
  engine.go:866		0x89c2b6		4885c0			TESTQ AX, AX								
  engine.go:866		0x89c2b9		0f857f010000		JNE 0x89c43e								
  engine.go:869		0x89c2bf		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:869		0x89c2c7		488b5818		MOVQ 0x18(AX), BX							
  engine.go:869		0x89c2cb		488b4820		MOVQ 0x20(AX), CX							
  engine.go:869		0x89c2cf		488b7828		MOVQ 0x28(AX), DI							
  engine.go:869		0x89c2d3		488d9424d0000000	LEAQ 0xd0(SP), DX							
  engine.go:869		0x89c2db		48891424		MOVQ DX, 0(SP)								
  engine.go:869		0x89c2df		48c744240810000000	MOVQ $0x10, 0x8(SP)							
  engine.go:869		0x89c2e8		48c744241010000000	MOVQ $0x10, 0x10(SP)							
  engine.go:869		0x89c2f1		488b942428010000	MOVQ 0x128(SP), DX							
  engine.go:869		0x89c2f9		4889542418		MOVQ DX, 0x18(SP)							
  engine.go:869		0x89c2fe		488b9424b0000000	MOVQ 0xb0(SP), DX							
  engine.go:869		0x89c306		4889542420		MOVQ DX, 0x20(SP)							
  engine.go:869		0x89c30b		488b9424b8000000	MOVQ 0xb8(SP), DX							
  engine.go:869		0x89c313		4889542428		MOVQ DX, 0x28(SP)							
  engine.go:869		0x89c318		488bb42478010000	MOVQ 0x178(SP), SI							
  engine.go:869		0x89c320		4c8b842480010000	MOVQ 0x180(SP), R8							
  engine.go:869		0x89c328		4c8b8c2488010000	MOVQ 0x188(SP), R9							
  engine.go:869		0x89c330		e8ebef0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).applyInsertVer(SB)		
  engine.go:869		0x89c335		4885c0			TESTQ AX, AX								
  engine.go:869		0x89c338		0f85b4000000		JNE 0x89c3f2								
  engine.go:872		0x89c33e		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:872		0x89c346		e835030100		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).publish(SB)		
  engine.go:872		0x89c34b		4885c0			TESTQ AX, AX								
  engine.go:872		0x89c34e		7409			JE 0x89c359								
  engine.go:873		0x89c350		4881c460010000		ADDQ $0x160, SP								
  engine.go:873		0x89c357		5d			POPQ BP									
  engine.go:873		0x89c358		c3			RET									
  engine.go:875		0x89c359		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:875		0x89c361		0fb75868		MOVZX 0x68(AX), BX							
  engine.go:875		0x89c365		b901000000		MOVL $0x1, CX								
  engine.go:875		0x89c36a		488bbc2478010000	MOVQ 0x178(SP), DI							
  engine.go:875		0x89c372		488bb42480010000	MOVQ 0x180(SP), SI							
  engine.go:875		0x89c37a		4c8b842488010000	MOVQ 0x188(SP), R8							
  engine.go:875		0x89c382		4c8b8c2490010000	MOVQ 0x190(SP), R9							
  engine.go:875		0x89c38a		4c8b942498010000	MOVQ 0x198(SP), R10							
  engine.go:875		0x89c392		4c8b9c24a0010000	MOVQ 0x1a0(SP), R11							
  engine.go:875		0x89c39a		e821c50100		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).replicate(SB)		
  engine.go:876		0x89c39f		0f108424d0000000	MOVUPS 0xd0(SP), X0							
  engine.go:876		0x89c3a7		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:876		0x89c3af		4c8b5870		MOVQ 0x70(AX), R11							
  engine.go:876		0x89c3b3		0f110424		MOVUPS X0, 0(SP)							
  engine.go:876		0x89c3b7		bb01000000		MOVL $0x1, BX								
  engine.go:876		0x89c3bc		488b8c2478010000	MOVQ 0x178(SP), CX							
  engine.go:876		0x89c3c4		488bbc2480010000	MOVQ 0x180(SP), DI							
  engine.go:876		0x89c3cc		488bb42488010000	MOVQ 0x188(SP), SI							
  engine.go:876		0x89c3d4		4531c0			XORL R8, R8								
  engine.go:876		0x89c3d7		4531c9			XORL R9, R9								
  engine.go:876		0x89c3da		4589ca			MOVL R9, R10								
  engine.go:876		0x89c3dd		0f1f00			NOPL 0(AX)								
  engine.go:876		0x89c3e0		e8db040000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).emitEvent(SB)		
  engine.go:877		0x89c3e5		31c0			XORL AX, AX								
  engine.go:877		0x89c3e7		31db			XORL BX, BX								
  engine.go:877		0x89c3e9		4881c460010000		ADDQ $0x160, SP								
  engine.go:877		0x89c3f0		5d			POPQ BP									
  engine.go:877		0x89c3f1		c3			RET									
  engine.go:869		0x89c3f2		48899c2400010000	MOVQ BX, 0x100(SP)							
  engine.go:869		0x89c3fa		4889842488000000	MOVQ AX, 0x88(SP)							
  engine.go:840		0x89c402		488b4c2478		MOVQ 0x78(SP), CX							
  engine.go:840		0x89c407		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:840		0x89c40f		48894848		MOVQ CX, 0x48(AX)							
  engine.go:841		0x89c413		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:841		0x89c418		48894850		MOVQ CX, 0x50(AX)							
  engine.go:842		0x89c41c		0f1f4000		NOPL 0(AX)								
  engine.go:842		0x89c420		e89bdf0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:870		0x89c425		488b842488000000	MOVQ 0x88(SP), AX							
  engine.go:870		0x89c42d		488b9c2400010000	MOVQ 0x100(SP), BX							
  engine.go:870		0x89c435		4881c460010000		ADDQ $0x160, SP								
  engine.go:870		0x89c43c		5d			POPQ BP									
  engine.go:870		0x89c43d		c3			RET									
  engine.go:865		0x89c43e		48899c2420010000	MOVQ BX, 0x120(SP)							
  engine.go:865		0x89c446		48898424a8000000	MOVQ AX, 0xa8(SP)							
  engine.go:840		0x89c44e		488b4c2478		MOVQ 0x78(SP), CX							
  engine.go:840		0x89c453		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:840		0x89c45b		48894848		MOVQ CX, 0x48(AX)							
  engine.go:841		0x89c45f		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:841		0x89c464		48894850		MOVQ CX, 0x50(AX)							
  engine.go:842		0x89c468		e853df0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:867		0x89c46d		488b8424a8000000	MOVQ 0xa8(SP), AX							
  engine.go:867		0x89c475		488b9c2420010000	MOVQ 0x120(SP), BX							
  engine.go:867		0x89c47d		4881c460010000		ADDQ $0x160, SP								
  engine.go:867		0x89c484		5d			POPQ BP									
  engine.go:867		0x89c485		c3			RET									
  engine.go:855		0x89c486		488bb424c0000000	MOVQ 0xc0(SP), SI							
  engine.go:855		0x89c48e		48ffc6			INCQ SI									
  engine.go:865		0x89c491		488b842428010000	MOVQ 0x128(SP), AX							
  engine.go:865		0x89c499		488b8c24b8000000	MOVQ 0xb8(SP), CX							
  engine.go:857		0x89c4a1		488b942470010000	MOVQ 0x170(SP), DX							
  engine.go:865		0x89c4a9		488b9c24b0000000	MOVQ 0xb0(SP), BX							
  engine.go:855		0x89c4b1		4c8b8424c8000000	MOVQ 0xc8(SP), R8							
  engine.go:855		0x89c4b9		0f1f8000000000		NOPL 0(AX)								
  engine.go:855		0x89c4c0		4c39c6			CMPQ SI, R8								
  engine.go:855		0x89c4c3		0f83a8000000		JAE 0x89c571								
  engine.go:856		0x89c4c9		4989f1			MOVQ SI, R9								
  engine.go:856		0x89c4cc		48c1e60e		SHLQ $0xe, SI								
  engine.go:857		0x89c4d0		4c8b5240		MOVQ 0x40(DX), R10							
  engine.go:857		0x89c4d4		4c8d9e00400000		LEAQ 0x4000(SI), R11							
  engine.go:857		0x89c4db		0f1f440000		NOPL 0(AX)(AX*1)							
  engine.go:857		0x89c4e0		4d39da			CMPQ R10, R11								
  engine.go:857		0x89c4e3		0f8200010000		JB 0x89c5e9								
  engine.go:857		0x89c4e9		4c39de			CMPQ SI, R11								
  engine.go:857		0x89c4ec		0f87f2000000		JA 0x89c5e4								
  engine.go:855		0x89c4f2		4c898c24c0000000	MOVQ R9, 0xc0(SP)							
  engine.go:857		0x89c4fa		488b4208		MOVQ 0x8(DX), AX							
  engine.go:857		0x89c4fe		4c89cb			MOVQ R9, BX								
  engine.go:857		0x89c501		48c1e302		SHLQ $0x2, BX								
  engine.go:857		0x89c505		488b5230		MOVQ 0x30(DX), DX							
  engine.go:857		0x89c509		4929f2			SUBQ SI, R10								
  engine.go:857		0x89c50c		488d0c32		LEAQ 0(DX)(SI*1), CX							
  engine.go:857		0x89c510		bf00400000		MOVL $0x4000, DI							
  engine.go:857		0x89c515		4c89d6			MOVQ R10, SI								
  engine.go:857		0x89c518		e883870100		CALL github.com/hazyhaar/c2pkg/c2db.(*Pager).PutPage(SB)		
  engine.go:857		0x89c51d		0f1f00			NOPL 0(AX)								
  engine.go:857		0x89c520		4885c0			TESTQ AX, AX								
  engine.go:857		0x89c523		0f845dffffff		JE 0x89c486								
  engine.go:857		0x89c529		48899c2410010000	MOVQ BX, 0x110(SP)							
  engine.go:857		0x89c531		4889842498000000	MOVQ AX, 0x98(SP)							
  engine.go:840		0x89c539		488b4c2478		MOVQ 0x78(SP), CX							
  engine.go:840		0x89c53e		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:840		0x89c546		48894848		MOVQ CX, 0x48(AX)							
  engine.go:841		0x89c54a		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:841		0x89c54f		48894850		MOVQ CX, 0x50(AX)							
  engine.go:842		0x89c553		e868de0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:858		0x89c558		488b842498000000	MOVQ 0x98(SP), AX							
  engine.go:858		0x89c560		488b9c2410010000	MOVQ 0x110(SP), BX							
  engine.go:858		0x89c568		4881c460010000		ADDQ $0x160, SP								
  engine.go:858		0x89c56f		5d			POPQ BP									
  engine.go:858		0x89c570		c3			RET									
  engine.go:861		0x89c571		488b4208		MOVQ 0x8(DX), AX							
  engine.go:861		0x89c575		e886890100		CALL github.com/hazyhaar/c2pkg/c2db.(*Pager).FlushDirty(SB)		
  engine.go:861		0x89c57a		4885db			TESTQ BX, BX								
  engine.go:861		0x89c57d		751d			JNE 0x89c59c								
  engine.go:865		0x89c57f		488b842428010000	MOVQ 0x128(SP), AX							
  engine.go:865		0x89c587		488b8c24b8000000	MOVQ 0xb8(SP), CX							
  engine.go:865		0x89c58f		488b9c24b0000000	MOVQ 0xb0(SP), BX							
  engine.go:861		0x89c597		e98bfcffff		JMP 0x89c227								
  engine.go:861		0x89c59c		48898c2408010000	MOVQ CX, 0x108(SP)							
  engine.go:861		0x89c5a4		48899c2490000000	MOVQ BX, 0x90(SP)							
  engine.go:840		0x89c5ac		488b4c2478		MOVQ 0x78(SP), CX							
  engine.go:840		0x89c5b1		488b842470010000	MOVQ 0x170(SP), AX							
  engine.go:840		0x89c5b9		48894848		MOVQ CX, 0x48(AX)							
  engine.go:841		0x89c5bd		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:841		0x89c5c2		48894850		MOVQ CX, 0x50(AX)							
  engine.go:842		0x89c5c6		e8f5dd0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:862		0x89c5cb		488b842490000000	MOVQ 0x90(SP), AX							
  engine.go:862		0x89c5d3		488b9c2408010000	MOVQ 0x108(SP), BX							
  engine.go:862		0x89c5db		4881c460010000		ADDQ $0x160, SP								
  engine.go:862		0x89c5e2		5d			POPQ BP									
  engine.go:862		0x89c5e3		c3			RET									
  engine.go:857		0x89c5e4		e8f778bfff		CALL runtime.panicBounds(SB)						
  engine.go:857		0x89c5e9		e8f278bfff		CALL runtime.panicBounds(SB)						
  engine.go:857		0x89c5ee		90			NOPL									
  engine.go:832		0x89c5ef		4889442408		MOVQ AX, 0x8(SP)							
  engine.go:832		0x89c5f4		48895c2410		MOVQ BX, 0x10(SP)							
  engine.go:832		0x89c5f9		48894c2418		MOVQ CX, 0x18(SP)							
  engine.go:832		0x89c5fe		48897c2420		MOVQ DI, 0x20(SP)							
  engine.go:832		0x89c603		4889742428		MOVQ SI, 0x28(SP)							
  engine.go:832		0x89c608		4c89442430		MOVQ R8, 0x30(SP)							
  engine.go:832		0x89c60d		4c894c2438		MOVQ R9, 0x38(SP)							
  engine.go:832		0x89c612		e8295bbfff		CALL runtime.morestack_noctxt.abi0(SB)					
  engine.go:832		0x89c617		488b442408		MOVQ 0x8(SP), AX							
  engine.go:832		0x89c61c		488b5c2410		MOVQ 0x10(SP), BX							
  engine.go:832		0x89c621		488b4c2418		MOVQ 0x18(SP), CX							
  engine.go:832		0x89c626		488b7c2420		MOVQ 0x20(SP), DI							
  engine.go:832		0x89c62b		488b742428		MOVQ 0x28(SP), SI							
  engine.go:832		0x89c630		4c8b442430		MOVQ 0x30(SP), R8							
  engine.go:832		0x89c635		4c8b4c2438		MOVQ 0x38(SP), R9							
  engine.go:832		0x89c63a		e9e1f9ffff		JMP github.com/hazyhaar/c2pkg/c2db.(*Shard).putBody(SB)			
