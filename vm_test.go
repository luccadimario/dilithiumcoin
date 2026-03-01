package main

import (
	"math/big"
	"testing"
)

func newTestVM(code []byte, gas uint64) *VM {
	stateDB := NewStateDB(nil, nil)
	ctx := &ExecutionContext{
		Caller:          "test_caller",
		ContractAddress: "test_contract",
		Origin:          "test_caller",
		BlockHeight:     100,
		BlockTimestamp:   1700000000,
		GasLimit:        gas,
		Calldata:        nil,
	}
	return NewVM(code, ctx, stateDB, gas)
}

func TestVMStopOpcode(t *testing.T) {
	t.Parallel()
	vm := newTestVM([]byte{byte(OpSTOP)}, 10000)
	result := vm.Execute()
	if result.Err != nil {
		t.Fatalf("STOP should not error: %v", result.Err)
	}
	if result.Reverted {
		t.Fatal("STOP should not revert")
	}
}

func TestVMPushAndPop(t *testing.T) {
	t.Parallel()
	// PUSH1 0x42, POP, STOP
	code := []byte{byte(OpPUSH1), 0x42, byte(OpPOP), byte(OpSTOP)}
	vm := newTestVM(code, 10000)
	result := vm.Execute()
	if result.Err != nil {
		t.Fatalf("PUSH1+POP should not error: %v", result.Err)
	}
	if vm.stackPtr != 0 {
		t.Fatalf("stack should be empty after POP, got %d items", vm.stackPtr)
	}
}

func TestVMAdd(t *testing.T) {
	t.Parallel()
	// PUSH1 3, PUSH1 5, ADD, STOP
	code := []byte{
		byte(OpPUSH1), 3,
		byte(OpPUSH1), 5,
		byte(OpADD),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 1 {
		t.Fatalf("expected 1 item on stack, got %d", vm.stackPtr)
	}
	result := vm.stack[0]
	if result.Cmp(big.NewInt(8)) != 0 {
		t.Fatalf("3 + 5 = %s, want 8", result.String())
	}
}

func TestVMSub(t *testing.T) {
	t.Parallel()
	// PUSH1 3, PUSH1 10, SUB => 10 - 3 = 7
	code := []byte{
		byte(OpPUSH1), 3,
		byte(OpPUSH1), 10,
		byte(OpSUB),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 1 {
		t.Fatalf("expected 1 item on stack, got %d", vm.stackPtr)
	}
	if vm.stack[0].Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("10 - 3 = %s, want 7", vm.stack[0].String())
	}
}

func TestVMMul(t *testing.T) {
	t.Parallel()
	// PUSH1 6, PUSH1 7, MUL => 42
	code := []byte{
		byte(OpPUSH1), 6,
		byte(OpPUSH1), 7,
		byte(OpMUL),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("6 * 7 = %s, want 42", vm.stack[0].String())
	}
}

func TestVMDiv(t *testing.T) {
	t.Parallel()
	// PUSH1 3, PUSH1 15, DIV => 15 / 3 = 5
	code := []byte{
		byte(OpPUSH1), 3,
		byte(OpPUSH1), 15,
		byte(OpDIV),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("15 / 3 = %s, want 5", vm.stack[0].String())
	}
}

func TestVMDivByZero(t *testing.T) {
	t.Parallel()
	// PUSH1 0, PUSH1 10, DIV => 0
	code := []byte{
		byte(OpPUSH1), 0,
		byte(OpPUSH1), 10,
		byte(OpDIV),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("10 / 0 = %s, want 0", vm.stack[0].String())
	}
}

func TestVMMod(t *testing.T) {
	t.Parallel()
	// PUSH1 3, PUSH1 10, MOD => 10 % 3 = 1
	code := []byte{
		byte(OpPUSH1), 3,
		byte(OpPUSH1), 10,
		byte(OpMOD),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("10 %% 3 = %s, want 1", vm.stack[0].String())
	}
}

func TestVMComparisons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		a, b   byte
		opcode Opcode
		want   int64
	}{
		{"5 < 10", 10, 5, OpLT, 1},
		{"10 < 5", 5, 10, OpLT, 0},
		{"10 > 5", 5, 10, OpGT, 1},
		{"5 > 10", 10, 5, OpGT, 0},
		{"5 == 5", 5, 5, OpEQ, 1},
		{"5 == 6", 6, 5, OpEQ, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := []byte{
				byte(OpPUSH1), tt.a,
				byte(OpPUSH1), tt.b,
				byte(tt.opcode),
				byte(OpSTOP),
			}
			vm := newTestVM(code, 10000)
			vm.Execute()
			if vm.stack[0].Cmp(big.NewInt(tt.want)) != 0 {
				t.Fatalf("got %s, want %d", vm.stack[0].String(), tt.want)
			}
		})
	}
}

