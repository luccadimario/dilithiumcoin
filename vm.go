package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
)

var (
	bigZero = new(big.Int)
	bigOne  = new(big.Int).SetUint64(1)
	bigMax  = new(big.Int).Sub(new(big.Int).Lsh(bigOne, 256), bigOne)
	bigMod  = new(big.Int).Lsh(bigOne, 256)
)

type ExecutionContext struct {
	Caller          string
	ContractAddress string
	Origin          string
	Value           int64
	GasPrice        int64
	BlockHeight     int64
	BlockTimestamp  int64
	DifficultyBits  int
	GasLimit        uint64
	Calldata        []byte
	Depth           int
}

type ExecutionResult struct {
	ReturnData  []byte
	GasUsed     uint64
	GasRefund   uint64
	Logs        []*Log
	Reverted    bool
	Err         error
}

type VM struct {
	stack      [1024]*big.Int
	stackPtr   int
	memory     []byte
	code       []byte
	pc         uint64
	gas        uint64
	context    *ExecutionContext
	state      *StateDB
	returnData []byte
	stopped    bool
	reverted   bool
	logs       []*Log

	// JUMPDEST analysis cache
	validJumpdests map[uint64]bool
	readOnly       bool
}

func NewVM(code []byte, context *ExecutionContext, state *StateDB, gas uint64) *VM {
	vm := &VM{
		code:    code,
		context: context,
		state:   state,
		gas:     gas,
	}
	vm.analyzeJumpdests()
	return vm
}

func (vm *VM) analyzeJumpdests() {
	vm.validJumpdests = make(map[uint64]bool)
	pc := uint64(0)
	for pc < uint64(len(vm.code)) {
		op := Opcode(vm.code[pc])
		if op == OpJUMPDEST {
			vm.validJumpdests[pc] = true
		}
		if op.IsPush() {
			pc += uint64(op.PushSize())
		}
		pc++
	}
}

func (vm *VM) Execute() *ExecutionResult {
	for !vm.stopped {
		if vm.pc >= uint64(len(vm.code)) {
			vm.stopped = true
			break
		}

		op := Opcode(vm.code[vm.pc])

		if !op.IsValid() {
			return vm.errorResult(fmt.Errorf("invalid opcode 0x%02x at pc %d", op, vm.pc))
		}

		info := op.Info()

		// Deduct base gas cost
		gasCost := info.GasCost
		if op == OpEXP {
			gasCost = vm.calcExpGas()
		} else if op == OpSHA256 {
			gasCost = vm.calcSha256Gas()
		} else if op == OpCALLDATACOPY || op == OpCODECOPY {
			gasCost += vm.calcCopyGas()
		} else if op.IsLog() {
			gasCost = vm.calcLogGas(op)
		} else if op == OpSSTORE {
			gasCost = vm.calcSstoreGas()
		}

		if !vm.consumeGas(gasCost) {
			return vm.outOfGasResult()
		}

		// Check stack requirements
		if vm.stackPtr < info.StackIn {
			return vm.errorResult(fmt.Errorf("stack underflow: need %d, have %d", info.StackIn, vm.stackPtr))
		}
		if vm.stackPtr-info.StackIn+info.StackOut > 1024 {
			return vm.errorResult(fmt.Errorf("stack overflow"))
		}

		if err := vm.executeOp(op); err != nil {
			return vm.errorResult(err)
		}
	}

	return &ExecutionResult{
		ReturnData: vm.returnData,
		GasUsed:    vm.context.GasLimit - vm.gas, // computed from initial allocation
		Logs:       vm.logs,
		Reverted:   vm.reverted,
	}
}

func (vm *VM) SetInitialGas(gas uint64) {
	vm.gas = gas
}

func (vm *VM) consumeGas(amount uint64) bool {
	if vm.gas < amount {
		vm.gas = 0
		vm.stopped = true
		return false
	}
	vm.gas -= amount
	return true
}

func (vm *VM) push(value *big.Int) {
	vm.stack[vm.stackPtr] = new(big.Int).Set(value)
	vm.stackPtr++
}

func (vm *VM) pop() *big.Int {
	vm.stackPtr--
	return vm.stack[vm.stackPtr]
}

func (vm *VM) peek(depth int) *big.Int {
	return vm.stack[vm.stackPtr-1-depth]
}

func (vm *VM) pushUint64(value uint64) {
	vm.push(new(big.Int).SetUint64(value))
}

