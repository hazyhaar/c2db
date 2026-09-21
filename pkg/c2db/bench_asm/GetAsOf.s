TEXT github.com/hazyhaar/c2pkg/c2db.(*Shard).GetAsOf(SB) /devhoros/c2simd/c2pkg/c2db/engine.go
  engine.go:1356	0x89f9a0		4c8d642488		LEAQ -0x78(SP), R12						
  engine.go:1356	0x89f9a5		4d3b6610		CMPQ R12, 0x10(R14)						
  engine.go:1356	0x89f9a9		0f8678030000		JBE 0x89fd27							
  engine.go:1356	0x89f9af		55			PUSHQ BP							
  engine.go:1356	0x89f9b0		4889e5			MOVQ SP, BP							
  engine.go:1356	0x89f9b3		4881ecf0000000		SUBQ $0xf0, SP							
  engine.go:1356	0x89f9ba		48899c2418010000	MOVQ BX, 0x118(SP)						
  engine.go:1765	0x89f9c2		4885c0			TESTQ AX, AX							
  engine.go:1765	0x89f9c5		7468			JE 0x89fa2f							
  engine.go:1765	0x89f9c7		48833800		CMPQ 0(AX), $0x0						
  engine.go:1765	0x89f9cb		7462			JE 0x89fa2f							
  engine.go:1765	0x89f9cd		4883781000		CMPQ 0x10(AX), $0x0						
  engine.go:1765	0x89f9d2		745b			JE 0x89fa2f							
  engine.go:1765	0x89f9d4		4883780800		CMPQ 0x8(AX), $0x0						
  engine.go:1765	0x89f9d9		7454			JE 0x89fa2f							
  type.go:58		0x89f9db		488b90e0010000		MOVQ 0x1e0(AX), DX						
  engine.go:1768	0x89f9e2		4885d2			TESTQ DX, DX							
  engine.go:1768	0x89f9e5		740c			JE 0x89f9f3							
  engine.go:1769	0x89f9e7		4c8b12			MOVQ 0(DX), R10							
  engine.go:1769	0x89f9ea		488b7208		MOVQ 0x8(DX), SI						
  engine.go:1357	0x89f9ee		4c89d2			MOVQ R10, DX							
  engine.go:1357	0x89f9f1		eb4d			JMP 0x89fa40							
  engine.go:1771	0x89f9f3		488b10			MOVQ 0(AX), DX							
  device.go:198		0x89f9f6		4885d2			TESTQ DX, DX							
  device.go:198		0x89f9f9		7424			JE 0x89fa1f							
  device.go:198		0x89f9fb		48833a00		CMPQ 0(DX), $0x0						
  device.go:198		0x89f9ff		90			NOPL								
  device.go:198		0x89fa00		7c1d			JL 0x89fa1f							
  type.go:19		0x89fa02		8b5220			MOVL 0x20(DX), DX						
  type.go:19		0x89fa05		85d2			TESTL DX, DX							
  device.go:201		0x89fa07		7410			JE 0x89fa19							
  engine.go:1771	0x89fa09		488b1580c0a800		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrDevicePoisoned(SB), DX	
  engine.go:1771	0x89fa10		488b3581c0a800		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrDevicePoisoned+8(SB), SI	
  engine.go:1771	0x89fa17		eb27			JMP 0x89fa40							
  engine.go:1771	0x89fa19		31d2			XORL DX, DX							
  engine.go:1771	0x89fa1b		31f6			XORL SI, SI							
  engine.go:1771	0x89fa1d		eb21			JMP 0x89fa40							
  engine.go:1771	0x89fa1f		488d15ea17a500		LEAQ 0xa517ea(IP), DX						
  engine.go:1771	0x89fa26		488d35cbb53f00		LEAQ 0x3fb5cb(IP), SI						
  engine.go:1771	0x89fa2d		eb11			JMP 0x89fa40							
  engine.go:1771	0x89fa2f		488d15da17a500		LEAQ 0xa517da(IP), DX						
  engine.go:1771	0x89fa36		488d35bbb53f00		LEAQ 0x3fb5bb(IP), SI						
  engine.go:1771	0x89fa3d		0f1f00			NOPL 0(AX)							
  engine.go:1357	0x89fa40		4885d2			TESTQ DX, DX							
  engine.go:1357	0x89fa43		7502			JNE 0x89fa47							
  engine.go:4008	0x89fa45		eb1f			JMP 0x89fa66							
  engine.go:1358	0x89fa47		31c0			XORL AX, AX							
  engine.go:1358	0x89fa49		31db			XORL BX, BX							
  engine.go:1358	0x89fa4b		89d9			MOVL BX, CX							
  engine.go:1358	0x89fa4d		4889d7			MOVQ DX, DI							
  engine.go:1358	0x89fa50		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1358	0x89fa57		5d			POPQ BP								
  engine.go:1358	0x89fa58		c3			RET								
  engine.go:4017	0x89fa59		90			NOPL								
  type.go:94		0x89fa5a		41baffffffff		MOVL $-0x1, R10							
  type.go:94		0x89fa60		f0440fc15234		LOCK XADDL R10, 0x34(DX)					
  engine.go:4009	0x89fa66		488b90e0000000		MOVQ 0xe0(AX), DX						
  engine.go:4010	0x89fa6d		4885d2			TESTQ DX, DX							
  engine.go:4010	0x89fa70		741b			JE 0x89fa8d							
  engine.go:4013	0x89fa72		90			NOPL								
  type.go:94		0x89fa73		41ba01000000		MOVL $0x1, R10							
  type.go:94		0x89fa79		f0440fc15234		LOCK XADDL R10, 0x34(DX)					
  type.go:58		0x89fa7f		4c8b90e0000000		MOVQ 0xe0(AX), R10						
  engine.go:4014	0x89fa86		4939d2			CMPQ R10, DX							
  engine.go:4014	0x89fa89		75ce			JNE 0x89fa59							
  engine.go:4014	0x89fa8b		eb02			JMP 0x89fa8f							
  engine.go:4014	0x89fa8d		31d2			XORL DX, DX							
  engine.go:1361	0x89fa8f		4885d2			TESTQ DX, DX							
  engine.go:1361	0x89fa92		0f8472020000		JE 0x89fd0a							
  engine.go:1765	0x89fa98		4889842410010000	MOVQ AX, 0x110(SP)						
  engine.go:1765	0x89faa0		48898c2420010000	MOVQ CX, 0x120(SP)						
  engine.go:1765	0x89faa8		48899c2418010000	MOVQ BX, 0x118(SP)						
  engine.go:1765	0x89fab0		4889bc2428010000	MOVQ DI, 0x128(SP)						
  engine.go:1360	0x89fab8		4889942490000000	MOVQ DX, 0x90(SP)						
  engine.go:1364	0x89fac0		0f10842400010000	MOVUPS 0x100(SP), X0						
  engine.go:1364	0x89fac8		488b02			MOVQ 0(DX), AX							
  engine.go:1364	0x89facb		4c8b5208		MOVQ 0x8(DX), R10						
  engine.go:1364	0x89facf		4c8b5a10		MOVQ 0x10(DX), R11						
  engine.go:1364	0x89fad3		488b5218		MOVQ 0x18(DX), DX						
  engine.go:1364	0x89fad7		0f110424		MOVUPS X0, 0(SP)						
  engine.go:1364	0x89fadb		4889de			MOVQ BX, SI							
  engine.go:1364	0x89fade		4989c8			MOVQ CX, R8							
  engine.go:1364	0x89fae1		4989f9			MOVQ DI, R9							
  engine.go:1364	0x89fae4		4c89d3			MOVQ R10, BX							
  engine.go:1364	0x89fae7		4c89d9			MOVQ R11, CX							
  engine.go:1364	0x89faea		4889d7			MOVQ DX, DI							
  engine.go:1364	0x89faed		e86e120000		CALL github.com/hazyhaar/c2pkg/c2db.getAsOfHeap(SB)		
  engine.go:1365	0x89faf2		90			NOPL								
  type.go:94		0x89faf3		baffffffff		MOVL $-0x1, DX							
  type.go:94		0x89faf8		4c8b942490000000	MOVQ 0x90(SP), R10						
  type.go:94		0x89fb00		f0410fc15234		LOCK XADDL DX, 0x34(R10)					
  engine.go:1366	0x89fb06		4885ff			TESTQ DI, DI							
  engine.go:4023	0x89fb09		0f84ee010000		JE 0x89fcfd							
  engine.go:1364	0x89fb0f		4889b42498000000	MOVQ SI, 0x98(SP)						
  engine.go:1364	0x89fb17		48897c2478		MOVQ DI, 0x78(SP)						
  engine.go:1369	0x89fb1c		488b0dddbfa800		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrNotFound(SB), CX		
  engine.go:1369	0x89fb23		488b15debfa800		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrNotFound+8(SB), DX	
  engine.go:1369	0x89fb2a		4889f8			MOVQ DI, AX							
  engine.go:1369	0x89fb2d		4889f3			MOVQ SI, BX							
  engine.go:1369	0x89fb30		4889d7			MOVQ DX, DI							
  engine.go:1369	0x89fb33		e808a4bfff		CALL errors.Is(SB)						
  engine.go:1369	0x89fb38		0f1f840000000000	NOPL 0(AX)(AX*1)						
  engine.go:1369	0x89fb40		84c0			TESTL AL, AL							
  engine.go:1369	0x89fb42		0f8499010000		JE 0x89fce1							
  engine.go:1369	0x89fb48		488b942410010000	MOVQ 0x110(SP), DX						
  engine.go:1369	0x89fb50		488b82f8020000		MOVQ 0x2f8(DX), AX						
  engine.go:1369	0x89fb57		660f1f840000000000	NOPW 0(AX)(AX*1)						
  engine.go:1369	0x89fb60		4885c0			TESTQ AX, AX							
  engine.go:1369	0x89fb63		0f8478010000		JE 0x89fce1							
  engine.go:1372	0x89fb69		488d9424a8000000	LEAQ 0xa8(SP), DX						
  engine.go:1372	0x89fb71		440f113a		MOVUPS X15, 0(DX)						
  engine.go:1372	0x89fb75		440f117a10		MOVUPS X15, 0x10(DX)						
  engine.go:1372	0x89fb7a		440f117a20		MOVUPS X15, 0x20(DX)						
  engine.go:1372	0x89fb7f		440f117a30		MOVUPS X15, 0x30(DX)						
  engine.go:1372	0x89fb84		440f117a38		MOVUPS X15, 0x38(DX)						
  engine.go:1372	0x89fb89		0f10842400010000	MOVUPS 0x100(SP), X0						
  engine.go:1372	0x89fb91		0f110424		MOVUPS X0, 0(SP)						
  engine.go:1372	0x89fb95		488b9c2418010000	MOVQ 0x118(SP), BX						
  engine.go:1372	0x89fb9d		488b8c2420010000	MOVQ 0x120(SP), CX						
  engine.go:1372	0x89fba5		488bbc2428010000	MOVQ 0x128(SP), DI						
  engine.go:1372	0x89fbad		e8eef60000		CALL github.com/hazyhaar/c2pkg/c2db.(*History).Resolve(SB)	
  engine.go:1372	0x89fbb2		488d9424a8000000	LEAQ 0xa8(SP), DX						
  engine.go:1372	0x89fbba		488d742410		LEAQ 0x10(SP), SI						
  engine.go:1372	0x89fbbf		440f1036		MOVUPS 0(SI), X14						
  engine.go:1372	0x89fbc3		440f1132		MOVUPS X14, 0(DX)						
  engine.go:1372	0x89fbc7		440f107610		MOVUPS 0x10(SI), X14						
  engine.go:1372	0x89fbcc		440f117210		MOVUPS X14, 0x10(DX)						
  engine.go:1372	0x89fbd1		440f107620		MOVUPS 0x20(SI), X14						
  engine.go:1372	0x89fbd6		440f117220		MOVUPS X14, 0x20(DX)						
  engine.go:1372	0x89fbdb		440f107630		MOVUPS 0x30(SI), X14						
  engine.go:1372	0x89fbe0		440f117230		MOVUPS X14, 0x30(DX)						
  engine.go:1372	0x89fbe5		440f107638		MOVUPS 0x38(SI), X14						
  engine.go:1372	0x89fbea		440f117238		MOVUPS X14, 0x38(DX)						
  engine.go:1379	0x89fbef		488b9424d0000000	MOVQ 0xd0(SP), DX						
  engine.go:1379	0x89fbf7		660f1f840000000000	NOPW 0(AX)(AX*1)						
  engine.go:1373	0x89fc00		4885db			TESTQ BX, BX							
  engine.go:1373	0x89fc03		0f85c3000000		JNE 0x89fccc							
  engine.go:1372	0x89fc09		84c0			TESTL AL, AL							
  engine.go:1376	0x89fc0b		0f849e000000		JE 0x89fcaf							
  engine.go:1376	0x89fc11		80bc24e800000000	CMPB 0xe8(SP), $0x0						
  engine.go:1376	0x89fc19		0f8590000000		JNE 0x89fcaf							
  engine.go:1379	0x89fc1f		488bbc24d8000000	MOVQ 0xd8(SP), DI						
  engine.go:1379	0x89fc27		4885ff			TESTQ DI, DI							
  engine.go:1379	0x89fc2a		7508			JNE 0x89fc34							
  engine.go:1379	0x89fc2c		31c0			XORL AX, AX							
  engine.go:1379	0x89fc2e		31c9			XORL CX, CX							
  engine.go:1379	0x89fc30		31db			XORL BX, BX							
  engine.go:1379	0x89fc32		eb33			JMP 0x89fc67							
  engine.go:1379	0x89fc34		4889bc2488000000	MOVQ DI, 0x88(SP)						
  engine.go:1379	0x89fc3c		48899424a0000000	MOVQ DX, 0xa0(SP)						
  engine.go:1379	0x89fc44		31c0			XORL AX, AX							
  engine.go:1379	0x89fc46		4889fb			MOVQ DI, BX							
  engine.go:1379	0x89fc49		31c9			XORL CX, CX							
  engine.go:1379	0x89fc4b		488d35ee459f00		LEAQ 0x9f45ee(IP), SI						
  engine.go:1379	0x89fc52		e889ecbeff		CALL runtime.growslice(SB)					
  engine.go:1379	0x89fc57		488b9424a0000000	MOVQ 0xa0(SP), DX						
  engine.go:1379	0x89fc5f		488bbc2488000000	MOVQ 0x88(SP), DI						
  engine.go:1379	0x89fc67		48898c2488000000	MOVQ CX, 0x88(SP)						
  engine.go:1379	0x89fc6f		48899c2480000000	MOVQ BX, 0x80(SP)						
  engine.go:1379	0x89fc77		48898424a0000000	MOVQ AX, 0xa0(SP)						
  engine.go:1379	0x89fc7f		4889d3			MOVQ DX, BX							
  engine.go:1379	0x89fc82		4889f9			MOVQ DI, CX							
  engine.go:1379	0x89fc85		e8f645bfff		CALL runtime.memmove(SB)					
  engine.go:1379	0x89fc8a		488b8424a0000000	MOVQ 0xa0(SP), AX						
  engine.go:1379	0x89fc92		488b9c2480000000	MOVQ 0x80(SP), BX						
  engine.go:1379	0x89fc9a		488b8c2488000000	MOVQ 0x88(SP), CX						
  engine.go:1379	0x89fca2		31ff			XORL DI, DI							
  engine.go:1379	0x89fca4		31f6			XORL SI, SI							
  engine.go:1379	0x89fca6		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1379	0x89fcad		5d			POPQ BP								
  engine.go:1379	0x89fcae		c3			RET								
  engine.go:1377	0x89fcaf		488b3daa19aa00		MOVQ github.com/hazyhaar/c2pkg/c2db.errNotFound(SB), DI		
  engine.go:1377	0x89fcb6		488b35ab19aa00		MOVQ github.com/hazyhaar/c2pkg/c2db.errNotFound+8(SB), SI	
  engine.go:1377	0x89fcbd		31c0			XORL AX, AX							
  engine.go:1377	0x89fcbf		31db			XORL BX, BX							
  engine.go:1377	0x89fcc1		89d9			MOVL BX, CX							
  engine.go:1377	0x89fcc3		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1377	0x89fcca		5d			POPQ BP								
  engine.go:1377	0x89fccb		c3			RET								
  engine.go:1374	0x89fccc		31c0			XORL AX, AX							
  engine.go:1374	0x89fcce		4889df			MOVQ BX, DI							
  engine.go:1374	0x89fcd1		4889ce			MOVQ CX, SI							
  engine.go:1374	0x89fcd4		31db			XORL BX, BX							
  engine.go:1374	0x89fcd6		89d9			MOVL BX, CX							
  engine.go:1374	0x89fcd8		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1374	0x89fcdf		5d			POPQ BP								
  engine.go:1374	0x89fce0		c3			RET								
  engine.go:1370	0x89fce1		31c0			XORL AX, AX							
  engine.go:1370	0x89fce3		31db			XORL BX, BX							
  engine.go:1370	0x89fce5		89d9			MOVL BX, CX							
  engine.go:1370	0x89fce7		488b7c2478		MOVQ 0x78(SP), DI						
  engine.go:1370	0x89fcec		488bb42498000000	MOVQ 0x98(SP), SI						
  engine.go:1370	0x89fcf4		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1370	0x89fcfb		5d			POPQ BP								
  engine.go:1370	0x89fcfc		c3			RET								
  engine.go:1367	0x89fcfd		31ff			XORL DI, DI							
  engine.go:1367	0x89fcff		31f6			XORL SI, SI							
  engine.go:1367	0x89fd01		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1367	0x89fd08		5d			POPQ BP								
  engine.go:1367	0x89fd09		c3			RET								
  engine.go:1362	0x89fd0a		31c0			XORL AX, AX							
  engine.go:1362	0x89fd0c		31db			XORL BX, BX							
  engine.go:1362	0x89fd0e		89d9			MOVL BX, CX							
  engine.go:1362	0x89fd10		488d3df914a500		LEAQ 0xa514f9(IP), DI						
  engine.go:1362	0x89fd17		488d35dab23f00		LEAQ 0x3fb2da(IP), SI						
  engine.go:1362	0x89fd1e		4881c4f0000000		ADDQ $0xf0, SP							
  engine.go:1362	0x89fd25		5d			POPQ BP								
  engine.go:1362	0x89fd26		c3			RET								
  engine.go:1356	0x89fd27		4889442418		MOVQ AX, 0x18(SP)						
  engine.go:1356	0x89fd2c		48895c2420		MOVQ BX, 0x20(SP)						
  engine.go:1356	0x89fd31		48894c2428		MOVQ CX, 0x28(SP)						
  engine.go:1356	0x89fd36		48897c2430		MOVQ DI, 0x30(SP)						
  engine.go:1356	0x89fd3b		0f1f440000		NOPL 0(AX)(AX*1)						
  engine.go:1356	0x89fd40		e8fb23bfff		CALL runtime.morestack_noctxt.abi0(SB)				
  engine.go:1356	0x89fd45		488b442418		MOVQ 0x18(SP), AX						
  engine.go:1356	0x89fd4a		488b5c2420		MOVQ 0x20(SP), BX						
  engine.go:1356	0x89fd4f		488b4c2428		MOVQ 0x28(SP), CX						
  engine.go:1356	0x89fd54		488b7c2430		MOVQ 0x30(SP), DI						
  engine.go:1356	0x89fd59		e942fcffff		JMP github.com/hazyhaar/c2pkg/c2db.(*Shard).GetAsOf(SB)		
