#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <cuda_runtime.h>

// ============================================================================
// SHA-256 Constants and Device Functions
// ============================================================================

__constant__ uint32_t d_K[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
    0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
    0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
    0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
    0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
    0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
};

__device__ __forceinline__ uint32_t d_rotr(uint32_t x, int n) {
    return __funnelshift_r(x, x, n);
}

__device__ __forceinline__ uint32_t d_Ch(uint32_t e, uint32_t f, uint32_t g) {
    return (e & f) ^ (~e & g);
}

__device__ __forceinline__ uint32_t d_Maj(uint32_t a, uint32_t b, uint32_t c) {
    return (a & b) ^ (a & c) ^ (b & c);
}

__device__ __forceinline__ uint32_t d_Sigma0(uint32_t a) {
    return d_rotr(a, 2) ^ d_rotr(a, 13) ^ d_rotr(a, 22);
}

__device__ __forceinline__ uint32_t d_Sigma1(uint32_t e) {
    return d_rotr(e, 6) ^ d_rotr(e, 11) ^ d_rotr(e, 25);
}

__device__ __forceinline__ uint32_t d_sigma0(uint32_t x) {
    return d_rotr(x, 7) ^ d_rotr(x, 18) ^ (x >> 3);
}

__device__ __forceinline__ uint32_t d_sigma1(uint32_t x) {
    return d_rotr(x, 17) ^ d_rotr(x, 19) ^ (x >> 10);
}

__device__ __forceinline__ uint32_t pack_be32(const uint8_t* p) {
    return (uint32_t(p[0]) << 24) | (uint32_t(p[1]) << 16) |
           (uint32_t(p[2]) << 8)  |  uint32_t(p[3]);
}

__device__ void sha256_compress(uint32_t state[8], const uint32_t block[16]) {
    uint32_t W[16];
    #pragma unroll
    for (int i = 0; i < 16; i++) {
        W[i] = block[i];
    }

    uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
    uint32_t e = state[4], f = state[5], g = state[6], h = state[7];

    #pragma unroll
    for (int i = 0; i < 16; i++) {
        uint32_t temp1 = h + d_Sigma1(e) + d_Ch(e, f, g) + d_K[i] + W[i];
        uint32_t temp2 = d_Sigma0(a) + d_Maj(a, b, c);
        h = g; g = f; f = e; e = d + temp1;
        d = c; c = b; b = a; a = temp1 + temp2;
    }

    #pragma unroll
    for (int i = 16; i < 64; i++) {
        W[i & 15] += d_sigma1(W[(i - 2) & 15]) + W[(i - 7) & 15] +
                      d_sigma0(W[(i - 15) & 15]);
        uint32_t w = W[i & 15];

        uint32_t temp1 = h + d_Sigma1(e) + d_Ch(e, f, g) + d_K[i] + w;
        uint32_t temp2 = d_Sigma0(a) + d_Maj(a, b, c);
        h = g; g = f; f = e; e = d + temp1;
        d = c; c = b; b = a; a = temp1 + temp2;
    }

    state[0] += a; state[1] += b; state[2] += c; state[3] += d;
    state[4] += e; state[5] += f; state[6] += g; state[7] += h;
}

__device__ int uint64_to_str(uint64_t val, uint8_t* buf) {
    if (val == 0) {
        buf[0] = '0';
        return 1;
    }

    uint8_t tmp[20];
    int len = 0;
    while (val > 0) {
        tmp[len++] = '0' + (uint8_t)(val % 10);
        val /= 10;
    }

    for (int i = 0; i < len; i++) {
        buf[i] = tmp[len - 1 - i];
    }
    return len;
}

__device__ bool meets_difficulty(const uint32_t hash_state[8], int bits) {
    int full_words = bits / 32;
    for (int i = 0; i < full_words; i++) {
        if (hash_state[i] != 0) return false;
    }

    int remaining = bits % 32;
    if (remaining > 0) {
        uint32_t mask = 0xFFFFFFFFu << (32 - remaining);
        if ((hash_state[full_words] & mask) != 0) return false;
    }

    return true;
}

// ============================================================================
// Mining Kernel Parameters (passed via struct)
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
// Mining Kernel
// ============================================================================

__global__ void __launch_bounds__(256, 5)
mine_kernel(MiningParams params, uint64_t batch_size,
            uint64_t* result_nonce, int* result_found) {

    uint64_t tid = uint64_t(blockIdx.x) * blockDim.x + threadIdx.x;
    if (tid >= batch_size) return;

    uint64_t nonce = params.nonce_start + tid;

    uint8_t nonce_str[20];
    int nonce_len = uint64_to_str(nonce, nonce_str);

    uint64_t total_msg_len = params.total_prefix_len + nonce_len + params.suffix_len;
    uint64_t total_bit_len = total_msg_len * 8;

    uint8_t data[128];
    int pos = 0;

    for (int i = 0; i < params.tail_len; i++) {
        data[pos++] = params.tail[i];
    }

    for (int i = 0; i < nonce_len; i++) {
        data[pos++] = nonce_str[i];
    }

    for (int i = 0; i < params.suffix_len; i++) {
        data[pos++] = params.suffix[i];
    }

    data[pos++] = 0x80;

    int num_blocks;
    if (pos + 8 <= 64) {
        num_blocks = 1;
        while (pos < 56) data[pos++] = 0;
    } else {
        num_blocks = 2;
        while (pos < 64) data[pos++] = 0;
        while (pos < 120) data[pos++] = 0;
    }

    int len_offset = num_blocks * 64 - 8;
    data[len_offset + 0] = uint8_t(total_bit_len >> 56);
    data[len_offset + 1] = uint8_t(total_bit_len >> 48);
    data[len_offset + 2] = uint8_t(total_bit_len >> 40);
    data[len_offset + 3] = uint8_t(total_bit_len >> 32);
    data[len_offset + 4] = uint8_t(total_bit_len >> 24);
    data[len_offset + 5] = uint8_t(total_bit_len >> 16);
    data[len_offset + 6] = uint8_t(total_bit_len >> 8);
    data[len_offset + 7] = uint8_t(total_bit_len);

    uint32_t state[8];
    #pragma unroll
    for (int i = 0; i < 8; i++) {
        state[i] = params.midstate[i];
    }

    for (int blk = 0; blk < num_blocks; blk++) {
        uint32_t W[16];
        const uint8_t* bp = data + blk * 64;
        #pragma unroll
        for (int i = 0; i < 16; i++) {
            W[i] = pack_be32(bp + i * 4);
        }
        sha256_compress(state, W);
    }

    if (meets_difficulty(state, params.difficulty_bits)) {
        if (atomicCAS(result_found, 0, 1) == 0) {
            *result_nonce = nonce;
            // Store hash result
            for (int i = 0; i < 8; i++) {
                ((uint32_t*)result_nonce)[i+2] = state[i];
            }
        }
    }
}