func (vm *VM) popUint64() uint64 {
	return vm.pop().Uint64()
}

func (vm *VM) expandMemory(offset, size uint64) bool {
	if size == 0 {
		return true
	}
	required := offset + size
	if required < offset {
		return false
	}
	if required > MaxMemorySize {
		return false
	}
	if required > uint64(len(vm.memory)) {
		expansionGas := CalcMemoryExpansionGas(uint64(len(vm.memory)), required)
		if !vm.consumeGas(expansionGas) {
			return false
		}
		newMem := make([]byte, required)
		copy(newMem, vm.memory)
		vm.memory = newMem
	}
	return true
}

func (vm *VM) executeOp(op Opcode) error {
	switch {
	case op == OpSTOP:
		vm.stopped = true
		return nil

	case op == OpADD:
		a, b := vm.pop(), vm.pop()
		result := new(big.Int).Add(a, b)
		result.Mod(result, bigMod)
		vm.push(result)

	case op == OpSUB:
		a, b := vm.pop(), vm.pop()
		result := new(big.Int).Sub(a, b)
		result.Mod(result, bigMod)
		if result.Sign() < 0 {
			result.Add(result, bigMod)
		}
		vm.push(result)

	case op == OpMUL:
		a, b := vm.pop(), vm.pop()
		result := new(big.Int).Mul(a, b)
		result.Mod(result, bigMod)
		vm.push(result)

	case op == OpDIV:
		a, b := vm.pop(), vm.pop()
		if b.Sign() == 0 {
			vm.push(new(big.Int))
		} else {
			vm.push(new(big.Int).Div(a, b))
		}

	case op == OpMOD:
		a, b := vm.pop(), vm.pop()
		if b.Sign() == 0 {
			vm.push(new(big.Int))
		} else {
			vm.push(new(big.Int).Mod(a, b))
		}

	case op == OpADDMOD:
		a, b, n := vm.pop(), vm.pop(), vm.pop()
		if n.Sign() == 0 {
			vm.push(new(big.Int))
		} else {
			result := new(big.Int).Add(a, b)
			result.Mod(result, n)
			vm.push(result)
		}

	case op == OpMULMOD:
		a, b, n := vm.pop(), vm.pop(), vm.pop()
		if n.Sign() == 0 {
			vm.push(new(big.Int))
		} else {
			result := new(big.Int).Mul(a, b)
			result.Mod(result, n)
			vm.push(result)
		}

	case op == OpEXP:
		base, exponent := vm.pop(), vm.pop()
		result := new(big.Int).Exp(base, exponent, bigMod)
		vm.push(result)

	case op == OpLT:
		a, b := vm.pop(), vm.pop()
		if a.Cmp(b) < 0 {
			vm.pushUint64(1)
		} else {
			vm.pushUint64(0)
		}

	case op == OpGT:
		a, b := vm.pop(), vm.pop()
		if a.Cmp(b) > 0 {
			vm.pushUint64(1)
		} else {
			vm.pushUint64(0)
		}

	case op == OpEQ:
		a, b := vm.pop(), vm.pop()
		if a.Cmp(b) == 0 {
			vm.pushUint64(1)
		} else {
			vm.pushUint64(0)
		}

	case op == OpISZERO:
		a := vm.pop()
		if a.Sign() == 0 {
			vm.pushUint64(1)
		} else {
			vm.pushUint64(0)
		}

	case op == OpAND:
		a, b := vm.pop(), vm.pop()
		vm.push(new(big.Int).And(a, b))

	case op == OpOR:
		a, b := vm.pop(), vm.pop()
		vm.push(new(big.Int).Or(a, b))

	case op == OpXOR:
		a, b := vm.pop(), vm.pop()
		vm.push(new(big.Int).Xor(a, b))

	case op == OpNOT:
		a := vm.pop()
		result := new(big.Int).Not(a)
		result.And(result, bigMax)
		vm.push(result)

	case op == OpBYTE:
		position, value := vm.pop(), vm.pop()
		pos := position.Uint64()
		if pos >= 32 {
			vm.pushUint64(0)
		} else {
			bytes := make([]byte, 32)
			valueBytes := value.Bytes()
			copy(bytes[32-len(valueBytes):], valueBytes)
			vm.pushUint64(uint64(bytes[pos]))
		}

	case op == OpSHL:
		shift, value := vm.pop(), vm.pop()
		if shift.Uint64() >= 256 {
			vm.push(new(big.Int))
		} else {
			result := new(big.Int).Lsh(value, uint(shift.Uint64()))
			result.And(result, bigMax)
			vm.push(result)
		}

	case op == OpSHR:
		shift, value := vm.pop(), vm.pop()
		if shift.Uint64() >= 256 {
			vm.push(new(big.Int))
		} else {
			vm.push(new(big.Int).Rsh(value, uint(shift.Uint64())))
		}

	case op == OpSHA256:
		offset, length := vm.popUint64(), vm.popUint64()
		if !vm.expandMemory(offset, length) {
			return fmt.Errorf("memory expansion failed")
		}
		var data []byte
		if length > 0 {
			data = vm.memory[offset : offset+length]
		}
		hash := sha256.Sum256(data)
		vm.push(new(big.Int).SetBytes(hash[:]))

	case op == OpADDRESS:
		addrBytes, _ := hex.DecodeString(vm.context.ContractAddress)
		vm.push(new(big.Int).SetBytes(addrBytes))

	case op == OpBALANCE:
		addrInt := vm.pop()
		addr := fmt.Sprintf("%040x", addrInt)
		// Check contract balance first, then wallet balance
		if vm.state.HasContract(addr) {
			vm.pushUint64(uint64(vm.state.GetContractBalance(addr)))
		} else if vm.state.blockchain != nil {
			vm.pushUint64(uint64(vm.state.blockchain.GetBalance(addr)))
		} else {
			vm.pushUint64(0)
		}

	case op == OpORIGIN:
		addrBytes, _ := hex.DecodeString(vm.context.Origin)
		vm.push(new(big.Int).SetBytes(addrBytes))

	case op == OpCALLER:
		addrBytes, _ := hex.DecodeString(vm.context.Caller)
		vm.push(new(big.Int).SetBytes(addrBytes))

	case op == OpCALLVALUE:
		vm.pushUint64(uint64(vm.context.Value))

	case op == OpCALLDATALOAD:
		offset := vm.popUint64()
		data := make([]byte, 32)
		for i := uint64(0); i < 32; i++ {
			if offset+i < uint64(len(vm.context.Calldata)) {
				data[i] = vm.context.Calldata[offset+i]
			}
		}
		vm.push(new(big.Int).SetBytes(data))

	case op == OpCALLDATASIZE:
		vm.pushUint64(uint64(len(vm.context.Calldata)))

	case op == OpCALLDATACOPY:
		destOffset := vm.popUint64()
		dataOffset := vm.popUint64()
		length := vm.popUint64()
		if !vm.expandMemory(destOffset, length) {
			return fmt.Errorf("memory expansion failed")
		}
		for i := uint64(0); i < length; i++ {
			if dataOffset+i < uint64(len(vm.context.Calldata)) {
				vm.memory[destOffset+i] = vm.context.Calldata[dataOffset+i]
			} else {
				vm.memory[destOffset+i] = 0
			}
		}

	case op == OpCODESIZE:
		vm.pushUint64(uint64(len(vm.code)))

	case op == OpCODECOPY:
		destOffset := vm.popUint64()
		codeOffset := vm.popUint64()
		length := vm.popUint64()
		if !vm.expandMemory(destOffset, length) {
			return fmt.Errorf("memory expansion failed")
		}
		for i := uint64(0); i < length; i++ {
			if codeOffset+i < uint64(len(vm.code)) {
				vm.memory[destOffset+i] = vm.code[codeOffset+i]
			} else {
				vm.memory[destOffset+i] = 0
			}
		}

	case op == OpTIMESTAMP:
		vm.pushUint64(uint64(vm.context.BlockTimestamp))

	case op == OpNUMBER:
		vm.pushUint64(uint64(vm.context.BlockHeight))

	case op == OpDIFFICULTY:
		vm.pushUint64(uint64(vm.context.DifficultyBits))

	case op == OpGASLIMIT:
		vm.pushUint64(vm.context.GasLimit)

	case op == OpPOP:
		vm.pop()

	case op == OpMLOAD:
		offset := vm.popUint64()
		if !vm.expandMemory(offset, 32) {
			return fmt.Errorf("memory expansion failed")
		}
		data := make([]byte, 32)
		copy(data, vm.memory[offset:offset+32])
		vm.push(new(big.Int).SetBytes(data))

	case op == OpMSTORE:
		offset := vm.popUint64()
		value := vm.pop()
		if !vm.expandMemory(offset, 32) {
			return fmt.Errorf("memory expansion failed")
		}
		bytes := make([]byte, 32)
		valueBytes := value.Bytes()
		copy(bytes[32-len(valueBytes):], valueBytes)
		copy(vm.memory[offset:], bytes)

	case op == OpMSTORE8:
		offset := vm.popUint64()
		value := vm.pop()
		if !vm.expandMemory(offset, 1) {
			return fmt.Errorf("memory expansion failed")
		}
		vm.memory[offset] = byte(value.Uint64() & 0xFF)

	case op == OpSLOAD:
		key := vm.pop()
		keyHex := fmt.Sprintf("%064x", key)
		data := vm.state.GetStorage(vm.context.ContractAddress, keyHex)
		if data == nil {
			vm.push(new(big.Int))
		} else {
			vm.push(new(big.Int).SetBytes(data))
		}

	case op == OpSSTORE:
		if vm.readOnly {
			return fmt.Errorf("SSTORE in read-only mode")
		}
		key := vm.pop()
		value := vm.pop()
		keyHex := fmt.Sprintf("%064x", key)
		valueBytes := make([]byte, 32)
		valBytes := value.Bytes()
		copy(valueBytes[32-len(valBytes):], valBytes)
		vm.state.SetStorage(vm.context.ContractAddress, keyHex, valueBytes)

	case op == OpJUMP:
		dest := vm.popUint64()
		if !vm.validJumpdests[dest] {
			return fmt.Errorf("invalid jump destination %d", dest)
		}
		vm.pc = dest
		return nil

	case op == OpJUMPI:
		dest := vm.popUint64()
		condition := vm.pop()
		if condition.Sign() != 0 {
			if !vm.validJumpdests[dest] {
				return fmt.Errorf("invalid jump destination %d", dest)
			}
			vm.pc = dest
			return nil
		}

	case op == OpMSIZE:
		vm.pushUint64(uint64(len(vm.memory)))

	case op == OpGAS:
		vm.pushUint64(vm.gas)

	case op == OpJUMPDEST:
		// No-op marker

	case op.IsPush():
		size := op.PushSize()
		value := new(big.Int)
		end := vm.pc + 1 + uint64(size)
		if end > uint64(len(vm.code)) {
			end = uint64(len(vm.code))
		}
		bytes := vm.code[vm.pc+1 : end]
		value.SetBytes(bytes)
		vm.push(value)
		vm.pc += uint64(size)

	case op.IsDup():
		index := op.DupIndex()
		value := vm.peek(index - 1)
		vm.push(new(big.Int).Set(value))

	case op.IsSwap():
		index := op.SwapIndex()
		top := vm.stackPtr - 1
		other := vm.stackPtr - 1 - index
		vm.stack[top], vm.stack[other] = vm.stack[other], vm.stack[top]

	case op.IsLog():
		if vm.readOnly {
			return fmt.Errorf("LOG in read-only mode")
		}
		topicCount := op.LogTopics()
		offset := vm.popUint64()
		length := vm.popUint64()
		if !vm.expandMemory(offset, length) {
			return fmt.Errorf("memory expansion failed")
		}
		topics := make([]string, topicCount)
		for i := 0; i < topicCount; i++ {
			topic := vm.pop()
			topics[i] = fmt.Sprintf("%064x", topic)
		}
		var data []byte
		if length > 0 {
			data = make([]byte, length)
			copy(data, vm.memory[offset:offset+length])
		}
		log := &Log{
			ContractAddress: vm.context.ContractAddress,
			Topics:          topics,
			Data:            data,
			BlockHeight:     vm.context.BlockHeight,
		}
		vm.logs = append(vm.logs, log)

	case op == OpCALL:
		return vm.executeCall()

	case op == OpRETURN:
		offset := vm.popUint64()
		length := vm.popUint64()
		if !vm.expandMemory(offset, length) {
			return fmt.Errorf("memory expansion failed")
		}
		vm.returnData = make([]byte, length)
		if length > 0 {
			copy(vm.returnData, vm.memory[offset:offset+length])
		}
		vm.stopped = true

	case op == OpREVERT:
		offset := vm.popUint64()
		length := vm.popUint64()
		if !vm.expandMemory(offset, length) {
			return fmt.Errorf("memory expansion failed")
		}
		vm.returnData = make([]byte, length)
		if length > 0 {
			copy(vm.returnData, vm.memory[offset:offset+length])
		}
		vm.stopped = true
		vm.reverted = true

	case op == OpSELFDESTRUCT:
		if vm.readOnly {
			return fmt.Errorf("SELFDESTRUCT in read-only mode")
		}
		beneficiary := vm.pop()
		beneficiaryAddr := fmt.Sprintf("%040x", beneficiary)
		contractBalance := vm.state.GetContractBalance(vm.context.ContractAddress)
		if contractBalance > 0 {
			if vm.state.HasContract(beneficiaryAddr) {
				currentBalance := vm.state.GetContractBalance(beneficiaryAddr)
				vm.state.SetContractBalance(beneficiaryAddr, currentBalance+contractBalance)
			}
			// Wallet balance transfers are handled at the blockchain level after execution
		}
		vm.state.DeleteContract(vm.context.ContractAddress)
		vm.stopped = true

	default:
		return fmt.Errorf("unimplemented opcode 0x%02x", op)
	}

	vm.pc++
	return nil
}

