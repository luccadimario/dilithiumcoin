package main

const (
	MaxGasLimit      uint64 = 10_000_000
	MinGasPrice      int64  = 100
	GasPerByte       uint64 = 4
	GasPerDeployByte uint64 = 200
	BaseDeployGas    uint64 = 32000
	BaseCallGas      uint64 = 21000
	MaxCodeSize      uint64 = 24576
	MaxCalldataSize  uint64 = 8192
	MaxCallDepth     int    = 256
	MaxMemorySize    uint64 = 1024 * 1024

	GasSstoreUpdate uint64 = 5000
	GasSstoreNew    uint64 = 20000

	GasLogBase     uint64 = 375
	GasLogTopic    uint64 = 375
	GasLogDataByte uint64 = 8

	GasExpBase    uint64 = 10
	GasExpPerByte uint64 = 10

	GasSha256Base    uint64 = 30
	GasSha256PerWord uint64 = 6

	GasCopyPerWord uint64 = 3

	GasCallValue     uint64 = 9000
	GasCallStipend   uint64 = 2300
	GasMemoryPerWord uint64 = 3
)

func CalcDeployGas(bytecodeLength int) uint64 {
	return BaseDeployGas + GasPerDeployByte*uint64(bytecodeLength)
}

func CalcCallGas(calldataLength int) uint64 {
	return BaseCallGas + GasPerByte*uint64(calldataLength)
}

func CalcExpGas(exponentByteLength int) uint64 {
	return GasExpBase + GasExpPerByte*uint64(exponentByteLength)
}

func CalcSha256Gas(dataLength int) uint64 {
	words := (uint64(dataLength) + 31) / 32
	return GasSha256Base + GasSha256PerWord*words
}

func CalcLogGas(dataLength int, topicCount int) uint64 {
	return GasLogBase + GasLogTopic*uint64(topicCount) + GasLogDataByte*uint64(dataLength)
}

func CalcCopyGas(length int) uint64 {
	words := (uint64(length) + 31) / 32
	return GasCopyPerWord * words
}

func CalcMemoryExpansionGas(currentSize, requiredSize uint64) uint64 {
	if requiredSize <= currentSize {
		return 0
	}
	currentWords := (currentSize + 31) / 32
	newWords := (requiredSize + 31) / 32
	if newWords <= currentWords {
		return 0
	}
	currentCost := currentWords*GasMemoryPerWord + (currentWords*currentWords)/512
	newCost := newWords*GasMemoryPerWord + (newWords*newWords)/512
	return newCost - currentCost
}
