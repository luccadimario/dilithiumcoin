package main

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// mockConn implements net.Conn for testing (all methods are no-ops)
type mockConn struct{}

func (m mockConn) Read(b []byte) (int, error)         { return 0, fmt.Errorf("mock") }
func (m mockConn) Write(b []byte) (int, error)        { return len(b), nil }
func (m mockConn) Close() error                        { return nil }
func (m mockConn) LocalAddr() net.Addr                 { return &net.TCPAddr{} }
func (m mockConn) RemoteAddr() net.Addr                { return &net.TCPAddr{} }
func (m mockConn) SetDeadline(t time.Time) error       { return nil }
func (m mockConn) SetReadDeadline(t time.Time) error   { return nil }
func (m mockConn) SetWriteDeadline(t time.Time) error  { return nil }

// newTestPeerManager creates a minimal PeerManager for testing
func newTestPeerManager(config *PeerConfig) *PeerManager {
	if config == nil {
		config = DefaultPeerConfig()
	}
	return &PeerManager{
		config: config,
		peers:  make(map[string]*Peer),
		banned: make(map[string]time.Time),
		stopCh: make(chan struct{}),
	}
}

// newTestPeer creates a Peer with a mock connection for testing
func newTestPeer(addr string, pm *PeerManager) *Peer {
	return &Peer{
		Addr:    addr,
		Conn:    mockConn{},
		State:   PeerStateActive,
		sendCh:  make(chan *P2PMessage, 100),
		stopCh:  make(chan struct{}),
		manager: pm,
	}
}

// ============================================================================
// MISBEHAVIOR SCORING TESTS
// ============================================================================

func TestAddMisbehaviorAccumulates(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("127.0.0.1:1234", pm)

	peer.AddMisbehavior(10, "test offense 1")

	peer.mutex.RLock()
	score := peer.misbehaviorScore
	peer.mutex.RUnlock()

	if score != 10 {
		t.Errorf("expected score 10, got %d", score)
	}

	peer.AddMisbehavior(20, "test offense 2")

	peer.mutex.RLock()
	score = peer.misbehaviorScore
	peer.mutex.RUnlock()

	if score != 30 {
		t.Errorf("expected score 30, got %d", score)
	}
}

func TestAddMisbehaviorNoBanBelowThreshold(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil) // BanThreshold = 100
	peer := newTestPeer("192.168.1.1:1234", pm)
	pm.peers[peer.Addr] = peer

	// Add 99 points — should NOT trigger ban
	peer.AddMisbehavior(99, "just under threshold")

	if pm.IsBanned("192.168.1.1") {
		t.Error("peer should NOT be banned at score 99 (threshold 100)")
	}
}

func TestAddMisbehaviorBansAtThreshold(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil) // BanThreshold = 100
	peer := newTestPeer("192.168.1.2:1234", pm)
	pm.peers[peer.Addr] = peer

	// Add exactly 100 points — should trigger ban
	peer.AddMisbehavior(100, "hit threshold")

	if !pm.IsBanned("192.168.1.2") {
		t.Error("peer should be banned at score 100")
	}
}

func TestAddMisbehaviorBansAboveThreshold(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("192.168.1.3:1234", pm)
	pm.peers[peer.Addr] = peer

	peer.AddMisbehavior(150, "way over threshold")

	if !pm.IsBanned("192.168.1.3") {
		t.Error("peer should be banned at score 150")
	}
}

// ============================================================================
// PENALTY CONSTANTS TESTS
// ============================================================================

func TestPenaltyInvalidBlockInstantBan(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.1:1234", pm)
	pm.peers[peer.Addr] = peer

	// A single invalid block should trigger instant ban (100 points)
	peer.AddMisbehavior(PenaltyInvalidBlock, "bad PoW")

	if !pm.IsBanned("10.0.0.1") {
		t.Error("invalid block (100 points) should cause instant ban")
	}
}