// ============================================================================
// Host-side Bridge Functions (C API for cgo)
// ============================================================================

static uint64_t* d_result_nonce = NULL;
static int* d_result_found = NULL;

extern "C" {

int gpu_init(int device_id) {
    cudaError_t err = cudaSetDevice(device_id);
    if (err != cudaSuccess) {
        fprintf(stderr, "CUDA error setting device %d: %s\n",
                device_id, cudaGetErrorString(err));
        return -1;
    }

    err = cudaMalloc(&d_result_nonce, sizeof(uint64_t) * 10); // nonce + 8 words for hash
    if (err != cudaSuccess) {
        fprintf(stderr, "CUDA malloc error: %s\n", cudaGetErrorString(err));
        return -1;
    }

    err = cudaMalloc(&d_result_found, sizeof(int));
    if (err != cudaSuccess) {
        fprintf(stderr, "CUDA malloc error: %s\n", cudaGetErrorString(err));
        return -1;
    }

    return 0;
}

void gpu_cleanup() {
    if (d_result_nonce) { cudaFree(d_result_nonce); d_result_nonce = NULL; }
    if (d_result_found) { cudaFree(d_result_found); d_result_found = NULL; }
}

int gpu_get_sm_count(int device_id) {
    int sm_count = 0;
    cudaDeviceGetAttribute(&sm_count, cudaDevAttrMultiProcessorCount, device_id);
    return sm_count;
}

// Mine a batch of nonces
// Returns 1 if found, 0 if not found, -1 on error
// result_nonce and result_hash are output parameters (32 bytes for hash)
int gpu_mine_batch(
    const uint32_t* midstate,     // 8 uint32s
    const uint8_t*  tail,         // 0-63 bytes
    int             tail_len,
    uint64_t        total_prefix_len,
    const uint8_t*  suffix,       // difficulty string
    int             suffix_len,
    int             diff_bits,
    uint64_t        start_nonce,
    uint64_t        batch_size,
    uint64_t*       result_nonce,
    uint8_t*        result_hash   // 32 bytes
) {
    // Validate inputs
    if (tail_len < 0 || tail_len > 64) {
        fprintf(stderr, "gpu_mine_batch: invalid tail_len=%d\n", tail_len);
        return -1;
    }
    if (suffix_len < 0 || suffix_len > 8) {
        fprintf(stderr, "gpu_mine_batch: invalid suffix_len=%d\n", suffix_len);
        return -1;
    }
    if (diff_bits <= 0 || diff_bits > 256) {
        fprintf(stderr, "gpu_mine_batch: invalid diff_bits=%d\n", diff_bits);
        return -1;
    }
    if (!d_result_nonce || !d_result_found) {
        fprintf(stderr, "gpu_mine_batch: device memory not initialized (call gpu_init first)\n");
        return -1;
    }

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

    cudaMemsetAsync(d_result_found, 0, sizeof(int));
    cudaMemsetAsync(d_result_nonce, 0, sizeof(uint64_t) * 10);

    // Check for any prior CUDA errors
    cudaError_t pre_err = cudaGetLastError();
    if (pre_err != cudaSuccess) {
        fprintf(stderr, "CUDA pre-launch error: %s\n", cudaGetErrorString(pre_err));
        return -1;
    }

    const int threads_per_block = 256;
    uint64_t grid_size = (batch_size + threads_per_block - 1) / threads_per_block;
    if (grid_size > 2147483647ULL) grid_size = 2147483647ULL;

    mine_kernel<<<(unsigned int)grid_size, threads_per_block>>>(
        params, batch_size, d_result_nonce, d_result_found);

    cudaError_t err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        fprintf(stderr, "CUDA kernel error: %s\n", cudaGetErrorString(err));
        return -1;
    }

    int found = 0;
    cudaMemcpy(&found, d_result_found, sizeof(int), cudaMemcpyDeviceToHost);

    if (found) {
        uint64_t host_result[10];
        cudaMemcpy(host_result, d_result_nonce, sizeof(uint64_t) * 10, cudaMemcpyDeviceToHost);
        *result_nonce = host_result[0];

        // Extract hash (stored at uint32 offset 2 from start of result buffer)
        uint32_t* hash_words = ((uint32_t*)host_result) + 2;
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
