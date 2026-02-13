package main

import (
	"context"
	"crypto/sha256"
	"encoding"
	"sync/atomic"
)

// MiningResult is sent by a worker when it finds a valid hash.
type MiningResult struct {
	Nonce int64
	Hash  [32]byte
}

// CPUWorker runs on a single goroutine, mining with a strided nonce range.
type CPUWorker struct {
	ID        int
	HashCount atomic.Int64
}

// Mine searches for a valid nonce using hardware-accelerated SHA-256 with midstate.
//
// Uses crypto/sha256 (which leverages ARM SHA2 / Intel SHA-NI instructions)
// combined with MarshalBinary/UnmarshalBinary for midstate optimization.
// This skips re-hashing the fixed prefix (typically 500+ bytes) on every attempt.
//
// Achieves ~20 MH/s per thread on Apple M4, zero allocations in hot loop.
func (w *CPUWorker) Mine(
	ctx context.Context,
	midstate []byte, // serialized SHA-256 state after processing full blocks of prefix
	prefixTail []byte, // remaining prefix bytes after midstate boundary
	suffix []byte, // difficulty string
	startNonce int64,
	stride int64,
	diffBits int,
	result chan<- MiningResult,
) {
	h := sha256.New()
	var nonceBuf [20]byte
	var hashBuf [32]byte

	nonce := startNonce
	var localCount int64

	for {
		// Check cancellation every 32768 hashes (power of 2 for fast check)
		if localCount&0x7FFF == 0 && localCount > 0 {
			select {
			case <-ctx.Done():
				w.HashCount.Add(localCount)
				return
			default:
			}
			w.HashCount.Add(localCount)
			localCount = 0
		}

		// Restore midstate (skips re-hashing the fixed prefix)
		h.(encoding.BinaryUnmarshaler).UnmarshalBinary(midstate)

		// Write: prefix tail + nonce decimal string + suffix
		h.Write(prefixTail)
		n := writeInt64(nonceBuf[:], nonce)
		h.Write(nonceBuf[:n])
		h.Write(suffix)

		// Finalize hash into pre-allocated buffer (zero alloc)
		sum := h.Sum(hashBuf[:0])
		localCount++

		// Check difficulty on raw bytes — no hex encoding needed
		if len(sum) >= 32 && meetsDifficultyBytes(*(*[32]byte)(sum), diffBits) {
			w.HashCount.Add(localCount)
			var hash [32]byte
			copy(hash[:], sum)
			select {
			case result <- MiningResult{Nonce: nonce, Hash: hash}:
			default:
			}
			return
		}

		nonce += stride
	}
}