func TestPenaltyInvalidTxGradual(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.2:1234", pm)
	pm.peers[peer.Addr] = peer

	// Send 9 invalid transactions — should NOT ban (9 * 10 = 90 < 100)
	for i := 0; i < 9; i++ {
		peer.AddMisbehavior(PenaltyInvalidTx, fmt.Sprintf("bad tx %d", i+1))
	}

	if pm.IsBanned("10.0.0.2") {
		t.Error("9 invalid txs (90 points) should NOT trigger ban")
	}

	peer.mutex.RLock()
	score := peer.misbehaviorScore
	peer.mutex.RUnlock()
	if score != 90 {
		t.Errorf("expected score 90, got %d", score)
	}

	// 10th invalid tx should trigger ban (100 points)
	peer.AddMisbehavior(PenaltyInvalidTx, "bad tx 10")

	if !pm.IsBanned("10.0.0.2") {
		t.Error("10 invalid txs (100 points) should trigger ban")
	}
}

func TestPenaltyMessageDecodeGradual(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.3:1234", pm)
	pm.peers[peer.Addr] = peer

	// Send 99 decode errors — should NOT ban
	for i := 0; i < 99; i++ {
		peer.AddMisbehavior(PenaltyMessageDecode, "decode error")
	}

	if pm.IsBanned("10.0.0.3") {
		t.Error("99 decode errors (99 points) should NOT trigger ban")
	}

	// 100th decode error should trigger ban
	peer.AddMisbehavior(PenaltyMessageDecode, "decode error 100")

	if !pm.IsBanned("10.0.0.3") {
		t.Error("100 decode errors (100 points) should trigger ban")
	}
}

func TestPenaltySpamInstantBan(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.4:1234", pm)
	pm.peers[peer.Addr] = peer

	peer.AddMisbehavior(PenaltySpam, "message spam")

	if !pm.IsBanned("10.0.0.4") {
		t.Error("spam penalty (100 points) should cause instant ban")
	}
}

func TestMixedPenaltiesAccumulate(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.5:1234", pm)
	pm.peers[peer.Addr] = peer

	// 5 decode errors = 5 points
	for i := 0; i < 5; i++ {
		peer.AddMisbehavior(PenaltyMessageDecode, "decode error")
	}
	// 3 invalid txs = 30 points
	for i := 0; i < 3; i++ {
		peer.AddMisbehavior(PenaltyInvalidTx, "bad tx")
	}
	// Total: 35, should NOT ban
	if pm.IsBanned("10.0.0.5") {
		t.Error("35 points should NOT trigger ban")
	}

	peer.mutex.RLock()
	score := peer.misbehaviorScore
	peer.mutex.RUnlock()
	if score != 35 {
		t.Errorf("expected mixed score 35, got %d", score)
	}
}

// ============================================================================
// PEER CONFIG TESTS
// ============================================================================

func TestDefaultPeerConfigValues(t *testing.T) {
	t.Parallel()
	config := DefaultPeerConfig()

	if config.BanThreshold != 100 {
		t.Errorf("expected BanThreshold 100, got %d", config.BanThreshold)
	}
	if config.RateLimitThreshold != 1000 {
		t.Errorf("expected RateLimitThreshold 1000, got %d", config.RateLimitThreshold)
	}
	if config.BanDuration != 1*time.Hour {
		t.Errorf("expected BanDuration 1h, got %v", config.BanDuration)
	}
}

func TestCustomBanThreshold(t *testing.T) {
	t.Parallel()
	config := DefaultPeerConfig()
	config.BanThreshold = 50 // lower threshold

	pm := newTestPeerManager(config)
	peer := newTestPeer("10.0.0.6:1234", pm)
	pm.peers[peer.Addr] = peer

	// 5 invalid txs = 50 points, should ban with threshold 50
	for i := 0; i < 5; i++ {
		peer.AddMisbehavior(PenaltyInvalidTx, "bad tx")
	}

	if !pm.IsBanned("10.0.0.6") {
		t.Error("5 invalid txs (50 points) should ban with threshold 50")
	}
}

