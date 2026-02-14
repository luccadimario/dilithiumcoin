#include <metal_stdlib>
using namespace metal;

// ============================================================================
// SHA-256 Constants
// ============================================================================

constexpr constant uint32_t K[64] = {
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

// ============================================================================
// SHA-256 Helper Functions
// ============================================================================

inline uint32_t rotr(uint32_t x, uint n) {
    return (x >> n) | (x << (32 - n));
}

inline uint32_t Ch(uint32_t x, uint32_t y, uint32_t z) {
    return (x & y) ^ (~x & z);
}

inline uint32_t Maj(uint32_t x, uint32_t y, uint32_t z) {
    return (x & y) ^ (x & z) ^ (y & z);
}

inline uint32_t Sigma0(uint32_t x) {
    return rotr(x, 2) ^ rotr(x, 13) ^ rotr(x, 22);
}

inline uint32_t Sigma1(uint32_t x) {
    return rotr(x, 6) ^ rotr(x, 11) ^ rotr(x, 25);
}

inline uint32_t sigma0(uint32_t x) {
    return rotr(x, 7) ^ rotr(x, 18) ^ (x >> 3);
}

inline uint32_t sigma1(uint32_t x) {
    return rotr(x, 17) ^ rotr(x, 19) ^ (x >> 10);
}

// ============================================================================
// SHA-256 Compression — ALL 64 rounds fully unrolled
// ============================================================================

#define RND(a,b,c,d,e,f,g,h,k,w) { \
    uint32_t T1 = h + Sigma1(e) + Ch(e,f,g) + k + w; \
    uint32_t T2 = Sigma0(a) + Maj(a,b,c); \
    h = g; g = f; f = e; e = d + T1; d = c; c = b; b = a; a = T1 + T2; \
}

#define SCHED(W, i) (W[i&15] += sigma1(W[(i-2)&15]) + W[(i-7)&15] + sigma0(W[(i-15)&15]))

