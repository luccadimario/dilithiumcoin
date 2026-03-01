package main

type Opcode byte

const (
	OpSTOP Opcode = 0x00

	// Arithmetic
	OpADD    Opcode = 0x01
	OpSUB    Opcode = 0x02
	OpMUL    Opcode = 0x03
	OpDIV    Opcode = 0x04
	OpMOD    Opcode = 0x05
	OpADDMOD Opcode = 0x06
	OpMULMOD Opcode = 0x07
	OpEXP    Opcode = 0x08

	// Comparison
	OpLT     Opcode = 0x10
	OpGT     Opcode = 0x11
	OpEQ     Opcode = 0x12
	OpISZERO Opcode = 0x13

	// Bitwise
	OpAND  Opcode = 0x16
	OpOR   Opcode = 0x17
	OpXOR  Opcode = 0x18
	OpNOT  Opcode = 0x19
	OpBYTE Opcode = 0x1A
	OpSHL  Opcode = 0x1B
	OpSHR  Opcode = 0x1C

	// Crypto
	OpSHA256 Opcode = 0x20

	// Context
	OpADDRESS      Opcode = 0x30
	OpBALANCE      Opcode = 0x31
	OpORIGIN       Opcode = 0x32
	OpCALLER       Opcode = 0x33
	OpCALLVALUE    Opcode = 0x34
	OpCALLDATALOAD Opcode = 0x35
	OpCALLDATASIZE Opcode = 0x36
	OpCALLDATACOPY Opcode = 0x37
	OpCODESIZE     Opcode = 0x38
	OpCODECOPY     Opcode = 0x39

	// Block info
	OpTIMESTAMP  Opcode = 0x42
	OpNUMBER     Opcode = 0x43
	OpDIFFICULTY Opcode = 0x44
	OpGASLIMIT   Opcode = 0x45

	// Stack, Memory, Storage
	OpPOP     Opcode = 0x50
	OpMLOAD   Opcode = 0x51
	OpMSTORE  Opcode = 0x52
	OpMSTORE8 Opcode = 0x53
	OpSLOAD   Opcode = 0x54
	OpSSTORE  Opcode = 0x55

	// Flow control
	OpJUMP     Opcode = 0x56
	OpJUMPI    Opcode = 0x57
	OpMSIZE    Opcode = 0x59
	OpGAS      Opcode = 0x5A
	OpJUMPDEST Opcode = 0x5B

	// Push
	OpPUSH1  Opcode = 0x60
	OpPUSH2  Opcode = 0x61
	OpPUSH3  Opcode = 0x62
	OpPUSH4  Opcode = 0x63
	OpPUSH5  Opcode = 0x64
	OpPUSH6  Opcode = 0x65
	OpPUSH7  Opcode = 0x66
	OpPUSH8  Opcode = 0x67
	OpPUSH9  Opcode = 0x68
	OpPUSH10 Opcode = 0x69
	OpPUSH11 Opcode = 0x6A
	OpPUSH12 Opcode = 0x6B
	OpPUSH13 Opcode = 0x6C
	OpPUSH14 Opcode = 0x6D
	OpPUSH15 Opcode = 0x6E
	OpPUSH16 Opcode = 0x6F
	OpPUSH17 Opcode = 0x70
	OpPUSH18 Opcode = 0x71
	OpPUSH19 Opcode = 0x72
	OpPUSH20 Opcode = 0x73
	OpPUSH21 Opcode = 0x74
	OpPUSH22 Opcode = 0x75
	OpPUSH23 Opcode = 0x76
	OpPUSH24 Opcode = 0x77
	OpPUSH25 Opcode = 0x78
	OpPUSH26 Opcode = 0x79
	OpPUSH27 Opcode = 0x7A
	OpPUSH28 Opcode = 0x7B
	OpPUSH29 Opcode = 0x7C
	OpPUSH30 Opcode = 0x7D
	OpPUSH31 Opcode = 0x7E
	OpPUSH32 Opcode = 0x7F

	// Dup
	OpDUP1  Opcode = 0x80
	OpDUP2  Opcode = 0x81
	OpDUP3  Opcode = 0x82
	OpDUP4  Opcode = 0x83
	OpDUP5  Opcode = 0x84
	OpDUP6  Opcode = 0x85
	OpDUP7  Opcode = 0x86
	OpDUP8  Opcode = 0x87
	OpDUP9  Opcode = 0x88
	OpDUP10 Opcode = 0x89
	OpDUP11 Opcode = 0x8A
	OpDUP12 Opcode = 0x8B
	OpDUP13 Opcode = 0x8C
	OpDUP14 Opcode = 0x8D
	OpDUP15 Opcode = 0x8E
	OpDUP16 Opcode = 0x8F

	// Swap
	OpSWAP1  Opcode = 0x90
	OpSWAP2  Opcode = 0x91
	OpSWAP3  Opcode = 0x92
	OpSWAP4  Opcode = 0x93
	OpSWAP5  Opcode = 0x94
	OpSWAP6  Opcode = 0x95
	OpSWAP7  Opcode = 0x96
	OpSWAP8  Opcode = 0x97
	OpSWAP9  Opcode = 0x98
	OpSWAP10 Opcode = 0x99
	OpSWAP11 Opcode = 0x9A
	OpSWAP12 Opcode = 0x9B
	OpSWAP13 Opcode = 0x9C
	OpSWAP14 Opcode = 0x9D
	OpSWAP15 Opcode = 0x9E
	OpSWAP16 Opcode = 0x9F

	// Logging
	OpLOG0 Opcode = 0xA0
	OpLOG1 Opcode = 0xA1
	OpLOG2 Opcode = 0xA2
	OpLOG3 Opcode = 0xA3
	OpLOG4 Opcode = 0xA4

	// System
	OpCALL         Opcode = 0xF1
	OpRETURN       Opcode = 0xF3
	OpREVERT       Opcode = 0xFD
	OpSELFDESTRUCT Opcode = 0xFF
)

