#define _ALLOW_COMPILER_AND_STL_VERSION_MISMATCH

#include "bridge.h"
#include "sha256.cuh"
#include <stdio.h>
#include <string.h>
#include <cuda_runtime.h>

// ============================================================================
// Mining Kernel Parameters
// ============================================================================

struct MiningParams {
    uint32_t midstate[8];
    uint8_t  tail[64];
    int      tail_len;
    uint64_t total_prefix_len;
    uint8_t  suffix[8];
    int      suffix_len;
    uint64_t nonce_start;
    int      difficulty_bits;
};

// ============================================================================
// Optimized Mining Kernel
// ============================================================================

__global__ void __launch_bounds__(256, 5)
mine_kernel(
    MiningParams params,
    uint64_t batch_size,
    uint64_t* result_nonce,
    int* result_found
) {
    // Calculate global thread ID
    uint64_t tid = uint64_t(blockIdx.x) * blockDim.x + threadIdx.x;
    if (tid >= batch_size) return;

    // Compute nonce for this thread
    uint64_t nonce = params.nonce_start + tid;

    // Convert nonce to decimal string
    uint8_t nonce_str[20];
    int nonce_len = uint64_to_str(nonce, nonce_str);

    // Calculate total message length
    uint64_t total_msg_len = params.total_prefix_len + nonce_len + params.suffix_len;
    uint64_t total_bit_len = total_msg_len * 8;

    // Build data buffer: tail + nonce + suffix + padding + length
    uint8_t data[128];
    int pos = 0;

    // Copy tail
    for (int i = 0; i < params.tail_len; i++) {
        data[pos++] = params.tail[i];
    }

    // Append nonce string
    for (int i = 0; i < nonce_len; i++) {
        data[pos++] = nonce_str[i];
    }

    // Append suffix (difficulty)
    for (int i = 0; i < params.suffix_len; i++) {
        data[pos++] = params.suffix[i];
    }

    // SHA-256 padding
    data[pos++] = 0x80;

    // Determine number of blocks needed
    int num_blocks;
    if (pos + 8 <= 64) {
        // Fits in one block
        num_blocks = 1;
        while (pos < 56) data[pos++] = 0;
    } else {
        // Need two blocks
        num_blocks = 2;
        while (pos < 64) data[pos++] = 0;
        while (pos < 120) data[pos++] = 0;
    }

    // Append 64-bit length (big-endian) at end of last block
    int len_offset = num_blocks * 64 - 8;
    data[len_offset + 0] = uint8_t(total_bit_len >> 56);
    data[len_offset + 1] = uint8_t(total_bit_len >> 48);
    data[len_offset + 2] = uint8_t(total_bit_len >> 40);
    data[len_offset + 3] = uint8_t(total_bit_len >> 32);
    data[len_offset + 4] = uint8_t(total_bit_len >> 24);
    data[len_offset + 5] = uint8_t(total_bit_len >> 16);
    data[len_offset + 6] = uint8_t(total_bit_len >> 8);
    data[len_offset + 7] = uint8_t(total_bit_len);

    // Initialize state from midstate
    uint32_t state[8];
    #pragma unroll
    for (int i = 0; i < 8; i++) {
        state[i] = params.midstate[i];
    }

    // Process remaining blocks
    for (int blk = 0; blk < num_blocks; blk++) {
        uint32_t W[16];
        const uint8_t* bp = data + blk * 64;

        // Pack bytes into words (big-endian)
        #pragma unroll
        for (int i = 0; i < 16; i++) {
            W[i] = pack_be32(bp + i * 4);
        }

        sha256_compress(state, W);
    }

    // Check if this hash meets difficulty
    if (meets_difficulty(state, params.difficulty_bits)) {
        // Atomic write to result (first one wins)
        if (atomicCAS(result_found, 0, 1) == 0) {
            *result_nonce = nonce;

            // Store hash (convert state to bytes)
            uint32_t* hash_out = (uint32_t*)(result_nonce + 1);
            #pragma unroll
            for (int i = 0; i < 8; i++) {
                hash_out[i] = state[i];
            }
        }
    }
}

// ============================================================================
// Host-side Bridge Functions
// ============================================================================

static uint64_t* d_result_nonce = NULL;
static int* d_result_found = NULL;

