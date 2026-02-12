package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

// Difficulty adjustment constants
const (
	// BlocksPerAdjustment is how often difficulty adjusts (like Bitcoin's 2016)
	BlocksPerAdjustment = 50

	// TargetBlockTime is the desired time between blocks in seconds
	TargetBlockTime = 60 // 1 minute per block

	// MinDifficulty is the minimum allowed difficulty (hex digits, legacy)
	MinDifficulty = 4

	// MaxDifficulty is the maximum allowed difficulty (hex digits, legacy)
	MaxDifficulty = 20

	// MinDifficultyBits is the minimum bit-based difficulty (= MinDifficulty * 4)
	MinDifficultyBits = 16

	// MaxDifficultyBits is the maximum bit-based difficulty (= MaxDifficulty * 4)
	MaxDifficultyBits = 80

	// MaxAdjustmentFactor limits how much difficulty can change per adjustment
	MaxAdjustmentFactor = 4.0

	// ============================================================================
	// SUPPLY CONTROL (Bitcoin-like halving)
	// ============================================================================

	// HalvingInterval is how many blocks between reward halvings
	// Bitcoin: 210,000 blocks (~4 years at 10min/block)
	// Dilithium: 250,000 blocks (~174 days at 1min/block)
	HalvingInterval = 250000
)

// Supply constants using int64 base units
var (
	// InitialBlockReward is the starting mining reward (like Bitcoin's 50 BTC)
	InitialBlockReward = int64(50 * DLTUnit)

	// MaxSupply is the theoretical maximum coins (sum of geometric series)
	// = InitialReward * HalvingInterval * 2 = 50 * 250,000 * 2 = 25,000,000
	MaxSupply = int64(25_000_000 * DLTUnit)

	// MinBlockReward is the smallest reward before it becomes zero
	MinBlockReward = int64(1) // 1 base unit
)

// ============================================================================
// BLOCK
// ============================================================================

// Block represents a single block in the blockchain
type Block struct {
	Index          int64
	Timestamp      int64
	Transactions   []*Transaction `json:"transactions"`
	PreviousHash   string
	Hash           string
	Nonce          int64
	Difficulty     int `json:"Difficulty"`                    // Leading zero hex digits (backward compat)
	DifficultyBits int `json:"DifficultyBits,omitempty"` // Bit-precise difficulty (soft fork)
}

// CalculateHash creates a SHA-256 hash of the block
func (b *Block) CalculateHash() string {
	txJSON, _ := json.Marshal(b.Transactions)

	blockData := strconv.FormatInt(b.Index, 10) +
		strconv.FormatInt(b.Timestamp, 10) +
		string(txJSON) +
		b.PreviousHash +
		strconv.FormatInt(b.Nonce, 10) +
		strconv.Itoa(b.Difficulty)

	hash := sha256.Sum256([]byte(blockData))
	return hex.EncodeToString(hash[:])
}

// MineBlock performs proof of work
func (b *Block) MineBlock(difficulty int) bool {
	return b.MineBlockWithCancel(difficulty, nil)
}

// MineBlockWithCancel performs proof of work with cancellation support
// Uses bit-based difficulty if DifficultyBits > 0, otherwise hex-digit difficulty
func (b *Block) MineBlockWithCancel(difficulty int, cancel <-chan struct{}) bool {
	useBits := b.DifficultyBits > 0
	fmt.Printf("Mining block %d (difficulty: %d", b.Index, difficulty)
	if useBits {
		fmt.Printf(", bits: %d", b.DifficultyBits)
	}
	fmt.Println(")...")
	start := time.Now()

	b.Hash = b.CalculateHash()

	for {
		var meets bool
		if useBits {
			meets = meetsDifficultyBits(b.Hash, b.DifficultyBits)
		} else {
			target := createTarget(difficulty)
			meets = len(b.Hash) >= difficulty && b.Hash[:difficulty] == target
		}

		if meets {
			break
		}

		// Check for cancellation every 10000 hashes for performance
		if cancel != nil && b.Nonce%10000 == 0 {
			select {
			case <-cancel:
				fmt.Printf("Mining block %d cancelled after %v\n", b.Index, time.Since(start))
				return false
			default:
			}
		}

		b.Nonce++
		b.Hash = b.CalculateHash()
	}

	elapsed := time.Since(start)
	fmt.Printf("Block %d mined! Hash: %s (took %v)\n", b.Index, b.Hash, elapsed)
	return true
}

// ============================================================================
// BLOCKCHAIN
// ============================================================================