void sha256_compress(thread uint32_t* state, thread uint32_t* W) {
    uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
    uint32_t e = state[4], f = state[5], g = state[6], h = state[7];

    RND(a,b,c,d,e,f,g,h, K[ 0], W[ 0]);
    RND(a,b,c,d,e,f,g,h, K[ 1], W[ 1]);
    RND(a,b,c,d,e,f,g,h, K[ 2], W[ 2]);
    RND(a,b,c,d,e,f,g,h, K[ 3], W[ 3]);
    RND(a,b,c,d,e,f,g,h, K[ 4], W[ 4]);
    RND(a,b,c,d,e,f,g,h, K[ 5], W[ 5]);
    RND(a,b,c,d,e,f,g,h, K[ 6], W[ 6]);
    RND(a,b,c,d,e,f,g,h, K[ 7], W[ 7]);
    RND(a,b,c,d,e,f,g,h, K[ 8], W[ 8]);
    RND(a,b,c,d,e,f,g,h, K[ 9], W[ 9]);
    RND(a,b,c,d,e,f,g,h, K[10], W[10]);
    RND(a,b,c,d,e,f,g,h, K[11], W[11]);
    RND(a,b,c,d,e,f,g,h, K[12], W[12]);
    RND(a,b,c,d,e,f,g,h, K[13], W[13]);
    RND(a,b,c,d,e,f,g,h, K[14], W[14]);
    RND(a,b,c,d,e,f,g,h, K[15], W[15]);

    RND(a,b,c,d,e,f,g,h, K[16], SCHED(W,16));
    RND(a,b,c,d,e,f,g,h, K[17], SCHED(W,17));
    RND(a,b,c,d,e,f,g,h, K[18], SCHED(W,18));
    RND(a,b,c,d,e,f,g,h, K[19], SCHED(W,19));
    RND(a,b,c,d,e,f,g,h, K[20], SCHED(W,20));
    RND(a,b,c,d,e,f,g,h, K[21], SCHED(W,21));
    RND(a,b,c,d,e,f,g,h, K[22], SCHED(W,22));
    RND(a,b,c,d,e,f,g,h, K[23], SCHED(W,23));
    RND(a,b,c,d,e,f,g,h, K[24], SCHED(W,24));
    RND(a,b,c,d,e,f,g,h, K[25], SCHED(W,25));
    RND(a,b,c,d,e,f,g,h, K[26], SCHED(W,26));
    RND(a,b,c,d,e,f,g,h, K[27], SCHED(W,27));
    RND(a,b,c,d,e,f,g,h, K[28], SCHED(W,28));
    RND(a,b,c,d,e,f,g,h, K[29], SCHED(W,29));
    RND(a,b,c,d,e,f,g,h, K[30], SCHED(W,30));
    RND(a,b,c,d,e,f,g,h, K[31], SCHED(W,31));
    RND(a,b,c,d,e,f,g,h, K[32], SCHED(W,32));
    RND(a,b,c,d,e,f,g,h, K[33], SCHED(W,33));
    RND(a,b,c,d,e,f,g,h, K[34], SCHED(W,34));
    RND(a,b,c,d,e,f,g,h, K[35], SCHED(W,35));
    RND(a,b,c,d,e,f,g,h, K[36], SCHED(W,36));
    RND(a,b,c,d,e,f,g,h, K[37], SCHED(W,37));
    RND(a,b,c,d,e,f,g,h, K[38], SCHED(W,38));
    RND(a,b,c,d,e,f,g,h, K[39], SCHED(W,39));
    RND(a,b,c,d,e,f,g,h, K[40], SCHED(W,40));
    RND(a,b,c,d,e,f,g,h, K[41], SCHED(W,41));
    RND(a,b,c,d,e,f,g,h, K[42], SCHED(W,42));
    RND(a,b,c,d,e,f,g,h, K[43], SCHED(W,43));
    RND(a,b,c,d,e,f,g,h, K[44], SCHED(W,44));
    RND(a,b,c,d,e,f,g,h, K[45], SCHED(W,45));
    RND(a,b,c,d,e,f,g,h, K[46], SCHED(W,46));
    RND(a,b,c,d,e,f,g,h, K[47], SCHED(W,47));
    RND(a,b,c,d,e,f,g,h, K[48], SCHED(W,48));
    RND(a,b,c,d,e,f,g,h, K[49], SCHED(W,49));
    RND(a,b,c,d,e,f,g,h, K[50], SCHED(W,50));
    RND(a,b,c,d,e,f,g,h, K[51], SCHED(W,51));
    RND(a,b,c,d,e,f,g,h, K[52], SCHED(W,52));
    RND(a,b,c,d,e,f,g,h, K[53], SCHED(W,53));
    RND(a,b,c,d,e,f,g,h, K[54], SCHED(W,54));
    RND(a,b,c,d,e,f,g,h, K[55], SCHED(W,55));
    RND(a,b,c,d,e,f,g,h, K[56], SCHED(W,56));
    RND(a,b,c,d,e,f,g,h, K[57], SCHED(W,57));
    RND(a,b,c,d,e,f,g,h, K[58], SCHED(W,58));
    RND(a,b,c,d,e,f,g,h, K[59], SCHED(W,59));
    RND(a,b,c,d,e,f,g,h, K[60], SCHED(W,60));
    RND(a,b,c,d,e,f,g,h, K[61], SCHED(W,61));
    RND(a,b,c,d,e,f,g,h, K[62], SCHED(W,62));
    RND(a,b,c,d,e,f,g,h, K[63], SCHED(W,63));

    state[0] += a; state[1] += b; state[2] += c; state[3] += d;
    state[4] += e; state[5] += f; state[6] += g; state[7] += h;
}

// ============================================================================
// SHA-256 Compression — Rounds 11-63 only (0-10 precomputed on CPU)
// final_state[i] = midstate[i] + round_result[i]
// ============================================================================

