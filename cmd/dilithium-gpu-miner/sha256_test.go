package main

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"
)

// TestSHA256Correctness verifies our custom SHA-256 matches crypto/sha256.
func TestSHA256Correctness(t *testing.T) {
	tests := []string{
		"",
		"a",
		"abc",
		"hello world",
		"The quick brown fox jumps over the lazy dog",
		// Simulate block data with various nonces
		`1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae06`,
		`1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae9999996`,
	}

	for _, input := range tests {
		data := []byte(input)

		// Standard library
		expected := sha256.Sum256(data)

		// Our implementation via midstate
		state := InitSHA256()
		fullBlocks := (len(data) / 64) * 64
		if fullBlocks > 0 {
			state.ProcessBlocks(data[:fullBlocks])
		}
		got := MineHash(state.H, data[fullBlocks:], state.Len)

		if got != expected {
			t.Errorf("SHA-256 mismatch for %q\n  expected: %x\n  got:      %x", input, expected, got)
		}
	}
}

// TestMidstateCorrectness tests that midstate produces correct results for mining-like data.
func TestMidstateCorrectness(t *testing.T) {
	// Simulate actual mining: prefix + nonce + suffix
	prefix := `1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`
	suffix := "6"

	for nonce := int64(0); nonce < 1000; nonce++ {
		nonceStr := strconv.FormatInt(nonce, 10)
		fullData := prefix + nonceStr + suffix

		// Standard library
		expected := sha256.Sum256([]byte(fullData))

		// Midstate approach
		prefixBytes := []byte(prefix)
		state := InitSHA256()
		fullBlocks := (len(prefixBytes) / 64) * 64
		if fullBlocks > 0 {
			state.ProcessBlocks(prefixBytes[:fullBlocks])
		}

		// Build remaining: prefix tail + nonce + suffix
		tail := prefixBytes[fullBlocks:]
		var buf [128]byte
		copy(buf[:], tail)
		pos := len(tail)
		pos += writeInt64(buf[pos:], nonce)
		copy(buf[pos:], []byte(suffix))
		pos += len(suffix)

		got := MineHash(state.H, buf[:pos], state.Len)

		if got != expected {
			t.Errorf("Midstate mismatch for nonce %d\n  expected: %x\n  got:      %x", nonce, expected, got)
		}
	}
}

// TestWriteInt64 verifies our integer-to-string conversion matches strconv.
func TestWriteInt64(t *testing.T) {
	tests := []int64{0, 1, 9, 10, 99, 100, 999, 1000, 12345, 99999,
		100000, 999999, 1000000, 9999999, 123456789, 9999999999,
		1738368000, 5892535, -1, -100}

	for _, n := range tests {
		expected := strconv.FormatInt(n, 10)
		var buf [20]byte
		written := writeInt64(buf[:], n)
		got := string(buf[:written])
		if got != expected {
			t.Errorf("writeInt64(%d) = %q, want %q", n, got, expected)
		}
	}
}

// TestHashToHex verifies hex encoding matches encoding/hex.
func TestHashToHex(t *testing.T) {
	data := []byte("test data for hex encoding")
	hash := sha256.Sum256(data)
	expected := hex.EncodeToString(hash[:])
	got := hashToHex(hash)
	if got != expected {
		t.Errorf("hashToHex mismatch:\n  expected: %s\n  got:      %s", expected, got)
	}
}

// TestMeetsDifficultyBytes checks difficulty validation on raw bytes.
func TestMeetsDifficultyBytes(t *testing.T) {
	// Hash with exactly 24 leading zero bits (3 zero bytes, then 0x80)
	var hash [32]byte
	hash[3] = 0x80 // binary 10000000 → 0 extra zero bits
	if !meetsDifficultyBytes(hash, 24) {
		t.Error("Should meet 24-bit difficulty")
	}
	if meetsDifficultyBytes(hash, 25) {
		t.Error("Should NOT meet 25-bit difficulty")
	}

	// Hash with 20 leading zero bits (2 zero bytes + 4 zero bits)
	var hash2 [32]byte
	hash2[2] = 0x0F // top 4 bits are 0, bottom 4 are 1
	if !meetsDifficultyBytes(hash2, 20) {
		t.Error("Should meet 20-bit difficulty")
	}
	if meetsDifficultyBytes(hash2, 21) {
		t.Error("Should NOT meet 21-bit difficulty")
	}
}

