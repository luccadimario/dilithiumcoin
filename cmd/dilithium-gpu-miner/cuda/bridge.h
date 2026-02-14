#ifndef DILITHIUM_CUDA_BRIDGE_H
#define DILITHIUM_CUDA_BRIDGE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Initialize GPU device
// Returns 0 on success, -1 on error
int gpu_init(int device_id);

// Cleanup GPU resources
void gpu_cleanup(void);

// Get number of streaming multiprocessors on device
int gpu_get_sm_count(int device_id);

// Mine a batch of nonces
// Returns: 1 if solution found, 0 if not found, -1 on error
//
// Parameters:
//   midstate: 32-byte SHA-256 midstate (8x uint32_t)
//   tail: remaining prefix bytes after midstate blocks (0-63 bytes)
//   tail_len: length of tail
//   total_prefix_len: total length of original prefix
//   suffix: difficulty suffix bytes
//   suffix_len: length of suffix
//   diff_bits: difficulty in bits (number of leading zero bits required)
//   start_nonce: starting nonce value
//   batch_size: number of nonces to test
//   result_nonce: output - the winning nonce if found
//   result_hash: output - 32-byte hash if found
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

#ifdef __cplusplus
}
#endif

#endif // DILITHIUM_CUDA_BRIDGE_H