// ============================================================================
// BAN DURATION TESTS
// ============================================================================

func TestBanDurationOneHour(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil) // BanDuration = 1h
	peer := newTestPeer("10.0.0.7:1234", pm)
	pm.peers[peer.Addr] = peer

	peer.AddMisbehavior(PenaltyInvalidBlock, "bad block")

	if !pm.IsBanned("10.0.0.7") {
		t.Error("peer should be banned")
	}

	// Check the ban expiry is approximately 1 hour from now
	pm.bannedMutex.RLock()
	expiry := pm.banned["10.0.0.7"]
	pm.bannedMutex.RUnlock()

	expectedExpiry := time.Now().Add(1 * time.Hour)
	diff := expiry.Sub(expectedExpiry)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("ban expiry should be ~1h from now, got %v (diff: %v)", expiry, diff)
	}
}

// ============================================================================
// BLOCK ERROR TESTS
// ============================================================================

func TestBlockErrorType(t *testing.T) {
	t.Parallel()
	err := &BlockError{
		Reason:  "block #42 has invalid hash",
		Penalty: PenaltyInvalidBlock,
	}

	if err.Error() != "block #42 has invalid hash" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
	if err.Penalty != 100 {
		t.Errorf("expected penalty 100, got %d", err.Penalty)
	}
}

func TestBlockErrorTypeAssertion(t *testing.T) {
	t.Parallel()
	var err error = &BlockError{
		Reason:  "test error",
		Penalty: PenaltyInvalidBlock,
	}

	blockErr, ok := err.(*BlockError)
	if !ok {
		t.Fatal("should be able to type-assert *BlockError")
	}
	if blockErr.Penalty != 100 {
		t.Errorf("expected penalty 100, got %d", blockErr.Penalty)
	}
}

// ============================================================================
// AddBlock VALIDATION ERROR TESTS
// ============================================================================

func TestAddBlockReturnsNilForDuplicateBlock(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	node := &Node{Blockchain: bc}

	// Create a block with index 0 (same as genesis) — should be silently ignored
	block := &Block{Index: 0}
	err := node.AddBlock(block, "test-peer")

	if err != nil {
		t.Errorf("duplicate block should return nil, got: %v", err)
	}
}

func TestAddBlockReturnsNilForNonConnecting(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	node := &Node{Blockchain: bc}

	// Block with wrong PreviousHash — doesn't connect but NOT penalized
	block := &Block{
		Index:        1,
		PreviousHash: "wrong_hash",
		Timestamp:    time.Now().Unix(),
	}
	err := node.AddBlock(block, "test-peer")

	if err != nil {
		t.Errorf("non-connecting block should return nil (not penalized), got: %v", err)
	}
}

func TestAddBlockReturnsErrorForBadHash(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	node := &Node{Blockchain: bc}

	genesis := bc.Blocks[0]
	block := &Block{
		Index:          1,
		PreviousHash:   genesis.Hash,
		Timestamp:      genesis.Timestamp + 60,
		Hash:           "fake_hash_that_is_wrong",
		DifficultyBits: 1, // easy difficulty so PoW check might pass
		Difficulty:     1,
	}

	err := node.AddBlock(block, "test-peer")
	if err == nil {
		t.Fatal("block with wrong hash should return error")
	}

	blockErr, ok := err.(*BlockError)
	if !ok {
		t.Fatalf("expected *BlockError, got %T", err)
	}
	if blockErr.Penalty != PenaltyInvalidBlock {
		t.Errorf("expected penalty %d, got %d", PenaltyInvalidBlock, blockErr.Penalty)
	}
}