func (vm *VM) executeCall() error {
	gasAlloc := vm.popUint64()
	toAddr := vm.pop()
	value := vm.pop()
	argsOffset := vm.popUint64()
	argsLength := vm.popUint64()
	retOffset := vm.popUint64()
	retLength := vm.popUint64()

	if vm.context.Depth >= MaxCallDepth {
		vm.pushUint64(0)
		return nil
	}

	targetAddress := fmt.Sprintf("%040x", toAddr)
	code := vm.state.GetCode(targetAddress)
	if code == nil {
		vm.pushUint64(0)
		return nil
	}

	if !vm.expandMemory(argsOffset, argsLength) {
		vm.pushUint64(0)
		return nil
	}

	calldata := make([]byte, argsLength)
	if argsLength > 0 {
		copy(calldata, vm.memory[argsOffset:argsOffset+argsLength])
	}

	// Cap gas at 63/64 of remaining
	maxGas := vm.gas - vm.gas/64
	if gasAlloc > maxGas {
		gasAlloc = maxGas
	}

	if value.Sign() > 0 {
		if vm.readOnly {
			vm.pushUint64(0)
			return nil
		}
		if !vm.consumeGas(GasCallValue) {
			return nil
		}
		gasAlloc += GasCallStipend
	}

	vm.gas -= gasAlloc

	snapshotID := vm.state.Snapshot()

	// Transfer value
	if value.Sign() > 0 {
		valueInt := value.Int64()
		callerBalance := vm.state.GetContractBalance(vm.context.ContractAddress)
		if callerBalance < valueInt {
			vm.state.RevertToSnapshot(snapshotID)
			vm.gas += gasAlloc
			vm.pushUint64(0)
			return nil
		}
		vm.state.SetContractBalance(vm.context.ContractAddress, callerBalance-valueInt)
		targetBalance := vm.state.GetContractBalance(targetAddress)
		vm.state.SetContractBalance(targetAddress, targetBalance+valueInt)
	}

	childContext := &ExecutionContext{
		Caller:          vm.context.ContractAddress,
		ContractAddress: targetAddress,
		Origin:          vm.context.Origin,
		Value:           value.Int64(),
		GasPrice:        vm.context.GasPrice,
		BlockHeight:     vm.context.BlockHeight,
		BlockTimestamp:  vm.context.BlockTimestamp,
		DifficultyBits: vm.context.DifficultyBits,
		GasLimit:        gasAlloc,
		Calldata:        calldata,
		Depth:           vm.context.Depth + 1,
	}

	childVM := NewVM(code, childContext, vm.state, gasAlloc)
	childVM.readOnly = vm.readOnly
	result := childVM.Execute()

	if result.Reverted || result.Err != nil {
		vm.state.RevertToSnapshot(snapshotID)
		vm.gas += gasAlloc - result.GasUsed
		vm.pushUint64(0)
	} else {
		vm.state.ClearSnapshots()
		vm.gas += gasAlloc - result.GasUsed
		vm.logs = append(vm.logs, result.Logs...)
		vm.pushUint64(1)
	}

	// Copy return data
	vm.returnData = result.ReturnData
	if retLength > 0 && len(result.ReturnData) > 0 {
		if !vm.expandMemory(retOffset, retLength) {
			return nil
		}
		copyLen := retLength
		if uint64(len(result.ReturnData)) < copyLen {
			copyLen = uint64(len(result.ReturnData))
		}
		copy(vm.memory[retOffset:retOffset+copyLen], result.ReturnData[:copyLen])
	}

	return nil
}