func TestVMIsZero(t *testing.T) {
	t.Parallel()

	// ISZERO(0) == 1
	code := []byte{byte(OpPUSH1), 0, byte(OpISZERO), byte(OpSTOP)}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ISZERO(0) = %s, want 1", vm.stack[0].String())
	}

	// ISZERO(5) == 0
	code2 := []byte{byte(OpPUSH1), 5, byte(OpISZERO), byte(OpSTOP)}
	vm2 := newTestVM(code2, 10000)
	vm2.Execute()
	if vm2.stack[0].Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("ISZERO(5) = %s, want 0", vm2.stack[0].String())
	}
}

func TestVMBitwiseOps(t *testing.T) {
	t.Parallel()

	// AND: 0xFF & 0x0F = 0x0F
	code := []byte{
		byte(OpPUSH1), 0x0F,
		byte(OpPUSH1), 0xFF,
		byte(OpAND),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(0x0F)) != 0 {
		t.Fatalf("0xFF & 0x0F = %s, want 15", vm.stack[0].String())
	}

	// OR: 0xF0 | 0x0F = 0xFF
	code2 := []byte{
		byte(OpPUSH1), 0x0F,
		byte(OpPUSH1), 0xF0,
		byte(OpOR),
		byte(OpSTOP),
	}
	vm2 := newTestVM(code2, 10000)
	vm2.Execute()
	if vm2.stack[0].Cmp(big.NewInt(0xFF)) != 0 {
		t.Fatalf("0xF0 | 0x0F = %s, want 255", vm2.stack[0].String())
	}

	// XOR: 0xFF ^ 0x0F = 0xF0
	code3 := []byte{
		byte(OpPUSH1), 0x0F,
		byte(OpPUSH1), 0xFF,
		byte(OpXOR),
		byte(OpSTOP),
	}
	vm3 := newTestVM(code3, 10000)
	vm3.Execute()
	if vm3.stack[0].Cmp(big.NewInt(0xF0)) != 0 {
		t.Fatalf("0xFF ^ 0x0F = %s, want 240", vm3.stack[0].String())
	}
}

func TestVMDup(t *testing.T) {
	t.Parallel()
	// PUSH1 42, DUP1, STOP => stack: [42, 42]
	code := []byte{
		byte(OpPUSH1), 42,
		byte(OpDUP1),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 2 {
		t.Fatalf("expected 2 items on stack, got %d", vm.stackPtr)
	}
	if vm.stack[0].Cmp(big.NewInt(42)) != 0 || vm.stack[1].Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("DUP1 failed: stack = [%s, %s]", vm.stack[0], vm.stack[1])
	}
}

func TestVMSwap(t *testing.T) {
	t.Parallel()
	// PUSH1 1, PUSH1 2, SWAP1, STOP => stack: [2, 1]
	code := []byte{
		byte(OpPUSH1), 1,
		byte(OpPUSH1), 2,
		byte(OpSWAP1),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 2 {
		t.Fatalf("expected 2 items on stack, got %d", vm.stackPtr)
	}
	if vm.stack[0].Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("bottom should be 2, got %s", vm.stack[0])
	}
	if vm.stack[1].Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("top should be 1, got %s", vm.stack[1])
	}
}

