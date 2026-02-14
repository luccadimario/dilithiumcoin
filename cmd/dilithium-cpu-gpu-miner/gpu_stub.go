//go:build !cuda

package main

import (
	"context"
	"fmt"
	"sync/atomic"
)

// GPUMiningAvailable is set to false when compiled without CUDA support
const GPUMiningAvailable = false

// GPUWorker is a stub type when CUDA is not available
type GPUWorker struct {
	ID        int
	DeviceID  int
	BatchSize uint64
	HashCount atomic.Int64
}

// GPUInit returns an error when CUDA is not available
func GPUInit(deviceID int) error {
	return fmt.Errorf("GPU mining not available - rebuild with 'make gpu' or build tag 'cuda'")
}

// GPUCleanup is a no-op when CUDA is not available
func GPUCleanup() {}

// GPUGetSMCount returns 0 when CUDA is not available
func GPUGetSMCount(deviceID int) int {
	return 0
}

// Mine returns an error when CUDA is not available
func (w *GPUWorker) Mine(
	ctx context.Context,
	midstate []byte,
	prefixTail []byte,
	suffix []byte,
	startNonce int64,
	stride int64,
	diffBits int,
	result chan<- MiningResult,
) {
	// Should never be called, but if it is, do nothing
}