func (vm *VM) calcExpGas() uint64 {
	if vm.stackPtr < 2 {
		return GasExpBase
	}
	exponent := vm.peek(1)
	byteLen := (exponent.BitLen() + 7) / 8
	return GasExpBase + GasExpPerByte*uint64(byteLen)
}

func (vm *VM) calcSha256Gas() uint64 {
	if vm.stackPtr < 2 {
		return GasSha256Base
	}
	length := vm.peek(1)
	return CalcSha256Gas(int(length.Uint64()))
}

func (vm *VM) calcCopyGas() uint64 {
	if vm.stackPtr < 3 {
		return 0
	}
	length := vm.peek(2)
	return CalcCopyGas(int(length.Uint64()))
}

func (vm *VM) calcLogGas(op Opcode) uint64 {
	if vm.stackPtr < 2 {
		return GasLogBase
	}
	length := vm.peek(1)
	return CalcLogGas(int(length.Uint64()), op.LogTopics())
}

func (vm *VM) calcSstoreGas() uint64 {
	if vm.stackPtr < 2 {
		return GasSstoreUpdate
	}
	key := vm.peek(0)
	keyHex := fmt.Sprintf("%064x", key)
	existing := vm.state.GetStorage(vm.context.ContractAddress, keyHex)
	if existing == nil {
		return GasSstoreNew
	}
	return GasSstoreUpdate
}