func TestAddBlockReturnsErrorForFutureTimestamp(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	node := &Node{Blockchain: bc}

	genesis := bc.Blocks[0]

	// Block with timestamp 3 hours in the future (limit is 2h)
	futureTime := time.Now().Unix() + 3*60*60
	block := &Block{
		Index:          1,
		PreviousHash:   genesis.Hash,
		Timestamp:      futureTime,
		DifficultyBits: 28,
		Nonce:          1,
	}
	block.Hash = block.CalculateHash()

	err := node.AddBlock(block, "test-peer")
	if err == nil {
		t.Fatal("block with future timestamp should return error")
	}

	blockErr, ok := err.(*BlockError)
	if !ok {
		t.Fatalf("expected *BlockError, got %T", err)
	}
	if blockErr.Penalty != PenaltyInvalidBlock {
		t.Errorf("expected penalty %d, got %d", PenaltyInvalidBlock, blockErr.Penalty)
	}
}

// ============================================================================
// REORG TESTS
// ============================================================================

// buildTestChain creates a chain of n blocks starting from genesis with given difficulty bits.
// Each block includes a valid coinbase transaction so ValidateBlockTransactions passes.
func buildTestChain(n int, diffBits int) []*Block {
	blocks := make([]*Block, 0, n)

	genesis := &Block{
		Index:          0,
		Timestamp:      1000,
		PreviousHash:   "0",
		Transactions:   []*Transaction{{From: "SYSTEM", To: "miner", Amount: GetBlockReward(0), Timestamp: 1000}},
		DifficultyBits: diffBits,
		Nonce:          0,
	}
	genesis.Hash = genesis.CalculateHash()
	blocks = append(blocks, genesis)

	for i := 1; i < n; i++ {
		prev := blocks[i-1]
		block := &Block{
			Index:          int64(i),
			Timestamp:      prev.Timestamp + 60,
			PreviousHash:   prev.Hash,
			Transactions:   []*Transaction{{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60}},
			DifficultyBits: diffBits,
			Nonce:          int64(i * 100),
		}
		block.Hash = block.CalculateHash()
		blocks = append(blocks, block)
	}

	return blocks
}

func TestAttemptReorgFindsCommonAncestor(t *testing.T) {
	t.Parallel()

	// Build two chains that share blocks 0-4 but diverge at block 5
	commonChain := buildTestChain(5, 0) // blocks 0-4

	// Our chain: common + 3 blocks (DifficultyBits 0 = legacy mode, bypasses PoW check in tests)
	ourChain := make([]*Block, len(commonChain))
	copy(ourChain, commonChain)
	for i := 5; i < 8; i++ {
		prev := ourChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Nonce: int64(i * 1000), // different nonce = different hash
		}
		block.Hash = block.CalculateHash()
		ourChain = append(ourChain, block)
	}

	// Peer chain: common + 5 blocks (more blocks = more cumulative work)
	peerChain := make([]*Block, len(commonChain))
	copy(peerChain, commonChain)
	for i := 5; i < 10; i++ {
		prev := peerChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Nonce: int64(i * 2000), // different nonce from our chain
		}
		block.Hash = block.CalculateHash()
		peerChain = append(peerChain, block)
	}

	// Verify chains diverge
	if ourChain[5].Hash == peerChain[5].Hash {
		t.Fatal("test chains should diverge at block 5")
	}

	// Set up blockchain with our chain
	bc := &Blockchain{Blocks: ourChain}
	node := &Node{Blockchain: bc}
	pm := newTestPeerManager(nil)
	pm.node = node
	peer := newTestPeer("10.0.0.1:1234", pm)
	pm.peers[peer.Addr] = peer
	peer.StartHeight = 10

	// Attempt reorg with peer's blocks from the fork point
	peerBlocks := peerChain[5:] // blocks 5-9

	pm.attemptReorg(peer, peerBlocks)

	// Should have switched to peer's chain (heavier: 5 blocks > 3 blocks at same diff)
	if len(bc.Blocks) != 10 {
		t.Errorf("expected chain length 10 after reorg, got %d", len(bc.Blocks))
	}

	// Verify the chain is the peer's chain
	if bc.Blocks[5].Hash != peerChain[5].Hash {
		t.Error("block 5 should be from peer's chain after reorg")
	}
	if bc.Blocks[9].Hash != peerChain[9].Hash {
		t.Error("block 9 should be from peer's chain after reorg")
	}
}