// Blockchain represents the entire chain with pending transactions
type Blockchain struct {
	Blocks              []*Block
	Difficulty          int // Legacy hex-digit difficulty
	DifficultyBits      int // Current bit-based difficulty
	PendingTransactions []*Transaction
	Mempool             map[string]*Transaction
	mutex               sync.RWMutex
	lastAdjustmentHeight int   // Cache: last height we computed adjustment
	lastAdjustmentBits   int   // Cache: result of that computation
}

// NewBlockchain initializes a new blockchain with genesis block
func NewBlockchain(difficulty int) *Blockchain {
	return &Blockchain{
		Blocks:              []*Block{createGenesisBlock(difficulty)},
		Difficulty:          difficulty,
		DifficultyBits:      hexDigitsToDifficultyBits(difficulty),
		PendingTransactions: make([]*Transaction, 0),
		Mempool:             make(map[string]*Transaction),
	}
}

// AddTransaction adds a transaction to the mempool
func (bc *Blockchain) AddTransaction(tx *Transaction) error {
	_, err := bc.AddTransactionIfNew(tx)
	return err
}

// AddTransactionIfNew adds a transaction to the mempool and returns true if it was new
func (bc *Blockchain) AddTransactionIfNew(tx *Transaction) (bool, error) {
	if err := validateTransaction(tx); err != nil {
		return false, err
	}

	bc.mutex.Lock()
	defer bc.mutex.Unlock()

	// Deduplicate by signature
	if _, exists := bc.Mempool[tx.Signature]; exists {
		return false, nil // Already in mempool
	}

	// Skip balance check for SYSTEM transactions (mining rewards)
	if tx.From != "SYSTEM" {
		// Calculate available balance (confirmed - pending outgoing)
		availableBalance := bc.getBalanceLocked(tx.From)

		// Subtract pending outgoing transactions from the same address
		for _, pendingTx := range bc.PendingTransactions {
			if pendingTx.From == tx.From {
				availableBalance -= pendingTx.Amount
			}
		}

		// Check if sender has sufficient funds
		if availableBalance < tx.Amount {
			return false, fmt.Errorf("insufficient funds: address %s has %s available, needs %s",
				tx.From, FormatDLT(availableBalance), FormatDLT(tx.Amount))
		}
	}

	bc.Mempool[tx.Signature] = tx
	bc.PendingTransactions = append(bc.PendingTransactions, tx)

	return true, nil
}

// MinePendingTransactions mines all pending transactions into a new block
func (bc *Blockchain) MinePendingTransactions(minerAddress string) *Block {
	block, _ := bc.MinePendingTransactionsWithCancel(minerAddress, nil)
	return block
}

// MinePendingTransactionsWithCancel mines pending transactions with cancellation support
// Returns the mined block and true if successful, nil and false if cancelled
// NOTE: Can mine empty blocks (just coinbase reward) - this is how new coins enter circulation
func (bc *Blockchain) MinePendingTransactionsWithCancel(minerAddress string, cancel <-chan struct{}) (*Block, bool) {
	bc.mutex.Lock()

	// Calculate block reward based on height (implements halving)
	nextBlockHeight := bc.Blocks[len(bc.Blocks)-1].Index + 1
	blockReward := GetBlockReward(nextBlockHeight)

	// Create coinbase (mining reward) transaction
	// This is how new coins are created - miners get rewarded for securing the network
	rewardTx := &Transaction{
		From:      "SYSTEM",
		To:        minerAddress,
		Amount:    blockReward,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", nextBlockHeight, time.Now().UnixNano()),
	}

	// Start with coinbase, then add any pending transactions
	txToMine := []*Transaction{rewardTx}
	txToMine = append(txToMine, bc.PendingTransactions...)

	// Get current difficulty in bits (may have adjusted)
	diffBits := bc.GetCurrentDifficultyBitsLocked()
	diffHex := difficultyBitsToHexDigits(diffBits)

	// Create new block with pending transactions
	previousBlock := bc.Blocks[len(bc.Blocks)-1]
	newBlock := &Block{
		Index:          previousBlock.Index + 1,
		Timestamp:      time.Now().Unix(),
		Transactions:   txToMine,
		PreviousHash:   previousBlock.Hash,
		Nonce:          0,
		Difficulty:     diffHex,
		DifficultyBits: diffBits,
	}

	bc.mutex.Unlock()

	// Mine the block (this can be cancelled)
	if !newBlock.MineBlockWithCancel(diffHex, cancel) {
		return nil, false
	}

	// Mining succeeded - add block to chain
	bc.mutex.Lock()
	defer bc.mutex.Unlock()

	// Verify the block still connects to our chain (another block might have been added)
	currentLastBlock := bc.Blocks[len(bc.Blocks)-1]
	if newBlock.PreviousHash != currentLastBlock.Hash {
		fmt.Printf("Block %d orphaned - chain moved on\n", newBlock.Index)
		return nil, false
	}

	bc.Blocks = append(bc.Blocks, newBlock)

	// Clear mined transactions from mempool
	bc.clearMinedTransactions(newBlock.Transactions)

	return newBlock, true
}

