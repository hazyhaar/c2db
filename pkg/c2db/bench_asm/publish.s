TEXT github.com/hazyhaar/c2pkg/c2db.(*Shard).publish(SB) /devhoros/c2simd/c2pkg/c2db/engine.go
  engine.go:4068	0x8ac680		4c8da42458ffffff	LEAQ 0xffffff58(SP), R12						
  engine.go:4068	0x8ac688		4d3b6610		CMPQ R12, 0x10(R14)							
  engine.go:4068	0x8ac68c		0f86420d0000		JBE 0x8ad3d4								
  engine.go:4068	0x8ac692		55			PUSHQ BP								
  engine.go:4068	0x8ac693		4889e5			MOVQ SP, BP								
  engine.go:4068	0x8ac696		4881ec20010000		SUBQ $0x120, SP								
  engine.go:4070	0x8ac69d		4889842430010000	MOVQ AX, 0x130(SP)							
  engine.go:4069	0x8ac6a5		488d05e87d3900		LEAQ 0x397de8(IP), AX							
  engine.go:4069	0x8ac6ac		bb0e000000		MOVL $0xe, BX								
  engine.go:4069	0x8ac6b1		e86a42daff		CALL github.com/hazyhaar/pkg/cihook55.Fire(SB)				
  engine.go:4070	0x8ac6b6		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4070	0x8ac6be		8b88e8010000		MOVL 0x1e8(AX), CX							
  type.go:19		0x8ac6c4		85c9			TESTL CX, CX								
  engine.go:4070	0x8ac6c6		753d			JNE 0x8ac705								
  engine.go:4075	0x8ac6c8		e8330e0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).reapHolds(SB)		
  engine.go:4076	0x8ac6cd		90			NOPL									
  type.go:58		0x8ac6ce		488b842430010000	MOVQ 0x130(SP), AX							
  type.go:58		0x8ac6d6		488b88e0000000		MOVQ 0xe0(AX), CX							
  type.go:58		0x8ac6dd		0f1f00			NOPL 0(AX)								
  engine.go:4077	0x8ac6e0		4885c9			TESTQ CX, CX								
  engine.go:4077	0x8ac6e3		740a			JE 0x8ac6ef								
  type.go:80		0x8ac6e5		8b5134			MOVL 0x34(CX), DX							
  engine.go:4077	0x8ac6e8		85d2			TESTL DX, DX								
  engine.go:4077	0x8ac6ea		0f95c2			SETNE DL								
  engine.go:4077	0x8ac6ed		eb02			JMP 0x8ac6f1								
  engine.go:4077	0x8ac6ef		31d2			XORL DX, DX								
  engine.go:4077	0x8ac6f1		84d2			TESTL DL, DL								
  engine.go:4077	0x8ac6f3		0f84c0000000		JE 0x8ac7b9								
  engine.go:4077	0x8ac6f9		31c9			XORL CX, CX								
  engine.go:4077	0x8ac6fb		0f1f440000		NOPL 0(AX)(AX*1)							
  engine.go:4077	0x8ac700		e9000c0000		JMP 0x8ad305								
  engine.go:4071	0x8ac705		90			NOPL									
  errors.go:65		0x8ac706		b810000000		MOVL $0x10, AX								
  errors.go:65		0x8ac70b		488d1d36679f00		LEAQ 0x9f6736(IP), BX							
  errors.go:65		0x8ac712		b901000000		MOVL $0x1, CX								
  errors.go:65		0x8ac717		e8e459b7ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)				
  errors.go:65		0x8ac71c		48898424d8000000	MOVQ AX, 0xd8(SP)							
  errors.go:65		0x8ac724		48c7400829000000	MOVQ $0x29, 0x8(AX)							
  errors.go:65		0x8ac72c		488d1587ae3b00		LEAQ 0x3bae87(IP), DX							
  errors.go:65		0x8ac733		488910			MOVQ DX, 0(AX)								
  engine.go:4072	0x8ac736		b810000000		MOVL $0x10, AX								
  engine.go:4072	0x8ac73b		488d1dfe2a9f00		LEAQ 0x9f2afe(IP), BX							
  engine.go:4072	0x8ac742		b901000000		MOVL $0x1, CX								
  engine.go:4072	0x8ac747		e8b459b7ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)				
  engine.go:4072	0x8ac74c		488d15dd4ba400		LEAQ 0xa44bdd(IP), DX							
  engine.go:4072	0x8ac753		488910			MOVQ DX, 0(AX)								
  engine.go:4072	0x8ac756		833dd38fab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4072	0x8ac75d		750a			JNE 0x8ac769								
  engine.go:4072	0x8ac75f		488b9c24d8000000	MOVQ 0xd8(SP), BX							
  engine.go:4072	0x8ac767		eb10			JMP 0x8ac779								
  engine.go:4072	0x8ac769		e8b273beff		CALL runtime.gcWriteBarrier1(SB)					
  engine.go:4072	0x8ac76e		488b9c24d8000000	MOVQ 0xd8(SP), BX							
  engine.go:4072	0x8ac776		49891b			MOVQ BX, 0(R11)								
  engine.go:4072	0x8ac779		48895808		MOVQ BX, 0x8(AX)							
  engine.go:1744	0x8ac77d		48833800		CMPQ 0(AX), $0x0							
  engine.go:1744	0x8ac781		742a			JE 0x8ac7ad								
  type.go:61		0x8ac783		488b8c2430010000	MOVQ 0x130(SP), CX							
  type.go:61		0x8ac78b		4881c1e0010000		ADDQ $0x1e0, CX								
  engine.go:1745	0x8ac792		90			NOPL									
  type.go:61		0x8ac793		4889c3			MOVQ AX, BX								
  type.go:61		0x8ac796		4889c8			MOVQ CX, AX								
  type.go:61		0x8ac799		e822cfbdff		CALL sync/atomic.StorePointer(SB)					
  type.go:61		0x8ac79e		488d158b4ba400		LEAQ 0xa44b8b(IP), DX							
  engine.go:4073	0x8ac7a5		488b9c24d8000000	MOVQ 0xd8(SP), BX							
  engine.go:4073	0x8ac7ad		4889d0			MOVQ DX, AX								
  engine.go:4073	0x8ac7b0		4881c420010000		ADDQ $0x120, SP								
  engine.go:4073	0x8ac7b7		5d			POPQ BP									
  engine.go:4073	0x8ac7b8		c3			RET									
  engine.go:4091	0x8ac7b9		488b5050		MOVQ 0x50(AX), DX							
  engine.go:4095	0x8ac7bd		488bb088010000		MOVQ 0x188(AX), SI							
  engine.go:4098	0x8ac7c4		4c8b4038		MOVQ 0x38(AX), R8							
  engine.go:4098	0x8ac7c8		4c8b4830		MOVQ 0x30(AX), R9							
  engine.go:4098	0x8ac7cc		488b4840		MOVQ 0x40(AX), CX							
  engine.go:4092	0x8ac7d0		4885d2			TESTQ DX, DX								
  engine.go:4095	0x8ac7d3		41ba01000000		MOVL $0x1, R10								
  engine.go:4095	0x8ac7d9		490f44d2		CMOVE R10, DX								
  engine.go:4095	0x8ac7dd		4839d6			CMPQ SI, DX								
  engine.go:4098	0x8ac7e0		480f42d6		CMOVB SI, DX								
  engine.go:4099	0x8ac7e4		4c398090010000		CMPQ 0x190(AX), R8							
  engine.go:4092	0x8ac7eb		760c			JBE 0x8ac7f9								
  engine.go:4100	0x8ac7ed		4c8b4818		MOVQ 0x18(AX), R9							
  engine.go:4100	0x8ac7f1		4c8b4020		MOVQ 0x20(AX), R8							
  engine.go:4100	0x8ac7f5		488b4828		MOVQ 0x28(AX), CX							
  engine.go:4102	0x8ac7f9		48ff8018010000		INCQ 0x118(AX)								
  engine.go:4103	0x8ac800		90			NOPL									
  engine.go:3931	0x8ac801		4983f838		CMPQ R8, $0x38								
  engine.go:3931	0x8ac805		7c1b			JL 0x8ac822								
  engine.go:3934	0x8ac807		488b7048		MOVQ 0x48(AX), SI							
  binary.go:124		0x8ac80b		49897120		MOVQ SI, 0x20(R9)							
  engine.go:3935	0x8ac80f		488b7050		MOVQ 0x50(AX), SI							
  binary.go:124		0x8ac813		49897128		MOVQ SI, 0x28(R9)							
  engine.go:3936	0x8ac817		488bb018010000		MOVQ 0x118(AX), SI							
  binary.go:124		0x8ac81e		49897130		MOVQ SI, 0x30(R9)							
  engine.go:4104	0x8ac822		4881f900400000		CMPQ CX, $0x4000							
  engine.go:4104	0x8ac829		0f82af0a0000		JB 0x8ad2de								
  engine.go:4102	0x8ac82f		48898c2480000000	MOVQ CX, 0x80(SP)							
  engine.go:4102	0x8ac837		4c89442478		MOVQ R8, 0x78(SP)							
  engine.go:4102	0x8ac83c		4c898c24a8000000	MOVQ R9, 0xa8(SP)							
  engine.go:4098	0x8ac844		4889542450		MOVQ DX, 0x50(SP)							
  engine.go:4104	0x8ac849		4c89c8			MOVQ R9, AX								
  engine.go:4104	0x8ac84c		bb00400000		MOVL $0x4000, BX							
  engine.go:4104	0x8ac851		bf00400000		MOVL $0x4000, DI							
  engine.go:4104	0x8ac856		e8053cfcff		CALL github.com/hazyhaar/c2pkg/c2db.C2db_crc32c_fullpage_store(SB)	
  engine.go:4105	0x8ac85b		90			NOPL									
  engine.go:3534	0x8ac85c		488b942430010000	MOVQ 0x130(SP), DX							
  engine.go:3534	0x8ac864		4883ba8801000000	CMPQ 0x188(DX), $0x0							
  engine.go:3534	0x8ac86c		745c			JE 0x8ac8ca								
  engine.go:3538	0x8ac86e		4883baa801000000	CMPQ 0x1a8(DX), $0x0							
  engine.go:3538	0x8ac876		7452			JE 0x8ac8ca								
  engine.go:3538	0x8ac878		4c8b82a0010000		MOVQ 0x1a0(DX), R8							
  engine.go:3542	0x8ac87f		4d8b08			MOVQ 0(R8), R9								
  engine.go:3542	0x8ac882		410fbae100		BTL $0x0, R9								
  engine.go:3542	0x8ac887		7241			JB 0x8ac8ca								
  engine.go:3545	0x8ac889		4983c901		ORQ $0x1, R9								
  engine.go:3545	0x8ac88d		4d8908			MOVQ R9, 0(R8)								
  engine.go:3546	0x8ac890		4c8b82d0010000		MOVQ 0x1d0(DX), R8							
  engine.go:3546	0x8ac897		4c8b8ac0010000		MOVQ 0x1c0(DX), R9							
  engine.go:3546	0x8ac89e		6690			NOPW									
  engine.go:3546	0x8ac8a0		4d39c1			CMPQ R9, R8								
  engine.go:3546	0x8ac8a3		7e1e			JLE 0x8ac8c3								
  engine.go:3547	0x8ac8a5		0f862e0a0000		JBE 0x8ad2d9								
  engine.go:3546	0x8ac8ab		4c8b8ab8010000		MOVQ 0x1b8(DX), R9							
  engine.go:3547	0x8ac8b2		43c7048100000000	MOVL $0x0, 0(R9)(R8*4)							
  engine.go:3548	0x8ac8ba		48ff82d0010000		INCQ 0x1d0(DX)								
  engine.go:3548	0x8ac8c1		eb07			JMP 0x8ac8ca								
  engine.go:3550	0x8ac8c3		c682d801000001		MOVB $0x1, 0x1d8(DX)							
  engine.go:4110	0x8ac8ca		48837a0800		CMPQ 0x8(DX), $0x0							
  engine.go:4110	0x8ac8cf		7460			JE 0x8ac931								
  engine.go:4111	0x8ac8d1		80bad801000000		CMPB 0x1d8(DX), $0x0							
  engine.go:4111	0x8ac8d8		741c			JE 0x8ac8f6								
  engine.go:4112	0x8ac8da		4c8b82a8010000		MOVQ 0x1a8(DX), R8							
  engine.go:4112	0x8ac8e1		4c89842498000000	MOVQ R8, 0x98(SP)							
  engine.go:4112	0x8ac8e9		4531c9			XORL R9, R9								
  engine.go:4112	0x8ac8ec		4c8b542450		MOVQ 0x50(SP), R10							
  engine.go:4112	0x8ac8f1		e9aa070000		JMP 0x8ad0a0								
  engine.go:4128	0x8ac8f6		4c8b82c8010000		MOVQ 0x1c8(DX), R8							
  engine.go:4128	0x8ac8fd		4c8b8ad0010000		MOVQ 0x1d0(DX), R9							
  engine.go:4128	0x8ac904		4d39c8			CMPQ R8, R9								
  engine.go:4128	0x8ac907		0f8282070000		JB 0x8ad08f								
  engine.go:4128	0x8ac90d		4c898c2498000000	MOVQ R9, 0x98(SP)							
  engine.go:4128	0x8ac915		4c8b82b8010000		MOVQ 0x1b8(DX), R8							
  engine.go:4128	0x8ac91c		4c898424d0000000	MOVQ R8, 0xd0(SP)							
  engine.go:4128	0x8ac924		4531d2			XORL R10, R10								
  engine.go:4128	0x8ac927		4c8b5c2450		MOVQ 0x50(SP), R11							
  engine.go:4128	0x8ac92c		e97f050000		JMP 0x8aceb0								
  engine.go:4142	0x8ac931		b838000000		MOVL $0x38, AX								
  engine.go:4142	0x8ac936		488d1d9b6da200		LEAQ 0xa26d9b(IP), BX							
  engine.go:4142	0x8ac93d		b901000000		MOVL $0x1, CX								
  engine.go:4142	0x8ac942		e89966b7ff		CALL runtime.mallocgcSmallScanNoHeaderSC6(SB)				
  engine.go:4142	0x8ac947		488b542478		MOVQ 0x78(SP), DX							
  engine.go:4142	0x8ac94c		48895008		MOVQ DX, 0x8(AX)							
  engine.go:4142	0x8ac950		488bb42480000000	MOVQ 0x80(SP), SI							
  engine.go:4142	0x8ac958		48897010		MOVQ SI, 0x10(AX)							
  engine.go:4142	0x8ac95c		833dcd8dab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4142	0x8ac963		750a			JNE 0x8ac96f								
  engine.go:4142	0x8ac965		488b8c24a8000000	MOVQ 0xa8(SP), CX							
  engine.go:4142	0x8ac96d		eb10			JMP 0x8ac97f								
  engine.go:4142	0x8ac96f		e8ac71beff		CALL runtime.gcWriteBarrier1(SB)					
  engine.go:4142	0x8ac974		488b8c24a8000000	MOVQ 0xa8(SP), CX							
  engine.go:4142	0x8ac97c		49890b			MOVQ CX, 0(R11)								
  engine.go:4142	0x8ac97f		488908			MOVQ CX, 0(AX)								
  engine.go:4142	0x8ac982		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4142	0x8ac98a		488b5148		MOVQ 0x48(CX), DX							
  engine.go:4142	0x8ac98e		48895018		MOVQ DX, 0x18(AX)							
  engine.go:4142	0x8ac992		0f1081a0000000		MOVUPS 0xa0(CX), X0							
  engine.go:4142	0x8ac999		0f114020		MOVUPS X0, 0x20(AX)							
  engine.go:4142	0x8ac99d		0fb691b0000000		MOVZX 0xb0(CX), DX							
  engine.go:4142	0x8ac9a4		885030			MOVB DL, 0x30(AX)							
  type.go:64		0x8ac9a7		4881c1e0000000		ADDQ $0xe0, CX								
  engine.go:4143	0x8ac9ae		90			NOPL									
  type.go:64		0x8ac9af		4889c3			MOVQ AX, BX								
  type.go:64		0x8ac9b2		4889c8			MOVQ CX, AX								
  type.go:64		0x8ac9b5		e846cdbdff		CALL sync/atomic.SwapPointer(SB)					
  engine.go:4144	0x8ac9ba		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4144	0x8ac9c2		488b5120		MOVQ 0x20(CX), DX							
  engine.go:4144	0x8ac9c6		488b7118		MOVQ 0x18(CX), SI							
  engine.go:4144	0x8ac9ca		488b7928		MOVQ 0x28(CX), DI							
  engine.go:4145	0x8ac9ce		4c8b442478		MOVQ 0x78(SP), R8							
  engine.go:4145	0x8ac9d3		4c894120		MOVQ R8, 0x20(CX)							
  engine.go:4145	0x8ac9d7		4c8b8c2480000000	MOVQ 0x80(SP), R9							
  engine.go:4145	0x8ac9df		4c894928		MOVQ R9, 0x28(CX)							
  engine.go:4145	0x8ac9e3		833d468dab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4145	0x8ac9ea		750a			JNE 0x8ac9f6								
  engine.go:4145	0x8ac9ec		4c8b8c24a8000000	MOVQ 0xa8(SP), R9							
  engine.go:4145	0x8ac9f4		eb18			JMP 0x8aca0e								
  engine.go:4145	0x8ac9f6		488b5918		MOVQ 0x18(CX), BX							
  engine.go:4145	0x8ac9fa		e84171beff		CALL runtime.gcWriteBarrier2(SB)					
  engine.go:4145	0x8ac9ff		4c8b8c24a8000000	MOVQ 0xa8(SP), R9							
  engine.go:4145	0x8aca07		4d890b			MOVQ R9, 0(R11)								
  engine.go:4145	0x8aca0a		49895b08		MOVQ BX, 0x8(R11)							
  engine.go:4144	0x8aca0e		48897c2460		MOVQ DI, 0x60(SP)							
  engine.go:4144	0x8aca13		4889542458		MOVQ DX, 0x58(SP)							
  engine.go:4144	0x8aca18		4889b424a0000000	MOVQ SI, 0xa0(SP)							
  type.go:64		0x8aca20		48898424c8000000	MOVQ AX, 0xc8(SP)							
  engine.go:4145	0x8aca28		4c894918		MOVQ R9, 0x18(CX)							
  engine.go:4146	0x8aca2c		4885c0			TESTQ AX, AX								
  engine.go:4146	0x8aca2f		7412			JE 0x8aca43								
  type.go:80		0x8aca31		8b5834			MOVL 0x34(AX), BX							
  engine.go:4146	0x8aca34		85db			TESTL BX, BX								
  engine.go:4146	0x8aca36		7408			JE 0x8aca40								
  engine.go:4146	0x8aca38		31db			XORL BX, BX								
  engine.go:4146	0x8aca3a		e946040000		JMP 0x8ace85								
  engine.go:4146	0x8aca3f		90			NOPL									
  engine.go:4146	0x8aca40		4885c0			TESTQ AX, AX								
  engine.go:4151	0x8aca43		740a			JE 0x8aca4f								
  type.go:80		0x8aca45		8b5834			MOVL 0x34(AX), BX							
  engine.go:4151	0x8aca48		85db			TESTL BX, BX								
  engine.go:4151	0x8aca4a		0f95c3			SETNE BL								
  engine.go:4151	0x8aca4d		eb02			JMP 0x8aca51								
  engine.go:4151	0x8aca4f		31db			XORL BX, BX								
  engine.go:4151	0x8aca51		84db			TESTL BL, BL								
  engine.go:4151	0x8aca53		0f859e000000		JNE 0x8acaf7								
  engine.go:4151	0x8aca59		0f1f8000000000		NOPL 0(AX)								
  engine.go:4146	0x8aca60		4885c0			TESTQ AX, AX								
  engine.go:4166	0x8aca63		0f857a020000		JNE 0x8acce3								
  engine.go:4184	0x8aca69		488b9108010000		MOVQ 0x108(CX), DX							
  engine.go:4184	0x8aca70		48399190010000		CMPQ 0x190(CX), DX							
  engine.go:4184	0x8aca77		7f53			JG 0x8acacc								
  engine.go:4184	0x8aca79		488b9910010000		MOVQ 0x110(CX), BX							
  engine.go:4184	0x8aca80		488bb100010000		MOVQ 0x100(CX), SI							
  engine.go:4185	0x8aca87		48895138		MOVQ DX, 0x38(CX)							
  engine.go:4185	0x8aca8b		48895940		MOVQ BX, 0x40(CX)							
  engine.go:4185	0x8aca8f		833d9a8cab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4185	0x8aca96		741b			JE 0x8acab3								
  engine.go:4185	0x8aca98		488b5130		MOVQ 0x30(CX), DX							
  engine.go:4186	0x8aca9c		488b9900010000		MOVQ 0x100(CX), BX							
  engine.go:4185	0x8acaa3		e8b870beff		CALL runtime.gcWriteBarrier3(SB)					
  engine.go:4185	0x8acaa8		498933			MOVQ SI, 0(R11)								
  engine.go:4185	0x8acaab		49895308		MOVQ DX, 0x8(R11)							
  engine.go:4186	0x8acaaf		49895b10		MOVQ BX, 0x10(R11)							
  engine.go:4185	0x8acab3		48897130		MOVQ SI, 0x30(CX)							
  engine.go:4186	0x8acab7		440f11b908010000	MOVUPS X15, 0x108(CX)							
  engine.go:4186	0x8acabf		48c7810001000000000000	MOVQ $0x0, 0x100(CX)							
  engine.go:4186	0x8acaca		eb15			JMP 0x8acae1								
  engine.go:4188	0x8acacc		4889c8			MOVQ CX, AX								
  engine.go:4188	0x8acacf		e82c090000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).takeSpareDirty(SB)		
  engine.go:4188	0x8acad4		4885c0			TESTQ AX, AX								
  engine.go:4188	0x8acad7		7515			JNE 0x8acaee								
  engine.go:4192	0x8acad9		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4192	0x8acae1		4889c8			MOVQ CX, AX								
  engine.go:4192	0x8acae4		e8d7d8ffff		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:4192	0x8acae9		e9af010000		JMP 0x8acc9d								
  engine.go:4189	0x8acaee		4881c420010000		ADDQ $0x120, SP								
  engine.go:4189	0x8acaf5		5d			POPQ BP									
  engine.go:4189	0x8acaf6		c3			RET									
  engine.go:4154	0x8acaf7		488b9108010000		MOVQ 0x108(CX), DX							
  engine.go:4154	0x8acafe		6690			NOPW									
  engine.go:4154	0x8acb00		48399190010000		CMPQ 0x190(CX), DX							
  engine.go:4154	0x8acb07		7f53			JG 0x8acb5c								
  engine.go:4154	0x8acb09		488b9910010000		MOVQ 0x110(CX), BX							
  engine.go:4154	0x8acb10		488bb100010000		MOVQ 0x100(CX), SI							
  engine.go:4155	0x8acb17		48895138		MOVQ DX, 0x38(CX)							
  engine.go:4155	0x8acb1b		48895940		MOVQ BX, 0x40(CX)							
  engine.go:4155	0x8acb1f		833d0a8cab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4155	0x8acb26		741b			JE 0x8acb43								
  engine.go:4155	0x8acb28		488b5130		MOVQ 0x30(CX), DX							
  engine.go:4156	0x8acb2c		488b9900010000		MOVQ 0x100(CX), BX							
  engine.go:4155	0x8acb33		e82870beff		CALL runtime.gcWriteBarrier3(SB)					
  engine.go:4155	0x8acb38		498933			MOVQ SI, 0(R11)								
  engine.go:4155	0x8acb3b		49895308		MOVQ DX, 0x8(R11)							
  engine.go:4156	0x8acb3f		49895b10		MOVQ BX, 0x10(R11)							
  engine.go:4155	0x8acb43		48897130		MOVQ SI, 0x30(CX)							
  engine.go:4156	0x8acb47		440f11b908010000	MOVUPS X15, 0x108(CX)							
  engine.go:4156	0x8acb4f		48c7810001000000000000	MOVQ $0x0, 0x100(CX)							
  engine.go:4156	0x8acb5a		eb1a			JMP 0x8acb76								
  engine.go:4158	0x8acb5c		4889c8			MOVQ CX, AX								
  engine.go:4158	0x8acb5f		90			NOPL									
  engine.go:4158	0x8acb60		e89b080000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).takeSpareDirty(SB)		
  engine.go:4158	0x8acb65		4885c0			TESTQ AX, AX								
  engine.go:4158	0x8acb68		0f85c2000000		JNE 0x8acc30								
  engine.go:4164	0x8acb6e		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4164	0x8acb76		4889c8			MOVQ CX, AX								
  engine.go:4164	0x8acb79		e822dfffff		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).syncDirtyFull(SB)		
  engine.go:4165	0x8acb7e		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4165	0x8acb86		488b88f8000000		MOVQ 0xf8(AX), CX							
  engine.go:4165	0x8acb8d		488b98f0000000		MOVQ 0xf0(AX), BX							
  engine.go:4165	0x8acb94		48ffc3			INCQ BX									
  engine.go:4165	0x8acb97		488b90e8000000		MOVQ 0xe8(AX), DX							
  engine.go:4165	0x8acb9e		6690			NOPW									
  engine.go:4165	0x8acba0		4839d9			CMPQ CX, BX								
  engine.go:4165	0x8acba3		7351			JAE 0x8acbf6								
  engine.go:4165	0x8acba5		4889d0			MOVQ DX, AX								
  engine.go:4165	0x8acba8		bf01000000		MOVL $0x1, DI								
  engine.go:4165	0x8acbad		488d354c5f9800		LEAQ 0x985f4c(IP), SI							
  engine.go:4165	0x8acbb4		e8271dbeff		CALL runtime.growslice(SB)						
  engine.go:4165	0x8acbb9		488b942430010000	MOVQ 0x130(SP), DX							
  engine.go:4165	0x8acbc1		48898af8000000		MOVQ CX, 0xf8(DX)							
  engine.go:4165	0x8acbc8		833d618bab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4165	0x8acbcf		7413			JE 0x8acbe4								
  engine.go:4165	0x8acbd1		488b8ae8000000		MOVQ 0xe8(DX), CX							
  engine.go:4165	0x8acbd8		e8636fbeff		CALL runtime.gcWriteBarrier2(SB)					
  engine.go:4165	0x8acbdd		498903			MOVQ AX, 0(R11)								
  engine.go:4165	0x8acbe0		49894b08		MOVQ CX, 0x8(R11)							
  engine.go:4165	0x8acbe4		488982e8000000		MOVQ AX, 0xe8(DX)							
  engine.go:4165	0x8acbeb		4889c2			MOVQ AX, DX								
  engine.go:4165	0x8acbee		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4165	0x8acbf6		488998f0000000		MOVQ BX, 0xf0(AX)							
  engine.go:4165	0x8acbfd		833d2c8bab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4165	0x8acc04		750a			JNE 0x8acc10								
  engine.go:4165	0x8acc06		488bb424c8000000	MOVQ 0xc8(SP), SI							
  engine.go:4165	0x8acc0e		eb19			JMP 0x8acc29								
  engine.go:4165	0x8acc10		488b4cdaf8		MOVQ -0x8(DX)(BX*8), CX							
  engine.go:4165	0x8acc15		e8266fbeff		CALL runtime.gcWriteBarrier2(SB)					
  engine.go:4165	0x8acc1a		488bb424c8000000	MOVQ 0xc8(SP), SI							
  engine.go:4165	0x8acc22		498933			MOVQ SI, 0(R11)								
  engine.go:4165	0x8acc25		49894b08		MOVQ CX, 0x8(R11)							
  engine.go:4165	0x8acc29		488974daf8		MOVQ SI, -0x8(DX)(BX*8)							
  engine.go:4165	0x8acc2e		eb09			JMP 0x8acc39								
  engine.go:4159	0x8acc30		4881c420010000		ADDQ $0x120, SP								
  engine.go:4159	0x8acc37		5d			POPQ BP									
  engine.go:4159	0x8acc38		c3			RET									
  engine.go:4194	0x8acc39		488b4838		MOVQ 0x38(AX), CX							
  engine.go:4194	0x8acc3d		0f1f00			NOPL 0(AX)								
  engine.go:4194	0x8acc40		48398890010000		CMPQ 0x190(AX), CX							
  engine.go:4194	0x8acc47		7e24			JLE 0x8acc6d								
  engine.go:4195	0x8acc49		e8b2070000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).takeSpareDirty(SB)		
  engine.go:4195	0x8acc4e		4885c0			TESTQ AX, AX								
  engine.go:4195	0x8acc51		7541			JNE 0x8acc94								
  engine.go:4198	0x8acc53		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4198	0x8acc5b		0f1f440000		NOPL 0(AX)(AX*1)							
  engine.go:4198	0x8acc60		e83bdeffff		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).syncDirtyFull(SB)		
  engine.go:4200	0x8acc65		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4200	0x8acc6d		488b4c2450		MOVQ 0x50(SP), CX							
  engine.go:4200	0x8acc72		48894858		MOVQ CX, 0x58(AX)							
  engine.go:4201	0x8acc76		488d0565683900		LEAQ 0x396865(IP), AX							
  engine.go:4201	0x8acc7d		bb0d000000		MOVL $0xd, BX								
  engine.go:4201	0x8acc82		e8993cdaff		CALL github.com/hazyhaar/pkg/cihook55.Fire(SB)				
  engine.go:4202	0x8acc87		31c0			XORL AX, AX								
  engine.go:4202	0x8acc89		31db			XORL BX, BX								
  engine.go:4202	0x8acc8b		4881c420010000		ADDQ $0x120, SP								
  engine.go:4202	0x8acc92		5d			POPQ BP									
  engine.go:4202	0x8acc93		c3			RET									
  engine.go:4196	0x8acc94		4881c420010000		ADDQ $0x120, SP								
  engine.go:4196	0x8acc9b		5d			POPQ BP									
  engine.go:4196	0x8acc9c		c3			RET									
  engine.go:4194	0x8acc9d		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4194	0x8acca5		eb92			JMP 0x8acc39								
  engine.go:4168	0x8acca7		90			NOPL									
  proc.go:403		0x8acca8		488d05f9a0a400		LEAQ 0xa4a0f9(IP), AX							
  proc.go:403		0x8accaf		e88c52beff		CALL runtime.mcall(SB)							
  type.go:80		0x8accb4		488b8424c8000000	MOVQ 0xc8(SP), AX							
  engine.go:4170	0x8accbc		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4172	0x8accc4		488b542458		MOVQ 0x58(SP), DX							
  engine.go:4172	0x8accc9		488bb424a0000000	MOVQ 0xa0(SP), SI							
  engine.go:4173	0x8accd1		488b7c2460		MOVQ 0x60(SP), DI							
  engine.go:4170	0x8accd6		4c8b442478		MOVQ 0x78(SP), R8							
  engine.go:4170	0x8accdb		4c8b8c24a8000000	MOVQ 0xa8(SP), R9							
  type.go:80		0x8acce3		8b5834			MOVL 0x34(AX), BX							
  engine.go:4167	0x8acce6		85db			TESTL BX, BX								
  engine.go:4167	0x8acce8		75bd			JNE 0x8acca7								
  engine.go:4170	0x8accea		488b5808		MOVQ 0x8(AX), BX							
  engine.go:4170	0x8accee		4c8b9190010000		MOVQ 0x190(CX), R10							
  engine.go:4170	0x8accf5		4939da			CMPQ R10, BX								
  engine.go:4170	0x8accf8		7f54			JG 0x8acd4e								
  engine.go:4170	0x8accfa		660f1f440000		NOPW 0(AX)(AX*1)							
  engine.go:4170	0x8acd00		4885db			TESTQ BX, BX								
  engine.go:4170	0x8acd03		0f862e010000		JBE 0x8ace37								
  engine.go:4170	0x8acd09		4d85c0			TESTQ R8, R8								
  engine.go:4170	0x8acd0c		0f8620010000		JBE 0x8ace32								
  engine.go:4170	0x8acd12		4c8b18			MOVQ 0(AX), R11								
  engine.go:4170	0x8acd15		4d39d9			CMPQ R9, R11								
  engine.go:4170	0x8acd18		7434			JE 0x8acd4e								
  engine.go:4170	0x8acd1a		488b5010		MOVQ 0x10(AX), DX							
  engine.go:4171	0x8acd1e		48895938		MOVQ BX, 0x38(CX)							
  engine.go:4171	0x8acd22		48895140		MOVQ DX, 0x40(CX)							
  engine.go:4171	0x8acd26		833d038aab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4171	0x8acd2d		7416			JE 0x8acd45								
  engine.go:4171	0x8acd2f		488b5130		MOVQ 0x30(CX), DX							
  engine.go:4170	0x8acd33		4c89d8			MOVQ R11, AX								
  engine.go:4171	0x8acd36		e8056ebeff		CALL runtime.gcWriteBarrier2(SB)					
  engine.go:4171	0x8acd3b		498903			MOVQ AX, 0(R11)								
  engine.go:4171	0x8acd3e		49895308		MOVQ DX, 0x8(R11)							
  engine.go:4171	0x8acd42		4989c3			MOVQ AX, R11								
  engine.go:4171	0x8acd45		4c895930		MOVQ R11, 0x30(CX)							
  engine.go:4171	0x8acd49		e9c4000000		JMP 0x8ace12								
  engine.go:4172	0x8acd4e		4939d2			CMPQ R10, DX								
  engine.go:4172	0x8acd51		7f42			JG 0x8acd95								
  engine.go:4172	0x8acd53		4885d2			TESTQ DX, DX								
  engine.go:4172	0x8acd56		0f86d1000000		JBE 0x8ace2d								
  engine.go:4172	0x8acd5c		0f1f4000		NOPL 0(AX)								
  engine.go:4172	0x8acd60		4d85c0			TESTQ R8, R8								
  engine.go:4172	0x8acd63		0f86bf000000		JBE 0x8ace28								
  engine.go:4172	0x8acd69		4939f1			CMPQ R9, SI								
  engine.go:4172	0x8acd6c		7427			JE 0x8acd95								
  engine.go:4173	0x8acd6e		48895138		MOVQ DX, 0x38(CX)							
  engine.go:4173	0x8acd72		48897940		MOVQ DI, 0x40(CX)							
  engine.go:4173	0x8acd76		833db389ab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4173	0x8acd7d		7410			JE 0x8acd8f								
  engine.go:4173	0x8acd7f		488b5130		MOVQ 0x30(CX), DX							
  engine.go:4173	0x8acd83		e8b86dbeff		CALL runtime.gcWriteBarrier2(SB)					
  engine.go:4173	0x8acd88		498933			MOVQ SI, 0(R11)								
  engine.go:4173	0x8acd8b		49895308		MOVQ DX, 0x8(R11)							
  engine.go:4173	0x8acd8f		48897130		MOVQ SI, 0x30(CX)							
  engine.go:4173	0x8acd93		eb7d			JMP 0x8ace12								
  engine.go:4174	0x8acd95		488b9108010000		MOVQ 0x108(CX), DX							
  engine.go:4174	0x8acd9c		0f1f4000		NOPL 0(AX)								
  engine.go:4174	0x8acda0		4c39d2			CMPQ DX, R10								
  engine.go:4174	0x8acda3		7c53			JL 0x8acdf8								
  engine.go:4174	0x8acda5		488b9910010000		MOVQ 0x110(CX), BX							
  engine.go:4174	0x8acdac		488bb100010000		MOVQ 0x100(CX), SI							
  engine.go:4175	0x8acdb3		48895138		MOVQ DX, 0x38(CX)							
  engine.go:4175	0x8acdb7		48895940		MOVQ BX, 0x40(CX)							
  engine.go:4175	0x8acdbb		833d6e89ab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4175	0x8acdc2		741b			JE 0x8acddf								
  engine.go:4175	0x8acdc4		488b5130		MOVQ 0x30(CX), DX							
  engine.go:4176	0x8acdc8		488b9900010000		MOVQ 0x100(CX), BX							
  engine.go:4175	0x8acdcf		e88c6dbeff		CALL runtime.gcWriteBarrier3(SB)					
  engine.go:4175	0x8acdd4		498933			MOVQ SI, 0(R11)								
  engine.go:4175	0x8acdd7		49895308		MOVQ DX, 0x8(R11)							
  engine.go:4176	0x8acddb		49895b10		MOVQ BX, 0x10(R11)							
  engine.go:4175	0x8acddf		48897130		MOVQ SI, 0x30(CX)							
  engine.go:4176	0x8acde3		440f11b908010000	MOVUPS X15, 0x108(CX)							
  engine.go:4176	0x8acdeb		48c7810001000000000000	MOVQ $0x0, 0x100(CX)							
  engine.go:4176	0x8acdf6		eb1a			JMP 0x8ace12								
  engine.go:4178	0x8acdf8		4889c8			MOVQ CX, AX								
  engine.go:4178	0x8acdfb		0f1f440000		NOPL 0(AX)(AX*1)							
  engine.go:4178	0x8ace00		e8fb050000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).takeSpareDirty(SB)		
  engine.go:4178	0x8ace05		4885c0			TESTQ AX, AX								
  engine.go:4178	0x8ace08		7515			JNE 0x8ace1f								
  engine.go:4182	0x8ace0a		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4182	0x8ace12		4889c8			MOVQ CX, AX								
  engine.go:4182	0x8ace15		e8a6d5ffff		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).convergeDirtyFromPub(SB)	
  engine.go:4182	0x8ace1a		e97efeffff		JMP 0x8acc9d								
  engine.go:4179	0x8ace1f		4881c420010000		ADDQ $0x120, SP								
  engine.go:4179	0x8ace26		5d			POPQ BP									
  engine.go:4179	0x8ace27		c3			RET									
  engine.go:4172	0x8ace28		e8b370beff		CALL runtime.panicBounds(SB)						
  engine.go:4172	0x8ace2d		e8ae70beff		CALL runtime.panicBounds(SB)						
  engine.go:4170	0x8ace32		e8a970beff		CALL runtime.panicBounds(SB)						
  engine.go:4170	0x8ace37		e8a470beff		CALL runtime.panicBounds(SB)						
  engine.go:4147	0x8ace3c		48895c2468		MOVQ BX, 0x68(SP)							
  engine.go:4148	0x8ace41		90			NOPL									
  proc.go:403		0x8ace42		488d055f9fa400		LEAQ 0xa49f5f(IP), AX							
  proc.go:403		0x8ace49		e8f250beff		CALL runtime.mcall(SB)							
  engine.go:4147	0x8ace4e		488b5c2468		MOVQ 0x68(SP), BX							
  engine.go:4147	0x8ace53		48ffc3			INCQ BX									
  type.go:80		0x8ace56		488b8424c8000000	MOVQ 0xc8(SP), AX							
  engine.go:4154	0x8ace5e		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4172	0x8ace66		488b542458		MOVQ 0x58(SP), DX							
  engine.go:4172	0x8ace6b		488bb424a0000000	MOVQ 0xa0(SP), SI							
  engine.go:4173	0x8ace73		488b7c2460		MOVQ 0x60(SP), DI							
  engine.go:4170	0x8ace78		4c8b442478		MOVQ 0x78(SP), R8							
  engine.go:4170	0x8ace7d		4c8b8c24a8000000	MOVQ 0xa8(SP), R9							
  engine.go:4147	0x8ace85		4883fb40		CMPQ BX, $0x40								
  engine.go:4147	0x8ace89		7d0d			JGE 0x8ace98								
  type.go:80		0x8ace8b		448b5034		MOVL 0x34(AX), R10							
  engine.go:4147	0x8ace8f		4585d2			TESTL R10, R10								
  engine.go:4147	0x8ace92		410f95c2		SETNE R10								
  engine.go:4147	0x8ace96		eb08			JMP 0x8acea0								
  engine.go:4147	0x8ace98		4531d2			XORL R10, R10								
  engine.go:4147	0x8ace9b		0f1f440000		NOPL 0(AX)(AX*1)							
  engine.go:4147	0x8acea0		4584d2			TESTL R10, R10								
  engine.go:4147	0x8acea3		7597			JNE 0x8ace3c								
  engine.go:4146	0x8acea5		4885c0			TESTQ AX, AX								
  engine.go:4147	0x8acea8		e996fbffff		JMP 0x8aca43								
  engine.go:4128	0x8acead		49ffc2			INCQ R10								
  engine.go:4128	0x8aceb0		4d39ca			CMPQ R10, R9								
  engine.go:4128	0x8aceb3		0f8d78faffff		JGE 0x8ac931								
  engine.go:4128	0x8aceb9		438b1c90		MOVL 0(R8)(R10*4), BX							
  engine.go:4128	0x8acebd		0f1f00			NOPL 0(AX)								
  engine.go:4130	0x8acec0		4c39db			CMPQ BX, R11								
  engine.go:4130	0x8acec3		73e8			JAE 0x8acead								
  engine.go:4133	0x8acec5		4189dc			MOVL BX, R12								
  engine.go:4133	0x8acec8		49c1e40e		SHLQ $0xe, R12								
  engine.go:4134	0x8acecc		4d8dac2400400000	LEAQ 0x4000(R12), R13							
  engine.go:4134	0x8aced4		488bb42480000000	MOVQ 0x80(SP), SI							
  engine.go:4134	0x8acedc		0f1f4000		NOPL 0(AX)								
  engine.go:4134	0x8acee0		4c39ee			CMPQ SI, R13								
  engine.go:4134	0x8acee3		0f82a1010000		JB 0x8ad08a								
  engine.go:4128	0x8acee9		4c89942490000000	MOVQ R10, 0x90(SP)							
  engine.go:4134	0x8acef1		488b4208		MOVQ 0x8(DX), AX							
  engine.go:4134	0x8acef5		48c1e302		SHLQ $0x2, BX								
  engine.go:4134	0x8acef9		4889f2			MOVQ SI, DX								
  engine.go:4134	0x8acefc		4c29e2			SUBQ R12, DX								
  engine.go:4134	0x8aceff		4c8b8424a8000000	MOVQ 0xa8(SP), R8							
  engine.go:4134	0x8acf07		4b8d0c04		LEAQ 0(R12)(R8*1), CX							
  engine.go:4134	0x8acf0b		bf00400000		MOVL $0x4000, DI							
  engine.go:4134	0x8acf10		4889d6			MOVQ DX, SI								
  engine.go:4134	0x8acf13		e8887d0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Pager).PutPage(SB)		
  engine.go:4134	0x8acf18		4885c0			TESTQ AX, AX								
  engine.go:4134	0x8acf1b		752a			JNE 0x8acf47								
  engine.go:4134	0x8acf1d		488b942430010000	MOVQ 0x130(SP), DX							
  engine.go:4128	0x8acf25		4c8b8424d0000000	MOVQ 0xd0(SP), R8							
  engine.go:4128	0x8acf2d		4c8b8c2498000000	MOVQ 0x98(SP), R9							
  engine.go:4128	0x8acf35		4c8b942490000000	MOVQ 0x90(SP), R10							
  engine.go:4130	0x8acf3d		4c8b5c2450		MOVQ 0x50(SP), R11							
  engine.go:4134	0x8acf42		e966ffffff		JMP 0x8acead								
  engine.go:4134	0x8acf47		48899c24c0000000	MOVQ BX, 0xc0(SP)							
  engine.go:4134	0x8acf4f		4889842488000000	MOVQ AX, 0x88(SP)							
  engine.go:4135	0x8acf57		b810000000		MOVL $0x10, AX								
  engine.go:4135	0x8acf5c		488d1ddd229f00		LEAQ 0x9f22dd(IP), BX							
  engine.go:4135	0x8acf63		b901000000		MOVL $0x1, CX								
  engine.go:4135	0x8acf68		e89351b7ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)				
  engine.go:4135	0x8acf6d		488b942488000000	MOVQ 0x88(SP), DX							
  engine.go:4135	0x8acf75		488910			MOVQ DX, 0(AX)								
  engine.go:4135	0x8acf78		833db187ab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4135	0x8acf7f		90			NOPL									
  engine.go:4135	0x8acf80		750d			JNE 0x8acf8f								
  engine.go:4134	0x8acf82		4885d2			TESTQ DX, DX								
  engine.go:4135	0x8acf85		4c8b8424c0000000	MOVQ 0xc0(SP), R8							
  engine.go:4135	0x8acf8d		eb13			JMP 0x8acfa2								
  engine.go:4135	0x8acf8f		e88c6bbeff		CALL runtime.gcWriteBarrier1(SB)					
  engine.go:4135	0x8acf94		4c8b8424c0000000	MOVQ 0xc0(SP), R8							
  engine.go:4135	0x8acf9c		4d8903			MOVQ R8, 0(R11)								
  engine.go:4134	0x8acf9f		4885d2			TESTQ DX, DX								
  engine.go:4135	0x8acfa2		4c894008		MOVQ R8, 0x8(AX)							
  engine.go:1744	0x8acfa6		742d			JE 0x8acfd5								
  type.go:61		0x8acfa8		488b8c2430010000	MOVQ 0x130(SP), CX							
  type.go:61		0x8acfb0		4881c1e0010000		ADDQ $0x1e0, CX								
  engine.go:1745	0x8acfb7		90			NOPL									
  type.go:61		0x8acfb8		4889c3			MOVQ AX, BX								
  type.go:61		0x8acfbb		4889c8			MOVQ CX, AX								
  type.go:61		0x8acfbe		6690			NOPW									
  type.go:61		0x8acfc0		e8fbc6bdff		CALL sync/atomic.StorePointer(SB)					
  engine.go:4134	0x8acfc5		488b942488000000	MOVQ 0x88(SP), DX							
  engine.go:4136	0x8acfcd		4c8b8424c0000000	MOVQ 0xc0(SP), R8							
  engine.go:4136	0x8acfd5		488d8c24e0000000	LEAQ 0xe0(SP), CX							
  engine.go:4136	0x8acfdd		440f1139		MOVUPS X15, 0(CX)							
  engine.go:4136	0x8acfe1		440f117910		MOVUPS X15, 0x10(CX)							
  engine.go:4136	0x8acfe6		4c8b0d03eda700		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrPostCommit(SB), R9		
  engine.go:4136	0x8acfed		4d85c9			TESTQ R9, R9								
  engine.go:4136	0x8acff0		7409			JE 0x8acffb								
  engine.go:4136	0x8acff2		4d8b4908		MOVQ 0x8(R9), R9							
  engine.go:4134	0x8acff6		4885d2			TESTQ DX, DX								
  engine.go:4136	0x8acff9		eb03			JMP 0x8acffe								
  engine.go:4134	0x8acffb		4885d2			TESTQ DX, DX								
  engine.go:4136	0x8acffe		4c8b15f3eca700		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrPostCommit+8(SB), R10		
  engine.go:4136	0x8ad005		4c898c24e0000000	MOVQ R9, 0xe0(SP)							
  engine.go:4136	0x8ad00d		4c899424e8000000	MOVQ R10, 0xe8(SP)							
  engine.go:4136	0x8ad015		7406			JE 0x8ad01d								
  engine.go:4136	0x8ad017		488b4208		MOVQ 0x8(DX), AX							
  engine.go:4136	0x8ad01b		eb03			JMP 0x8ad020								
  engine.go:4136	0x8ad01d		4889d0			MOVQ DX, AX								
  engine.go:4136	0x8ad020		48898424f0000000	MOVQ AX, 0xf0(SP)							
  engine.go:4136	0x8ad028		4c898424f8000000	MOVQ R8, 0xf8(SP)							
  errors.go:26		0x8ad030		488d05d7853900		LEAQ 0x3985d7(IP), AX							
  errors.go:26		0x8ad037		bb0f000000		MOVL $0xf, BX								
  errors.go:26		0x8ad03c		bf02000000		MOVL $0x2, DI								
  errors.go:26		0x8ad041		89fe			MOVL DI, SI								
  errors.go:26		0x8ad043		e8d872c4ff		CALL fmt.errorf(SB)							
  errors.go:26		0x8ad048		4885c0			TESTQ AX, AX								
  errors.go:26		0x8ad04b		7534			JNE 0x8ad081								
  errors.go:31		0x8ad04d		90			NOPL									
  errors.go:65		0x8ad04e		b810000000		MOVL $0x10, AX								
  errors.go:65		0x8ad053		488d1dee5d9f00		LEAQ 0x9f5dee(IP), BX							
  errors.go:65		0x8ad05a		b901000000		MOVL $0x1, CX								
  errors.go:65		0x8ad05f		90			NOPL									
  errors.go:65		0x8ad060		e89b50b7ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)				
  errors.go:65		0x8ad065		48c740080f000000	MOVQ $0xf, 0x8(AX)							
  errors.go:65		0x8ad06d		488d159a853900		LEAQ 0x39859a(IP), DX							
  errors.go:65		0x8ad074		488910			MOVQ DX, 0(AX)								
  engine.go:4136	0x8ad077		4889c3			MOVQ AX, BX								
  engine.go:4136	0x8ad07a		488d05af42a400		LEAQ 0xa442af(IP), AX							
  engine.go:4136	0x8ad081		4881c420010000		ADDQ $0x120, SP								
  engine.go:4136	0x8ad088		5d			POPQ BP									
  engine.go:4136	0x8ad089		c3			RET									
  engine.go:4134	0x8ad08a		e8516ebeff		CALL runtime.panicBounds(SB)						
  engine.go:4128	0x8ad08f		e84c6ebeff		CALL runtime.panicBounds(SB)						
  engine.go:4112	0x8ad094		4d8d4c2401		LEAQ 0x1(R12), R9							
  engine.go:4112	0x8ad099		0f1f8000000000		NOPL 0(AX)								
  engine.go:4112	0x8ad0a0		4d39c1			CMPQ R9, R8								
  engine.go:4112	0x8ad0a3		0f8dd1000000		JGE 0x8ad17a								
  engine.go:4113	0x8ad0a9		4c8b9aa8010000		MOVQ 0x1a8(DX), R11							
  engine.go:4113	0x8ad0b0		4d39d9			CMPQ R9, R11								
  engine.go:4113	0x8ad0b3		0f831b020000		JAE 0x8ad2d4								
  engine.go:4112	0x8ad0b9		4c894c2448		MOVQ R9, 0x48(SP)							
  engine.go:4113	0x8ad0be		4c8b9aa0010000		MOVQ 0x1a0(DX), R11							
  engine.go:4113	0x8ad0c5		4f8b1ccb		MOVQ 0(R11)(R9*8), R11							
  engine.go:4116	0x8ad0c9		4d89cc			MOVQ R9, R12								
  engine.go:4116	0x8ad0cc		49c1e106		SHLQ $0x6, R9								
  engine.go:4116	0x8ad0d0		4c898c2490000000	MOVQ R9, 0x90(SP)							
  engine.go:4114	0x8ad0d8		eb07			JMP 0x8ad0e1								
  engine.go:4124	0x8ad0da		4d8d6bff		LEAQ -0x1(R11), R13							
  engine.go:4124	0x8ad0de		4d21eb			ANDQ R13, R11								
  engine.go:4114	0x8ad0e1		4d85db			TESTQ R11, R11								
  engine.go:4114	0x8ad0e4		74ae			JE 0x8ad094								
  engine.go:4115	0x8ad0e6		4d0fbceb		BSFQ R11, R13								
  engine.go:4116	0x8ad0ea		4b8d5c0d00		LEAQ 0(R13)(R9*1), BX							
  engine.go:4117	0x8ad0ef		4939da			CMPQ R10, BX								
  engine.go:4117	0x8ad0f2		76e6			JBE 0x8ad0da								
  engine.go:4118	0x8ad0f4		4989dd			MOVQ BX, R13								
  engine.go:4118	0x8ad0f7		49c1e50e		SHLQ $0xe, R13								
  engine.go:4119	0x8ad0fb		4d8dbd00400000		LEAQ 0x4000(R13), R15							
  engine.go:4119	0x8ad102		488bb42480000000	MOVQ 0x80(SP), SI							
  engine.go:4119	0x8ad10a		4c39fe			CMPQ SI, R15								
  engine.go:4119	0x8ad10d		0f82bc010000		JB 0x8ad2cf								
  engine.go:4119	0x8ad113		4d39fd			CMPQ R13, R15								
  engine.go:4119	0x8ad116		0f87ae010000		JA 0x8ad2ca								
  engine.go:4114	0x8ad11c		4c895c2440		MOVQ R11, 0x40(SP)							
  engine.go:4119	0x8ad121		488b4208		MOVQ 0x8(DX), AX							
  engine.go:4119	0x8ad125		48c1e302		SHLQ $0x2, BX								
  engine.go:4119	0x8ad129		4889f2			MOVQ SI, DX								
  engine.go:4119	0x8ad12c		4c29ea			SUBQ R13, DX								
  engine.go:4119	0x8ad12f		4c8b8424a8000000	MOVQ 0xa8(SP), R8							
  engine.go:4119	0x8ad137		4b8d4c0500		LEAQ 0(R13)(R8*1), CX							
  engine.go:4119	0x8ad13c		bf00400000		MOVL $0x4000, DI							
  engine.go:4119	0x8ad141		4889d6			MOVQ DX, SI								
  engine.go:4119	0x8ad144		e8577b0000		CALL github.com/hazyhaar/c2pkg/c2db.(*Pager).PutPage(SB)		
  engine.go:4119	0x8ad149		4885c0			TESTQ AX, AX								
  engine.go:4119	0x8ad14c		7537			JNE 0x8ad185								
  engine.go:4119	0x8ad14e		488b942430010000	MOVQ 0x130(SP), DX							
  engine.go:4112	0x8ad156		4c8b842498000000	MOVQ 0x98(SP), R8							
  engine.go:4116	0x8ad15e		4c8b8c2490000000	MOVQ 0x90(SP), R9							
  engine.go:4117	0x8ad166		4c8b542450		MOVQ 0x50(SP), R10							
  engine.go:4124	0x8ad16b		4c8b5c2440		MOVQ 0x40(SP), R11							
  engine.go:4112	0x8ad170		4c8b642448		MOVQ 0x48(SP), R12							
  engine.go:4119	0x8ad175		e960ffffff		JMP 0x8ad0da								
  engine.go:4200	0x8ad17a		4d89d3			MOVQ R10, R11								
  engine.go:4200	0x8ad17d		0f1f00			NOPL 0(AX)								
  engine.go:4142	0x8ad180		e9acf7ffff		JMP 0x8ac931								
  engine.go:4119	0x8ad185		48899c24c0000000	MOVQ BX, 0xc0(SP)							
  engine.go:4119	0x8ad18d		4889842488000000	MOVQ AX, 0x88(SP)							
  engine.go:4120	0x8ad195		b810000000		MOVL $0x10, AX								
  engine.go:4120	0x8ad19a		488d1d9f209f00		LEAQ 0x9f209f(IP), BX							
  engine.go:4120	0x8ad1a1		b901000000		MOVL $0x1, CX								
  engine.go:4120	0x8ad1a6		e8554fb7ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)				
  engine.go:4120	0x8ad1ab		488b942488000000	MOVQ 0x88(SP), DX							
  engine.go:4120	0x8ad1b3		488910			MOVQ DX, 0(AX)								
  engine.go:4120	0x8ad1b6		833d7385ab0000		CMPL runtime.writeBarrier(SB), $0x0					
  engine.go:4120	0x8ad1bd		750d			JNE 0x8ad1cc								
  engine.go:4119	0x8ad1bf		4885d2			TESTQ DX, DX								
  engine.go:4120	0x8ad1c2		4c8b8424c0000000	MOVQ 0xc0(SP), R8							
  engine.go:4120	0x8ad1ca		eb13			JMP 0x8ad1df								
  engine.go:4120	0x8ad1cc		e84f69beff		CALL runtime.gcWriteBarrier1(SB)					
  engine.go:4120	0x8ad1d1		4c8b8424c0000000	MOVQ 0xc0(SP), R8							
  engine.go:4120	0x8ad1d9		4d8903			MOVQ R8, 0(R11)								
  engine.go:4119	0x8ad1dc		4885d2			TESTQ DX, DX								
  engine.go:4120	0x8ad1df		4c894008		MOVQ R8, 0x8(AX)							
  engine.go:1744	0x8ad1e3		7430			JE 0x8ad215								
  type.go:61		0x8ad1e5		488b8c2430010000	MOVQ 0x130(SP), CX							
  type.go:61		0x8ad1ed		4881c1e0010000		ADDQ $0x1e0, CX								
  engine.go:1745	0x8ad1f4		90			NOPL									
  type.go:61		0x8ad1f5		4889c3			MOVQ AX, BX								
  type.go:61		0x8ad1f8		4889c8			MOVQ CX, AX								
  type.go:61		0x8ad1fb		0f1f440000		NOPL 0(AX)(AX*1)							
  type.go:61		0x8ad200		e8bbc4bdff		CALL sync/atomic.StorePointer(SB)					
  engine.go:4119	0x8ad205		488b942488000000	MOVQ 0x88(SP), DX							
  engine.go:4121	0x8ad20d		4c8b8424c0000000	MOVQ 0xc0(SP), R8							
  engine.go:4121	0x8ad215		488d8c2400010000	LEAQ 0x100(SP), CX							
  engine.go:4121	0x8ad21d		440f1139		MOVUPS X15, 0(CX)							
  engine.go:4121	0x8ad221		440f117910		MOVUPS X15, 0x10(CX)							
  engine.go:4121	0x8ad226		4c8b0dc3eaa700		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrPostCommit(SB), R9		
  engine.go:4121	0x8ad22d		4d85c9			TESTQ R9, R9								
  engine.go:4121	0x8ad230		7409			JE 0x8ad23b								
  engine.go:4121	0x8ad232		4d8b4908		MOVQ 0x8(R9), R9							
  engine.go:4119	0x8ad236		4885d2			TESTQ DX, DX								
  engine.go:4121	0x8ad239		eb03			JMP 0x8ad23e								
  engine.go:4119	0x8ad23b		4885d2			TESTQ DX, DX								
  engine.go:4121	0x8ad23e		4c8b15b3eaa700		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrPostCommit+8(SB), R10		
  engine.go:4121	0x8ad245		4c898c2400010000	MOVQ R9, 0x100(SP)							
  engine.go:4121	0x8ad24d		4c89942408010000	MOVQ R10, 0x108(SP)							
  engine.go:4121	0x8ad255		7406			JE 0x8ad25d								
  engine.go:4121	0x8ad257		488b4208		MOVQ 0x8(DX), AX							
  engine.go:4121	0x8ad25b		eb03			JMP 0x8ad260								
  engine.go:4121	0x8ad25d		4889d0			MOVQ DX, AX								
  engine.go:4121	0x8ad260		4889842410010000	MOVQ AX, 0x110(SP)							
  engine.go:4121	0x8ad268		4c89842418010000	MOVQ R8, 0x118(SP)							
  errors.go:26		0x8ad270		488d0597833900		LEAQ 0x398397(IP), AX							
  errors.go:26		0x8ad277		bb0f000000		MOVL $0xf, BX								
  errors.go:26		0x8ad27c		bf02000000		MOVL $0x2, DI								
  errors.go:26		0x8ad281		89fe			MOVL DI, SI								
  errors.go:26		0x8ad283		e89870c4ff		CALL fmt.errorf(SB)							
  errors.go:26		0x8ad288		4885c0			TESTQ AX, AX								
  errors.go:26		0x8ad28b		7534			JNE 0x8ad2c1								
  errors.go:31		0x8ad28d		90			NOPL									
  errors.go:65		0x8ad28e		b810000000		MOVL $0x10, AX								
  errors.go:65		0x8ad293		488d1dae5b9f00		LEAQ 0x9f5bae(IP), BX							
  errors.go:65		0x8ad29a		b901000000		MOVL $0x1, CX								
  errors.go:65		0x8ad29f		90			NOPL									
  errors.go:65		0x8ad2a0		e85b4eb7ff		CALL runtime.mallocgcSmallScanNoHeaderSC2(SB)				
  errors.go:65		0x8ad2a5		48c740080f000000	MOVQ $0xf, 0x8(AX)							
  errors.go:65		0x8ad2ad		488d155a833900		LEAQ 0x39835a(IP), DX							
  errors.go:65		0x8ad2b4		488910			MOVQ DX, 0(AX)								
  engine.go:4121	0x8ad2b7		4889c3			MOVQ AX, BX								
  engine.go:4121	0x8ad2ba		488d056f40a400		LEAQ 0xa4406f(IP), AX							
  engine.go:4121	0x8ad2c1		4881c420010000		ADDQ $0x120, SP								
  engine.go:4121	0x8ad2c8		5d			POPQ BP									
  engine.go:4121	0x8ad2c9		c3			RET									
  engine.go:4119	0x8ad2ca		e8116cbeff		CALL runtime.panicBounds(SB)						
  engine.go:4119	0x8ad2cf		e80c6cbeff		CALL runtime.panicBounds(SB)						
  engine.go:4113	0x8ad2d4		e8076cbeff		CALL runtime.panicBounds(SB)						
  engine.go:3547	0x8ad2d9		e8026cbeff		CALL runtime.panicBounds(SB)						
  engine.go:4104	0x8ad2de		b800400000		MOVL $0x4000, AX							
  engine.go:4104	0x8ad2e3		e8f86bbeff		CALL runtime.panicBounds(SB)						
  engine.go:4083	0x8ad2e8		90			NOPL									
  proc.go:403		0x8ad2e9		488d05b89aa400		LEAQ 0xa49ab8(IP), AX							
  proc.go:403		0x8ad2f0		e84b4cbeff		CALL runtime.mcall(SB)							
  engine.go:4078	0x8ad2f5		488b4c2470		MOVQ 0x70(SP), CX							
  engine.go:4078	0x8ad2fa		48ffc1			INCQ CX									
  engine.go:4078	0x8ad2fd		488b842430010000	MOVQ 0x130(SP), AX							
  engine.go:4078	0x8ad305		4881f900010000		CMPQ CX, $0x100								
  engine.go:4078	0x8ad30c		7d2b			JGE 0x8ad339								
  engine.go:4078	0x8ad30e		4883b8f000000008	CMPQ 0xf0(AX), $0x8							
  engine.go:4078	0x8ad316		7c21			JL 0x8ad339								
  engine.go:4078	0x8ad318		48894c2470		MOVQ CX, 0x70(SP)							
  engine.go:4078	0x8ad31d		0f1f00			NOPL 0(AX)								
  engine.go:4079	0x8ad320		e8db010000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).reapHolds(SB)		
  engine.go:4080	0x8ad325		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4080	0x8ad32d		4883b9f000000008	CMPQ 0xf0(CX), $0x8							
  engine.go:4080	0x8ad335		7db1			JGE 0x8ad2e8								
  engine.go:4080	0x8ad337		eb03			JMP 0x8ad33c								
  engine.go:4085	0x8ad339		4889c1			MOVQ AX, CX								
  engine.go:4085	0x8ad33c		488b81f0000000		MOVQ 0xf0(CX), AX							
  engine.go:4085	0x8ad343		4883f808		CMPQ AX, $0x8								
  engine.go:4085	0x8ad347		7d08			JGE 0x8ad351								
  engine.go:4091	0x8ad349		4889c8			MOVQ CX, AX								
  engine.go:4085	0x8ad34c		e968f4ffff		JMP 0x8ac7b9								
  engine.go:4086	0x8ad351		440f11bc24b0000000	MOVUPS X15, 0xb0(SP)							
  engine.go:4086	0x8ad35a		e801cabdff		CALL runtime.convT64(SB)						
  engine.go:4086	0x8ad35f		488d0d9a709e00		LEAQ 0x9e709a(IP), CX							
  engine.go:4086	0x8ad366		48898c24b0000000	MOVQ CX, 0xb0(SP)							
  engine.go:4086	0x8ad36e		48898424b8000000	MOVQ AX, 0xb8(SP)							
  engine.go:4086	0x8ad376		488d05572b3c00		LEAQ 0x3c2b57(IP), AX							
  engine.go:4086	0x8ad37d		bb31000000		MOVL $0x31, BX								
  engine.go:4086	0x8ad382		488d8c24b0000000	LEAQ 0xb0(SP), CX							
  engine.go:4086	0x8ad38a		bf01000000		MOVL $0x1, DI								
  engine.go:4086	0x8ad38f		89fe			MOVL DI, SI								
  engine.go:4086	0x8ad391		e8ea9ec4ff		CALL fmt.Sprintf(SB)							
  engine.go:4086	0x8ad396		488b8c2430010000	MOVQ 0x130(SP), CX							
  engine.go:4086	0x8ad39e		0fb74968		MOVZX 0x68(CX), CX							
  engine.go:4086	0x8ad3a2		31ff			XORL DI, DI								
  engine.go:4086	0x8ad3a4		31f6			XORL SI, SI								
  engine.go:4086	0x8ad3a6		4531c0			XORL R8, R8								
  engine.go:4086	0x8ad3a9		4989c1			MOVQ AX, R9								
  engine.go:4086	0x8ad3ac		4989da			MOVQ BX, R10								
  engine.go:4086	0x8ad3af		89c8			MOVL CX, AX								
  engine.go:4086	0x8ad3b1		bb0f000000		MOVL $0xf, BX								
  engine.go:4086	0x8ad3b6		89f9			MOVL DI, CX								
  engine.go:4086	0x8ad3b8		e823960000		CALL github.com/hazyhaar/c2pkg/c2db.probeEmit(SB)			
  engine.go:4087	0x8ad3bd		488b051ce7a700		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrViewHeld(SB), AX			
  engine.go:4087	0x8ad3c4		488b1d1de7a700		MOVQ github.com/hazyhaar/c2pkg/c2db.ErrViewHeld+8(SB), BX		
  engine.go:4087	0x8ad3cb		4881c420010000		ADDQ $0x120, SP								
  engine.go:4087	0x8ad3d2		5d			POPQ BP									
  engine.go:4087	0x8ad3d3		c3			RET									
  engine.go:4068	0x8ad3d4		4889442408		MOVQ AX, 0x8(SP)							
  engine.go:4068	0x8ad3d9		e8624dbeff		CALL runtime.morestack_noctxt.abi0(SB)					
  engine.go:4068	0x8ad3de		488b442408		MOVQ 0x8(SP), AX							
  engine.go:4068	0x8ad3e3		e998f2ffff		JMP github.com/hazyhaar/c2pkg/c2db.(*Shard).publish(SB)			