func TestAttemptReorgKeepsHeavierChain(t *testing.T) {
	t.Parallel()

	commonChain := buildTestChain(5, 0) // blocks 0-4

	// Our chain: common + 7 blocks (MORE blocks = heavier)
	ourChain := make([]*Block, len(commonChain))
	copy(ourChain, commonChain)
	for i := 5; i < 12; i++ {
		prev := ourChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Difficulty: 1,
			Nonce:      int64(i * 1000),
		}
		block.Hash = block.CalculateHash()
		ourChain = append(ourChain, block)
	}

	// Peer chain: common + 3 blocks (fewer blocks = lighter)
	peerChain := make([]*Block, len(commonChain))
	copy(peerChain, commonChain)
	for i := 5; i < 8; i++ {
		prev := peerChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Difficulty: 1,
			Nonce:      int64(i * 2000),
		}
		block.Hash = block.CalculateHash()
		peerChain = append(peerChain, block)
	}

	bc := &Blockchain{Blocks: ourChain}
	node := &Node{Blockchain: bc}
	pm := newTestPeerManager(nil)
	pm.node = node
	peer := newTestPeer("10.0.0.2:1234", pm)
	pm.peers[peer.Addr] = peer
	peer.StartHeight = 8

	origBlock5Hash := ourChain[5].Hash

	pm.attemptReorg(peer, peerChain[5:])

	// Should NOT have switched — our chain is heavier (7 blocks > 3 blocks)
	if len(bc.Blocks) != 12 {
		t.Errorf("chain length should stay 12, got %d", len(bc.Blocks))
	}
	if bc.Blocks[5].Hash != origBlock5Hash {
		t.Error("block 5 should still be from our chain (heavier)")
	}
}

func TestAttemptReorgRejectsInvalidHash(t *testing.T) {
	t.Parallel()

	commonChain := buildTestChain(5, 0)

	ourChain := make([]*Block, len(commonChain))
	copy(ourChain, commonChain)
	for i := 5; i < 7; i++ {
		prev := ourChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Difficulty: 1,
			Nonce:      int64(i * 1000),
		}
		block.Hash = block.CalculateHash()
		ourChain = append(ourChain, block)
	}

	// Peer chain with a bad hash on block 6
	peerChain := make([]*Block, len(commonChain))
	copy(peerChain, commonChain)
	for i := 5; i < 10; i++ {
		prev := peerChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Difficulty: 1,
			Nonce:      int64(i * 2000),
		}
		block.Hash = block.CalculateHash()
		peerChain = append(peerChain, block)
	}

	// Tamper with block 6's hash
	peerChain[6].Hash = "tampered_hash_value"

	bc := &Blockchain{Blocks: ourChain}
	node := &Node{Blockchain: bc}
	pm := newTestPeerManager(nil)
	pm.node = node
	peer := newTestPeer("10.0.0.3:1234", pm)
	pm.peers[peer.Addr] = peer
	peer.StartHeight = 10

	origLen := len(bc.Blocks)
	pm.attemptReorg(peer, peerChain[5:])

	// Should NOT have reorged — peer block has invalid hash
	if len(bc.Blocks) != origLen {
		t.Errorf("chain length should stay %d after rejected reorg, got %d", origLen, len(bc.Blocks))
	}

	// Peer should be penalized
	if !pm.IsBanned("10.0.0.3") {
		t.Error("peer should be banned for sending block with invalid hash in reorg")
	}
}

