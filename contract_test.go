package main

import (
	"encoding/hex"
	"math/big"
	"testing"
)

func newTestContext(caller, contractAddr string, gasLimit uint64) *ExecutionContext {
	return &ExecutionContext{
		Caller:          caller,
		ContractAddress: contractAddr,
		Origin:          caller,
		Value:           0,
		GasPrice:        100,
		BlockHeight:     50,
		BlockTimestamp:   1700000000,
		DifficultyBits:  4,
		GasLimit:        gasLimit,
	}
}

func TestContractDeployAndCall(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	// Runtime code: SLOAD(0), PUSH1 1, ADD, PUSH1 0, SSTORE (increment slot 0)
	runtimeCode := []byte{
		byte(OpPUSH1), 0,     // key = 0
		byte(OpSLOAD),        // load current value
		byte(OpPUSH1), 1,     // push 1
		byte(OpADD),          // value + 1
		byte(OpPUSH1), 0,     // key = 0
		byte(OpSSTORE),       // store back
		byte(OpSTOP),
	}

	deployCode := buildDeployCode(runtimeCode)
	contractAddr := DeriveContractAddress("deployer", 0)

	ctx := newTestContext("deployer", contractAddr, 1000000)
	deployResult, runtimeOut := ExecuteDeployment(stateDB, deployCode, ctx, 1000000)
	if deployResult.Err != nil {
		t.Fatalf("deployment failed: %v", deployResult.Err)
	}
	if len(runtimeOut) == 0 {
		t.Fatal("deployment returned empty runtime code")
	}

	stateDB.SetContract(&ContractAccount{
		Address: contractAddr,
		Code:    runtimeOut,
		Storage: make(map[string][]byte),
	})

	// Call the counter (increment)
	callCtx := newTestContext("caller", contractAddr, 1000000)
	callResult := ExecuteContract(stateDB, contractAddr, callCtx, 1000000)
	if callResult.Err != nil {
		t.Fatalf("first call failed: %v", callResult.Err)
	}

	key := padKey(0)
	val := stateDB.GetStorage(contractAddr, key)
	if val == nil {
		t.Fatal("storage slot 0 should have a value after increment")
	}
	storedValue := new(big.Int).SetBytes(val)
	if storedValue.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("counter after 1 call = %s, want 1", storedValue)
	}

	// Call again
	callResult2 := ExecuteContract(stateDB, contractAddr, callCtx, 1000000)
	if callResult2.Err != nil {
		t.Fatalf("second call failed: %v", callResult2.Err)
	}

	val2 := stateDB.GetStorage(contractAddr, key)
	storedValue2 := new(big.Int).SetBytes(val2)
	if storedValue2.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("counter after 2 calls = %s, want 2", storedValue2)
	}
}

func TestContractRevertOnFailure(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	contractAddr := "revert_contract"
	stateDB.SetContract(&ContractAccount{
		Address: contractAddr,
		Code: []byte{
			byte(OpPUSH1), 42,
			byte(OpPUSH1), 0,
			byte(OpSSTORE),
			byte(OpPUSH1), 0,
			byte(OpPUSH1), 0,
			byte(OpREVERT),
		},
		Storage: make(map[string][]byte),
	})

	snapID := stateDB.Snapshot()

	ctx := newTestContext("caller", contractAddr, 1000000)
	result := ExecuteContract(stateDB, contractAddr, ctx, 1000000)
	if !result.Reverted {
		t.Fatal("expected execution to be reverted")
	}

	stateDB.RevertToSnapshot(snapID)

	key := padKey(0)
	val := stateDB.GetStorage(contractAddr, key)
	if val != nil {
		t.Fatalf("storage should be empty after revert, got %x", val)
	}
}

func TestContractOutOfGas(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	contractAddr := "oog_contract"
	stateDB.SetContract(&ContractAccount{
		Address: contractAddr,
		Code: []byte{
			byte(OpJUMPDEST), // offset 0
			byte(OpPUSH1), 0, // push 0
			byte(OpJUMP),     // jump to 0
		},
		Storage: make(map[string][]byte),
	})

	ctx := newTestContext("caller", contractAddr, 100)
	result := ExecuteContract(stateDB, contractAddr, ctx, 100)
	if result.Err == nil {
		t.Fatal("expected out of gas error for infinite loop")
	}
	if result.GasUsed != 100 {
		t.Fatalf("gas used = %d, want 100 (all gas consumed)", result.GasUsed)
	}
}