extern "C" {

int gpu_init(int device_id) {
    cudaError_t err = cudaSetDevice(device_id);
    if (err != cudaSuccess) {
        fprintf(stderr, "[GPU] Error setting device %d: %s\n",
                device_id, cudaGetErrorString(err));
        return -1;
    }

    // Allocate result buffers (nonce + 8 words for hash)
    err = cudaMalloc(&d_result_nonce, sizeof(uint64_t) * 9);
    if (err != cudaSuccess) {
        fprintf(stderr, "[GPU] cudaMalloc error: %s\n", cudaGetErrorString(err));
        return -1;
    }

    err = cudaMalloc(&d_result_found, sizeof(int));
    if (err != cudaSuccess) {
        fprintf(stderr, "[GPU] cudaMalloc error: %s\n", cudaGetErrorString(err));
        cudaFree(d_result_nonce);
        return -1;
    }

    printf("[GPU] Initialized device %d\n", device_id);
    return 0;
}

void gpu_cleanup() {
    if (d_result_nonce) {
        cudaFree(d_result_nonce);
        d_result_nonce = NULL;
    }
    if (d_result_found) {
        cudaFree(d_result_found);
        d_result_found = NULL;
    }
    printf("[GPU] Cleanup complete\n");
}

int gpu_get_sm_count(int device_id) {
    int sm_count = 0;
    cudaDeviceGetAttribute(&sm_count, cudaDevAttrMultiProcessorCount, device_id);
    return sm_count;
}

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
) {
    // Validate inputs
    if (tail_len < 0 || tail_len > 64) {
        fprintf(stderr, "[GPU] Invalid tail_len: %d\n", tail_len);
        return -1;
    }
    if (suffix_len < 0 || suffix_len > 8) {
        fprintf(stderr, "[GPU] Invalid suffix_len: %d\n", suffix_len);
        return -1;
    }
    if (diff_bits <= 0 || diff_bits > 256) {
        fprintf(stderr, "[GPU] Invalid diff_bits: %d\n", diff_bits);
        return -1;
    }
    if (!d_result_nonce || !d_result_found) {
        fprintf(stderr, "[GPU] Device memory not initialized\n");
        return -1;
    }

    // Prepare parameters
    MiningParams params;
    memset(&params, 0, sizeof(params));
    memcpy(params.midstate, midstate, 32);
    memcpy(params.tail, tail, tail_len);
    params.tail_len = tail_len;
    params.total_prefix_len = total_prefix_len;
    memcpy(params.suffix, suffix, suffix_len);
    params.suffix_len = suffix_len;
    params.nonce_start = start_nonce;
    params.difficulty_bits = diff_bits;

    // Reset result buffers
    cudaMemsetAsync(d_result_found, 0, sizeof(int));
    cudaMemsetAsync(d_result_nonce, 0, sizeof(uint64_t) * 9);

    // Check for pre-launch errors
    cudaError_t err = cudaGetLastError();
    if (err != cudaSuccess) {
        fprintf(stderr, "[GPU] Pre-launch error: %s\n", cudaGetErrorString(err));
        return -1;
    }

    // Launch kernel
    const int threads_per_block = 256;
    uint64_t grid_size = (batch_size + threads_per_block - 1) / threads_per_block;
    if (grid_size > 2147483647ULL) {
        grid_size = 2147483647ULL;
    }

    mine_kernel<<<(unsigned int)grid_size, threads_per_block>>>(
        params, batch_size, d_result_nonce, d_result_found
    );

    // Wait for completion
    err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        fprintf(stderr, "[GPU] Kernel error: %s\n", cudaGetErrorString(err));
        return -1;
    }

    // Check if solution found
    int found = 0;
    cudaMemcpy(&found, d_result_found, sizeof(int), cudaMemcpyDeviceToHost);

    if (found) {
        // Copy result
        uint64_t host_result[9];
        cudaMemcpy(host_result, d_result_nonce, sizeof(uint64_t) * 9, cudaMemcpyDeviceToHost);

        *result_nonce = host_result[0];

        // Extract hash (big-endian format)
        uint32_t* hash_words = (uint32_t*)(host_result + 1);
        for (int i = 0; i < 8; i++) {
            result_hash[i*4 + 0] = (hash_words[i] >> 24) & 0xFF;
            result_hash[i*4 + 1] = (hash_words[i] >> 16) & 0xFF;
            result_hash[i*4 + 2] = (hash_words[i] >> 8) & 0xFF;
            result_hash[i*4 + 3] = hash_words[i] & 0xFF;
        }

        return 1;
    }

    return 0;
}

} // extern "C"