// GetPendingCount returns the number of pending transactions
func (bc *Blockchain) GetPendingCount() int {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return len(bc.PendingTransactions)
}

// GetLastBlock returns the last block in the chain
func (bc *Blockchain) GetLastBlock() *Block {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return bc.Blocks[len(bc.Blocks)-1]
}

// GetBlocks returns a copy of all blocks
func (bc *Blockchain) GetBlocks() []*Block {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	blocks := make([]*Block, len(bc.Blocks))
	copy(blocks, bc.Blocks)
	return blocks
}

// GetBlockCount returns the number of blocks
func (bc *Blockchain) GetBlockCount() int64 {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return int64(len(bc.Blocks))
}

// HasTransaction checks if a transaction exists in the mempool
func (bc *Blockchain) HasTransaction(signature string) bool {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	_, exists := bc.Mempool[signature]
	return exists
}

// GetTransaction returns a transaction from the mempool by signature
func (bc *Blockchain) GetTransaction(signature string) *Transaction {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return bc.Mempool[signature]
}

// GetPendingTransactions returns a copy of all pending transactions
func (bc *Blockchain) GetPendingTransactions() []*Transaction {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	txs := make([]*Transaction, len(bc.PendingTransactions))
	copy(txs, bc.PendingTransactions)
	return txs
}

// HasBlock checks if a block with the given hash exists
func (bc *Blockchain) HasBlock(hash string) bool {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	for _, block := range bc.Blocks {
		if block.Hash == hash {
			return true
		}
	}
	return false
}

// GetBlock returns a block by its hash
func (bc *Blockchain) GetBlock(hash string) *Block {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	for _, block := range bc.Blocks {
		if block.Hash == hash {
			return block
		}
	}
	return nil
}

// GetBlockByIndex returns a block by its index
func (bc *Blockchain) GetBlockByIndex(index int64) *Block {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	if index >= 0 && index < int64(len(bc.Blocks)) {
		return bc.Blocks[index]
	}
	return nil
}

// GetCurrentDifficulty returns the legacy hex-digit difficulty for the next block
func (bc *Blockchain) GetCurrentDifficulty() int {
	return difficultyBitsToHexDigits(bc.GetCurrentDifficultyBits())
}

// GetCurrentDifficultyBits returns the bit-based difficulty for the next block
func (bc *Blockchain) GetCurrentDifficultyBits() int {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return bc.GetCurrentDifficultyBitsLocked()
}

// GetCurrentDifficultyLocked returns legacy hex-digit difficulty (assumes lock held)
func (bc *Blockchain) GetCurrentDifficultyLocked() int {
	return difficultyBitsToHexDigits(bc.GetCurrentDifficultyBitsLocked())
}

// GetCurrentDifficultyBitsLocked returns bit-based difficulty (assumes lock held)
func (bc *Blockchain) GetCurrentDifficultyBitsLocked() int {
	height := len(bc.Blocks)

	// Use initial difficulty until we have enough blocks
	if height < BlocksPerAdjustment {
		return bc.DifficultyBits
	}

	// Check if we're at an adjustment boundary
	if height%BlocksPerAdjustment != 0 {
		// Not at adjustment boundary — use blockchain's tracked difficulty
		return bc.DifficultyBits
	}

	// Use cached result if we already calculated for this height
	if bc.lastAdjustmentHeight == height {
		return bc.lastAdjustmentBits
	}

	// Calculate difficulty adjustment, cache it, and update blockchain state
	bits := bc.calculateNewDifficultyBits()
	bc.lastAdjustmentHeight = height
	bc.lastAdjustmentBits = bits
	bc.DifficultyBits = bits
	bc.Difficulty = difficultyBitsToHexDigits(bits)
	return bits
}