func TestAttemptReorgRejectsFakeHighDifficulty(t *testing.T) {
	t.Parallel()

	commonChain := buildTestChain(5, 0)

	// Our chain: common + 3 blocks
	ourChain := make([]*Block, len(commonChain))
	copy(ourChain, commonChain)
	for i := 5; i < 8; i++ {
		prev := ourChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Difficulty: 1,
			Nonce:      int64(i * 1000),
		}
		block.Hash = block.CalculateHash()
		ourChain = append(ourChain, block)
	}

	// Attacker chain: claims DifficultyBits: 100 but hash doesn't meet it
	// This simulates the exact attack the user is worried about
	attackChain := make([]*Block, len(commonChain))
	copy(attackChain, commonChain)
	for i := 5; i < 8; i++ {
		prev := attackChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			DifficultyBits: 100, // Claims extremely high difficulty
			Nonce:          int64(i * 3000),
		}
		block.Hash = block.CalculateHash() // Hash won't actually have 100 leading zero bits
		attackChain = append(attackChain, block)
	}

	bc := &Blockchain{Blocks: ourChain}
	node := &Node{Blockchain: bc}
	pm := newTestPeerManager(nil)
	pm.node = node
	peer := newTestPeer("10.0.0.7:1234", pm)
	pm.peers[peer.Addr] = peer
	peer.StartHeight = 8

	origLen := len(bc.Blocks)
	pm.attemptReorg(peer, attackChain[5:])

	// Should NOT reorg — attacker's blocks claim high difficulty but hash doesn't meet it
	if len(bc.Blocks) != origLen {
		t.Errorf("chain length should stay %d after fake difficulty reorg, got %d", origLen, len(bc.Blocks))
	}

	// Attacker should be banned
	if !pm.IsBanned("10.0.0.7") {
		t.Error("peer should be banned for claiming high difficulty without meeting PoW target")
	}
}

// ============================================================================
// handleBlocks LOOP PREVENTION TEST
// ============================================================================

func TestHandleBlocksNoLoopOnFork(t *testing.T) {
	t.Parallel()

	commonChain := buildTestChain(5, 0)

	// Our chain
	ourChain := make([]*Block, len(commonChain))
	copy(ourChain, commonChain)
	for i := 5; i < 8; i++ {
		prev := ourChain[i-1]
		block := &Block{
			Index:        int64(i),
			Timestamp:    prev.Timestamp + 60,
			PreviousHash: prev.Hash,
			Transactions: []*Transaction{
				{From: "SYSTEM", To: "miner", Amount: GetBlockReward(int64(i)), Timestamp: prev.Timestamp + 60},
			},
			Difficulty: 1,
			Nonce:      int64(i * 1000),
		}
		block.Hash = block.CalculateHash()
		ourChain = append(ourChain, block)
	}

	bc := &Blockchain{
		Blocks: ourChain,
		mutex:  sync.RWMutex{},
	}
	node := &Node{Blockchain: bc}
	pm := newTestPeerManager(nil)
	pm.node = node
	peer := newTestPeer("10.0.0.4:1234", pm)
	pm.peers[peer.Addr] = peer
	peer.StartHeight = 10 // Peer claims higher height

	// Create blocks that don't connect (from a different fork)
	badBlocks := make([]*Block, 3)
	for i := 0; i < 3; i++ {
		badBlocks[i] = &Block{
			Index:        int64(8 + i),
			PreviousHash: "completely_wrong_hash",
			Timestamp:    commonChain[4].Timestamp + int64(60*(i+4)),
			Nonce: int64(i * 9999),
		}
		badBlocks[i].Hash = badBlocks[i].CalculateHash()
	}

	origHeight := len(bc.Blocks)

	// handleBlocks should detect fork and NOT re-request endlessly
	pm.handleBlocks(peer, &BlocksMsg{Blocks: badBlocks})

	// Chain should be unchanged (reorg should fail since no common ancestor)
	if len(bc.Blocks) != origHeight {
		t.Errorf("chain length should stay %d, got %d", origHeight, len(bc.Blocks))
	}
}

// ============================================================================
// CONCURRENCY SAFETY TEST
// ============================================================================