func (vm *VM) errorResult(err error) *ExecutionResult {
	vm.stopped = true
	totalGas := vm.context.GasLimit
	if totalGas == 0 {
		// Fallback: use initial gas that was set
		totalGas = vm.gas
	}
	return &ExecutionResult{
		GasUsed: totalGas,
		Err:     err,
	}
}

func (vm *VM) outOfGasResult() *ExecutionResult {
	return &ExecutionResult{
		GasUsed: vm.context.GasLimit,
		Err:     fmt.Errorf("out of gas"),
	}
}

// ExecuteContract runs contract bytecode in the VM and returns the result.
// This is the main entry point for contract execution from the blockchain.
func ExecuteContract(state *StateDB, contractAddress string, context *ExecutionContext, gasLimit uint64) *ExecutionResult {
	code := state.GetCode(contractAddress)
	if code == nil {
		return &ExecutionResult{
			Err: fmt.Errorf("contract not found: %s", contractAddress),
		}
	}

	context.GasLimit = gasLimit
	vm := NewVM(code, context, state, gasLimit)
	return vm.Execute()
}

// ExecuteDeployment runs deployment bytecode and returns the runtime code.
func ExecuteDeployment(state *StateDB, bytecode []byte, context *ExecutionContext, gasLimit uint64) (*ExecutionResult, []byte) {
	context.GasLimit = gasLimit
	vm := NewVM(bytecode, context, state, gasLimit)
	result := vm.Execute()

	if result.Err != nil || result.Reverted {
		return result, nil
	}

	return result, result.ReturnData
}

// ExecuteReadOnly runs a contract call in read-only mode (no state changes).
func ExecuteReadOnly(state *StateDB, contractAddress string, context *ExecutionContext, gasLimit uint64) *ExecutionResult {
	code := state.GetCode(contractAddress)
	if code == nil {
		return &ExecutionResult{
			Err: fmt.Errorf("contract not found: %s", contractAddress),
		}
	}

	context.GasLimit = gasLimit
	vm := NewVM(code, context, state, gasLimit)
	vm.readOnly = true
	return vm.Execute()
}

// addressToBytes converts a hex address string to 20 bytes
func addressToBytes(address string) []byte {
	bytes, _ := hex.DecodeString(address)
	if len(bytes) > 20 {
		return bytes[:20]
	}
	return bytes
}

// bytesToAddress converts bytes to a 40-char hex address string
func bytesToAddress(data []byte) string {
	if len(data) > 20 {
		data = data[len(data)-20:]
	}
	return hex.EncodeToString(data)
}

// uint64ToBytes converts a uint64 to big-endian bytes
func uint64ToBytes(value uint64) []byte {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, value)
	return bytes
}