// calculateNewDifficultyBits computes new bit-based difficulty from recent block times
// Each bit step = 2x difficulty change (vs 16x per hex digit). This allows fine-grained
// convergence to the target block time.
func (bc *Blockchain) calculateNewDifficultyBits() int {
	height := len(bc.Blocks)

	// Get the block at the start of the adjustment period
	startBlock := bc.Blocks[height-BlocksPerAdjustment]
	endBlock := bc.Blocks[height-1]

	// Calculate actual time for the last adjustment period
	actualTime := endBlock.Timestamp - startBlock.Timestamp
	if actualTime <= 0 {
		actualTime = 1
	}

	// Expected time for the period
	expectedTime := int64(BlocksPerAdjustment * TargetBlockTime)

	// Current difficulty in bits (from blockchain state, not from blocks)
	// Legacy v3.0.1 blocks don't set DifficultyBits, so reading from blocks
	// would always return the initial value. Use blockchain's tracked state.
	currentBits := bc.DifficultyBits

	// Calculate adjustment ratio
	ratio := float64(expectedTime) / float64(actualTime)

	// Clamp ratio to prevent extreme adjustments
	if ratio > MaxAdjustmentFactor {
		ratio = MaxAdjustmentFactor
	} else if ratio < 1.0/MaxAdjustmentFactor {
		ratio = 1.0 / MaxAdjustmentFactor
	}

	// Use logarithmic scaling: each bit = 2x difficulty
	// log2(ratio) gives exact number of bits to adjust
	// ratio=2 → +1 bit, ratio=4 → +2 bits, ratio=0.5 → -1 bit
	var adjustment int
	logRatio := math.Log2(ratio)
	if math.Abs(logRatio) < 0.25 {
		// Within ~19% of target — no adjustment needed
		adjustment = 0
	} else {
		// Round to nearest integer bit adjustment
		adjustment = int(math.Round(logRatio))
	}

	newBits := currentBits + adjustment

	// Clamp to min/max
	if newBits < MinDifficultyBits {
		newBits = MinDifficultyBits
	} else if newBits > MaxDifficultyBits {
		newBits = MaxDifficultyBits
	}

	// Log the adjustment
	avgBlockTime := float64(actualTime) / float64(BlocksPerAdjustment)
	if newBits != currentBits {
		fmt.Printf("=== DIFFICULTY ADJUSTMENT ===\n")
		fmt.Printf("  Block height: %d\n", height)
		fmt.Printf("  Actual time for %d blocks: %ds (avg %.1fs/block)\n",
			BlocksPerAdjustment, actualTime, avgBlockTime)
		fmt.Printf("  Expected time: %ds (target %ds/block)\n",
			expectedTime, TargetBlockTime)
		fmt.Printf("  Ratio: %.2f (log2: %.2f)\n", ratio, logRatio)
		fmt.Printf("  DifficultyBits: %d -> %d (hex digits: %d -> %d)\n",
			currentBits, newBits,
			difficultyBitsToHexDigits(currentBits), difficultyBitsToHexDigits(newBits))
		fmt.Printf("=============================\n")
	} else {
		fmt.Printf("Difficulty check at height %d: no change (bits=%d, avg=%.1fs/block)\n",
			height, currentBits, avgBlockTime)
	}

	// Update the blockchain's tracked difficulty
	bc.DifficultyBits = newBits

	return newBits
}

// recalcDifficultyFromChain sets DifficultyBits based on the last adjustment
// period in the synced chain. We can't replay all periods retroactively because
// the existing blocks were mined at their actual difficulty — we just need to
// compute what difficulty the NEXT block should be at based on recent block times.
func (bc *Blockchain) recalcDifficultyFromChain() {
	height := len(bc.Blocks)
	if height < BlocksPerAdjustment {
		return
	}

	// Use the last block's effective difficulty as baseline
	lastBlock := bc.Blocks[height-1]
	currentBits := lastBlock.getEffectiveDifficultyBits()
	if currentBits == 0 {
		currentBits = hexDigitsToDifficultyBits(bc.Difficulty)
	}

	// Find the last complete adjustment period
	lastBoundary := (height / BlocksPerAdjustment) * BlocksPerAdjustment
	if lastBoundary < BlocksPerAdjustment {
		bc.DifficultyBits = currentBits
		return
	}

	// Calculate adjustment from the last period's block times
	startBlock := bc.Blocks[lastBoundary-BlocksPerAdjustment]
	endBlock := bc.Blocks[lastBoundary-1]

	actualTime := endBlock.Timestamp - startBlock.Timestamp
	if actualTime <= 0 {
		actualTime = 1
	}
	expectedTime := int64(BlocksPerAdjustment * TargetBlockTime)

	ratio := float64(expectedTime) / float64(actualTime)
	if ratio > MaxAdjustmentFactor {
		ratio = MaxAdjustmentFactor
	} else if ratio < 1.0/MaxAdjustmentFactor {
		ratio = 1.0 / MaxAdjustmentFactor
	}

	logRatio := math.Log2(ratio)
	var adjustment int
	if math.Abs(logRatio) < 0.25 {
		adjustment = 0
	} else {
		adjustment = int(math.Round(logRatio))
	}

	newBits := currentBits + adjustment
	if newBits < MinDifficultyBits {
		newBits = MinDifficultyBits
	} else if newBits > MaxDifficultyBits {
		newBits = MaxDifficultyBits
	}

	bc.DifficultyBits = newBits
	bc.Difficulty = difficultyBitsToHexDigits(newBits)
	bc.lastAdjustmentHeight = 0 // Reset cache
	fmt.Printf("Recalculated difficulty from chain: %d bits (hex %d) at height %d\n",
		newBits, bc.Difficulty, height)
}

