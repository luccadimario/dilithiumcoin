package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
)

type ABIType uint8

const (
	ABIUint256 ABIType = iota
	ABIInt256
	ABIAddress
	ABIBool
	ABIBytes32
	ABIBytes
)

func FunctionSelector(signature string) [4]byte {
	hash := sha256.Sum256([]byte(signature))
	var selector [4]byte
	copy(selector[:], hash[:4])
	return selector
}

func FunctionSelectorHex(signature string) string {
	sel := FunctionSelector(signature)
	return hex.EncodeToString(sel[:])
}

func EncodeCall(funcSig string, args ...interface{}) ([]byte, error) {
	selector := FunctionSelector(funcSig)
	result := make([]byte, 4)
	copy(result, selector[:])

	for index, arg := range args {
		encoded, err := encodeArgument(arg)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", index, err)
		}
		result = append(result, encoded...)
	}

	return result, nil
}

func encodeArgument(arg interface{}) ([]byte, error) {
	padded := make([]byte, 32)

	switch value := arg.(type) {
	case uint64:
		binary.BigEndian.PutUint64(padded[24:], value)
		return padded, nil

	case int64:
		if value >= 0 {
			binary.BigEndian.PutUint64(padded[24:], uint64(value))
		} else {
			// Two's complement for negative values
			for i := range padded {
				padded[i] = 0xFF
			}
			binary.BigEndian.PutUint64(padded[24:], uint64(value))
		}
		return padded, nil

	case *big.Int:
		if value == nil {
			return padded, nil
		}
		bytes := value.Bytes()
		if len(bytes) > 32 {
			return nil, fmt.Errorf("big.Int exceeds 32 bytes")
		}
		if value.Sign() >= 0 {
			copy(padded[32-len(bytes):], bytes)
		} else {
			// Two's complement
			for i := range padded {
				padded[i] = 0xFF
			}
			abs := new(big.Int).Abs(value)
			abs.Sub(abs, big.NewInt(1))
			absBytes := abs.Bytes()
			for i := range padded {
				padded[i] = ^padded[i]
			}
			copy(padded[32-len(absBytes):], absBytes)
			// Negate via two's complement
			carry := true
			for i := 31; i >= 0; i-- {
				if carry {
					padded[i]++
					carry = padded[i] == 0
				}
			}
			// Simpler: set all FF then put the int64 representation
			for i := range padded {
				padded[i] = 0xFF
			}
			binary.BigEndian.PutUint64(padded[24:], uint64(value.Int64()))
		}
		return padded, nil

	case bool:
		if value {
			padded[31] = 1
		}
		return padded, nil

	case string:
		// Treat as hex address
		addrBytes, err := hex.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("invalid hex string: %w", err)
		}
		if len(addrBytes) > 32 {
			return nil, fmt.Errorf("hex string exceeds 32 bytes")
		}
		copy(padded[32-len(addrBytes):], addrBytes)
		return padded, nil

	case [32]byte:
		return value[:], nil

	case []byte:
		if len(value) > 32 {
			return nil, fmt.Errorf("byte slice exceeds 32 bytes for fixed encoding")
		}
		copy(padded[32-len(value):], value)
		return padded, nil

	default:
		return nil, fmt.Errorf("unsupported argument type: %T", arg)
	}
}

func DecodeArgs(data []byte, types ...ABIType) ([]interface{}, error) {
	if len(data) < len(types)*32 {
		return nil, fmt.Errorf("data too short: need %d bytes, have %d", len(types)*32, len(data))
	}

	results := make([]interface{}, len(types))
	for index, abiType := range types {
		offset := index * 32
		word := data[offset : offset+32]

		switch abiType {
		case ABIUint256:
			value := new(big.Int).SetBytes(word)
			results[index] = value

		case ABIInt256:
			value := new(big.Int).SetBytes(word)
			// Check sign bit
			if word[0]&0x80 != 0 {
				// Negative: compute two's complement
				max := new(big.Int).Lsh(big.NewInt(1), 256)
				value.Sub(value, max)
			}
			results[index] = value

		case ABIAddress:
			results[index] = hex.EncodeToString(word[12:32])

		case ABIBool:
			results[index] = word[31] != 0

		case ABIBytes32:
			var fixed [32]byte
			copy(fixed[:], word)
			results[index] = fixed

		case ABIBytes:
			// For dynamic bytes, the word is an offset — simplified: just return the raw word
			results[index] = word

		default:
			return nil, fmt.Errorf("unsupported ABI type: %d", abiType)
		}
	}

	return results, nil
}
