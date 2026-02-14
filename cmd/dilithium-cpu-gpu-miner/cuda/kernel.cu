/*
 * Dilithium Coin CUDA SHA-256 Mining Kernel
 *
 * This kernel performs SHA-256 proof-of-work mining using the midstate
 * optimization. The fixed prefix of the block data is pre-hashed on
 * the CPU, and the GPU only processes the variable tail (nonce + suffix).
 *
 * Build:
 *   nvcc -O3 -arch=sm_75 -o dilithium-cuda-miner kernel.cu -lcurl
 *
 * Or use the Makefile:
 *   make gpu
 */

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cstdint>
#include <ctime>
#include <csignal>

// ============================================================================
// SHA-256 Constants
// ============================================================================

__device__ __constant__ uint32_t K[64] = {
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
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
};

// Midstate and work data in constant memory (broadcast to all threads)
__constant__ uint32_t d_midstate[8];
__constant__ uint8_t  d_prefix_tail[64];   // Remaining prefix after midstate
__constant__ int      d_prefix_tail_len;
__constant__ uint8_t  d_suffix[8];         // Difficulty string
__constant__ int      d_suffix_len;
__constant__ uint64_t d_midstate_bytes;    // Bytes processed into midstate
__constant__ int      d_difficulty_bits;

// ============================================================================
// SHA-256 Device Functions
// ============================================================================

__device__ __forceinline__ uint32_t rotr(uint32_t x, int n) {
    return (x >> n) | (x << (32 - n));
}

__device__ __forceinline__ uint32_t ch(uint32_t e, uint32_t f, uint32_t g) {
    return (e & f) ^ (~e & g);
}

__device__ __forceinline__ uint32_t maj(uint32_t a, uint32_t b, uint32_t c) {
    return (a & b) ^ (a & c) ^ (b & c);
}

__device__ __forceinline__ uint32_t sigma0(uint32_t a) {
    return rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
}

__device__ __forceinline__ uint32_t sigma1(uint32_t e) {
    return rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
}

__device__ __forceinline__ uint32_t gamma0(uint32_t x) {
    return rotr(x, 7) ^ rotr(x, 18) ^ (x >> 3);
}

__device__ __forceinline__ uint32_t gamma1(uint32_t x) {
    return rotr(x, 17) ^ rotr(x, 19) ^ (x >> 10);
}

// Load a big-endian uint32 from a byte array
__device__ __forceinline__ uint32_t load_be32(const uint8_t *p) {
    return ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) |
           ((uint32_t)p[2] << 8)  | ((uint32_t)p[3]);
}

// SHA-256 compression of a single 64-byte block
__device__ void sha256_compress(uint32_t state[8], const uint8_t block[64]) {
    uint32_t w[64];

    // Load message schedule
    #pragma unroll
    for (int i = 0; i < 16; i++) {
        w[i] = load_be32(block + i * 4);
    }

    // Extend message schedule
    #pragma unroll
    for (int i = 16; i < 64; i++) {
        w[i] = gamma1(w[i-2]) + w[i-7] + gamma0(w[i-15]) + w[i-16];
    }

    uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
    uint32_t e = state[4], f = state[5], g = state[6], h = state[7];

    // 64 rounds
    #pragma unroll
    for (int i = 0; i < 64; i++) {
        uint32_t t1 = h + sigma1(e) + ch(e, f, g) + K[i] + w[i];
        uint32_t t2 = sigma0(a) + maj(a, b, c);
        h = g; g = f; f = e; e = d + t1;
        d = c; c = b; b = a; a = t1 + t2;
    }

    state[0] += a; state[1] += b; state[2] += c; state[3] += d;
    state[4] += e; state[5] += f; state[6] += g; state[7] += h;
}

// Convert uint64 to decimal string, return length
__device__ int uint64_to_dec(uint64_t val, uint8_t *buf) {
    if (val == 0) { buf[0] = '0'; return 1; }

    uint8_t tmp[20];
    int len = 0;
    while (val > 0) {
        tmp[len++] = '0' + (val % 10);
        val /= 10;
    }
    for (int i = 0; i < len; i++) {
        buf[i] = tmp[len - 1 - i];
    }
    return len;
}