// IsValid validates the entire blockchain
func (bc *Blockchain) IsValid() bool {
	for i := 1; i < len(bc.Blocks); i++ {
		if !bc.isBlockValid(bc.Blocks[i], bc.Blocks[i-1]) {
			return false
		}
	}
	return true
}

// isBlockValid validates a single block against its predecessor
func (bc *Blockchain) isBlockValid(currentBlock, previousBlock *Block) bool {
	// Verify current block's hash
	if currentBlock.Hash != currentBlock.CalculateHash() {
		fmt.Printf("Block %d has invalid hash\n", currentBlock.Index)
		return false
	}

	// Verify previous hash matches
	if currentBlock.PreviousHash != previousBlock.Hash {
		fmt.Printf("Block %d has invalid previous hash\n", currentBlock.Index)
		return false
	}

	// Verify proof of work
	if currentBlock.DifficultyBits > 0 {
		// Bit-based validation
		if !meetsDifficultyBits(currentBlock.Hash, currentBlock.DifficultyBits) {
			fmt.Printf("Block %d has invalid bit-based proof of work (bits=%d)\n",
				currentBlock.Index, currentBlock.DifficultyBits)
			return false
		}
	} else {
		// Legacy hex-digit validation
		blockDifficulty := currentBlock.Difficulty
		if blockDifficulty == 0 {
			blockDifficulty = bc.Difficulty
		}
		target := createTarget(blockDifficulty)
		if len(currentBlock.Hash) < blockDifficulty || currentBlock.Hash[:blockDifficulty] != target {
			fmt.Printf("Block %d has invalid proof of work\n", currentBlock.Index)
			return false
		}
	}

	return true
}

// PrintBlockchain displays all blocks in the chain
func (bc *Blockchain) PrintBlockchain() {
	fmt.Println("\n========== BLOCKCHAIN ==========")
	for _, block := range bc.Blocks {
		bc.printBlock(block)
	}
	fmt.Println("================================")
}