func TestAddMisbehaviorConcurrent(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.5:1234", pm)
	pm.peers[peer.Addr] = peer

	var wg sync.WaitGroup
	// 50 goroutines each adding 1 point — total should be 50
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			peer.AddMisbehavior(1, fmt.Sprintf("concurrent test %d", n))
		}(i)
	}
	wg.Wait()

	peer.mutex.RLock()
	score := peer.misbehaviorScore
	peer.mutex.RUnlock()

	if score != 50 {
		t.Errorf("expected score 50 after concurrent adds, got %d", score)
	}

	// Should NOT be banned (50 < 100)
	if pm.IsBanned("10.0.0.5") {
		t.Error("peer should NOT be banned at score 50")
	}
}

func TestAddMisbehaviorConcurrentBan(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.6:1234", pm)
	pm.peers[peer.Addr] = peer

	var wg sync.WaitGroup
	// 10 goroutines each adding PenaltyInvalidTx (10) = total 100 → ban
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			peer.AddMisbehavior(PenaltyInvalidTx, fmt.Sprintf("concurrent tx %d", n))
		}(i)
	}
	wg.Wait()

	if !pm.IsBanned("10.0.0.6") {
		t.Error("peer should be banned after 10 concurrent invalid txs (100 points)")
	}
}

// ============================================================================
// EDGE CASE TESTS
// ============================================================================

func TestNewPeerStartsWithZeroScore(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer := newTestPeer("10.0.0.7:1234", pm)

	peer.mutex.RLock()
	score := peer.misbehaviorScore
	peer.mutex.RUnlock()

	if score != 0 {
		t.Errorf("new peer should have score 0, got %d", score)
	}
}

func TestMultiplePeersIndependentScores(t *testing.T) {
	t.Parallel()
	pm := newTestPeerManager(nil)
	peer1 := newTestPeer("10.0.0.8:1234", pm)
	peer2 := newTestPeer("10.0.0.9:1234", pm)
	pm.peers[peer1.Addr] = peer1
	pm.peers[peer2.Addr] = peer2

	// Penalize peer1 heavily
	peer1.AddMisbehavior(PenaltyInvalidBlock, "bad block")

	// Peer1 should be banned, peer2 should NOT
	if !pm.IsBanned("10.0.0.8") {
		t.Error("peer1 should be banned")
	}
	if pm.IsBanned("10.0.0.9") {
		t.Error("peer2 should NOT be banned (independent scores)")
	}

	peer2.mutex.RLock()
	score2 := peer2.misbehaviorScore
	peer2.mutex.RUnlock()
	if score2 != 0 {
		t.Errorf("peer2 score should be 0, got %d", score2)
	}
}

func TestPenaltyConstantsAreCorrect(t *testing.T) {
	t.Parallel()
	if PenaltyInvalidBlock != 100 {
		t.Errorf("PenaltyInvalidBlock should be 100, got %d", PenaltyInvalidBlock)
	}
	if PenaltyInvalidTx != 10 {
		t.Errorf("PenaltyInvalidTx should be 10, got %d", PenaltyInvalidTx)
	}
	if PenaltyMessageDecode != 1 {
		t.Errorf("PenaltyMessageDecode should be 1, got %d", PenaltyMessageDecode)
	}
	if PenaltySpam != 100 {
		t.Errorf("PenaltySpam should be 100, got %d", PenaltySpam)
	}
}

// ============================================================================
// RATE LIMIT THRESHOLD TEST
// ============================================================================

func TestRateLimitThresholdIsGenerous(t *testing.T) {
	t.Parallel()
	config := DefaultPeerConfig()

	// 500 blocks in a sync batch should be well under the limit
	// Each block is ~1 message, so 500 messages should be fine
	if config.RateLimitThreshold < 500 {
		t.Errorf("RateLimitThreshold (%d) is too low for sync batches of 500 blocks",
			config.RateLimitThreshold)
	}

	// Should be at least 1000 to give headroom
	if config.RateLimitThreshold != 1000 {
		t.Errorf("expected RateLimitThreshold 1000, got %d", config.RateLimitThreshold)
	}
}