func TestContractReadOnlyMode(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	contractAddr := "readonly_contract"
	stateDB.SetContract(&ContractAccount{
		Address: contractAddr,
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

	ctx := newTestContext("", contractAddr, 1000000)
	result := ExecuteReadOnly(stateDB, contractAddr, ctx, 1000000)
	if result.Err != nil {
		t.Fatalf("read-only execution failed: %v", result.Err)
	}
	if len(result.ReturnData) != 32 {
		t.Fatalf("return data length = %d, want 32", len(result.ReturnData))
	}
	value := new(big.Int).SetBytes(result.ReturnData)
	if value.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("returned value = %s, want 42", value)
	}
}

func TestABIFunctionSelector(t *testing.T) {
	t.Parallel()

	sel1 := FunctionSelector("transfer(address,uint256)")
	sel2 := FunctionSelector("transfer(address,uint256)")
	if sel1 != sel2 {
		t.Fatalf("selectors differ for same signature: %x vs %x", sel1, sel2)
	}

	sel3 := FunctionSelector("approve(address,uint256)")
	if sel1 == sel3 {
		t.Fatal("different signatures should produce different selectors")
	}

	if len(sel1) != 4 {
		t.Fatalf("selector length = %d, want 4", len(sel1))
	}
}

func TestABIEncodeCall(t *testing.T) {
	t.Parallel()

	encoded, err := EncodeCall("transfer(address,uint256)", "00000000000000000000000000000000deadbeef", big.NewInt(1000))
	if err != nil {
		t.Fatalf("EncodeCall error: %v", err)
	}

	if len(encoded) != 4+32+32 {
		t.Fatalf("encoded length = %d, want %d", len(encoded), 4+32+32)
	}

	selector := FunctionSelector("transfer(address,uint256)")
	if encoded[0] != selector[0] || encoded[1] != selector[1] || encoded[2] != selector[2] || encoded[3] != selector[3] {
		t.Fatalf("selector mismatch: %x vs %x", encoded[:4], selector[:])
	}
}

func TestABIDecodeArgs(t *testing.T) {
	t.Parallel()

	value := big.NewInt(42)
	encoded, err := EncodeCall("test(uint256)", value)
	if err != nil {
		t.Fatalf("EncodeCall error: %v", err)
	}

	args, err := DecodeArgs(encoded[4:], ABIUint256)
	if err != nil {
		t.Fatalf("DecodeArgs error: %v", err)
	}
	if len(args) != 1 {
		t.Fatalf("decoded %d args, want 1", len(args))
	}

	decoded, ok := args[0].(*big.Int)
	if !ok {
		t.Fatal("decoded arg should be *big.Int")
	}
	if decoded.Cmp(value) != 0 {
		t.Fatalf("decoded = %s, want 42", decoded)
	}
}

func TestContractTransactionTypes(t *testing.T) {
	t.Parallel()

	tx := NewTransaction("from", "to", 100, 10000)
	if tx.Type != TxTransfer {
		t.Fatalf("NewTransaction type = %d, want %d", tx.Type, TxTransfer)
	}

	deployTx := NewContractTransaction(TxDeploy, "from", "", 0, 1000000, 100, "aabbcc")
	if deployTx.Type != TxDeploy {
		t.Fatalf("deploy tx type = %d, want %d", deployTx.Type, TxDeploy)
	}
	if deployTx.GasLimit != 1000000 {
		t.Fatalf("gas limit = %d, want 1000000", deployTx.GasLimit)
	}

	callTx := NewContractTransaction(TxCall, "from", "contract_addr", 0, 500000, 100, "deadbeef")
	if callTx.Type != TxCall {
		t.Fatalf("call tx type = %d, want %d", callTx.Type, TxCall)
	}
}

func TestContractSigningFormat(t *testing.T) {
	t.Parallel()

	w, _ := NewWallet()

	deployTx := NewContractTransaction(TxDeploy, w.Address, "", 0, 1000000, 100, "aabbcc")
	if err := deployTx.Sign(w); err != nil {
		t.Fatalf("deploy tx sign error: %v", err)
	}
	if deployTx.Signature == "" {
		t.Fatal("deploy tx signature should not be empty")
	}
	if err := VerifyTransactionSignature(deployTx); err != nil {
		t.Fatalf("deploy tx verification failed: %v", err)
	}

	callTx := NewContractTransaction(TxCall, w.Address, "some_contract", 0, 500000, 100, "deadbeef")
	if err := callTx.Sign(w); err != nil {
		t.Fatalf("call tx sign error: %v", err)
	}
	if err := VerifyTransactionSignature(callTx); err != nil {
		t.Fatalf("call tx verification failed: %v", err)
	}
}

func TestContractGasConstants(t *testing.T) {
	t.Parallel()

	if MaxGasLimit != 10_000_000 {
		t.Fatalf("MaxGasLimit = %d, want 10000000", MaxGasLimit)
	}
	if MinGasPrice != 100 {
		t.Fatalf("MinGasPrice = %d, want 100", MinGasPrice)
	}
	if MaxCodeSize != 24576 {
		t.Fatalf("MaxCodeSize = %d, want 24576", MaxCodeSize)
	}
	if MaxCallDepth != 256 {
		t.Fatalf("MaxCallDepth = %d, want 256", MaxCallDepth)
	}
}

func TestGasCalculations(t *testing.T) {
	t.Parallel()

	deployGas := CalcDeployGas(100)
	if deployGas != BaseDeployGas+100*GasPerDeployByte {
		t.Fatalf("CalcDeployGas(100) = %d, want %d", deployGas, BaseDeployGas+100*GasPerDeployByte)
	}

	callGas := CalcCallGas(64)
	if callGas != BaseCallGas+64*GasPerByte {
		t.Fatalf("CalcCallGas(64) = %d, want %d", callGas, BaseCallGas+64*GasPerByte)
	}
}

func TestContractValidation(t *testing.T) {
	origFork := SmartContractForkHeight
	defer func() { SmartContractForkHeight = origFork }()
	SmartContractForkHeight = 5

	w, _ := NewWallet()

	deployTx := NewContractTransaction(TxDeploy, w.Address, "", 0, 1000000, 100, "aabbcc")
	deployTx.Sign(w)
	if err := validateTransactionForBlock(deployTx, 4); err == nil {
		t.Fatal("deploy tx before fork height should be rejected")
	}

	if err := validateTransactionForBlock(deployTx, 5); err != nil {
		t.Fatalf("deploy tx at fork height should be accepted: %v", err)
	}

	callTx := NewContractTransaction(TxCall, w.Address, "some_contract", 0, 500000, 100, "deadbeef")
	callTx.Sign(w)
	if err := validateTransactionForBlock(callTx, 10); err != nil {
		t.Fatalf("call tx after fork height should be accepted: %v", err)
	}
}

func TestSmartContractForkHeight(t *testing.T) {
	t.Parallel()
	if SmartContractForkHeight <= 0 {
		t.Fatalf("SmartContractForkHeight should be positive, got %d", SmartContractForkHeight)
	}
}

func TestContractAddressDerivation(t *testing.T) {
	t.Parallel()

	addr := DeriveContractAddress("deployer_wallet", 0)
	if len(addr) != 40 {
		t.Fatalf("contract address length = %d, want 40 hex chars", len(addr))
	}

	_, err := hex.DecodeString(addr)
	if err != nil {
		t.Fatalf("contract address is not valid hex: %v", err)
	}

	addr2 := DeriveContractAddress("deployer_wallet", 1)
	if addr == addr2 {
		t.Fatal("different nonces should yield different contract addresses")
	}

	addr3 := DeriveContractAddress("deployer_wallet", 0)
	if addr != addr3 {
		t.Fatal("same inputs should yield same contract address")
	}
}

// buildDeployCode creates init code that returns the given runtime code
func buildDeployCode(runtimeCode []byte) []byte {
	codeLen := len(runtimeCode)
	var code []byte
	for i, b := range runtimeCode {
		code = append(code, byte(OpPUSH1), b)
		code = append(code, byte(OpPUSH1), byte(i))
		code = append(code, byte(OpMSTORE8))
	}
	code = append(code, byte(OpPUSH1), byte(codeLen))
	code = append(code, byte(OpPUSH1), 0)
	code = append(code, byte(OpRETURN))
	return code
}

// padKey creates a 64-char hex key from an integer
func padKey(n int) string {
	b := make([]byte, 32)
	big.NewInt(int64(n)).FillBytes(b)
	return hex.EncodeToString(b)
}