// Check if hash meets difficulty requirement (leading zero bits)
__device__ bool meets_difficulty(const uint32_t state[8], int bits) {
    // Convert state words to bytes and check leading zeros
    int full_bytes = bits / 8;
    int remaining = bits % 8;

    for (int i = 0; i < full_bytes; i++) {
        int word_idx = i / 4;
        int byte_idx = 3 - (i % 4);  // big-endian within word
        uint8_t byte_val = (state[word_idx] >> (byte_idx * 8)) & 0xFF;
        if (byte_val != 0) return false;
    }

    if (remaining > 0) {
        int word_idx = full_bytes / 4;
        int byte_idx = 3 - (full_bytes % 4);
        uint8_t byte_val = (state[word_idx] >> (byte_idx * 8)) & 0xFF;
        uint8_t mask = 0xFF << (8 - remaining);
        if (byte_val & mask) return false;
    }

    return true;
}

// ============================================================================
// Mining Kernel
// ============================================================================

__global__ void mine_kernel(uint64_t nonce_start, uint64_t *result_nonce, int *found) {
    // Early exit if another thread already found a solution
    if (*found) return;

    uint64_t nonce = nonce_start + (uint64_t)blockIdx.x * blockDim.x + threadIdx.x;

    // Build data to hash after midstate: prefix_tail + nonce_decimal + suffix
    uint8_t data[192];
    int pos = 0;

    // Copy prefix tail from constant memory
    for (int i = 0; i < d_prefix_tail_len; i++) {
        data[pos++] = d_prefix_tail[i];
    }

    // Write nonce as decimal string
    pos += uint64_to_dec(nonce, data + pos);

    // Write suffix (difficulty string)
    for (int i = 0; i < d_suffix_len; i++) {
        data[pos++] = d_suffix[i];
    }

    // Total message length
    uint64_t msg_len = d_midstate_bytes + (uint64_t)pos;

    // SHA-256 padding
    data[pos++] = 0x80;

    // Calculate padded length (round up to multiple of 64)
    int padded_len = ((pos + 8 + 63) / 64) * 64;

    // Zero fill
    while (pos < padded_len - 8) {
        data[pos++] = 0;
    }

    // Append bit length as big-endian uint64
    uint64_t bit_len = msg_len * 8;
    data[pos++] = (bit_len >> 56) & 0xFF;
    data[pos++] = (bit_len >> 48) & 0xFF;
    data[pos++] = (bit_len >> 40) & 0xFF;
    data[pos++] = (bit_len >> 32) & 0xFF;
    data[pos++] = (bit_len >> 24) & 0xFF;
    data[pos++] = (bit_len >> 16) & 0xFF;
    data[pos++] = (bit_len >> 8) & 0xFF;
    data[pos++] = bit_len & 0xFF;

    // Initialize state from midstate
    uint32_t state[8];
    #pragma unroll
    for (int i = 0; i < 8; i++) state[i] = d_midstate[i];

    // Process remaining blocks (1 or 2 blocks of 64 bytes)
    for (int i = 0; i < padded_len; i += 64) {
        sha256_compress(state, data + i);
    }

    // Check difficulty
    if (meets_difficulty(state, d_difficulty_bits)) {
        if (atomicCAS(found, 0, 1) == 0) {
            *result_nonce = nonce;
        }
    }
}

// ============================================================================
// Host Code
// ============================================================================

static volatile bool g_running = true;

void signal_handler(int sig) {
    g_running = false;
    printf("\nShutting down...\n");
}

// CPU SHA-256 for verification
static const uint32_t cpu_K[64] = {
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
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
};