func TestVMMemoryOps(t *testing.T) {
	t.Parallel()
	// PUSH1 0xFF, PUSH1 0 (offset), MSTORE8, PUSH1 0, MLOAD, STOP
	code := []byte{
		byte(OpPUSH1), 0xFF,
		byte(OpPUSH1), 0, // offset 0
		byte(OpMSTORE8),
		byte(OpPUSH1), 0, // offset 0
		byte(OpMLOAD),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 100000)
	vm.Execute()
	if vm.stackPtr != 1 {
		t.Fatalf("expected 1 item on stack, got %d", vm.stackPtr)
	}
	// MSTORE8 at offset 0 stores 0xFF, MLOAD at offset 0 loads 32 bytes
	// First byte is 0xFF, rest is 0 => big-endian = 0xFF << 248
	expected := new(big.Int).Lsh(big.NewInt(0xFF), 248)
	if vm.stack[0].Cmp(expected) != 0 {
		t.Fatalf("MLOAD after MSTORE8 = %s, want %s", vm.stack[0].String(), expected.String())
	}
}

func TestVMStorageOps(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)
	stateDB.SetContract(&ContractAccount{
		Address: "test_contract",
		Code:    []byte{},
		Storage: make(map[string][]byte),
	})

	// PUSH1 0xAB (value), PUSH1 0x01 (key), SSTORE
	// PUSH1 0x01 (key), SLOAD, STOP
	code := []byte{
		byte(OpPUSH1), 0xAB,
		byte(OpPUSH1), 0x01,
		byte(OpSSTORE),
		byte(OpPUSH1), 0x01,
		byte(OpSLOAD),
		byte(OpSTOP),
	}

	ctx := &ExecutionContext{
		Caller:          "test_caller",
		ContractAddress: "test_contract",
		Origin:          "test_caller",
		BlockHeight:     100,
		GasLimit:        100000,
	}
	vm := NewVM(code, ctx, stateDB, 100000)
	result := vm.Execute()
	if result.Err != nil {
		t.Fatalf("storage ops failed: %v", result.Err)
	}
	if vm.stack[0].Cmp(big.NewInt(0xAB)) != 0 {
		t.Fatalf("SLOAD after SSTORE = %s, want 171 (0xAB)", vm.stack[0].String())
	}
}

