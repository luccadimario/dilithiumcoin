package main

// Custom SHA-256 implementation with midstate support for mining.
// The midstate optimization allows us to pre-compute the SHA-256 state
// for the fixed prefix of block data, then only process the variable
// nonce + suffix for each hash attempt. This saves re-hashing ~500+ bytes
// of constant data per attempt.

// SHA-256 round constants
var k256 = [64]uint32{
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
}

// SHA256State holds the intermediate SHA-256 hash state.
type SHA256State struct {
	H   [8]uint32
	Len uint64 // total bytes processed into this state
}

// InitSHA256 returns the initial SHA-256 state (IV).
func InitSHA256() SHA256State {
	return SHA256State{
		H: [8]uint32{
			0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
			0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
		},
	}
}

func rotr(x uint32, n uint) uint32 {
	return (x >> n) | (x << (32 - n))
}

// sha256Block processes a single 64-byte block through SHA-256 compression.
func sha256Block(h *[8]uint32, block []byte) {
	var w [64]uint32

	// Load message schedule from block (big-endian)
	for i := 0; i < 16; i++ {
		w[i] = uint32(block[i*4])<<24 | uint32(block[i*4+1])<<16 |
			uint32(block[i*4+2])<<8 | uint32(block[i*4+3])
	}

	// Extend message schedule
	for i := 16; i < 64; i++ {
		s0 := rotr(w[i-15], 7) ^ rotr(w[i-15], 18) ^ (w[i-15] >> 3)
		s1 := rotr(w[i-2], 17) ^ rotr(w[i-2], 19) ^ (w[i-2] >> 10)
		w[i] = w[i-16] + s0 + w[i-7] + s1
	}

	// Initialize working variables
	a, b, c, d, e, f, g, hh := h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]

	// 64 rounds of compression
	for i := 0; i < 64; i++ {
		S1 := rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)
		ch := (e & f) ^ (^e & g)
		temp1 := hh + S1 + ch + k256[i] + w[i]
		S0 := rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)
		maj := (a & b) ^ (a & c) ^ (b & c)
		temp2 := S0 + maj

		hh = g
		g = f
		f = e
		e = d + temp1
		d = c
		c = b
		b = a
		a = temp1 + temp2
	}

	// Add compressed chunk to hash state
	h[0] += a
	h[1] += b
	h[2] += c
	h[3] += d
	h[4] += e
	h[5] += f
	h[6] += g
	h[7] += hh
}

// ProcessBlocks feeds complete 64-byte blocks into the SHA-256 state.
// Only pass data whose length is a multiple of 64.
func (s *SHA256State) ProcessBlocks(data []byte) {
	for len(data) >= 64 {
		sha256Block(&s.H, data[:64])
		s.Len += 64
		data = data[64:]
	}
}

// MineHash computes SHA-256 from a midstate and remaining data.
// This is the hot-path function called for every nonce attempt.
// It copies the midstate to stack variables to avoid mutating the original.
//
// remaining = prefixTail + nonceStr + suffix (variable length, < 128 bytes typically)
// midstateLen = number of bytes already processed into midstate
func MineHash(midH [8]uint32, remaining []byte, midstateLen uint64) [32]byte {
	h := midH // copy state to stack

	totalLen := midstateLen + uint64(len(remaining))

	// If remaining has any complete 64-byte blocks, process them
	for len(remaining) >= 64 {
		sha256Block(&h, remaining[:64])
		remaining = remaining[64:]
	}

	// Build final padded block(s) on stack — no heap allocation
	var buf [128]byte
	rlen := len(remaining)
	copy(buf[:rlen], remaining)
	buf[rlen] = 0x80

	// Determine padding length: need room for 8-byte length at end of 64-byte block
	padLen := 64
	if rlen >= 56 {
		padLen = 128
	}

	// Bytes rlen+1 through padLen-9 are already zero (stack-allocated)

	// Write total bit length as big-endian uint64
	bitLen := totalLen * 8
	buf[padLen-8] = byte(bitLen >> 56)
	buf[padLen-7] = byte(bitLen >> 48)
	buf[padLen-6] = byte(bitLen >> 40)
	buf[padLen-5] = byte(bitLen >> 32)
	buf[padLen-4] = byte(bitLen >> 24)
	buf[padLen-3] = byte(bitLen >> 16)
	buf[padLen-2] = byte(bitLen >> 8)
	buf[padLen-1] = byte(bitLen)

	// Process final block(s)
	sha256Block(&h, buf[:64])
	if padLen == 128 {
		sha256Block(&h, buf[64:128])
	}

	// Serialize hash as big-endian bytes
	var hash [32]byte
	for i := 0; i < 8; i++ {
		hash[i*4] = byte(h[i] >> 24)
		hash[i*4+1] = byte(h[i] >> 16)
		hash[i*4+2] = byte(h[i] >> 8)
		hash[i*4+3] = byte(h[i])
	}
	return hash
}

// writeInt64 writes n as a decimal string into buf and returns bytes written.
// Zero allocations — critical for the mining hot loop.
func writeInt64(buf []byte, n int64) int {
	if n == 0 {
		buf[0] = '0'
		return 1
	}
	neg := false
	un := uint64(n)
	if n < 0 {
		neg = true
		un = uint64(-n)
	}

	// Extract digits in reverse
	var digits [20]byte
	pos := 0
	for un > 0 {
		digits[pos] = byte('0' + un%10)
		un /= 10
		pos++
	}

	offset := 0
	if neg {
		buf[0] = '-'
		offset = 1
	}
	for i := 0; i < pos; i++ {
		buf[offset+i] = digits[pos-1-i]
	}
	return offset + pos
}

// meetsDifficultyBytes checks if a raw SHA-256 hash has the required number
// of leading zero bits. Operates on raw bytes — no hex encoding needed.
func meetsDifficultyBytes(hash [32]byte, bits int) bool {
	if bits <= 0 {
		return true
	}
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
		if hash[i] != 0 {
			return false
		}
	}
	rem := bits % 8
	if rem > 0 {
		mask := byte(0xFF) << uint(8-rem)
		if hash[fullBytes]&mask != 0 {
			return false
		}
	}
	return true
}

// hashToHex converts a raw 32-byte hash to a hex string.
// Only called once when a valid hash is found — not in the hot loop.
func hashToHex(hash [32]byte) string {
	const hexChars = "0123456789abcdef"
	var buf [64]byte
	for i, b := range hash {
		buf[i*2] = hexChars[b>>4]
		buf[i*2+1] = hexChars[b&0x0f]
	}
	return string(buf[:])
}
