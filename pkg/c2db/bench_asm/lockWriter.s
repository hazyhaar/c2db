TEXT github.com/hazyhaar/c2pkg/c2db.(*Shard).lockWriter(SB) /devhoros/c2simd/c2pkg/c2db/engine.go
  engine.go:645		0x89b1a0		493b6610		CMPQ SP, 0x10(R14)						
  engine.go:645		0x89b1a4		0f86f3000000		JBE 0x89b29d							
  engine.go:645		0x89b1aa		55			PUSHQ BP							
  engine.go:645		0x89b1ab		4889e5			MOVQ SP, BP							
  engine.go:645		0x89b1ae		4883ec28		SUBQ $0x28, SP							
  engine.go:647		0x89b1b2		4889442438		MOVQ AX, 0x38(SP)						
  engine.go:646		0x89b1b7		e884feffff		CALL github.com/hazyhaar/c2pkg/c2db.goid(SB)			
  engine.go:647		0x89b1bc		488b4c2438		MOVQ 0x38(SP), CX						
  engine.go:647		0x89b1c1		488b91c0000000		MOVQ 0xc0(CX), DX						
  engine.go:647		0x89b1c8		4839d0			CMPQ AX, DX							
  engine.go:647		0x89b1cb		0f84bb000000		JE 0x89b28c							
  engine.go:646		0x89b1d1		4889442410		MOVQ AX, 0x10(SP)						
  engine.go:651		0x89b1d6		4889c8			MOVQ CX, AX							
  engine.go:651		0x89b1d9		e882010000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).tryWriteMu(SB)	
  engine.go:651		0x89b1de		6690			NOPW								
  engine.go:651		0x89b1e0		4885c0			TESTQ AX, AX							
  engine.go:651		0x89b1e3		0f859d000000		JNE 0x89b286							
  engine.go:654		0x89b1e9		488b442438		MOVQ 0x38(SP), AX						
  engine.go:654		0x89b1ee		e8cd030000		CALL github.com/hazyhaar/c2pkg/c2db.(*Shard).lockOFD(SB)	
  engine.go:654		0x89b1f3		4885c0			TESTQ AX, AX							
  engine.go:654		0x89b1f6		743f			JE 0x89b237							
  engine.go:655		0x89b1f8		90			NOPL								
  mutex.go:194		0x89b1f9		b9ffffffff		MOVL $-0x1, CX							
  mutex.go:194		0x89b1fe		488b542438		MOVQ 0x38(SP), DX						
  mutex.go:194		0x89b203		f00fc18ab4000000	LOCK XADDL CX, 0xb4(DX)						
  mutex.go:194		0x89b20b		ffc9			DECL CX								
  mutex.go:195		0x89b20d		7422			JE 0x89b231							
  engine.go:654		0x89b20f		48895c2420		MOVQ BX, 0x20(SP)						
  engine.go:654		0x89b214		4889442418		MOVQ AX, 0x18(SP)						
  mutex.go:65		0x89b219		488d82b4000000		LEAQ 0xb4(DX), AX						
  mutex.go:198		0x89b220		89cb			MOVL CX, BX							
  mutex.go:198		0x89b222		e819fcbfff		CALL internal/sync.(*Mutex).unlockSlow(SB)			
  engine.go:656		0x89b227		488b442418		MOVQ 0x18(SP), AX						
  engine.go:656		0x89b22c		488b5c2420		MOVQ 0x20(SP), BX						
  engine.go:656		0x89b231		4883c428		ADDQ $0x28, SP							
  engine.go:656		0x89b235		5d			POPQ BP								
  engine.go:656		0x89b236		c3			RET								
  engine.go:658		0x89b237		90			NOPL								
  mutex.go:63		0x89b238		31c0			XORL AX, AX							
  mutex.go:63		0x89b23a		488b4c2438		MOVQ 0x38(SP), CX						
  mutex.go:63		0x89b23f		ba01000000		MOVL $0x1, DX							
  mutex.go:63		0x89b244		f00fb191d0000000	LOCK CMPXCHGL DX, 0xd0(CX)					
  mutex.go:63		0x89b24c		0f94c2			SETE DL								
  mutex.go:63		0x89b24f		84d2			TESTL DL, DL							
  mutex.go:63		0x89b251		7511			JNE 0x89b264							
  mutex.go:46		0x89b253		488d81d0000000		LEAQ 0xd0(CX), AX						
  mutex.go:70		0x89b25a		e801f9bfff		CALL internal/sync.(*Mutex).lockSlow(SB)			
  type.go:184		0x89b25f		488b4c2438		MOVQ 0x38(SP), CX						
  engine.go:659		0x89b264		90			NOPL								
  type.go:184		0x89b265		488b542410		MOVQ 0x10(SP), DX						
  type.go:184		0x89b26a		488791c0000000		XCHGQ DX, 0xc0(CX)						
  engine.go:660		0x89b271		48c781c800000001000000	MOVQ $0x1, 0xc8(CX)						
  engine.go:661		0x89b27c		31c0			XORL AX, AX							
  engine.go:661		0x89b27e		31db			XORL BX, BX							
  engine.go:661		0x89b280		4883c428		ADDQ $0x28, SP							
  engine.go:661		0x89b284		5d			POPQ BP								
  engine.go:661		0x89b285		c3			RET								
  engine.go:652		0x89b286		4883c428		ADDQ $0x28, SP							
  engine.go:652		0x89b28a		5d			POPQ BP								
  engine.go:652		0x89b28b		c3			RET								
  engine.go:648		0x89b28c		48ff81c8000000		INCQ 0xc8(CX)							
  engine.go:649		0x89b293		31c0			XORL AX, AX							
  engine.go:649		0x89b295		31db			XORL BX, BX							
  engine.go:649		0x89b297		4883c428		ADDQ $0x28, SP							
  engine.go:649		0x89b29b		5d			POPQ BP								
  engine.go:649		0x89b29c		c3			RET								
  engine.go:645		0x89b29d		4889442408		MOVQ AX, 0x8(SP)						
  engine.go:645		0x89b2a2		e8996ebfff		CALL runtime.morestack_noctxt.abi0(SB)				
  engine.go:645		0x89b2a7		488b442408		MOVQ 0x8(SP), AX						
  engine.go:645		0x89b2ac		e9effeffff		JMP github.com/hazyhaar/c2pkg/c2db.(*Shard).lockWriter(SB)	