type OpcodeInfo struct {
	Name     string
	GasCost  uint64
	StackIn  int
	StackOut int
}

var opcodeTable [256]OpcodeInfo

func init() {
	// Default: all unrecognized opcodes are invalid (gas 0 signals invalid)
	for i := range opcodeTable {
		opcodeTable[i] = OpcodeInfo{Name: "INVALID", GasCost: 0, StackIn: 0, StackOut: 0}
	}

	opcodeTable[OpSTOP] = OpcodeInfo{"STOP", 0, 0, 0}

	// Arithmetic
	opcodeTable[OpADD] = OpcodeInfo{"ADD", 3, 2, 1}
	opcodeTable[OpSUB] = OpcodeInfo{"SUB", 3, 2, 1}
	opcodeTable[OpMUL] = OpcodeInfo{"MUL", 5, 2, 1}
	opcodeTable[OpDIV] = OpcodeInfo{"DIV", 5, 2, 1}
	opcodeTable[OpMOD] = OpcodeInfo{"MOD", 5, 2, 1}
	opcodeTable[OpADDMOD] = OpcodeInfo{"ADDMOD", 8, 3, 1}
	opcodeTable[OpMULMOD] = OpcodeInfo{"MULMOD", 8, 3, 1}
	opcodeTable[OpEXP] = OpcodeInfo{"EXP", 10, 2, 1}

	// Comparison
	opcodeTable[OpLT] = OpcodeInfo{"LT", 3, 2, 1}
	opcodeTable[OpGT] = OpcodeInfo{"GT", 3, 2, 1}
	opcodeTable[OpEQ] = OpcodeInfo{"EQ", 3, 2, 1}
	opcodeTable[OpISZERO] = OpcodeInfo{"ISZERO", 3, 1, 1}

	// Bitwise
	opcodeTable[OpAND] = OpcodeInfo{"AND", 3, 2, 1}
	opcodeTable[OpOR] = OpcodeInfo{"OR", 3, 2, 1}
	opcodeTable[OpXOR] = OpcodeInfo{"XOR", 3, 2, 1}
	opcodeTable[OpNOT] = OpcodeInfo{"NOT", 3, 1, 1}
	opcodeTable[OpBYTE] = OpcodeInfo{"BYTE", 3, 2, 1}
	opcodeTable[OpSHL] = OpcodeInfo{"SHL", 3, 2, 1}
	opcodeTable[OpSHR] = OpcodeInfo{"SHR", 3, 2, 1}

	// Crypto
	opcodeTable[OpSHA256] = OpcodeInfo{"SHA256", 30, 2, 1}

	// Context
	opcodeTable[OpADDRESS] = OpcodeInfo{"ADDRESS", 2, 0, 1}
	opcodeTable[OpBALANCE] = OpcodeInfo{"BALANCE", 400, 1, 1}
	opcodeTable[OpORIGIN] = OpcodeInfo{"ORIGIN", 2, 0, 1}
	opcodeTable[OpCALLER] = OpcodeInfo{"CALLER", 2, 0, 1}
	opcodeTable[OpCALLVALUE] = OpcodeInfo{"CALLVALUE", 2, 0, 1}
	opcodeTable[OpCALLDATALOAD] = OpcodeInfo{"CALLDATALOAD", 3, 1, 1}
	opcodeTable[OpCALLDATASIZE] = OpcodeInfo{"CALLDATASIZE", 2, 0, 1}
	opcodeTable[OpCALLDATACOPY] = OpcodeInfo{"CALLDATACOPY", 3, 3, 0}
	opcodeTable[OpCODESIZE] = OpcodeInfo{"CODESIZE", 2, 0, 1}
	opcodeTable[OpCODECOPY] = OpcodeInfo{"CODECOPY", 3, 3, 0}

	// Block info
	opcodeTable[OpTIMESTAMP] = OpcodeInfo{"TIMESTAMP", 2, 0, 1}
	opcodeTable[OpNUMBER] = OpcodeInfo{"NUMBER", 2, 0, 1}
	opcodeTable[OpDIFFICULTY] = OpcodeInfo{"DIFFICULTY", 2, 0, 1}
	opcodeTable[OpGASLIMIT] = OpcodeInfo{"GASLIMIT", 2, 0, 1}

	// Stack/Memory/Storage
	opcodeTable[OpPOP] = OpcodeInfo{"POP", 2, 1, 0}
	opcodeTable[OpMLOAD] = OpcodeInfo{"MLOAD", 3, 1, 1}
	opcodeTable[OpMSTORE] = OpcodeInfo{"MSTORE", 3, 2, 0}
	opcodeTable[OpMSTORE8] = OpcodeInfo{"MSTORE8", 3, 2, 0}
	opcodeTable[OpSLOAD] = OpcodeInfo{"SLOAD", 800, 1, 1}
	opcodeTable[OpSSTORE] = OpcodeInfo{"SSTORE", 5000, 2, 0}

	// Flow control
	opcodeTable[OpJUMP] = OpcodeInfo{"JUMP", 8, 1, 0}
	opcodeTable[OpJUMPI] = OpcodeInfo{"JUMPI", 10, 2, 0}
	opcodeTable[OpMSIZE] = OpcodeInfo{"MSIZE", 2, 0, 1}
	opcodeTable[OpGAS] = OpcodeInfo{"GAS", 2, 0, 1}
	opcodeTable[OpJUMPDEST] = OpcodeInfo{"JUMPDEST", 1, 0, 0}

	// Push1-Push32
	for i := 0; i < 32; i++ {
		name := "PUSH" + string(rune('0'+i+1))
		if i+1 >= 10 {
			tens := (i + 1) / 10
			ones := (i + 1) % 10
			name = "PUSH" + string(rune('0'+tens)) + string(rune('0'+ones))
		}
		opcodeTable[OpPUSH1+Opcode(i)] = OpcodeInfo{name, 3, 0, 1}
	}

	// Dup1-Dup16
	for i := 0; i < 16; i++ {
		name := "DUP" + string(rune('0'+i+1))
		if i+1 >= 10 {
			tens := (i + 1) / 10
			ones := (i + 1) % 10
			name = "DUP" + string(rune('0'+tens)) + string(rune('0'+ones))
		}
		opcodeTable[OpDUP1+Opcode(i)] = OpcodeInfo{name, 3, i + 1, i + 2}
	}

	// Swap1-Swap16
	for i := 0; i < 16; i++ {
		name := "SWAP" + string(rune('0'+i+1))
		if i+1 >= 10 {
			tens := (i + 1) / 10
			ones := (i + 1) % 10
			name = "SWAP" + string(rune('0'+tens)) + string(rune('0'+ones))
		}
		opcodeTable[OpSWAP1+Opcode(i)] = OpcodeInfo{name, 3, i + 2, i + 2}
	}

	// Logging
	opcodeTable[OpLOG0] = OpcodeInfo{"LOG0", 375, 2, 0}
	opcodeTable[OpLOG1] = OpcodeInfo{"LOG1", 750, 3, 0}
	opcodeTable[OpLOG2] = OpcodeInfo{"LOG2", 1125, 4, 0}
	opcodeTable[OpLOG3] = OpcodeInfo{"LOG3", 1500, 5, 0}
	opcodeTable[OpLOG4] = OpcodeInfo{"LOG4", 1875, 6, 0}

	// System
	opcodeTable[OpCALL] = OpcodeInfo{"CALL", 700, 7, 1}
	opcodeTable[OpRETURN] = OpcodeInfo{"RETURN", 0, 2, 0}
	opcodeTable[OpREVERT] = OpcodeInfo{"REVERT", 0, 2, 0}
	opcodeTable[OpSELFDESTRUCT] = OpcodeInfo{"SELFDESTRUCT", 5000, 1, 0}
}

func (op Opcode) Info() OpcodeInfo {
	return opcodeTable[op]
}

func (op Opcode) IsPush() bool {
	return op >= OpPUSH1 && op <= OpPUSH32
}

func (op Opcode) PushSize() int {
	if !op.IsPush() {
		return 0
	}
	return int(op-OpPUSH1) + 1
}

func (op Opcode) IsDup() bool {
	return op >= OpDUP1 && op <= OpDUP16
}

func (op Opcode) DupIndex() int {
	return int(op-OpDUP1) + 1
}

func (op Opcode) IsSwap() bool {
	return op >= OpSWAP1 && op <= OpSWAP16
}

func (op Opcode) SwapIndex() int {
	return int(op-OpSWAP1) + 1
}

func (op Opcode) IsLog() bool {
	return op >= OpLOG0 && op <= OpLOG4
}

func (op Opcode) LogTopics() int {
	return int(op - OpLOG0)
}

func (op Opcode) IsValid() bool {
	return opcodeTable[op].Name != "INVALID"
}