void cpu_sha256(const uint8_t *data, size_t len, uint8_t hash[32]) {
    uint32_t h[8] = {
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
        0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19
    };

    // Pad message
    size_t padded_len = ((len + 9 + 63) / 64) * 64;
    uint8_t *padded = (uint8_t*)calloc(padded_len, 1);
    memcpy(padded, data, len);
    padded[len] = 0x80;
    uint64_t bit_len = (uint64_t)len * 8;
    for (int i = 7; i >= 0; i--) {
        padded[padded_len - 8 + (7 - i)] = (bit_len >> (i * 8)) & 0xFF;
    }

    // Process blocks
    for (size_t off = 0; off < padded_len; off += 64) {
        uint32_t w[64];
        for (int i = 0; i < 16; i++) {
            w[i] = ((uint32_t)padded[off+i*4] << 24) | ((uint32_t)padded[off+i*4+1] << 16) |
                    ((uint32_t)padded[off+i*4+2] << 8) | padded[off+i*4+3];
        }
        for (int i = 16; i < 64; i++) {
            uint32_t s0 = ((w[i-15]>>7)|(w[i-15]<<25)) ^ ((w[i-15]>>18)|(w[i-15]<<14)) ^ (w[i-15]>>3);
            uint32_t s1 = ((w[i-2]>>17)|(w[i-2]<<15)) ^ ((w[i-2]>>19)|(w[i-2]<<13)) ^ (w[i-2]>>10);
            w[i] = w[i-16] + s0 + w[i-7] + s1;
        }

        uint32_t a=h[0],b=h[1],c=h[2],d=h[3],e=h[4],f=h[5],g=h[6],hh=h[7];
        for (int i = 0; i < 64; i++) {
            uint32_t S1 = ((e>>6)|(e<<26)) ^ ((e>>11)|(e<<21)) ^ ((e>>25)|(e<<7));
            uint32_t t1 = hh + S1 + ((e&f)^(~e&g)) + cpu_K[i] + w[i];
            uint32_t S0 = ((a>>2)|(a<<30)) ^ ((a>>13)|(a<<19)) ^ ((a>>22)|(a<<10));
            uint32_t t2 = S0 + ((a&b)^(a&c)^(b&c));
            hh=g; g=f; f=e; e=d+t1; d=c; c=b; b=a; a=t1+t2;
        }
        h[0]+=a; h[1]+=b; h[2]+=c; h[3]+=d; h[4]+=e; h[5]+=f; h[6]+=g; h[7]+=hh;
    }
    free(padded);

    for (int i = 0; i < 8; i++) {
        hash[i*4]   = (h[i] >> 24) & 0xFF;
        hash[i*4+1] = (h[i] >> 16) & 0xFF;
        hash[i*4+2] = (h[i] >> 8) & 0xFF;
        hash[i*4+3] = h[i] & 0xFF;
    }
}

void compute_midstate(const uint8_t *prefix, int prefix_len, uint32_t midstate[8], int *processed) {
    uint32_t h[8] = {
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
        0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19
    };

    int full_blocks = prefix_len / 64;
    for (int b = 0; b < full_blocks; b++) {
        uint32_t w[64];
        const uint8_t *block = prefix + b * 64;
        for (int i = 0; i < 16; i++) {
            w[i] = ((uint32_t)block[i*4] << 24) | ((uint32_t)block[i*4+1] << 16) |
                    ((uint32_t)block[i*4+2] << 8) | block[i*4+3];
        }
        for (int i = 16; i < 64; i++) {
            uint32_t s0 = ((w[i-15]>>7)|(w[i-15]<<25)) ^ ((w[i-15]>>18)|(w[i-15]<<14)) ^ (w[i-15]>>3);
            uint32_t s1 = ((w[i-2]>>17)|(w[i-2]<<15)) ^ ((w[i-2]>>19)|(w[i-2]<<13)) ^ (w[i-2]>>10);
            w[i] = w[i-16] + s0 + w[i-7] + s1;
        }
        uint32_t a=h[0],b_=h[1],c=h[2],d=h[3],e=h[4],f=h[5],g=h[6],hh=h[7];
        for (int i = 0; i < 64; i++) {
            uint32_t S1 = ((e>>6)|(e<<26)) ^ ((e>>11)|(e<<21)) ^ ((e>>25)|(e<<7));
            uint32_t t1 = hh + S1 + ((e&f)^(~e&g)) + cpu_K[i] + w[i];
            uint32_t S0 = ((a>>2)|(a<<30)) ^ ((a>>13)|(a<<19)) ^ ((a>>22)|(a<<10));
            uint32_t t2 = S0 + ((a&b_)^(a&c)^(b_&c));
            hh=g; g=f; f=e; e=d+t1; d=c; c=b_; b_=a; a=t1+t2;
        }
        h[0]+=a; h[1]+=b_; h[2]+=c; h[3]+=d; h[4]+=e; h[5]+=f; h[6]+=g; h[7]+=hh;
    }

    memcpy(midstate, h, 32);
    *processed = full_blocks * 64;
}