// printBlock displays a single block's information
func (bc *Blockchain) printBlock(block *Block) {
	fmt.Printf("\nBlock #%d\n", block.Index)
	fmt.Printf("Timestamp: %d\n", block.Timestamp)
	fmt.Printf("Transactions: %d\n", len(block.Transactions))

	for j, tx := range block.Transactions {
		fromDisplay := tx.From
		toDisplay := tx.To
		if len(tx.From) > 16 {
			fromDisplay = tx.From[:8] + "..." + tx.From[len(tx.From)-8:]
		}
		if len(tx.To) > 16 {
			toDisplay = tx.To[:8] + "..." + tx.To[len(tx.To)-8:]
		}
		fmt.Printf("  TX %d: %s -> %s: %s DLT\n", j, fromDisplay, toDisplay, FormatDLT(tx.Amount))
	}

	fmt.Printf("Previous Hash: %s\n", block.PreviousHash)
	fmt.Printf("Hash: %s\n", block.Hash)
	fmt.Printf("Nonce: %d\n", block.Nonce)
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// InitialDifficulty is the starting difficulty for the network
const InitialDifficulty = 6

// Dilithium Genesis Block - Mined once, hardcoded forever
// This is the root of the entire blockchain - all nodes must have this exact block
var GenesisBlock = &Block{
	Index:        0,
	Timestamp:    1738368000, // Feb 1, 2025 00:00:00 UTC - Dilithium launch
	Transactions: []*Transaction{},
	PreviousHash: "0",
	Difficulty:   InitialDifficulty,
	Nonce:        5892535,
	Hash:         "0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae",
}

// createGenesisBlock returns the hardcoded genesis block
// The difficulty parameter is ignored - genesis block is pre-mined
func createGenesisBlock(difficulty int) *Block {
	// Verify the genesis block hash is correct
	calculatedHash := GenesisBlock.CalculateHash()
	if GenesisBlock.Hash != calculatedHash {
		// Genesis block needs to be mined (first time setup)
		fmt.Println("Mining genesis block (one-time setup)...")
		genesis := &Block{
			Index:        0,
			Timestamp:    1738368000,
			Transactions: make([]*Transaction, 0),
			PreviousHash: "0",
			Difficulty:   InitialDifficulty,
			Nonce:        0,
		}
		// Mine with initial difficulty
		target := createTarget(InitialDifficulty)
		for {
			genesis.Hash = genesis.CalculateHash()
			if len(genesis.Hash) >= InitialDifficulty && genesis.Hash[:InitialDifficulty] == target {
				break
			}
			genesis.Nonce++
		}
		fmt.Printf("\n=== GENESIS BLOCK MINED ===\n")
		fmt.Printf("Difficulty: %d\n", InitialDifficulty)
		fmt.Printf("Nonce: %d\n", genesis.Nonce)
		fmt.Printf("Hash:  %s\n", genesis.Hash)
		fmt.Printf("Update GenesisBlock in blockchain.go with these values!\n")
		fmt.Printf("===========================\n\n")
		return genesis
	}

	// Return a copy of the genesis block
	return &Block{
		Index:        GenesisBlock.Index,
		Timestamp:    GenesisBlock.Timestamp,
		Transactions: GenesisBlock.Transactions,
		PreviousHash: GenesisBlock.PreviousHash,
		Difficulty:   GenesisBlock.Difficulty,
		Nonce:        GenesisBlock.Nonce,
		Hash:         GenesisBlock.Hash,
	}
}

// meetsDifficultyBits checks if a hash meets the required number of leading zero BITS.
// Each bit doubles the difficulty (vs 16x per hex digit). This gives fine-grained control.
func meetsDifficultyBits(hash string, bits int) bool {
	if bits <= 0 {
		return true
	}
	hashBytes, err := hex.DecodeString(hash)
	if err != nil || len(hashBytes) < (bits+7)/8 {
		return false
	}
	// Check full zero bytes
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
		if hashBytes[i] != 0 {
			return false
		}
	}
	// Check remaining bits in the next byte
	remainingBits := bits % 8
	if remainingBits > 0 {
		mask := byte(0xFF) << uint(8-remainingBits)
		if hashBytes[fullBytes]&mask != 0 {
			return false
		}
	}
	return true
}

// difficultyBitsToHexDigits converts bit-based difficulty to hex-digit difficulty
func difficultyBitsToHexDigits(bits int) int {
	return bits / 4
}

// hexDigitsToDifficultyBits converts hex-digit difficulty to bit-based difficulty
func hexDigitsToDifficultyBits(hexDigits int) int {
	return hexDigits * 4
}

// getEffectiveDifficultyBits returns the bit-based difficulty for a block
// For legacy blocks (DifficultyBits == 0), converts from hex-digit Difficulty
func (b *Block) getEffectiveDifficultyBits() int {
	if b.DifficultyBits > 0 {
		return b.DifficultyBits
	}
	return hexDigitsToDifficultyBits(b.Difficulty)
}

// createTarget creates a target string with the required number of leading zeros
func createTarget(difficulty int) string {
	target := ""
	for i := 0; i < difficulty; i++ {
		target += "0"
	}
	return target
}

// ============================================================================
// SIGNATURE VERIFICATION
// ============================================================================

// VerifyTransactionSignature verifies the CRYSTALS-Dilithium signature on a transaction
func VerifyTransactionSignature(tx *Transaction) error {
	if tx.PublicKey == "" {
		return fmt.Errorf("transaction missing public key")
	}

	// Decode hex-encoded public key
	pubKeyBytes, err := hex.DecodeString(tx.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to decode public key hex: %w", err)
	}

	var pk mode3.PublicKey
	if err := pk.UnmarshalBinary(pubKeyBytes); err != nil {
		return fmt.Errorf("failed to unmarshal Dilithium public key: %w", err)
	}

	// Recreate the signed data with chain ID for replay protection (shannon #11)
	// Try new format first, fall back to legacy for pre-upgrade transactions
	txData := fmt.Sprintf("%s:%s%s%d%d", NetworkName, tx.From, tx.To, tx.Amount, tx.Timestamp)

	// Decode signature
	sigBytes, err := hex.DecodeString(tx.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}

	// Verify signature - try new format with chain ID first
	if !mode3.Verify(&pk, []byte(txData), sigBytes) {
		// Fallback to legacy format for pre-upgrade transactions
		legacyTxData := fmt.Sprintf("%s%s%d%d", tx.From, tx.To, tx.Amount, tx.Timestamp)
		if !mode3.Verify(&pk, []byte(legacyTxData), sigBytes) {
			return fmt.Errorf("signature verification failed")
		}
	}

	return nil
}