void sha256_compress_from11(thread uint32_t* state, threadgroup const uint32_t* midstate,
                            thread uint32_t* W, threadgroup const uint32_t* precomp_w) {
    uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
    uint32_t e = state[4], f = state[5], g = state[6], h = state[7];

    // Rounds 11-15: use W[] directly
    RND(a,b,c,d,e,f,g,h, K[11], W[11]);
    RND(a,b,c,d,e,f,g,h, K[12], W[12]);
    RND(a,b,c,d,e,f,g,h, K[13], W[13]);
    RND(a,b,c,d,e,f,g,h, K[14], W[14]);
    RND(a,b,c,d,e,f,g,h, K[15], W[15]);

    // Rounds 16-17: fully precomputed on CPU (no SCHED needed)
    W[0] = precomp_w[0];
    RND(a,b,c,d,e,f,g,h, K[16], W[0]);
    W[1] = precomp_w[1];
    RND(a,b,c,d,e,f,g,h, K[17], W[1]);
    // Rounds 18-19: partially precomputed (base + nonce word)
    W[2] = precomp_w[2] + W[11];
    RND(a,b,c,d,e,f,g,h, K[18], W[2]);
    W[3] = precomp_w[3] + W[12];
    RND(a,b,c,d,e,f,g,h, K[19], W[3]);
    // Rounds 20-24: partially precomputed (base + sigma1 of nonce-dependent W)
    W[4] = precomp_w[4] + sigma1(W[2]) + W[13];
    RND(a,b,c,d,e,f,g,h, K[20], W[4]);
    W[5] = precomp_w[5] + sigma1(W[3]);
    RND(a,b,c,d,e,f,g,h, K[21], W[5]);
    W[6] = precomp_w[6] + sigma1(W[4]);
    RND(a,b,c,d,e,f,g,h, K[22], W[6]);
    W[7] = precomp_w[7] + sigma1(W[5]);
    RND(a,b,c,d,e,f,g,h, K[23], W[7]);
    W[8] = precomp_w[8] + sigma1(W[6]);
    RND(a,b,c,d,e,f,g,h, K[24], W[8]);
    // Rounds 25-63: standard SCHED
    RND(a,b,c,d,e,f,g,h, K[25], SCHED(W,25));
    RND(a,b,c,d,e,f,g,h, K[26], SCHED(W,26));
    RND(a,b,c,d,e,f,g,h, K[27], SCHED(W,27));
    RND(a,b,c,d,e,f,g,h, K[28], SCHED(W,28));
    RND(a,b,c,d,e,f,g,h, K[29], SCHED(W,29));
    RND(a,b,c,d,e,f,g,h, K[30], SCHED(W,30));
    RND(a,b,c,d,e,f,g,h, K[31], SCHED(W,31));
    RND(a,b,c,d,e,f,g,h, K[32], SCHED(W,32));
    RND(a,b,c,d,e,f,g,h, K[33], SCHED(W,33));
    RND(a,b,c,d,e,f,g,h, K[34], SCHED(W,34));
    RND(a,b,c,d,e,f,g,h, K[35], SCHED(W,35));
    RND(a,b,c,d,e,f,g,h, K[36], SCHED(W,36));
    RND(a,b,c,d,e,f,g,h, K[37], SCHED(W,37));
    RND(a,b,c,d,e,f,g,h, K[38], SCHED(W,38));
    RND(a,b,c,d,e,f,g,h, K[39], SCHED(W,39));
    RND(a,b,c,d,e,f,g,h, K[40], SCHED(W,40));
    RND(a,b,c,d,e,f,g,h, K[41], SCHED(W,41));
    RND(a,b,c,d,e,f,g,h, K[42], SCHED(W,42));
    RND(a,b,c,d,e,f,g,h, K[43], SCHED(W,43));
    RND(a,b,c,d,e,f,g,h, K[44], SCHED(W,44));
    RND(a,b,c,d,e,f,g,h, K[45], SCHED(W,45));
    RND(a,b,c,d,e,f,g,h, K[46], SCHED(W,46));
    RND(a,b,c,d,e,f,g,h, K[47], SCHED(W,47));
    RND(a,b,c,d,e,f,g,h, K[48], SCHED(W,48));
    RND(a,b,c,d,e,f,g,h, K[49], SCHED(W,49));
    RND(a,b,c,d,e,f,g,h, K[50], SCHED(W,50));
    RND(a,b,c,d,e,f,g,h, K[51], SCHED(W,51));
    RND(a,b,c,d,e,f,g,h, K[52], SCHED(W,52));
    RND(a,b,c,d,e,f,g,h, K[53], SCHED(W,53));
    RND(a,b,c,d,e,f,g,h, K[54], SCHED(W,54));
    RND(a,b,c,d,e,f,g,h, K[55], SCHED(W,55));
    RND(a,b,c,d,e,f,g,h, K[56], SCHED(W,56));
    RND(a,b,c,d,e,f,g,h, K[57], SCHED(W,57));
    RND(a,b,c,d,e,f,g,h, K[58], SCHED(W,58));
    RND(a,b,c,d,e,f,g,h, K[59], SCHED(W,59));
    RND(a,b,c,d,e,f,g,h, K[60], SCHED(W,60));
    RND(a,b,c,d,e,f,g,h, K[61], SCHED(W,61));
    RND(a,b,c,d,e,f,g,h, K[62], SCHED(W,62));
    RND(a,b,c,d,e,f,g,h, K[63], SCHED(W,63));

    // Add back to ORIGINAL midstate (not partial_state)
    state[0] = midstate[0] + a; state[1] = midstate[1] + b;
    state[2] = midstate[2] + c; state[3] = midstate[3] + d;
    state[4] = midstate[4] + e; state[5] = midstate[5] + f;
    state[6] = midstate[6] + g; state[7] = midstate[7] + h;
}

