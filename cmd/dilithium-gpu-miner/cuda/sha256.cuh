#ifndef SHA256_CUH
#define SHA256_CUH

#include <cuda_runtime.h>
#include <stdint.h>

// SHA-256 constants
__constant__ uint32_t K[64] = {
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

// Rotate right - uses fast funnel shift intrinsic
__device__ __forceinline__ uint32_t rotr(uint32_t x, int n) {
    return __funnelshift_r(x, x, n);
}

// SHA-256 functions
__device__ __forceinline__ uint32_t Ch(uint32_t x, uint32_t y, uint32_t z) {
    return (x & y) ^ (~x & z);
}

__device__ __forceinline__ uint32_t Maj(uint32_t x, uint32_t y, uint32_t z) {
    return (x & y) ^ (x & z) ^ (y & z);
}

__device__ __forceinline__ uint32_t Sigma0(uint32_t x) {
    return rotr(x, 2) ^ rotr(x, 13) ^ rotr(x, 22);
}

__device__ __forceinline__ uint32_t Sigma1(uint32_t x) {
    return rotr(x, 6) ^ rotr(x, 11) ^ rotr(x, 25);
}

__device__ __forceinline__ uint32_t sigma0(uint32_t x) {
    return rotr(x, 7) ^ rotr(x, 18) ^ (x >> 3);
}

__device__ __forceinline__ uint32_t sigma1(uint32_t x) {
    return rotr(x, 17) ^ rotr(x, 19) ^ (x >> 10);
}

// Pack big-endian bytes into uint32
__device__ __forceinline__ uint32_t pack_be32(const uint8_t* p) {
    return (uint32_t(p[0]) << 24) | (uint32_t(p[1]) << 16) |
           (uint32_t(p[2]) << 8)  |  uint32_t(p[3]);
}

// SHA-256 compression function
// Processes one 512-bit block
__device__ void sha256_compress(uint32_t state[8], const uint32_t block[16]) {
    uint32_t W[16];

    // Initialize first 16 words
    #pragma unroll
    for (int i = 0; i < 16; i++) {
        W[i] = block[i];
    }

    // Initialize working variables
    uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
    uint32_t e = state[4], f = state[5], g = state[6], h = state[7];

    // First 16 rounds
    #pragma unroll
    for (int i = 0; i < 16; i++) {
        uint32_t T1 = h + Sigma1(e) + Ch(e, f, g) + K[i] + W[i];
        uint32_t T2 = Sigma0(a) + Maj(a, b, c);
        h = g;
        g = f;
        f = e;
        e = d + T1;
        d = c;
        c = b;
        b = a;
        a = T1 + T2;
    }

    // Remaining 48 rounds with message schedule
    #pragma unroll
    for (int i = 16; i < 64; i++) {
        W[i & 15] += sigma1(W[(i - 2) & 15]) + W[(i - 7) & 15] + sigma0(W[(i - 15) & 15]);
        uint32_t w = W[i & 15];

        uint32_t T1 = h + Sigma1(e) + Ch(e, f, g) + K[i] + w;
        uint32_t T2 = Sigma0(a) + Maj(a, b, c);
        h = g;
        g = f;
        f = e;
        e = d + T1;
        d = c;
        c = b;
        b = a;
        a = T1 + T2;
    }

    // Add compressed chunk to state
    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

// Convert uint64 to decimal string
// Returns length of string
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

    // Reverse
    for (int i = 0; i < len; i++) {
        buf[i] = tmp[len - 1 - i];
    }

    return len;
}

// Check if hash meets difficulty requirement
// difficulty_bits = number of leading zero bits required
__device__ bool meets_difficulty(const uint32_t hash[8], int difficulty_bits) {
    int full_words = difficulty_bits / 32;

    // Check full 32-bit words
    for (int i = 0; i < full_words; i++) {
        if (hash[i] != 0) return false;
    }

    // Check remaining bits in partial word
    int remaining_bits = difficulty_bits % 32;
    if (remaining_bits > 0) {
        uint32_t mask = 0xFFFFFFFFu << (32 - remaining_bits);
        if ((hash[full_words] & mask) != 0) return false;
    }

    return true;
}

#endif // SHA256_CUH