// VerifyAddressMatchesPublicKey derives the address from the public key and compares
func VerifyAddressMatchesPublicKey(address, publicKeyHex string) error {
	// Decode hex-encoded public key
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode public key hex: %w", err)
	}

	// Derive address from public key (same algorithm as wallet.go)
	hash := sha256.Sum256(pubKeyBytes)
	derivedAddress := hex.EncodeToString(hash[:])[:40]

	if derivedAddress != address {
		return fmt.Errorf("address %s does not match public key (expected %s)", address, derivedAddress)
	}

	return nil
}

// validateTransaction checks if a transaction is valid
func validateTransaction(tx *Transaction) error {
	if tx.From == "" || tx.To == "" {
		return fmt.Errorf("transaction must include from and to address")
	}

	if tx.Amount <= 0 {
		return fmt.Errorf("transaction amount must be positive")
	}

	if tx.Signature == "" {
		return fmt.Errorf("transaction must be signed")
	}

	// Skip signature verification for SYSTEM (coinbase) transactions
	if tx.From != "SYSTEM" {
		// Verify cryptographic signature
		if err := VerifyTransactionSignature(tx); err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}

		// Verify the address matches the public key
		if err := VerifyAddressMatchesPublicKey(tx.From, tx.PublicKey); err != nil {
			return fmt.Errorf("address verification failed: %w", err)
		}
	}

	return nil
}

// ============================================================================
// BALANCE & REWARD FUNCTIONS
// ============================================================================

// GetBalance calculates the confirmed balance of an address from the blockchain
func (bc *Blockchain) GetBalance(address string) int64 {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return bc.getBalanceLocked(address)
}

// getBalanceLocked calculates balance (must hold lock)
func (bc *Blockchain) getBalanceLocked(address string) int64 {
	var balance int64

	for _, block := range bc.Blocks {
		for _, tx := range block.Transactions {
			if tx.To == address {
				balance += tx.Amount
			}
			if tx.From == address {
				balance -= tx.Amount
			}
		}
	}

	return balance
}

// GetAvailableBalance returns balance minus pending outgoing transactions
func (bc *Blockchain) GetAvailableBalance(address string) int64 {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()

	balance := bc.getBalanceLocked(address)

	// Subtract pending outgoing transactions
	for _, tx := range bc.PendingTransactions {
		if tx.From == address {
			balance -= tx.Amount
		}
	}

	return balance
}

// GetBlockReward calculates the mining reward for a given block height
// Implements Bitcoin-style halving: reward cuts in half every HalvingInterval blocks
func GetBlockReward(blockHeight int64) int64 {
	// Calculate number of halvings that have occurred
	halvings := blockHeight / HalvingInterval

	// After ~64 halvings, reward becomes negligible (like Bitcoin)
	if halvings >= 64 {
		return 0
	}

	// Calculate reward: InitialReward / (2^halvings)
	reward := InitialBlockReward
	for i := int64(0); i < halvings; i++ {
		reward /= 2
	}

	// Don't go below minimum
	if reward < MinBlockReward {
		return 0
	}

	return reward
}

// GetTotalSupply calculates total coins in circulation
func (bc *Blockchain) GetTotalSupply() int64 {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()

	var total int64
	for _, block := range bc.Blocks {
		for _, tx := range block.Transactions {
			if tx.From == "SYSTEM" {
				total += tx.Amount
			}
		}
	}
	return total
}

// GetSupplyInfo returns supply statistics
func (bc *Blockchain) GetSupplyInfo() map[string]interface{} {
	bc.mutex.RLock()
	height := int64(len(bc.Blocks))
	bc.mutex.RUnlock()

	currentReward := GetBlockReward(height)
	totalSupply := bc.GetTotalSupply()
	halvings := height / HalvingInterval
	blocksUntilHalving := HalvingInterval - (height % HalvingInterval)

	var percentMined float64
	if MaxSupply > 0 {
		percentMined = float64(totalSupply) / float64(MaxSupply) * 100
	}

	return map[string]interface{}{
		"current_block_reward":     FormatDLT(currentReward),
		"current_block_reward_raw": currentReward,
		"total_supply":             FormatDLT(totalSupply),
		"total_supply_raw":         totalSupply,
		"max_supply":               FormatDLT(MaxSupply),
		"max_supply_raw":           MaxSupply,
		"percent_mined":            percentMined,
		"halvings_occurred":        halvings,
		"blocks_until_halving":     blocksUntilHalving,
		"halving_interval":         HalvingInterval,
	}
}