void hash_to_hex(const uint8_t hash[32], char hex[65]) {
    const char *chars = "0123456789abcdef";
    for (int i = 0; i < 32; i++) {
        hex[i*2]   = chars[hash[i] >> 4];
        hex[i*2+1] = chars[hash[i] & 0x0f];
    }
    hex[64] = '\0';
}

int main(int argc, char **argv) {
    signal(SIGINT, signal_handler);

    // Check for CUDA device
    int device_count = 0;
    cudaGetDeviceCount(&device_count);
    if (device_count == 0) {
        fprintf(stderr, "Error: No CUDA-capable GPU found\n");
        return 1;
    }

    cudaDeviceProp prop;
    cudaGetDeviceProperties(&prop, 0);
    printf("=== Dilithium CUDA Miner ===\n");
    printf("GPU: %s (%d SMs, %d threads/block max)\n",
           prop.name, prop.multiProcessorCount, prop.maxThreadsPerBlock);
    printf("\n");

    // --- Configuration ---
    // In production, these would come from command-line args and the node API.
    // This standalone kernel can be integrated with the Go miner via CGo or
    // used independently with libcurl for HTTP communication.

    if (argc < 4) {
        printf("Usage: %s <prefix_hex> <suffix> <difficulty_bits>\n", argv[0]);
        printf("\n");
        printf("  prefix_hex:      Hex-encoded block data prefix (Index+Timestamp+txJSON+PrevHash)\n");
        printf("  suffix:          Difficulty string (e.g., \"6\")\n");
        printf("  difficulty_bits: Required leading zero bits (e.g., 24)\n");
        printf("\n");
        printf("Example:\n");
        printf("  %s $(echo -n 'prefix_data_here' | xxd -p) 6 24\n", argv[0]);
        printf("\n");
        printf("The kernel will output the winning nonce and hash when found.\n");
        printf("Integrate with the Go miner for full node communication.\n");
        return 0;
    }

    // Parse prefix from hex
    const char *prefix_hex = argv[1];
    int prefix_hex_len = strlen(prefix_hex);
    int prefix_len = prefix_hex_len / 2;
    uint8_t *prefix = (uint8_t*)malloc(prefix_len);
    for (int i = 0; i < prefix_len; i++) {
        sscanf(prefix_hex + i*2, "%2hhx", &prefix[i]);
    }

    const char *suffix_str = argv[2];
    int suffix_len = strlen(suffix_str);
    int diff_bits = atoi(argv[3]);

    printf("Prefix:     %d bytes\n", prefix_len);
    printf("Suffix:     \"%s\"\n", suffix_str);
    printf("Difficulty: %d bits\n", diff_bits);

    // Compute midstate
    uint32_t midstate[8];
    int processed;
    compute_midstate(prefix, prefix_len, midstate, &processed);
    int tail_len = prefix_len - processed;
    printf("Midstate:   %d blocks processed (%d bytes), %d bytes tail\n",
           processed/64, processed, tail_len);

    // Copy to constant memory
    cudaMemcpyToSymbol(d_midstate, midstate, 32);
    cudaMemcpyToSymbol(d_prefix_tail, prefix + processed, tail_len);
    cudaMemcpyToSymbol(d_prefix_tail_len, &tail_len, sizeof(int));
    cudaMemcpyToSymbol(d_suffix, suffix_str, suffix_len);
    cudaMemcpyToSymbol(d_suffix_len, &suffix_len, sizeof(int));
    uint64_t midstate_bytes = (uint64_t)processed;
    cudaMemcpyToSymbol(d_midstate_bytes, &midstate_bytes, sizeof(uint64_t));
    cudaMemcpyToSymbol(d_difficulty_bits, &diff_bits, sizeof(int));

    // Allocate device memory for result
    uint64_t *d_result_nonce;
    int *d_found;
    cudaMalloc(&d_result_nonce, sizeof(uint64_t));
    cudaMalloc(&d_found, sizeof(int));

    // Kernel launch configuration
    // Tune these for your GPU:
    //   - blocks * threads = total nonces per kernel launch
    //   - More blocks = better occupancy on large GPUs
    int threads_per_block = 256;
    int blocks_per_grid = prop.multiProcessorCount * 32; // ~32 blocks per SM
    uint64_t nonces_per_launch = (uint64_t)blocks_per_grid * threads_per_block;

    printf("Config:     %d blocks x %d threads = %lu nonces/launch\n",
           blocks_per_grid, threads_per_block, (unsigned long)nonces_per_launch);
    printf("\nMining...\n\n");

    uint64_t nonce_offset = 0;
    uint64_t total_hashes = 0;
    time_t start_time = time(NULL);
    time_t last_report = start_time;

    while (g_running) {
        // Reset found flag
        int zero = 0;
        cudaMemcpy(d_found, &zero, sizeof(int), cudaMemcpyHostToDevice);

        // Launch kernel
        mine_kernel<<<blocks_per_grid, threads_per_block>>>(
            nonce_offset, d_result_nonce, d_found);
        cudaDeviceSynchronize();

        // Check for errors
        cudaError_t err = cudaGetLastError();
        if (err != cudaSuccess) {
            fprintf(stderr, "CUDA error: %s\n", cudaGetErrorString(err));
            break;
        }

        // Check if found
        int found = 0;
        cudaMemcpy(&found, d_found, sizeof(int), cudaMemcpyDeviceToHost);

        if (found) {
            uint64_t winning_nonce;
            cudaMemcpy(&winning_nonce, d_result_nonce, sizeof(uint64_t), cudaMemcpyDeviceToHost);

            // Verify on CPU
            char nonce_str[21];
            snprintf(nonce_str, sizeof(nonce_str), "%lu", (unsigned long)winning_nonce);

            int data_len = prefix_len + strlen(nonce_str) + suffix_len;
            uint8_t *full_data = (uint8_t*)malloc(data_len);
            memcpy(full_data, prefix, prefix_len);
            memcpy(full_data + prefix_len, nonce_str, strlen(nonce_str));
            memcpy(full_data + prefix_len + strlen(nonce_str), suffix_str, suffix_len);

            uint8_t hash[32];
            cpu_sha256(full_data, data_len, hash);
            free(full_data);

            char hex[65];
            hash_to_hex(hash, hex);

            printf("FOUND! Nonce: %lu\n", (unsigned long)winning_nonce);
            printf("Hash:  %s\n", hex);
            printf("Total hashes: %lu\n", (unsigned long)(total_hashes + nonces_per_launch));

            // Output in machine-readable format for Go miner integration
            printf("\n---RESULT---\n");
            printf("NONCE=%lu\n", (unsigned long)winning_nonce);
            printf("HASH=%s\n", hex);
            printf("---END---\n");

            break;
        }

        nonce_offset += nonces_per_launch;
        total_hashes += nonces_per_launch;

        // Periodic hashrate report
        time_t now = time(NULL);
        if (now - last_report >= 3) {
            double elapsed = difftime(now, start_time);
            double rate = (double)total_hashes / elapsed;
            if (rate > 1e9) {
                printf("Hashrate: %.2f GH/s | Hashes: %lu | Elapsed: %.0fs\n",
                       rate / 1e9, (unsigned long)total_hashes, elapsed);
            } else {
                printf("Hashrate: %.2f MH/s | Hashes: %lu | Elapsed: %.0fs\n",
                       rate / 1e6, (unsigned long)total_hashes, elapsed);
            }
            last_report = now;
        }
    }

    // Cleanup
    cudaFree(d_result_nonce);
    cudaFree(d_found);
    free(prefix);

    double elapsed = difftime(time(NULL), start_time);
    printf("\nSession: %.0fs | Total hashes: %lu | Avg: %.2f MH/s\n",
           elapsed, (unsigned long)total_hashes,
           elapsed > 0 ? (double)total_hashes / elapsed / 1e6 : 0);

    return 0;
}