func TestVMJump(t *testing.T) {
	t.Parallel()
	// PUSH1 4 (dest), JUMP, STOP, JUMPDEST, PUSH1 99, STOP
	code := []byte{
		byte(OpPUSH1), 4,
		byte(OpJUMP),
		byte(OpSTOP),     // skipped
		byte(OpJUMPDEST), // offset 4
		byte(OpPUSH1), 99,
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 1 {
		t.Fatalf("expected 1 item on stack, got %d", vm.stackPtr)
	}
	if vm.stack[0].Cmp(big.NewInt(99)) != 0 {
		t.Fatalf("after JUMP, expected 99 on stack, got %s", vm.stack[0])
	}
}

func TestVMJumpI(t *testing.T) {
	t.Parallel()
	// Test with condition = true (should jump)
	code := []byte{
		byte(OpPUSH1), 1,  // condition: true
		byte(OpPUSH1), 7,  // destination: offset 7
		byte(OpJUMPI),     // offset 4
		byte(OpPUSH1), 11, // offset 5, 6: fallthrough path
		byte(OpJUMPDEST),  // offset 7
		byte(OpPUSH1), 99, // offset 8, 9
		byte(OpSTOP),      // offset 10
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 1 {
		t.Fatalf("expected 1 item on stack, got %d", vm.stackPtr)
	}
	if vm.stack[0].Cmp(big.NewInt(99)) != 0 {
		t.Fatalf("after JUMPI(true), expected 99, got %s", vm.stack[0])
	}

	// Now test with condition = 0 (should NOT jump)
	code2 := []byte{
		byte(OpPUSH1), 0,  // condition: false
		byte(OpPUSH1), 7,  // destination
		byte(OpJUMPI),     // offset 4: does not jump
		byte(OpPUSH1), 11, // offset 5, 6: fallthrough
		byte(OpJUMPDEST),  // offset 7
		byte(OpPUSH1), 99, // offset 8, 9
		byte(OpSTOP),      // offset 10
	}
	vm2 := newTestVM(code2, 10000)
	vm2.Execute()
	if vm2.stackPtr != 2 {
		t.Fatalf("expected 2 items on stack (11 and 99), got %d", vm2.stackPtr)
	}
}

func TestVMReturn(t *testing.T) {
	t.Parallel()
	// Store 0xDEAD in memory at offset 0, then RETURN 2 bytes from offset 30
	code := []byte{
		byte(OpPUSH2), 0xDE, 0xAD, // push 0xDEAD
		byte(OpPUSH1), 0, // memory offset 0
		byte(OpMSTORE),         // store at offset 0 (32-byte word, value at end)
		byte(OpPUSH1), 2,       // length = 2
		byte(OpPUSH1), 30,      // offset = 30 (last 2 bytes of the 32-byte word)
		byte(OpRETURN),
	}
	vm := newTestVM(code, 100000)
	result := vm.Execute()
	if result.Err != nil {
		t.Fatalf("RETURN failed: %v", result.Err)
	}
	if len(result.ReturnData) != 2 {
		t.Fatalf("return data length = %d, want 2", len(result.ReturnData))
	}
	if result.ReturnData[0] != 0xDE || result.ReturnData[1] != 0xAD {
		t.Fatalf("return data = %x, want dead", result.ReturnData)
	}
}

func TestVMRevert(t *testing.T) {
	t.Parallel()
	// PUSH1 0, PUSH1 0, REVERT
	code := []byte{
		byte(OpPUSH1), 0, // length
		byte(OpPUSH1), 0, // offset
		byte(OpREVERT),
	}
	vm := newTestVM(code, 10000)
	result := vm.Execute()
	if !result.Reverted {
		t.Fatal("REVERT should set Reverted flag")
	}
}

func TestVMOutOfGas(t *testing.T) {
	t.Parallel()
	// Run with very little gas — PUSH1 costs 3
	code := []byte{byte(OpPUSH1), 1, byte(OpPUSH1), 2, byte(OpADD), byte(OpSTOP)}
	vm := newTestVM(code, 5) // only 5 gas, need 3+3+3 = 9
	result := vm.Execute()
	if result.Err == nil {
		t.Fatal("expected out of gas error")
	}
}

func TestVMCallerContext(t *testing.T) {
	t.Parallel()
	// CALLER pushes msg.sender, ADDRESS pushes contract address
	code := []byte{
		byte(OpCALLER),
		byte(OpADDRESS),
		byte(OpSTOP),
	}
	stateDB := NewStateDB(nil, nil)
	ctx := &ExecutionContext{
		Caller:          "sender_addr",
		ContractAddress: "contract_addr",
		Origin:          "sender_addr",
		BlockHeight:     100,
		GasLimit:        10000,
	}
	vm := NewVM(code, ctx, stateDB, 10000)
	vm.Execute()

	if vm.stackPtr != 2 {
		t.Fatalf("expected 2 items on stack, got %d", vm.stackPtr)
	}
}

func TestVMCalldataLoad(t *testing.T) {
	t.Parallel()
	// CALLDATASIZE, PUSH1 0, CALLDATALOAD, STOP
	code := []byte{
		byte(OpCALLDATASIZE),
		byte(OpPUSH1), 0,
		byte(OpCALLDATALOAD),
		byte(OpSTOP),
	}
	stateDB := NewStateDB(nil, nil)
	calldata := make([]byte, 32)
	calldata[31] = 42 // last byte = 42
	ctx := &ExecutionContext{
		Caller:          "test_caller",
		ContractAddress: "test_contract",
		Origin:          "test_caller",
		BlockHeight:     100,
		GasLimit:        10000,
		Calldata:        calldata,
	}
	vm := NewVM(code, ctx, stateDB, 10000)
	vm.Execute()

	if vm.stackPtr != 2 {
		t.Fatalf("expected 2 items on stack, got %d", vm.stackPtr)
	}
	// CALLDATASIZE should be 32
	if vm.stack[0].Cmp(big.NewInt(32)) != 0 {
		t.Fatalf("CALLDATASIZE = %s, want 32", vm.stack[0])
	}
	// CALLDATALOAD at offset 0 should load the 32-byte word (value 42)
	if vm.stack[1].Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("CALLDATALOAD(0) = %s, want 42", vm.stack[1])
	}
}

func TestVMGasOpcode(t *testing.T) {
	t.Parallel()
	// GAS, STOP — should push remaining gas after GAS opcode
	code := []byte{byte(OpGAS), byte(OpSTOP)}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stackPtr != 1 {
		t.Fatalf("expected 1 item on stack, got %d", vm.stackPtr)
	}
	// GAS costs 2, so remaining should be 10000 - 2 = 9998
	if vm.stack[0].Cmp(big.NewInt(9998)) != 0 {
		t.Fatalf("GAS = %s, want 9998", vm.stack[0])
	}
}

func TestVMPush32(t *testing.T) {
	t.Parallel()
	// PUSH32 with 32 bytes of 0xFF
	code := make([]byte, 34) // PUSH32 + 32 bytes + STOP
	code[0] = byte(OpPUSH32)
	for i := 1; i <= 32; i++ {
		code[i] = 0xFF
	}
	code[33] = byte(OpSTOP)

	vm := newTestVM(code, 10000)
	vm.Execute()

	// Max uint256
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if vm.stack[0].Cmp(maxUint256) != 0 {
		t.Fatalf("PUSH32(all 0xFF) = %s, want max uint256", vm.stack[0].String())
	}
}

func TestVMStackUnderflow(t *testing.T) {
	t.Parallel()
	// ADD with empty stack
	code := []byte{byte(OpADD), byte(OpSTOP)}
	vm := newTestVM(code, 10000)
	result := vm.Execute()
	if result.Err == nil {
		t.Fatal("expected stack underflow error")
	}
}

func TestVMInvalidJumpDest(t *testing.T) {
	t.Parallel()
	// PUSH1 3, JUMP — offset 3 is PUSH1 immediate, not JUMPDEST
	code := []byte{
		byte(OpPUSH1), 3,
		byte(OpJUMP),
		byte(OpPUSH1), 99, // offset 3 is PUSH1, not JUMPDEST
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	result := vm.Execute()
	if result.Err == nil {
		t.Fatal("expected invalid jump destination error")
	}
}

func TestVMGasConsumption(t *testing.T) {
	t.Parallel()
	// PUSH1 (3 gas) + PUSH1 (3 gas) + ADD (3 gas) + STOP (0 gas) = 9 gas
	code := []byte{byte(OpPUSH1), 1, byte(OpPUSH1), 2, byte(OpADD), byte(OpSTOP)}
	vm := newTestVM(code, 10000)
	result := vm.Execute()
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.GasUsed != 9 {
		t.Fatalf("gas used = %d, want 9", result.GasUsed)
	}
}

func TestVMDeployment(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	// Deployment bytecode that returns [0xAA, 0xBB] as the runtime code
	deployCode := []byte{
		byte(OpPUSH2), 0xAA, 0xBB,
		byte(OpPUSH1), 0,
		byte(OpMSTORE),
		byte(OpPUSH1), 2,
		byte(OpPUSH1), 30,
		byte(OpRETURN),
	}

	ctx := &ExecutionContext{
		Caller:          "deployer",
		ContractAddress: "new_contract",
		Origin:          "deployer",
		Value:           0,
		GasPrice:        100,
		BlockHeight:     50,
		BlockTimestamp:   1700000000,
		DifficultyBits:  4,
		GasLimit:        1000000,
	}

	result, runtimeCode := ExecuteDeployment(stateDB, deployCode, ctx, 1000000)
	if result.Err != nil {
		t.Fatalf("deployment failed: %v", result.Err)
	}
	if len(runtimeCode) != 2 {
		t.Fatalf("runtime code length = %d, want 2", len(runtimeCode))
	}
	if runtimeCode[0] != 0xAA || runtimeCode[1] != 0xBB {
		t.Fatalf("runtime code = %x, want aabb", runtimeCode)
	}
}

func TestVMReadOnly(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)
	stateDB.SetContract(&ContractAccount{
		Address: "query_contract",
		Code: []byte{
			byte(OpPUSH1), 42,
			byte(OpPUSH1), 0,
			byte(OpMSTORE),
			byte(OpPUSH1), 32,
			byte(OpPUSH1), 0,
			byte(OpRETURN),
		},
		Storage: make(map[string][]byte),
	})

	ctx := &ExecutionContext{
		Caller:          "",
		ContractAddress: "query_contract",
		Origin:          "",
		BlockHeight:     50,
		BlockTimestamp:   1700000000,
		DifficultyBits:  4,
		GasLimit:        1000000,
	}

	result := ExecuteReadOnly(stateDB, "query_contract", ctx, 1000000)
	if result.Err != nil {
		t.Fatalf("read-only execution failed: %v", result.Err)
	}
	if len(result.ReturnData) != 32 {
		t.Fatalf("return data length = %d, want 32", len(result.ReturnData))
	}
	if result.ReturnData[31] != 42 {
		t.Fatalf("return data last byte = %d, want 42", result.ReturnData[31])
	}
}

func TestVMShl(t *testing.T) {
	t.Parallel()
	// PUSH1 1, PUSH1 8, SHL => 1 << 8 = 256
	code := []byte{
		byte(OpPUSH1), 1,
		byte(OpPUSH1), 8,
		byte(OpSHL),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(256)) != 0 {
		t.Fatalf("1 << 8 = %s, want 256", vm.stack[0])
	}
}

func TestVMShr(t *testing.T) {
	t.Parallel()
	// PUSH2 0x0100, PUSH1 4, SHR => 256 >> 4 = 16
	code := []byte{
		byte(OpPUSH2), 0x01, 0x00, // 256
		byte(OpPUSH1), 4,
		byte(OpSHR),
		byte(OpSTOP),
	}
	vm := newTestVM(code, 10000)
	vm.Execute()
	if vm.stack[0].Cmp(big.NewInt(16)) != 0 {
		t.Fatalf("256 >> 4 = %s, want 16", vm.stack[0])
	}
}

func TestVMMsize(t *testing.T) {
	t.Parallel()
	// Initially memory size is 0, after MSTORE at offset 0 it becomes 32
	code := []byte{
		byte(OpMSIZE),             // should be 0
		byte(OpPUSH1), 1,         // value
		byte(OpPUSH1), 0,         // offset
		byte(OpMSTORE),           // stores 32 bytes starting at offset 0
		byte(OpMSIZE),            // should be 32
		byte(OpSTOP),
	}
	vm := newTestVM(code, 100000)
	vm.Execute()
	if vm.stackPtr != 2 {
		t.Fatalf("expected 2 items on stack, got %d", vm.stackPtr)
	}
	if vm.stack[0].Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("initial MSIZE = %s, want 0", vm.stack[0])
	}
	if vm.stack[1].Cmp(big.NewInt(32)) != 0 {
		t.Fatalf("MSIZE after MSTORE = %s, want 32", vm.stack[1])
	}
}