// BenchmarkMineHash measures the per-hash cost with midstate optimization.
func BenchmarkMineHash(b *testing.B) {
	prefix := []byte(`1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`)
	suffix := []byte("6")

	state := InitSHA256()
	fullBlocks := (len(prefix) / 64) * 64
	if fullBlocks > 0 {
		state.ProcessBlocks(prefix[:fullBlocks])
	}
	tail := prefix[fullBlocks:]

	var buf [128]byte
	copy(buf[:], tail)
	tailLen := len(tail)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pos := tailLen
		pos += writeInt64(buf[pos:], int64(i))
		copy(buf[pos:], suffix)
		pos += len(suffix)
		_ = MineHash(state.H, buf[:pos], state.Len)
	}
}

// BenchmarkStdlibSHA256 measures crypto/sha256 with string concat (like original miner).
func BenchmarkStdlibSHA256(b *testing.B) {
	prefix := `1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`
	suffix := "6"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data := prefix + strconv.FormatInt(int64(i), 10) + suffix
		_ = sha256.Sum256([]byte(data))
	}
}

// BenchmarkStdlibZeroAlloc measures stdlib SHA-256 with zero allocations.
func BenchmarkStdlibZeroAlloc(b *testing.B) {
	prefix := []byte(`1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`)
	suffix := []byte("6")
	var nonceBuf [20]byte
	var hashBuf [32]byte

	h := sha256.New()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		h.Reset()
		h.Write(prefix)
		n := writeInt64(nonceBuf[:], int64(i)+1000000)
		h.Write(nonceBuf[:n])
		h.Write(suffix)
		h.Sum(hashBuf[:0])
	}
}