// ============================================================================
// Fast divide-by-10: multiply-shift trick (no hardware division)
// ============================================================================

inline uint64_t fast_div10(uint64_t x) {
    uint32_t x_lo = uint32_t(x);
    uint32_t x_hi = uint32_t(x >> 32);
    uint32_t m_lo = 0xCCCCCCCDu;
    uint32_t m_hi = 0xCCCCCCCCu;

    uint64_t t0 = uint64_t(x_lo) * uint64_t(m_lo);
    uint64_t t1 = uint64_t(x_lo) * uint64_t(m_hi);
    uint64_t t2 = uint64_t(x_hi) * uint64_t(m_lo);
    uint64_t t3 = uint64_t(x_hi) * uint64_t(m_hi);

    uint64_t mid = (t0 >> 32) + (t1 & 0xFFFFFFFF) + (t2 & 0xFFFFFFFF);
    uint64_t hi = t3 + (t1 >> 32) + (t2 >> 32) + (mid >> 32);

    return hi >> 3;
}

int uint64_to_str(uint64_t val, thread uint8_t* buf) {
    if (val == 0) { buf[0] = '0'; return 1; }

    uint8_t tmp[20];
    int len = 0;
    while (val > 0) {
        uint64_t q = fast_div10(val);
        tmp[len++] = '0' + uint8_t(val - q * 10);
        val = q;
    }
    for (int i = 0; i < len; i++)
        buf[i] = tmp[len - 1 - i];
    return len;
}

// ============================================================================
// Difficulty check
// ============================================================================

bool meets_difficulty(thread const uint32_t* hash, int difficulty_bits) {
    int full_words = difficulty_bits >> 5;
    for (int i = 0; i < full_words; i++)
        if (hash[i] != 0) return false;
    int remaining_bits = difficulty_bits & 31;
    if (remaining_bits > 0) {
        uint32_t mask = 0xFFFFFFFFu << (32 - remaining_bits);
        if ((hash[full_words] & mask) != 0) return false;
    }
    return true;
}

// ============================================================================
// Place a byte into a big-endian-packed W[] word array
// ============================================================================

inline void place_byte(thread uint32_t* W, int pos, uint8_t b) {
    W[pos >> 2] |= uint32_t(b) << (24 - 8 * (pos & 3));
}

// ============================================================================
// Mining Parameters (must match Rust #[repr(C)] struct — 256 bytes)
// ============================================================================

struct MiningParams {
    uint32_t midstate[8];            // offset 0   (32 bytes)
    uint32_t tail_w[16];             // offset 32  (64 bytes) — precomputed BE words from tail
    uint32_t tail_len;               // offset 96  (4 bytes)  — byte count for nonce positioning
    uint32_t base_nonce_len;         // offset 100 (4 bytes)  — length of base_nonce_str
    uint64_t total_prefix_len;       // offset 104 (8 bytes)
    uint8_t  suffix[8];              // offset 112 (8 bytes)
    uint32_t suffix_len;             // offset 120 (4 bytes)
    uint32_t _pad1;                  // offset 124 (4 bytes)
    uint64_t nonce_start;            // offset 128 (8 bytes)
    uint32_t difficulty_bits;        // offset 136 (4 bytes)
    uint32_t num_precomputed_rounds; // offset 140 (4 bytes) — rounds done on CPU
    uint64_t batch_size;             // offset 144 (8 bytes)
    uint8_t  base_nonce_str[20];     // offset 152 (20 bytes) — nonce_start as decimal string
    uint32_t _pad3;                  // offset 172 (4 bytes)
    uint32_t partial_state[8];       // offset 176 (32 bytes) — state after precomputed rounds
    uint32_t precomputed_w[12];      // offset 208 (48 bytes) — SCHED bases for rounds 16-24, round 11 precomp
};                                   // Total: 256 bytes

// ============================================================================
// Mining Kernel — NO data[] buffer, W[] built directly from precomputed tail_w
// ============================================================================

// Fast 32-bit div by 10 (single multiply + shift, no hardware division)
inline uint32_t fast_div10_32(uint32_t x) {
    return uint32_t((uint64_t(x) * 0xCCCCCCCDUL) >> 35);
}

