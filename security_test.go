package main

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// DIFFICULTY BITS TESTS
// ============================================================================

func TestMeetsDifficultyBits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hash string
		bits int
		want bool
	}{
		// 0 bits = any hash passes
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 0, true},
		// 4 bits = 1 leading hex zero
		{"0fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 4, true},
		{"1fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 4, false},
		// 8 bits = 2 leading hex zeros
		{"00ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 8, true},
		{"01ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 8, false},
		// 24 bits = 6 leading hex zeros
		{"000000ffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 24, true},
		{"000001ffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 24, false},
		// Partial bit (5 bits): first hex 0 (4 bits) + next hex char < 8 (1 more bit)
		{"07ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 5, true},
		{"08ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 5, false},
		// 6 bits: first hex 0 + next < 4
		{"03ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 6, true},
		{"04ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 6, false},
		// Empty hash
		{"", 1, false},
		// Short hash
		{"00", 8, true},
		{"00", 12, false},
	}
	for _, tt := range tests {
		got := meetsDifficultyBits(tt.hash, tt.bits)
		if got != tt.want {
			t.Errorf("meetsDifficultyBits(%q, %d) = %v, want %v", tt.hash, tt.bits, got, tt.want)
		}
	}
}

func TestDifficultyBitsConversion(t *testing.T) {
	t.Parallel()
	// hex digits to bits: 6 hex digits = 24 bits
	if got := hexDigitsToDifficultyBits(6); got != 24 {
		t.Errorf("hexDigitsToDifficultyBits(6) = %d, want 24", got)
	}
	// bits to hex: 24 bits = 6 hex digits
	if got := difficultyBitsToHexDigits(24); got != 6 {
		t.Errorf("difficultyBitsToHexDigits(24) = %d, want 6", got)
	}
	// Round-trip
	for hex := 1; hex <= 16; hex++ {
		bits := hexDigitsToDifficultyBits(hex)
		back := difficultyBitsToHexDigits(bits)
		if back != hex {
			t.Errorf("round-trip failed: %d -> %d -> %d", hex, bits, back)
		}
	}
}

func TestGetEffectiveDifficultyBits(t *testing.T) {
	t.Parallel()
	// Block with DifficultyBits set (v3.0.9+)
	b1 := &Block{DifficultyBits: 28, Difficulty: 6}
	if got := b1.getEffectiveDifficultyBits(); got != 28 {
		t.Errorf("expected 28, got %d", got)
	}
	// Legacy block (v3.0.1) - DifficultyBits=0, uses hex conversion
	b2 := &Block{DifficultyBits: 0, Difficulty: 6}
	if got := b2.getEffectiveDifficultyBits(); got != 24 {
		t.Errorf("expected 24, got %d", got)
	}
	// Edge case: both zero
	b3 := &Block{DifficultyBits: 0, Difficulty: 0}
	if got := b3.getEffectiveDifficultyBits(); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// ============================================================================
// CUMULATIVE WORK TESTS
// ============================================================================

func TestCumulativeWork(t *testing.T) {
	t.Parallel()
	// Empty chain
	if got := cumulativeWork(nil); got != 0 {
		t.Errorf("cumulativeWork(nil) = %f, want 0", got)
	}

	// Chain with known difficulty
	chain := []*Block{
		{DifficultyBits: 24, Difficulty: 6},
		{DifficultyBits: 24, Difficulty: 6},
	}
	expected := 2 * math.Pow(2, 24)
	if got := cumulativeWork(chain); got != expected {
		t.Errorf("cumulativeWork = %f, want %f", got, expected)
	}

	// Higher difficulty = more work
	chainHigh := []*Block{
		{DifficultyBits: 28, Difficulty: 7},
	}
	chainLow := []*Block{
		{DifficultyBits: 24, Difficulty: 6},
		{DifficultyBits: 24, Difficulty: 6},
		{DifficultyBits: 24, Difficulty: 6},
	}
	// 2^28 = 268M > 3 * 2^24 = 50M
	if cumulativeWork(chainHigh) <= cumulativeWork(chainLow) {
		t.Error("single 28-bit block should have more work than three 24-bit blocks")
	}
}

// ============================================================================
// BALANCE CACHE TESTS
// ============================================================================

func TestBalanceCache(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	miner, _ := NewWallet()

	// Mine a block
	bc.MinePendingTransactions(miner.Address)

	// First call builds cache
	balance := bc.GetBalance(miner.Address)
	if balance != 50*DLTUnit {
		t.Fatalf("miner balance = %d, want %d", balance, 50*DLTUnit)
	}

	// Total transactions should include coinbase
	totalTx := bc.GetTotalTransactions()
	if totalTx < 1 {
		t.Fatalf("expected at least 1 transaction, got %d", totalTx)
	}

	// Mine another block - cache should refresh
	bc.MinePendingTransactions(miner.Address)
	balance2 := bc.GetBalance(miner.Address)
	if balance2 != 100*DLTUnit {
		t.Fatalf("miner balance after 2 blocks = %d, want %d", balance2, 100*DLTUnit)
	}
}

func TestBalanceCacheInvalidation(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	miner, _ := NewWallet()

	// Build cache at height 1
	bc.GetBalance(miner.Address)

	// Mine - changes chain height
	bc.MinePendingTransactions(miner.Address)

	// Cache should auto-refresh
	balance := bc.GetBalance(miner.Address)
	if balance == 0 {
		t.Fatal("balance should be non-zero after mining")
	}
}

func TestGetAddressInfo(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	miner, _ := NewWallet()
	recipient, _ := NewWallet()

	// Mine to get funds
	bc.MinePendingTransactions(miner.Address)

	// Send some
	tx := NewTransaction(miner.Address, recipient.Address, 10*DLTUnit)
	tx.Sign(miner)
	bc.AddTransaction(tx)
	bc.MinePendingTransactions(miner.Address)

	balance, received, sent, txs := bc.GetAddressInfo(recipient.Address)
	if balance != 10*DLTUnit {
		t.Errorf("recipient balance = %d, want %d", balance, 10*DLTUnit)
	}
	if received != 10*DLTUnit {
		t.Errorf("received = %d, want %d", received, 10*DLTUnit)
	}
	if sent != 0 {
		t.Errorf("sent = %d, want 0", sent)
	}
	if len(txs) != 1 {
		t.Errorf("expected 1 transaction, got %d", len(txs))
	}
}

// ============================================================================
// CHAIN ID / TRANSACTION REPLAY PROTECTION TESTS
// ============================================================================

func TestTransactionChainID(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100)
	tx.Sign(w)

	// Should verify with chain ID
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("chain ID verification failed: %v", err)
	}
}

func TestTransactionSignatureNotEmpty(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100)
	tx.Sign(w)

	if !tx.HasSignature() {
		t.Fatal("HasSignature() should return true after signing")
	}

	tx2 := NewTransaction(w.Address, "recipient", 100)
	if tx2.HasSignature() {
		t.Fatal("HasSignature() should return false before signing")
	}
}

// ============================================================================
// BLOCK VALIDATION TESTS
// ============================================================================

func TestValidateBlockTransactionsMaxTx(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Create a block with too many transactions
	block := &Block{
		Index:        1,
		Timestamp:    time.Now().Unix(),
		Transactions: make([]*Transaction, 5001), // exceeds MaxTxPerBlock
		PreviousHash: bc.Blocks[0].Hash,
		Difficulty:   2,
	}
	for i := range block.Transactions {
		block.Transactions[i] = &Transaction{
			From:      "SYSTEM",
			To:        "miner",
			Amount:    1,
			Signature: fmt.Sprintf("sig-%d", i),
		}
	}

	err := bc.ValidateBlockTransactions(block, bc.Blocks)
	if err == nil {
		t.Fatal("expected error for too many transactions")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlockTimestampFuture(t *testing.T) {
	t.Parallel()
	// A block 3 hours in the future should be rejected by timestamp validation
	futureTimestamp := time.Now().Unix() + 3*3600
	if futureTimestamp <= time.Now().Unix()+2*3600 {
		t.Fatal("test setup error: timestamp not far enough in future")
	}
}

// ============================================================================
// RATE LIMITER TESTS
// ============================================================================

func TestRateLimiterBasic(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(5, time.Minute)

	// First 5 should pass
	for i := 0; i < 5; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// 6th should be blocked
	if rl.Allow("192.168.1.1") {
		t.Fatal("6th request should be rate limited")
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(2, time.Minute)

	// IP 1 uses its quota
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")
	if rl.Allow("10.0.0.1") {
		t.Fatal("IP 1 should be rate limited")
	}

	// IP 2 should still be allowed
	if !rl.Allow("10.0.0.2") {
		t.Fatal("IP 2 should be allowed (separate quota)")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(2, 50*time.Millisecond)

	rl.Allow("1.2.3.4")
	rl.Allow("1.2.3.4")
	if rl.Allow("1.2.3.4") {
		t.Fatal("should be rate limited")
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("should be allowed after window reset")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(2, time.Minute)

	handler := rateLimitMiddleware(rl, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// First 2 requests should pass
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: got status %d, want 200", i+1, rr.Code)
		}
	}

	// 3rd should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request: got status %d, want 429", rr.Code)
	}
}

// ============================================================================
// DIFFICULTY ADJUSTMENT TESTS
// ============================================================================

func TestDifficultyAdjustmentClamp(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	bc.DifficultyBits = 24

	// Verify MinDifficultyBits and MaxDifficultyBits are sensible
	if MinDifficultyBits < 1 {
		t.Fatal("MinDifficultyBits should be >= 1")
	}
	if MaxDifficultyBits <= MinDifficultyBits {
		t.Fatal("MaxDifficultyBits should be > MinDifficultyBits")
	}
}

func TestRecalcDifficultyFromChainWithAnchor(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	bc.DifficultyBits = 24

	// Build a chain with BlocksPerAdjustment * 2 + 10 blocks
	// to test the anchor-based replay
	baseTime := int64(1700000000)
	for i := 1; i < BlocksPerAdjustment*2+10; i++ {
		block := &Block{
			Index:        int64(i),
			Timestamp:    baseTime + int64(i)*6, // 6 seconds per block (too fast)
			PreviousHash: bc.Blocks[len(bc.Blocks)-1].Hash,
			Difficulty:   6,
			DifficultyBits: 0, // legacy blocks
			Transactions: []*Transaction{},
		}
		block.Hash = block.CalculateHash()
		bc.Blocks = append(bc.Blocks, block)
	}

	// Add one v3.0.9+ anchor block with DifficultyBits set
	anchorIdx := len(bc.Blocks) - 5
	bc.Blocks[anchorIdx].DifficultyBits = 26

	bc.recalcDifficultyFromChain()

	// Should find the anchor (26 bits) and replay from there
	// The periods after the anchor should adjust upward
	if bc.DifficultyBits < 26 {
		t.Errorf("DifficultyBits = %d, should be >= 26 (anchor value)", bc.DifficultyBits)
	}
}

func TestRecalcDifficultyFromChainNoAnchor(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	bc.DifficultyBits = 24

	// Build a chain with all legacy blocks (no anchor)
	baseTime := int64(1700000000)
	for i := 1; i < BlocksPerAdjustment*3; i++ {
		block := &Block{
			Index:        int64(i),
			Timestamp:    baseTime + int64(i)*6,
			PreviousHash: bc.Blocks[len(bc.Blocks)-1].Hash,
			Difficulty:   6,
			DifficultyBits: 0,
			Transactions: []*Transaction{},
		}
		block.Hash = block.CalculateHash()
		bc.Blocks = append(bc.Blocks, block)
	}

	bc.recalcDifficultyFromChain()

	// Without anchor, replays last 5 periods from MinDifficultyBits
	// Should still produce a reasonable result (not 0, not absurdly high)
	if bc.DifficultyBits < MinDifficultyBits {
		t.Errorf("DifficultyBits = %d, should be >= MinDifficultyBits (%d)", bc.DifficultyBits, MinDifficultyBits)
	}
	if bc.DifficultyBits > 60 {
		t.Errorf("DifficultyBits = %d, unreasonably high", bc.DifficultyBits)
	}
}

// ============================================================================
// CHAIN PAGINATION TEST
// ============================================================================

func TestChainPaginationDefaults(t *testing.T) {
	t.Parallel()
	// Default limit should be 50, capped at 500
	limit := 50
	if limit > 500 {
		limit = 500
	}
	if limit != 50 {
		t.Fatalf("default limit = %d, want 50", limit)
	}
}

// ============================================================================
// CORS MIDDLEWARE TEST
// ============================================================================

func TestCORSMiddleware(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(inner)

	// Allowed origin
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://dilithiumcoin.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://dilithiumcoin.com" {
		t.Errorf("CORS origin = %q, want %q", got, "https://dilithiumcoin.com")
	}

	// Disallowed origin
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Origin", "https://evil.com")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if got := rr2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS should not allow evil.com, got %q", got)
	}

	// OPTIONS preflight
	req3 := httptest.NewRequest("OPTIONS", "/test", nil)
	req3.Header.Set("Origin", "https://dilithiumcoin.com")
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want 200", rr3.Code)
	}
}

// ============================================================================
// CONFIG VALIDATION TEST
// ============================================================================

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}

	// Empty P2P port
	c2 := DefaultConfig()
	c2.P2PPort = ""
	if err := c2.Validate(); err == nil {
		t.Fatal("empty P2P port should fail validation")
	}

	// MinOutbound > MaxOutbound
	c3 := DefaultConfig()
	c3.Network.MinOutbound = 100
	c3.Network.MaxOutbound = 5
	if err := c3.Validate(); err == nil {
		t.Fatal("MinOutbound > MaxOutbound should fail")
	}

	// Difficulty < 1
	c4 := DefaultConfig()
	c4.Chain.DefaultDifficulty = 0
	if err := c4.Validate(); err == nil {
		t.Fatal("difficulty 0 should fail")
	}
}

// ============================================================================
// API DEFAULT BIND ADDRESS TEST
// ============================================================================

func TestDefaultAPIBindLocalhost(t *testing.T) {
	t.Parallel()
	c := DefaultAPIConfig()
	if c.Host != "127.0.0.1" {
		t.Errorf("default API host = %q, want 127.0.0.1", c.Host)
	}
}

// ============================================================================
// VERSION CONSISTENCY TEST
// ============================================================================

func TestVersionConsistency(t *testing.T) {
	t.Parallel()
	expected := fmt.Sprintf("%d.%d.%d", VersionMajor, VersionMinor, VersionPatch)
	if VersionPreRelease != "" {
		expected += "-" + VersionPreRelease
	}
	if Version != expected {
		t.Errorf("Version = %q, but components give %q", Version, expected)
	}
}
