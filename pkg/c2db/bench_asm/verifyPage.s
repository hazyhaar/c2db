TEXT github.com/hazyhaar/c2pkg/c2db.(*Pager).verifyPage(SB) /devhoros/c2simd/c2pkg/c2db/pager_seal.go
  pager_seal.go:33	0x8b6120		4c8d642488		LEAQ -0x78(SP), R12						
  pager_seal.go:33	0x8b6125		4d3b6610		CMPQ R12, 0x10(R14)						
  pager_seal.go:33	0x8b6129		0f8669020000		JBE 0x8b6398							
  pager_seal.go:33	0x8b612f		55			PUSHQ BP							
  pager_seal.go:33	0x8b6130		4889e5			MOVQ SP, BP							
  pager_seal.go:33	0x8b6133		4881ecf0000000		SUBQ $0xf0, SP							
  pager_seal.go:33	0x8b613a		48898c2410010000	MOVQ CX, 0x110(SP)						
  pager_seal.go:34	0x8b6142		4885c0			TESTQ AX, AX							
  pager_seal.go:34	0x8b6145		747d			JE 0x8b61c4							
  pager_seal.go:34	0x8b6147		80b8b200000000		CMPB 0xb2(AX), $0x0						
  pager_seal.go:34	0x8b614e		7474			JE 0x8b61c4							
  pager_seal.go:34	0x8b6150		4883b88800000000	CMPQ 0x88(AX), $0x0						
  pager_seal.go:34	0x8b6158		746a			JE 0x8b61c4							
  pager_seal.go:34	0x8b615a		4889842400010000	MOVQ AX, 0x100(SP)						
  pager_seal.go:34	0x8b6162		48898c2410010000	MOVQ CX, 0x110(SP)						
  pager_seal.go:34	0x8b616a		4889b42420010000	MOVQ SI, 0x120(SP)						
  pager_seal.go:34	0x8b6172		4889bc2418010000	MOVQ DI, 0x118(SP)						
  pager_seal.go:30	0x8b617a		48c1eb02		SHRQ $0x2, BX							
  pager_seal.go:30	0x8b617e		48899c24e8000000	MOVQ BX, 0xe8(SP)						
  pager_seal.go:37	0x8b6186		90			NOPL								
  pager_seal.go:38	0x8b6187		e8f4050000		CALL github.com/hazyhaar/c2pkg/c2db.(*Pager).readTag(SB)	
  pager_seal.go:38	0x8b618c		0f100424		MOVUPS 0(SP), X0						
  pager_seal.go:38	0x8b6190		0f118424d8000000	MOVUPS X0, 0xd8(SP)						
  pager_seal.go:38	0x8b6198		0f108424d8000000	MOVUPS 0xd8(SP), X0						
  pager_seal.go:38	0x8b61a0		0f11442478		MOVUPS X0, 0x78(SP)						
  pager_seal.go:39	0x8b61a5		4885c0			TESTQ AX, AX							
  pager_seal.go:39	0x8b61a8		7511			JNE 0x8b61bb							
  pager_seal.go:42	0x8b61aa		488d542468		LEAQ 0x68(SP), DX						
  pager_seal.go:42	0x8b61af		440f113a		MOVUPS X15, 0(DX)						
  constant_time.go:22	0x8b61b3		90			NOPL								
  constant_time.go:18	0x8b61b4		31d2			XORL DX, DX							
  constant_time.go:18	0x8b61b6		4531c0			XORL R8, R8							
  constant_time.go:18	0x8b61b9		eb2b			JMP 0x8b61e6							
  pager_seal.go:40	0x8b61bb		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:40	0x8b61c2		5d			POPQ BP								
  pager_seal.go:40	0x8b61c3		c3			RET								
  pager_seal.go:35	0x8b61c4		31c0			XORL AX, AX							
  pager_seal.go:35	0x8b61c6		31db			XORL BX, BX							
  pager_seal.go:35	0x8b61c8		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:35	0x8b61cf		5d			POPQ BP								
  pager_seal.go:35	0x8b61d0		c3			RET								
  constant_time.go:25	0x8b61d1		440fb64c1478		MOVZX 0x78(SP)(DX*1), R9					
  constant_time.go:25	0x8b61d7		440fb6541468		MOVZX 0x68(SP)(DX*1), R10					
  constant_time.go:25	0x8b61dd		4531ca			XORL R9, R10							
  constant_time.go:25	0x8b61e0		4509d0			ORL R10, R8							
  constant_time.go:24	0x8b61e3		48ffc2			INCQ DX								
  constant_time.go:24	0x8b61e6		4883fa10		CMPQ DX, $0x10							
  constant_time.go:24	0x8b61ea		7ce5			JL 0x8b61d1							
  constant_time.go:28	0x8b61ec		90			NOPL								
  constant_time.go:24	0x8b61ed		4584c0			TESTL R8, R8							
  pager_seal.go:43	0x8b61f0		0f84da000000		JE 0x8b62d0							
  pager_seal.go:56	0x8b61f6		488b942400010000	MOVQ 0x100(SP), DX						
  pager_seal.go:56	0x8b61fe		4c8d4260		LEAQ 0x60(DX), R8						
  pager_seal.go:56	0x8b6202		4c8d8c24b8000000	LEAQ 0xb8(SP), R9						
  pager_seal.go:56	0x8b620a		450f1030		MOVUPS 0(R8), X14						
  pager_seal.go:56	0x8b620e		450f1131		MOVUPS X14, 0(R9)						
  pager_seal.go:56	0x8b6212		450f107010		MOVUPS 0x10(R8), X14						
  pager_seal.go:56	0x8b6217		450f117110		MOVUPS X14, 0x10(R9)						
  pager_seal.go:56	0x8b621c		0fb78280000000		MOVZX 0x80(DX), AX						
  pager_seal.go:56	0x8b6223		4889e2			MOVQ SP, DX							
  pager_seal.go:56	0x8b6226		450f1031		MOVUPS 0(R9), X14						
  pager_seal.go:56	0x8b622a		440f1132		MOVUPS X14, 0(DX)						
  pager_seal.go:56	0x8b622e		450f107110		MOVUPS 0x10(R9), X14						
  pager_seal.go:56	0x8b6233		440f117210		MOVUPS X14, 0x10(DX)						
  pager_seal.go:56	0x8b6238		488b9c24e8000000	MOVQ 0xe8(SP), BX						
  pager_seal.go:56	0x8b6240		488b8c2410010000	MOVQ 0x110(SP), CX						
  pager_seal.go:56	0x8b6248		488bbc2418010000	MOVQ 0x118(SP), DI						
  pager_seal.go:56	0x8b6250		488bb42420010000	MOVQ 0x120(SP), SI						
  pager_seal.go:56	0x8b6258		e8e3abffff		CALL github.com/hazyhaar/c2pkg/c2db.DerivePageSealKey(SB)	
  pager_seal.go:56	0x8b625d		488d942498000000	LEAQ 0x98(SP), DX						
  pager_seal.go:56	0x8b6265		4c8d442420		LEAQ 0x20(SP), R8						
  pager_seal.go:56	0x8b626a		450f1030		MOVUPS 0(R8), X14						
  pager_seal.go:56	0x8b626e		440f1132		MOVUPS X14, 0(DX)						
  pager_seal.go:56	0x8b6272		450f107010		MOVUPS 0x10(R8), X14						
  pager_seal.go:56	0x8b6277		440f117210		MOVUPS X14, 0x10(DX)						
  pager_seal.go:57	0x8b627c		488d842488000000	LEAQ 0x88(SP), AX						
  pager_seal.go:57	0x8b6284		440f1138		MOVUPS X15, 0(AX)						
  pager_seal.go:58	0x8b6288		48891424		MOVQ DX, 0(SP)							
  pager_seal.go:58	0x8b628c		48c744240820000000	MOVQ $0x20, 0x8(SP)						
  pager_seal.go:58	0x8b6295		48c744241020000000	MOVQ $0x20, 0x10(SP)						
  pager_seal.go:58	0x8b629e		bb10000000		MOVL $0x10, BX							
  pager_seal.go:58	0x8b62a3		89d9			MOVL BX, CX							
  pager_seal.go:58	0x8b62a5		488bbc2410010000	MOVQ 0x110(SP), DI						
  pager_seal.go:58	0x8b62ad		488bb42418010000	MOVQ 0x118(SP), SI						
  pager_seal.go:58	0x8b62b5		4c8b842420010000	MOVQ 0x120(SP), R8						
  pager_seal.go:58	0x8b62bd		4989f1			MOVQ SI, R9							
  pager_seal.go:58	0x8b62c0		e8fb5dd7ff		CALL github.com/hazyhaar/c2pkg/c2poly1305.Crypto_poly1305(SB)	
  constant_time.go:22	0x8b62c5		90			NOPL								
  constant_time.go:18	0x8b62c6		31d2			XORL DX, DX							
  constant_time.go:18	0x8b62c8		4531c0			XORL R8, R8							
  constant_time.go:18	0x8b62cb		e999000000		JMP 0x8b6369							
  pager_seal.go:46	0x8b62d0		488b8c24e8000000	MOVQ 0xe8(SP), CX						
  pager_seal.go:46	0x8b62d8		4885c9			TESTQ CX, CX							
  pager_seal.go:46	0x8b62db		7455			JE 0x8b6332							
  pager_seal.go:49	0x8b62dd		488b8c2418010000	MOVQ 0x118(SP), CX						
  pager_seal.go:49	0x8b62e5		4883f914		CMPQ CX, $0x14							
  pager_seal.go:49	0x8b62e9		7663			JBE 0x8b634e							
  pager_seal.go:50	0x8b62eb		488b8c2420010000	MOVQ 0x120(SP), CX						
  pager_seal.go:50	0x8b62f3		4883f918		CMPQ CX, $0x18							
  pager_seal.go:50	0x8b62f7		7250			JB 0x8b6349							
  pager_seal.go:49	0x8b62f9		488b8c2410010000	MOVQ 0x110(SP), CX						
  pager_seal.go:49	0x8b6301		80791400		CMPB 0x14(CX), $0x0						
  pager_seal.go:51	0x8b6305		7514			JNE 0x8b631b							
  binary.go:71		0x8b6307		6683791600		CMPW 0x16(CX), $0x0						
  pager_seal.go:51	0x8b630c		750d			JNE 0x8b631b							
  pager_seal.go:52	0x8b630e		31c0			XORL AX, AX							
  pager_seal.go:52	0x8b6310		31db			XORL BX, BX							
  pager_seal.go:52	0x8b6312		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:52	0x8b6319		5d			POPQ BP								
  pager_seal.go:52	0x8b631a		c3			RET								
  pager_seal.go:54	0x8b631b		488b05ce57a700		MOVQ github.com/hazyhaar/c2pkg/c2db.errPageSeal(SB), AX		
  pager_seal.go:54	0x8b6322		488b1dcf57a700		MOVQ github.com/hazyhaar/c2pkg/c2db.errPageSeal+8(SB), BX	
  pager_seal.go:54	0x8b6329		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:54	0x8b6330		5d			POPQ BP								
  pager_seal.go:54	0x8b6331		c3			RET								
  pager_seal.go:47	0x8b6332		488b05b757a700		MOVQ github.com/hazyhaar/c2pkg/c2db.errPageSeal(SB), AX		
  pager_seal.go:47	0x8b6339		488b1db857a700		MOVQ github.com/hazyhaar/c2pkg/c2db.errPageSeal+8(SB), BX	
  pager_seal.go:47	0x8b6340		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:47	0x8b6347		5d			POPQ BP								
  pager_seal.go:47	0x8b6348		c3			RET								
  pager_seal.go:50	0x8b6349		e892dbbdff		CALL runtime.panicBounds(SB)					
  pager_seal.go:49	0x8b634e		e88ddbbdff		CALL runtime.panicBounds(SB)					
  constant_time.go:25	0x8b6353		420fb68c0488000000	MOVZX 0x88(SP)(R8*1), CX					
  constant_time.go:25	0x8b635c		420fb6740478		MOVZX 0x78(SP)(R8*1), SI					
  constant_time.go:25	0x8b6362		31ce			XORL CX, SI							
  constant_time.go:25	0x8b6364		09f2			ORL SI, DX							
  constant_time.go:24	0x8b6366		49ffc0			INCQ R8								
  constant_time.go:24	0x8b6369		4983f810		CMPQ R8, $0x10							
  constant_time.go:24	0x8b636d		7ce4			JL 0x8b6353							
  constant_time.go:28	0x8b636f		90			NOPL								
  constant_time.go:24	0x8b6370		84d2			TESTL DL, DL							
  pager_seal.go:59	0x8b6372		750d			JNE 0x8b6381							
  pager_seal.go:62	0x8b6374		31c0			XORL AX, AX							
  pager_seal.go:62	0x8b6376		31db			XORL BX, BX							
  pager_seal.go:62	0x8b6378		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:62	0x8b637f		5d			POPQ BP								
  pager_seal.go:62	0x8b6380		c3			RET								
  pager_seal.go:60	0x8b6381		488b056857a700		MOVQ github.com/hazyhaar/c2pkg/c2db.errPageSeal(SB), AX		
  pager_seal.go:60	0x8b6388		488b1d6957a700		MOVQ github.com/hazyhaar/c2pkg/c2db.errPageSeal+8(SB), BX	
  pager_seal.go:60	0x8b638f		4881c4f0000000		ADDQ $0xf0, SP							
  pager_seal.go:60	0x8b6396		5d			POPQ BP								
  pager_seal.go:60	0x8b6397		c3			RET								
  pager_seal.go:33	0x8b6398		4889442408		MOVQ AX, 0x8(SP)						
  pager_seal.go:33	0x8b639d		48895c2410		MOVQ BX, 0x10(SP)						
  pager_seal.go:33	0x8b63a2		48894c2418		MOVQ CX, 0x18(SP)						
  pager_seal.go:33	0x8b63a7		48897c2420		MOVQ DI, 0x20(SP)						
  pager_seal.go:33	0x8b63ac		4889742428		MOVQ SI, 0x28(SP)						
  pager_seal.go:33	0x8b63b1		e88abdbdff		CALL runtime.morestack_noctxt.abi0(SB)				
  pager_seal.go:33	0x8b63b6		488b442408		MOVQ 0x8(SP), AX						
  pager_seal.go:33	0x8b63bb		488b5c2410		MOVQ 0x10(SP), BX						
  pager_seal.go:33	0x8b63c0		488b4c2418		MOVQ 0x18(SP), CX						
  pager_seal.go:33	0x8b63c5		488b7c2420		MOVQ 0x20(SP), DI						
  pager_seal.go:33	0x8b63ca		488b742428		MOVQ 0x28(SP), SI						
  pager_seal.go:33	0x8b63cf		e94cfdffff		JMP github.com/hazyhaar/c2pkg/c2db.(*Pager).verifyPage(SB)	
