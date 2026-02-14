//go:build cuda

package main

/*
#cgo LDFLAGS: -L${SRCDIR}/cuda -L/usr/local/cuda/lib64 -lgpuminer -lcudart -lstdc++
#cgo CFLAGS: -I/usr/local/cuda/include

#include <stdint.h>
#include <stdlib.h>

int gpu_init(int device_id);
void gpu_cleanup();
int gpu_get_sm_count(int device_id);

int gpu_mine_batch(
    const uint32_t* midstate,
    const uint8_t*  tail,
    int             tail_len,
    uint64_t        total_prefix_len,
    const uint8_t*  suffix,
    int             suffix_len,
    int             diff_bits,
    uint64_t        start_nonce,
    uint64_t        batch_size,
    uint64_t*       result_nonce,
    uint8_t*        result_hash
);
*/
import "C"
import (
	"context"
	"crypto/sha256"
	"encoding"
	"fmt"
	"sync/atomic"
	"unsafe"
)

// GPUMiningAvailable is set to true when compiled with CUDA support
const GPUMiningAvailable = true

// GPUWorker performs GPU mining using CUDA
type GPUWorker struct {
	ID        int
	DeviceID  int
	BatchSize uint64
	HashCount atomic.Int64
}

// GPUInit initializes the CUDA device
func GPUInit(deviceID int) error {
	result := C.gpu_init(C.int(deviceID))
	if result != 0 {
		return fmt.Errorf("failed to initialize CUDA device %d", deviceID)
	}
	return nil
}

// GPUCleanup releases CUDA resources
func GPUCleanup() {
	C.gpu_cleanup()
}

// GPUGetSMCount returns the number of streaming multiprocessors on the GPU
func GPUGetSMCount(deviceID int) int {
	return int(C.gpu_get_sm_count(C.int(deviceID)))
}

// Mine searches for a valid nonce using GPU acceleration
func (w *GPUWorker) Mine(
	ctx context.Context,
	midstate []byte,
	prefixTail []byte,
	suffix []byte,
	startNonce int64,
	stride int64, // ignored for GPU - GPU processes batches
	diffBits int,
	result chan<- MiningResult,
) {
	// Go sha256 MarshalBinary format:
	//   bytes 0-3:    magic "sha\x03"
	//   bytes 4-35:   H[0..7] as big-endian uint32s
	//   bytes 36-99:  block buffer (64 bytes)
	//   bytes 100-107: bytes processed (big-endian uint64)
	var midstateWords [8]uint32
	for i := 0; i < 8; i++ {
		offset := 4 + i*4 // H values start after 4-byte magic
		if offset+4 <= len(midstate) {
			midstateWords[i] = uint32(midstate[offset])<<24 |
				uint32(midstate[offset+1])<<16 |
				uint32(midstate[offset+2])<<8 |
				uint32(midstate[offset+3])
		}
	}

	// Read bytes-processed count from end of marshaled state (big-endian uint64 at offset 100)
	var totalPrefixLen uint64
	if len(midstate) >= 108 {
		totalPrefixLen = uint64(midstate[100])<<56 | uint64(midstate[101])<<48 |
			uint64(midstate[102])<<40 | uint64(midstate[103])<<32 |
			uint64(midstate[104])<<24 | uint64(midstate[105])<<16 |
			uint64(midstate[106])<<8 | uint64(midstate[107])
	}
	totalPrefixLen += uint64(len(prefixTail))

	// Safety: ensure slices are non-empty for unsafe.Pointer
	if len(prefixTail) == 0 {
		prefixTail = []byte{0}
	}
	if len(suffix) == 0 {
		suffix = []byte("0")
	}

	nonce := uint64(startNonce)
	var localHashes int64

	for {
		select {
		case <-ctx.Done():
			w.HashCount.Add(localHashes)
			return
		default:
		}

		var resultNonce C.uint64_t
		var resultHash [32]C.uint8_t

		ret := C.gpu_mine_batch(
			(*C.uint32_t)(unsafe.Pointer(&midstateWords[0])),
			(*C.uint8_t)(unsafe.Pointer(&prefixTail[0])),
			C.int(len(prefixTail)),
			C.uint64_t(totalPrefixLen),
			(*C.uint8_t)(unsafe.Pointer(&suffix[0])),
			C.int(len(suffix)),
			C.int(diffBits),
			C.uint64_t(nonce),
			C.uint64_t(w.BatchSize),
			&resultNonce,
			&resultHash[0],
		)

		localHashes += int64(w.BatchSize)

		if ret < 0 {
			// Error
			w.HashCount.Add(localHashes)
			return
		}

		if ret > 0 {
			// Found!
			w.HashCount.Add(localHashes)

			var hash [32]byte
			for i := 0; i < 32; i++ {
				hash[i] = byte(resultHash[i])
			}

			select {
			case result <- MiningResult{Nonce: int64(resultNonce), Hash: hash}:
			default:
			}
			return
		}

		nonce += w.BatchSize

		// Report progress periodically
		if localHashes%(int64(w.BatchSize)*10) == 0 {
			w.HashCount.Add(localHashes)
			localHashes = 0
		}
	}
}

// ComputeMidstateFromPrefix computes SHA-256 midstate from a prefix
// This is a helper for GPU mining
func ComputeMidstateFromPrefix(prefix []byte) ([]byte, []byte) {
	h := sha256.New()
	fullBlocks := (len(prefix) / 64) * 64
	if fullBlocks > 0 {
		h.Write(prefix[:fullBlocks])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	tail := prefix[fullBlocks:]
	return midstate, tail
}