// Cached params in threadgroup memory — loaded once by leader, shared by 1024 threads
struct CachedParams {
    uint32_t midstate[8];
    uint32_t tail_w[16];
    uint32_t tail_len;
    uint64_t total_prefix_len;
    uint8_t  suffix[8];
    uint32_t suffix_len;
    uint64_t nonce_start;
    uint32_t difficulty_bits;
    uint32_t num_precomputed_rounds;
    uint64_t batch_size;
    uint8_t  base_nonce_str[20];
    uint32_t base_nonce_len;
    uint32_t partial_state[8];
    uint32_t precomputed_w[12];
};

kernel void mine_kernel(
    const device MiningParams& params [[buffer(0)]],
    device atomic_uint&        result_found [[buffer(1)]],
    device uint64_t*           result_nonce [[buffer(2)]],
    device uint32_t*           result_hash  [[buffer(3)]],
    uint                       tid [[thread_position_in_grid]],
    uint                       lid [[thread_position_in_threadgroup]]
) {
    // Cache params in threadgroup shared memory (1 load per 1024 threads)
    threadgroup CachedParams cp;
    if (lid == 0) {
        for (int i = 0; i < 8; i++) cp.midstate[i] = params.midstate[i];
        for (int i = 0; i < 16; i++) cp.tail_w[i] = params.tail_w[i];
        cp.tail_len = params.tail_len;
        cp.total_prefix_len = params.total_prefix_len;
        for (int i = 0; i < 8; i++) cp.suffix[i] = params.suffix[i];
        cp.suffix_len = params.suffix_len;
        cp.nonce_start = params.nonce_start;
        cp.difficulty_bits = params.difficulty_bits;
        cp.num_precomputed_rounds = params.num_precomputed_rounds;
        cp.batch_size = params.batch_size;
        for (int i = 0; i < 20; i++) cp.base_nonce_str[i] = params.base_nonce_str[i];
        cp.base_nonce_len = params.base_nonce_len;
        for (int i = 0; i < 8; i++) cp.partial_state[i] = params.partial_state[i];
        for (int i = 0; i < 12; i++) cp.precomputed_w[i] = params.precomputed_w[i];
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    if (uint64_t(tid) >= cp.batch_size) return;

    uint64_t nonce = cp.nonce_start + uint64_t(tid);

    // Build nonce string by adding tid to precomputed base string.
    // start_offset guarantees all nonces have the same digit count.
    uint8_t nonce_str[10];
    int nonce_len = int(cp.base_nonce_len);
    for (int i = 0; i < nonce_len; i++)
        nonce_str[i] = cp.base_nonce_str[i];

    // Add tid with carry propagation (no overflow possible with start_offset)
    uint32_t carry = uint32_t(tid);
    for (int i = nonce_len - 1; i >= 0 && carry > 0; i--) {
        uint32_t d = uint32_t(nonce_str[i] - '0') + carry;
        uint32_t q = fast_div10_32(d);
        nonce_str[i] = '0' + uint8_t(d - q * 10);
        carry = q;
    }

    // Total payload after midstate
    int pos = int(cp.tail_len) + nonce_len + int(cp.suffix_len);

    uint32_t state[8];

    if (pos <= 55) {
        // ==== FAST PATH: single SHA-256 block, rounds 0-10 precomputed on CPU ====
        uint32_t W[16];
        W[ 0] = cp.tail_w[ 0]; W[ 1] = cp.tail_w[ 1];
        W[ 2] = cp.tail_w[ 2]; W[ 3] = cp.tail_w[ 3];
        W[ 4] = cp.tail_w[ 4]; W[ 5] = cp.tail_w[ 5];
        W[ 6] = cp.tail_w[ 6]; W[ 7] = cp.tail_w[ 7];
        W[ 8] = cp.tail_w[ 8]; W[ 9] = cp.tail_w[ 9];
        W[10] = cp.tail_w[10];
        // W[14] and W[15] are precomputed on CPU with bit-length
        W[14] = cp.tail_w[14]; W[15] = cp.tail_w[15];

        // Direct word construction: pack nonce digits + suffix + padding
        // into W words without per-byte place_byte calls.
        // Requires tail_len % 4 == 0 and nonce_len == 10 and suffix_len == 1.
        if (nonce_len == 10 && (cp.tail_len & 3) == 0 && cp.suffix_len == 1) {
            int nw = int(cp.tail_len) >> 2;  // word where nonce starts
            W[nw]   = (uint32_t(nonce_str[0]) << 24) | (uint32_t(nonce_str[1]) << 16) |
                      (uint32_t(nonce_str[2]) << 8)  | uint32_t(nonce_str[3]);
            W[nw+1] = (uint32_t(nonce_str[4]) << 24) | (uint32_t(nonce_str[5]) << 16) |
                      (uint32_t(nonce_str[6]) << 8)  | uint32_t(nonce_str[7]);
            W[nw+2] = (uint32_t(nonce_str[8]) << 24) | (uint32_t(nonce_str[9]) << 16) |
                      (uint32_t(cp.suffix[0]) << 8)  | 0x80;
        } else {
            W[11] = cp.tail_w[11]; W[12] = cp.tail_w[12]; W[13] = cp.tail_w[13];
            int p = int(cp.tail_len);
            for (int i = 0; i < nonce_len; i++, p++)
                place_byte(W, p, nonce_str[i]);
            for (uint32_t i = 0; i < cp.suffix_len; i++, p++)
                place_byte(W, p, cp.suffix[i]);
            place_byte(W, p, 0x80);
        }

        // Use precomputed partial state (skips rounds 0-10)
        state[0] = cp.partial_state[0]; state[1] = cp.partial_state[1];
        state[2] = cp.partial_state[2]; state[3] = cp.partial_state[3];
        state[4] = cp.partial_state[4]; state[5] = cp.partial_state[5];
        state[6] = cp.partial_state[6]; state[7] = cp.partial_state[7];
        sha256_compress_from11(state, cp.midstate, W, cp.precomputed_w);
    } else {
        // ==== SLOW PATH: two SHA-256 blocks (no precomputation) ====
        uint64_t total_msg_len = cp.total_prefix_len + nonce_len + cp.suffix_len;
        uint64_t total_bit_len = total_msg_len * 8;

        state[0] = cp.midstate[0]; state[1] = cp.midstate[1];
        state[2] = cp.midstate[2]; state[3] = cp.midstate[3];
        state[4] = cp.midstate[4]; state[5] = cp.midstate[5];
        state[6] = cp.midstate[6]; state[7] = cp.midstate[7];
        uint32_t W[16];
        W[ 0] = cp.tail_w[ 0]; W[ 1] = cp.tail_w[ 1];
        W[ 2] = cp.tail_w[ 2]; W[ 3] = cp.tail_w[ 3];
        W[ 4] = cp.tail_w[ 4]; W[ 5] = cp.tail_w[ 5];
        W[ 6] = cp.tail_w[ 6]; W[ 7] = cp.tail_w[ 7];
        W[ 8] = cp.tail_w[ 8]; W[ 9] = cp.tail_w[ 9];
        W[10] = cp.tail_w[10]; W[11] = cp.tail_w[11];
        W[12] = cp.tail_w[12]; W[13] = cp.tail_w[13];
        W[14] = cp.tail_w[14]; W[15] = cp.tail_w[15];

        int p = int(cp.tail_len);
        int nonce_idx = 0, suffix_idx = 0;
        while (nonce_idx < nonce_len && p < 64)
            { place_byte(W, p, nonce_str[nonce_idx++]); p++; }
        while (suffix_idx < int(cp.suffix_len) && p < 64)
            { place_byte(W, p, cp.suffix[suffix_idx++]); p++; }
        bool padded = false;
        if (p < 64) { place_byte(W, p, 0x80); p++; padded = true; }
        sha256_compress(state, W);

        for (int i = 0; i < 16; i++) W[i] = 0;
        int p2 = 0;
        while (nonce_idx < nonce_len)
            { place_byte(W, p2, nonce_str[nonce_idx++]); p2++; }
        while (suffix_idx < int(cp.suffix_len))
            { place_byte(W, p2, cp.suffix[suffix_idx++]); p2++; }
        if (!padded) { place_byte(W, p2, 0x80); p2++; }
        W[14] = uint32_t(total_bit_len >> 32);
        W[15] = uint32_t(total_bit_len);
        sha256_compress(state, W);
    }

    // Check difficulty
    if (meets_difficulty(state, cp.difficulty_bits)) {
        uint expected = 0;
        if (atomic_compare_exchange_weak_explicit(
                &result_found, &expected, 1u,
                memory_order_relaxed, memory_order_relaxed)) {
            result_nonce[0] = nonce;
            for (int i = 0; i < 8; i++)
                result_hash[i] = state[i];
        }
    }
}