// TestGPUMidstateExtraction verifies that the GPU midstate extraction from
// MarshalBinary matches the actual SHA-256 state and produces correct hashes.
// This validates the same logic used in gpu_cuda.go Mine() without needing CUDA.
func TestGPUMidstateExtraction(t *testing.T) {
	prefix := []byte(`5791173900000[{"from":"SYSTEM","to":"f21d6b988a1dfe7d519fddafbb1efea4bf234cb4","amount":5000000000,"timestamp":1739000000,"signature":"coinbase-5791-1739000000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`)
	suffix := []byte("10")

	// Compute midstate using stdlib
	h := sha256.New()
	fullBlocks := (len(prefix) / 64) * 64
	if fullBlocks > 0 {
		h.Write(prefix[:fullBlocks])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlocks:]

	t.Logf("Prefix length: %d, fullBlocks: %d, tail: %d", len(prefix), fullBlocks, len(prefixTail))
	t.Logf("Midstate length: %d", len(midstate))
	t.Logf("Midstate magic: %x", midstate[0:4])

	// Extract H values the same way gpu_cuda.go does (after fix)
	var gpuH [8]uint32
	for i := 0; i < 8; i++ {
		offset := 4 + i*4
		gpuH[i] = uint32(midstate[offset])<<24 |
			uint32(midstate[offset+1])<<16 |
			uint32(midstate[offset+2])<<8 |
			uint32(midstate[offset+3])
	}

	// Extract H values using our Go SHA-256 implementation for comparison
	cpuState := InitSHA256()
	if fullBlocks > 0 {
		cpuState.ProcessBlocks(prefix[:fullBlocks])
	}

	// Compare H values
	for i := 0; i < 8; i++ {
		if gpuH[i] != cpuState.H[i] {
			t.Errorf("H[%d] mismatch: GPU extracted 0x%08x, CPU computed 0x%08x", i, gpuH[i], cpuState.H[i])
		}
	}

	// Extract totalPrefixLen the same way gpu_cuda.go does (after fix)
	var gpuPrefixLen uint64
	if len(midstate) >= 108 {
		gpuPrefixLen = uint64(midstate[100])<<56 | uint64(midstate[101])<<48 |
			uint64(midstate[102])<<40 | uint64(midstate[103])<<32 |
			uint64(midstate[104])<<24 | uint64(midstate[105])<<16 |
			uint64(midstate[106])<<8 | uint64(midstate[107])
	}

	if gpuPrefixLen != cpuState.Len {
		t.Errorf("Prefix len mismatch: GPU extracted %d, CPU computed %d", gpuPrefixLen, cpuState.Len)
	}

	gpuTotalLen := gpuPrefixLen + uint64(len(prefixTail))
	t.Logf("GPU H: %08x %08x %08x %08x %08x %08x %08x %08x",
		gpuH[0], gpuH[1], gpuH[2], gpuH[3], gpuH[4], gpuH[5], gpuH[6], gpuH[7])
	t.Logf("CPU H: %08x %08x %08x %08x %08x %08x %08x %08x",
		cpuState.H[0], cpuState.H[1], cpuState.H[2], cpuState.H[3],
		cpuState.H[4], cpuState.H[5], cpuState.H[6], cpuState.H[7])
	t.Logf("GPU prefixLen=%d, totalLen=%d (CPU prefixLen=%d)", gpuPrefixLen, gpuTotalLen, cpuState.Len)

	// Now verify that computing the hash with extracted midstate matches stdlib
	for _, nonce := range []int64{0, 1, 42, 999, 123456789} {
		nonceStr := strconv.FormatInt(nonce, 10)
		fullData := string(prefix) + nonceStr + string(suffix)
		expected := sha256.Sum256([]byte(fullData))

		// Compute using our MineHash (same as CPU worker)
		var buf [128]byte
		copy(buf[:], prefixTail)
		pos := len(prefixTail)
		pos += writeInt64(buf[pos:], nonce)
		copy(buf[pos:], suffix)
		pos += len(suffix)

		got := MineHash(gpuH, buf[:pos], gpuPrefixLen)

		if got != expected {
			t.Errorf("Hash mismatch for nonce %d:\n  expected: %s\n  got:      %s",
				nonce, hex.EncodeToString(expected[:]), hex.EncodeToString(got[:]))
		} else {
			t.Logf("Nonce %d: hash=%s OK", nonce, hex.EncodeToString(got[:8]))
		}
	}
}