// ValidateBlockTransactions checks if all transactions in a block are valid
// This includes checking sufficient balances at the point before this block
func (bc *Blockchain) ValidateBlockTransactions(block *Block, previousBlocks []*Block) error {
	// Enforce max transactions per block (shannon #13)
	if len(block.Transactions) > 5000 {
		return fmt.Errorf("block %d has %d transactions, exceeds max 5000", block.Index, len(block.Transactions))
	}

	// Enforce max block size (shannon #13)
	blockJSON, _ := json.Marshal(block)
	if len(blockJSON) > 1*1024*1024 {
		return fmt.Errorf("block %d size %d bytes exceeds max 1MB", block.Index, len(blockJSON))
	}

	// Build balance map from previous blocks
	balances := make(map[string]int64)

	for _, b := range previousBlocks {
		for _, tx := range b.Transactions {
			if tx.To != "" {
				balances[tx.To] += tx.Amount
			}
			if tx.From != "" && tx.From != "SYSTEM" {
				balances[tx.From] -= tx.Amount
			}
		}
	}

	// Coinbase validation: count SYSTEM transactions
	coinbaseCount := 0
	for _, tx := range block.Transactions {
		if tx.From == "SYSTEM" {
			coinbaseCount++
		}
	}
	if coinbaseCount != 1 {
		return fmt.Errorf("block %d must have exactly 1 coinbase transaction, has %d", block.Index, coinbaseCount)
	}

	// Verify coinbase amount matches expected block reward
	for _, tx := range block.Transactions {
		if tx.From == "SYSTEM" {
			expectedReward := GetBlockReward(block.Index)
			if tx.Amount != expectedReward {
				return fmt.Errorf("block %d coinbase amount %d does not match expected reward %d",
					block.Index, tx.Amount, expectedReward)
			}
			break
		}
	}

	// Now validate each transaction in the new block
	for _, tx := range block.Transactions {
		// Skip SYSTEM transactions (mining rewards) - already validated above
		if tx.From == "SYSTEM" {
			continue
		}

		// Basic validation (includes signature verification)
		if err := validateTransaction(tx); err != nil {
			return fmt.Errorf("invalid transaction in block %d: %v", block.Index, err)
		}

		// Check sender has sufficient balance
		if balances[tx.From] < tx.Amount {
			return fmt.Errorf("insufficient funds in block %d: %s has %s, needs %s",
				block.Index, tx.From, FormatDLT(balances[tx.From]), FormatDLT(tx.Amount))
		}

		// Update balances for subsequent transactions in same block
		balances[tx.From] -= tx.Amount
		balances[tx.To] += tx.Amount
	}

	return nil
}

// ValidateBlockTransactionsWithChain validates transactions using the current chain
func (bc *Blockchain) ValidateBlockTransactionsWithChain(block *Block) error {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()

	return bc.ValidateBlockTransactions(block, bc.Blocks)
}

// clearMempool empties the pending transactions and mempool
func (bc *Blockchain) clearMempool() {
	bc.PendingTransactions = make([]*Transaction, 0)
	bc.Mempool = make(map[string]*Transaction)
}

// clearMinedTransactions removes transactions that were included in a block
func (bc *Blockchain) clearMinedTransactions(minedTxs []*Transaction) {
	// Build set of mined transaction signatures
	minedSigs := make(map[string]bool)
	for _, tx := range minedTxs {
		minedSigs[tx.Signature] = true
	}

	// Remove mined transactions from mempool
	for sig := range minedSigs {
		delete(bc.Mempool, sig)
	}

	// Rebuild pending transactions from remaining mempool
	bc.PendingTransactions = make([]*Transaction, 0, len(bc.Mempool))
	for _, tx := range bc.Mempool {
		bc.PendingTransactions = append(bc.PendingTransactions, tx)
	}
}

// RemoveTransactionsFromMempool removes specific transactions (used when receiving blocks)
func (bc *Blockchain) RemoveTransactionsFromMempool(txs []*Transaction) {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	bc.clearMinedTransactions(txs)
}