// TestGPUvsCPUHashMatch verifies the CUDA kernel's hash computation logic
// matches Go by simulating what the kernel does in pure Go.
func TestGPUvsCPUHashMatch(t *testing.T) {
	prefix := []byte(`5791173900000[{"from":"SYSTEM","to":"f21d6b988a1dfe7d519fddafbb1efea4bf234cb4","amount":5000000000,"timestamp":1739000000,"signature":"coinbase-5791-1739000000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`)
	suffix := []byte("10")

	h := sha256.New()
	fullBlocks := (len(prefix) / 64) * 64
	if fullBlocks > 0 {
		h.Write(prefix[:fullBlocks])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlocks:]

	// Extract like gpu_cuda.go
	var gpuH [8]uint32
	for i := 0; i < 8; i++ {
		offset := 4 + i*4
		gpuH[i] = uint32(midstate[offset])<<24 |
			uint32(midstate[offset+1])<<16 |
			uint32(midstate[offset+2])<<8 |
			uint32(midstate[offset+3])
	}
	var gpuPrefixLen uint64
	if len(midstate) >= 108 {
		gpuPrefixLen = uint64(midstate[100])<<56 | uint64(midstate[101])<<48 |
			uint64(midstate[102])<<40 | uint64(midstate[103])<<32 |
			uint64(midstate[104])<<24 | uint64(midstate[105])<<16 |
			uint64(midstate[106])<<8 | uint64(midstate[107])
	}
	gpuTotalPrefixLen := gpuPrefixLen + uint64(len(prefixTail))

	// Simulate what the CUDA kernel does for nonce=42
	nonce := int64(42)
	nonceStr := fmt.Sprintf("%d", nonce)

	// Build the data buffer like the CUDA kernel
	var data [128]byte
	pos := 0
	copy(data[pos:], prefixTail)
	pos += len(prefixTail)
	copy(data[pos:], nonceStr)
	pos += len(nonceStr)
	copy(data[pos:], suffix)
	pos += len(suffix)

	totalMsgLen := gpuTotalPrefixLen + uint64(len(nonceStr)) + uint64(len(suffix))

	// Add padding like CUDA kernel
	data[pos] = 0x80
	pos++

	numBlocks := 1
	if pos+8 > 64 {
		numBlocks = 2
		for pos < 64 {
			data[pos] = 0
			pos++
		}
		for pos < 120 {
			data[pos] = 0
			pos++
		}
	} else {
		for pos < 56 {
			data[pos] = 0
			pos++
		}
	}

	// Write bit length
	bitLen := totalMsgLen * 8
	lenOffset := numBlocks*64 - 8
	data[lenOffset+0] = byte(bitLen >> 56)
	data[lenOffset+1] = byte(bitLen >> 48)
	data[lenOffset+2] = byte(bitLen >> 40)
	data[lenOffset+3] = byte(bitLen >> 32)
	data[lenOffset+4] = byte(bitLen >> 24)
	data[lenOffset+5] = byte(bitLen >> 16)
	data[lenOffset+6] = byte(bitLen >> 8)
	data[lenOffset+7] = byte(bitLen)

	// Process blocks through SHA-256
	state := gpuH
	for blk := 0; blk < numBlocks; blk++ {
		sha256Block(&state, data[blk*64:(blk+1)*64])
	}

	// Serialize to hash
	var gpuHash [32]byte
	for i := 0; i < 8; i++ {
		gpuHash[i*4] = byte(state[i] >> 24)
		gpuHash[i*4+1] = byte(state[i] >> 16)
		gpuHash[i*4+2] = byte(state[i] >> 8)
		gpuHash[i*4+3] = byte(state[i])
	}

	// Compare with stdlib
	fullData := string(prefix) + nonceStr + string(suffix)
	expected := sha256.Sum256([]byte(fullData))

	if gpuHash != expected {
		t.Errorf("GPU simulation hash mismatch:\n  expected: %s\n  got:      %s\n  totalMsgLen: %d\n  numBlocks: %d",
			hex.EncodeToString(expected[:]), hex.EncodeToString(gpuHash[:]), totalMsgLen, numBlocks)
	} else {
		t.Logf("GPU simulation matches stdlib! hash=%s", hex.EncodeToString(gpuHash[:16]))
	}
}

// BenchmarkStdlibMidstate measures stdlib SHA-256 with midstate via MarshalBinary.
func BenchmarkStdlibMidstate(b *testing.B) {
	prefix := []byte(`1173849284[{"from":"SYSTEM","to":"addr123","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`)
	suffix := []byte("6")

	// Compute midstate using stdlib
	h := sha256.New()
	fullBlocks := (len(prefix) / 64) * 64
	h.Write(prefix[:fullBlocks])
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlocks:]

	var nonceBuf [20]byte
	var hashBuf [32]byte

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		h.(encoding.BinaryUnmarshaler).UnmarshalBinary(midstate)
		h.Write(prefixTail)
		n := writeInt64(nonceBuf[:], int64(i)+1000000)
		h.Write(nonceBuf[:n])
		h.Write(suffix)
		h.Sum(hashBuf[:0])
	}
}
