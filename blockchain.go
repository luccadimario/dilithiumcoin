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

// MerkleRootForkHeight is the block height at which block hashes switch from
// including JSON(Transactions) to using a Merkle root. This is a consensus-
// breaking hard fork: all nodes must upgrade before this height.
// This is a var (not const) so that tests can override it.
var MerkleRootForkHeight int64 = 6000

// DataForkHeight is the block height at which transaction Data fields and
// dlt1-prefixed checksummed addresses are activated. This is a consensus-
// breaking hard fork: all nodes must upgrade before this height.
// This is a var (not const) so that tests can override it.
var DataForkHeight int64 = 6300

// MaxTransactionDataSize is the maximum allowed size of the Data field in bytes
const MaxTransactionDataSize = 256

// Difficulty adjustment constants
const (
	// BlocksPerAdjustment is how often difficulty adjusts (legacy, kept for compatibility)
	BlocksPerAdjustment = 50

	// DAAWindow is the number of recent blocks to average for per-block adjustment
	DAAWindow = 20

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

	// MaxAdjustmentFactor limits how much difficulty can change per adjustment (legacy pre-fork)
	MaxAdjustmentFactor = 4.0

	// DAAForkHeight is the block height at which the per-block DAA activates.
	// Before this height, the legacy 50-block adjustment algorithm is used.
	// This enables a clean hard fork: nodes must upgrade before this height.
	DAAForkHeight = 600

	// DAAv2ForkHeight is the block height at which difficulty-adjusted LWMA activates.
	// Before this height, solve times are used raw. After this height, solve times are
	// normalized by the difficulty they were mined at, preventing cascading oscillations
	// when difficulty changes rapidly within the LWMA window.
	DAAv2ForkHeight = 600

	// ============================================================================
	// TRANSACTION FEES
	// ============================================================================

	// MinTransactionFee is the minimum fee for non-SYSTEM transactions (0.0001 DLT)
	MinTransactionFee int64 = 10000

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
	MerkleRoot     string         `json:"MerkleRoot,omitempty"`
	PreviousHash   string
	Hash           string
	Nonce          int64
	Difficulty     int `json:"Difficulty"`                // Leading zero hex digits (backward compat)
	DifficultyBits int `json:"DifficultyBits,omitempty"` // Bit-precise difficulty (soft fork)
}

// ComputeMerkleRoot computes the Merkle root of a list of transactions.
// Uses a standard binary Merkle tree (Bitcoin-style):
//   - Leaf = SHA-256(JSON(tx))
//   - Odd leaves: duplicate last
//   - Pair adjacent and SHA-256(left + right) up the tree
//   - Empty list: SHA-256("")
func ComputeMerkleRoot(transactions []*Transaction) string {
	if len(transactions) == 0 {
		h := sha256.Sum256([]byte{})
		return hex.EncodeToString(h[:])
	}

	hashes := make([][]byte, len(transactions))
	for i, tx := range transactions {
		txJSON, _ := json.Marshal(tx)
		h := sha256.Sum256(txJSON)
		hashes[i] = h[:]
	}

	for len(hashes) > 1 {
		if len(hashes)%2 != 0 {
			hashes = append(hashes, hashes[len(hashes)-1])
		}
		var next [][]byte
		for i := 0; i < len(hashes); i += 2 {
			combined := append(hashes[i], hashes[i+1]...)
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}
		hashes = next
	}

	return hex.EncodeToString(hashes[0])
}

// CalculateHash creates a SHA-256 hash of the block
func (b *Block) CalculateHash() string {
	var txData string
	if b.Index >= MerkleRootForkHeight {
		txData = b.MerkleRoot
	} else {
		txJSON, _ := json.Marshal(b.Transactions)
		txData = string(txJSON)
	}

	blockData := strconv.FormatInt(b.Index, 10) +
		strconv.FormatInt(b.Timestamp, 10) +
		txData +
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

	// Balance cache (shannon #19) — avoids full chain scan per request
	balanceCache       map[string]int64 // address -> balance
	balanceCacheHeight int              // chain height when cache was built
	totalTxCache       int              // cached total transaction count

	// Mined transaction index — prevents duplicate tx mining and mempool re-entry
	MinedTxIndex map[string]bool // signature -> true for all non-SYSTEM txs in chain

	// Persistence
	store            *ChainStore // nil = no persistence (tests)
	persistRequired  bool        // true once SetStore is called — blocks MUST be persisted
}

// NewBlockchain initializes a new blockchain with genesis block
func NewBlockchain(difficulty int) *Blockchain {
	return &Blockchain{
		Blocks:              []*Block{createGenesisBlock(difficulty)},
		Difficulty:          difficulty,
		DifficultyBits:      hexDigitsToDifficultyBits(difficulty),
		PendingTransactions: make([]*Transaction, 0),
		Mempool:             make(map[string]*Transaction),
		MinedTxIndex:        make(map[string]bool),
	}
}

// SetStore attaches a ChainStore for disk persistence.
// If the store contains a saved chain, it replaces the in-memory chain.
// Once called, persistence becomes mandatory — AppendBlock will fail if
// the store is nil or write fails, preventing silent data loss.
func (bc *Blockchain) SetStore(store *ChainStore) error {
	bc.store = store
	bc.persistRequired = true

	blocks, err := store.LoadChain()
	if err != nil {
		return fmt.Errorf("failed to load chain from disk: %w", err)
	}

	if len(blocks) > 0 {
		bc.Blocks = blocks
		bc.recalcDifficultyFromChain()
		bc.rebuildMinedTxIndex()
		fmt.Printf("Loaded %d blocks from disk (height %d)\n", len(blocks), blocks[len(blocks)-1].Index)
	} else {
		// First run — save genesis block
		if err := store.SaveBlock(bc.Blocks[0]); err != nil {
			return fmt.Errorf("failed to save genesis block: %w", err)
		}
		fmt.Println("Initialized chain storage with genesis block")
	}

	return nil
}

// persistBlock saves a single block to disk.
// Returns an error if the block could not be written — callers MUST NOT
// add the block to the in-memory chain if this returns non-nil.
// If SetStore was called (production), a nil store is a fatal error.
// If SetStore was never called (tests), a nil store silently succeeds.
func (bc *Blockchain) persistBlock(block *Block) error {
	if bc.store == nil {
		if bc.persistRequired {
			return fmt.Errorf("chain store is nil after SetStore — block %d cannot be persisted (data loss risk)", block.Index)
		}
		return nil // Test mode: no store configured
	}
	if err := bc.store.SaveBlock(block); err != nil {
		return fmt.Errorf("failed to persist block %d: %w", block.Index, err)
	}
	return nil
}

// AppendBlock persists a block to disk and then adds it to the in-memory chain.
// This is the ONLY correct way to add a block — it guarantees disk-before-memory
// ordering so blocks can never exist in memory without being on disk.
// Caller MUST hold bc.mutex.
func (bc *Blockchain) AppendBlock(block *Block) error {
	if err := bc.persistBlock(block); err != nil {
		return err
	}
	bc.Blocks = append(bc.Blocks, block)
	// Index mined transaction signatures for duplicate prevention
	for _, tx := range block.Transactions {
		if tx.From != "SYSTEM" {
			bc.MinedTxIndex[tx.Signature] = true
		}
	}
	bc.clearMinedTransactions(block.Transactions)
	return nil
}

// persistChainFrom saves all blocks from startIndex onward and prunes orphans.
// Returns an error if persistence fails — callers MUST handle this to avoid
// in-memory chain diverging from on-disk chain.
func (bc *Blockchain) persistChainFrom(startIndex int) error {
	if bc.store == nil {
		if bc.persistRequired {
			return fmt.Errorf("chain store is nil after SetStore — cannot persist chain from index %d", startIndex)
		}
		return nil // Test mode
	}
	if err := bc.store.SaveChain(bc.Blocks, startIndex); err != nil {
		return fmt.Errorf("failed to persist chain from index %d: %w", startIndex, err)
	}
	return nil
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

	// Enforce minimum transaction fee for new mempool transactions (not historical blocks)
	if tx.From != "SYSTEM" && tx.Fee < MinTransactionFee {
		return false, fmt.Errorf("transaction fee %d below minimum %d (0.0001 DLT)", tx.Fee, MinTransactionFee)
	}

	bc.mutex.Lock()
	defer bc.mutex.Unlock()

	// Deduplicate by signature
	if _, exists := bc.Mempool[tx.Signature]; exists {
		return false, nil // Already in mempool
	}

	// Reject transactions that have already been mined into the blockchain
	if bc.MinedTxIndex[tx.Signature] {
		return false, nil // Already confirmed in a block
	}

	// Skip balance check for SYSTEM transactions (mining rewards)
	if tx.From != "SYSTEM" {
		// Calculate available balance (confirmed - pending outgoing)
		availableBalance := bc.getBalanceLocked(tx.From)

		// Subtract pending outgoing transactions from the same address (amount + fee)
		for _, pendingTx := range bc.PendingTransactions {
			if pendingTx.From == tx.From {
				availableBalance -= pendingTx.Amount + pendingTx.Fee
			}
		}

		// Check if sender has sufficient funds (amount + fee)
		totalCost := tx.Amount + tx.Fee
		if availableBalance < totalCost {
			return false, fmt.Errorf("insufficient funds: address %s has %s available, needs %s (amount) + %s (fee)",
				tx.From, FormatDLT(availableBalance), FormatDLT(tx.Amount), FormatDLT(tx.Fee))
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

	// Sum fees from all pending transactions
	var totalFees int64
	for _, tx := range bc.PendingTransactions {
		totalFees += tx.Fee
	}

	// Create coinbase (mining reward) transaction
	// Miners get block reward + transaction fees
	rewardTx := &Transaction{
		From:      "SYSTEM",
		To:        minerAddress,
		Amount:    blockReward + totalFees,
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
		MerkleRoot:     ComputeMerkleRoot(txToMine),
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

	if err := bc.AppendBlock(newBlock); err != nil {
		fmt.Printf("ERROR: %v — block %d discarded\n", err, newBlock.Index)
		return nil, false
	}

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
// Deprecated: Use GetBlockRange() for large chains to avoid full copies.
func (bc *Blockchain) GetBlocks() []*Block {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	blocks := make([]*Block, len(bc.Blocks))
	copy(blocks, bc.Blocks)
	return blocks
}

// GetBlockRange returns a slice of blocks from start (inclusive) to end (exclusive).
// Returns nil if range is invalid. Uses direct slice indexing (no full copy).
func (bc *Blockchain) GetBlockRange(start, end int64) []*Block {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()

	chainLen := int64(len(bc.Blocks))
	if start < 0 {
		start = 0
	}
	if end > chainLen {
		end = chainLen
	}
	if start >= end {
		return nil
	}

	result := make([]*Block, end-start)
	copy(result, bc.Blocks[start:end])
	return result
}

// GetHeaderRange returns lightweight block headers for the given range.
// start is inclusive, end is exclusive.
func (bc *Blockchain) GetHeaderRange(start, end int64) []*BlockHeader {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()

	chainLen := int64(len(bc.Blocks))
	if start < 0 {
		start = 0
	}
	if end > chainLen {
		end = chainLen
	}
	if start >= end {
		return nil
	}

	headers := make([]*BlockHeader, 0, end-start)
	for i := start; i < end; i++ {
		b := bc.Blocks[i]
		headers = append(headers, &BlockHeader{
			Index:          b.Index,
			Timestamp:      b.Timestamp,
			PreviousHash:   b.PreviousHash,
			Hash:           b.Hash,
			Nonce:          b.Nonce,
			Difficulty:     b.Difficulty,
			DifficultyBits: b.DifficultyBits,
			TxCount:        len(b.Transactions),
		})
	}
	return headers
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

// GetCurrentDifficultyBits returns the bit-based difficulty for the next block.
// Uses a write lock because GetCurrentDifficultyBitsLocked may cache difficulty state.
func (bc *Blockchain) GetCurrentDifficultyBits() int {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	return bc.GetCurrentDifficultyBitsLocked()
}

// GetCurrentDifficultyLocked returns legacy hex-digit difficulty (assumes lock held)
func (bc *Blockchain) GetCurrentDifficultyLocked() int {
	return difficultyBitsToHexDigits(bc.GetCurrentDifficultyBitsLocked())
}

// GetCurrentDifficultyBitsLocked returns bit-based difficulty (assumes lock held)
func (bc *Blockchain) GetCurrentDifficultyBitsLocked() int {
	height := len(bc.Blocks)

	// Use cached result if we already calculated for this height
	if bc.lastAdjustmentHeight == height {
		return bc.lastAdjustmentBits
	}

	// Before fork: use legacy 50-block adjustment
	if height < DAAForkHeight {
		return bc.getLegacyDifficultyBits(height)
	}

	// After fork: use per-block DAA (LWMA)
	if height < DAAWindow {
		return bc.DifficultyBits
	}

	bits := bc.calculateNewDifficultyBits()
	bc.lastAdjustmentHeight = height
	bc.lastAdjustmentBits = bits
	bc.DifficultyBits = bits
	bc.Difficulty = difficultyBitsToHexDigits(bits)
	return bits
}

// getLegacyDifficultyBits implements the pre-fork 50-block adjustment algorithm
func (bc *Blockchain) getLegacyDifficultyBits(height int) int {
	if height < BlocksPerAdjustment {
		return bc.DifficultyBits
	}

	if height%BlocksPerAdjustment != 0 {
		return bc.DifficultyBits
	}

	bits := bc.calculateLegacyDifficultyBits()
	bc.lastAdjustmentHeight = height
	bc.lastAdjustmentBits = bits
	bc.DifficultyBits = bits
	bc.Difficulty = difficultyBitsToHexDigits(bits)
	return bits
}

// calculateLegacyDifficultyBits is the pre-fork algorithm: compare actual vs expected
// time over the last BlocksPerAdjustment (50) blocks, adjust by up to 2 bits.
func (bc *Blockchain) calculateLegacyDifficultyBits() int {
	height := len(bc.Blocks)

	startBlock := bc.Blocks[height-BlocksPerAdjustment]
	endBlock := bc.Blocks[height-1]

	actualTime := endBlock.Timestamp - startBlock.Timestamp
	if actualTime <= 0 {
		actualTime = 1
	}

	expectedTime := int64(BlocksPerAdjustment * TargetBlockTime)
	currentBits := bc.DifficultyBits

	ratio := float64(expectedTime) / float64(actualTime)
	if ratio > MaxAdjustmentFactor {
		ratio = MaxAdjustmentFactor
	} else if ratio < 1.0/MaxAdjustmentFactor {
		ratio = 1.0 / MaxAdjustmentFactor
	}

	var adjustment int
	logRatio := math.Log2(ratio)
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

	avgBlockTime := float64(actualTime) / float64(BlocksPerAdjustment)
	if newBits != currentBits {
		fmt.Printf("=== DIFFICULTY ADJUSTMENT (Legacy) ===\n")
		fmt.Printf("  Block height: %d\n", height)
		fmt.Printf("  Actual time for %d blocks: %ds (avg %.1fs/block)\n",
			BlocksPerAdjustment, actualTime, avgBlockTime)
		fmt.Printf("  Expected time: %ds (target %ds/block)\n",
			expectedTime, TargetBlockTime)
		fmt.Printf("  Ratio: %.2f (log2: %.2f)\n", ratio, logRatio)
		fmt.Printf("  DifficultyBits: %d -> %d\n", currentBits, newBits)
		fmt.Printf("======================================\n")
	}

	bc.DifficultyBits = newBits
	return newBits
}

// calculateNewDifficultyBits computes new bit-based difficulty using LWMA.
// Post-DAAv2ForkHeight: uses difficulty-adjusted LWMA where solve times are normalized
// by the difficulty they were mined at. This prevents cascading oscillations where fast
// blocks at low difficulty fool the algorithm into thinking hashrate increased.
// Pre-DAAv2ForkHeight: uses raw solve times with emergency -3/-2 adjustments.
// Max adjustment = +/- 1 bit per block (post-v2), +1/-3 (pre-v2).
func (bc *Blockchain) calculateNewDifficultyBits() int {
	height := len(bc.Blocks)
	currentBits := bc.DifficultyBits
	useV2 := height >= DAAv2ForkHeight

	maxSolveTime := int64(TargetBlockTime) * 10

	var weightedSum float64
	var totalWeight float64

	for i := 0; i < DAAWindow; i++ {
		blockIndex := height - DAAWindow + i
		if blockIndex <= 0 || blockIndex < DAAForkHeight {
			continue
		}

		currentBlock := bc.Blocks[blockIndex]
		previousBlock := bc.Blocks[blockIndex-1]

		solveTime := currentBlock.Timestamp - previousBlock.Timestamp
		if solveTime <= 0 {
			solveTime = 1
		}
		if solveTime > maxSolveTime {
			solveTime = maxSolveTime
		}

		adjustedTime := float64(solveTime)

		// Post-v2: normalize solve time to current difficulty level
		if useV2 {
			blockBits := currentBlock.getEffectiveDifficultyBits()
			diffDelta := currentBits - blockBits
			if diffDelta > 0 {
				adjustedTime *= float64(int64(1) << diffDelta)
			} else if diffDelta < 0 {
				adjustedTime /= float64(int64(1) << (-diffDelta))
			}
			if adjustedTime > float64(maxSolveTime) {
				adjustedTime = float64(maxSolveTime)
			}
			if adjustedTime < 1 {
				adjustedTime = 1
			}
		}

		weight := float64(i + 1)
		weightedSum += adjustedTime * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return currentBits
	}

	weightedAvg := weightedSum / totalWeight

	var adjustment int
	if useV2 {
		// Post-v2: symmetric +/- 1 bit only
		if weightedAvg < float64(TargetBlockTime)*0.7 {
			adjustment = 1
		} else if weightedAvg > float64(TargetBlockTime)*1.3 {
			adjustment = -1
		}
	} else {
		// Pre-v2: asymmetric with emergency drops
		if weightedAvg < float64(TargetBlockTime)*0.7 {
			adjustment = 1
		} else if weightedAvg > float64(TargetBlockTime)*5.0 {
			adjustment = -3
		} else if weightedAvg > float64(TargetBlockTime)*3.0 {
			adjustment = -2
		} else if weightedAvg > float64(TargetBlockTime)*1.3 {
			adjustment = -1
		}
	}

	newBits := currentBits + adjustment

	if newBits < MinDifficultyBits {
		newBits = MinDifficultyBits
	} else if newBits > MaxDifficultyBits {
		newBits = MaxDifficultyBits
	}

	if newBits != currentBits {
		daaVersion := "v1"
		if useV2 {
			daaVersion = "v2"
		}
		fmt.Printf("=== DIFFICULTY ADJUSTMENT (Per-Block DAA %s) ===\n", daaVersion)
		fmt.Printf("  Block height: %d\n", height)
		fmt.Printf("  Weighted avg block time: %.1fs (target %ds)\n", weightedAvg, TargetBlockTime)
		fmt.Printf("  DifficultyBits: %d -> %d (hex digits: %d -> %d)\n",
			currentBits, newBits,
			difficultyBitsToHexDigits(currentBits), difficultyBitsToHexDigits(newBits))
		if adjustment > 0 {
			fmt.Printf("  Mining too fast — increased difficulty\n")
		} else {
			fmt.Printf("  Mining too slow — decreased difficulty\n")
		}
		fmt.Printf("=============================================\n")
	} else {
		fmt.Printf("Difficulty check at height %d: no change (bits=%d, adj avg=%.1fs/block)\n",
			height, currentBits, weightedAvg)
	}

	bc.DifficultyBits = newBits

	return newBits
}

// recalcDifficultyFromChain sets DifficultyBits based on the synced chain.
// If we're pre-fork, uses the legacy algorithm. Post-fork, uses LWMA.
func (bc *Blockchain) recalcDifficultyFromChain() {
	height := len(bc.Blocks)

	// Find the most recent block with DifficultyBits set as anchor
	anchorBits := 0
	searchDepth := DAAWindow * 2
	if searchDepth < BlocksPerAdjustment*2 {
		searchDepth = BlocksPerAdjustment * 2
	}
	for i := height - 1; i >= 0 && i >= height-searchDepth; i-- {
		if bc.Blocks[i].DifficultyBits > 0 {
			anchorBits = bc.Blocks[i].DifficultyBits
			break
		}
	}

	// Set baseline from anchor or chain state
	if anchorBits > 0 {
		bc.DifficultyBits = anchorBits
	} else {
		bc.DifficultyBits = hexDigitsToDifficultyBits(bc.Difficulty)
		if bc.DifficultyBits < MinDifficultyBits {
			bc.DifficultyBits = MinDifficultyBits
		}
	}

	// Pre-fork: use legacy recalc (replay last adjustment period)
	if height < DAAForkHeight {
		bc.recalcLegacyDifficulty(height)
		return
	}

	// Post-fork: use LWMA
	if height < DAAWindow {
		bc.Difficulty = difficultyBitsToHexDigits(bc.DifficultyBits)
		return
	}

	bc.recalcLWMADifficulty(height)
}

// recalcLegacyDifficulty replays the last 50-block adjustment period
func (bc *Blockchain) recalcLegacyDifficulty(height int) {
	if height < BlocksPerAdjustment {
		bc.Difficulty = difficultyBitsToHexDigits(bc.DifficultyBits)
		fmt.Printf("Recalculated difficulty from chain (legacy): %d bits at height %d\n",
			bc.DifficultyBits, height)
		return
	}

	// Find the last adjustment boundary
	lastBoundary := (height / BlocksPerAdjustment) * BlocksPerAdjustment
	if lastBoundary >= height {
		lastBoundary -= BlocksPerAdjustment
	}
	if lastBoundary < BlocksPerAdjustment {
		bc.Difficulty = difficultyBitsToHexDigits(bc.DifficultyBits)
		fmt.Printf("Recalculated difficulty from chain (legacy): %d bits at height %d\n",
			bc.DifficultyBits, height)
		return
	}

	startBlock := bc.Blocks[lastBoundary-BlocksPerAdjustment]
	endBlock := bc.Blocks[lastBoundary-1]

	actualTime := endBlock.Timestamp - startBlock.Timestamp
	if actualTime <= 0 {
		actualTime = 1
	}

	expectedTime := int64(BlocksPerAdjustment * TargetBlockTime)
	// Use the difficulty from the block before the adjustment boundary,
	// not bc.DifficultyBits which may have been set from a later anchor block during sync
	currentBits := endBlock.getEffectiveDifficultyBits()

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
	bc.lastAdjustmentHeight = 0
	fmt.Printf("Recalculated difficulty from chain (legacy): %d bits (hex %d) at height %d\n",
		newBits, bc.Difficulty, height)
}

// recalcLWMADifficulty uses the post-fork LWMA algorithm (called during chain sync)
// Applies difficulty-adjusted normalization post-DAAv2ForkHeight.
func (bc *Blockchain) recalcLWMADifficulty(height int) {
	currentBits := bc.Blocks[height-1].getEffectiveDifficultyBits()
	useV2 := height >= DAAv2ForkHeight

	maxSolveTime := int64(TargetBlockTime) * 10

	var weightedSum float64
	var totalWeight float64

	for i := 0; i < DAAWindow; i++ {
		blockIndex := height - DAAWindow + i
		if blockIndex <= 0 || blockIndex < DAAForkHeight {
			continue
		}

		solveTime := bc.Blocks[blockIndex].Timestamp - bc.Blocks[blockIndex-1].Timestamp
		if solveTime <= 0 {
			solveTime = 1
		}
		if solveTime > maxSolveTime {
			solveTime = maxSolveTime
		}

		adjustedTime := float64(solveTime)

		if useV2 {
			blockBits := bc.Blocks[blockIndex].getEffectiveDifficultyBits()
			diffDelta := currentBits - blockBits
			if diffDelta > 0 {
				adjustedTime *= float64(int64(1) << diffDelta)
			} else if diffDelta < 0 {
				adjustedTime /= float64(int64(1) << (-diffDelta))
			}
			if adjustedTime > float64(maxSolveTime) {
				adjustedTime = float64(maxSolveTime)
			}
			if adjustedTime < 1 {
				adjustedTime = 1
			}
		}

		weight := float64(i + 1)
		weightedSum += adjustedTime * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		bc.Difficulty = difficultyBitsToHexDigits(currentBits)
		bc.lastAdjustmentHeight = 0
		fmt.Printf("Recalculated difficulty from chain (LWMA): %d bits at height %d (no post-fork blocks in window)\n",
			currentBits, height)
		return
	}

	weightedAvg := weightedSum / totalWeight

	var adjustment int
	if useV2 {
		if weightedAvg < float64(TargetBlockTime)*0.7 {
			adjustment = 1
		} else if weightedAvg > float64(TargetBlockTime)*1.3 {
			adjustment = -1
		}
	} else {
		if weightedAvg < float64(TargetBlockTime)*0.7 {
			adjustment = 1
		} else if weightedAvg > float64(TargetBlockTime)*5.0 {
			adjustment = -3
		} else if weightedAvg > float64(TargetBlockTime)*3.0 {
			adjustment = -2
		} else if weightedAvg > float64(TargetBlockTime)*1.3 {
			adjustment = -1
		}
	}

	newBits := currentBits + adjustment
	if newBits < MinDifficultyBits {
		newBits = MinDifficultyBits
	} else if newBits > MaxDifficultyBits {
		newBits = MaxDifficultyBits
	}

	bc.DifficultyBits = newBits
	bc.Difficulty = difficultyBitsToHexDigits(newBits)
	bc.lastAdjustmentHeight = 0
	fmt.Printf("Recalculated difficulty from chain (LWMA): %d bits (hex %d) at height %d (weighted avg: %.1fs)\n",
		newBits, bc.Difficulty, height, weightedAvg)
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
	// Try formats in order: with-data → with-fee → pre-fee → legacy
	// Decode signature
	sigBytes, err := hex.DecodeString(tx.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}

	// Try with-data format first (post-DataForkHeight)
	if tx.Data != "" {
		txDataWithData := fmt.Sprintf("%s:%s%s%d%d%d:%s", NetworkName, tx.From, tx.To, tx.Amount, tx.Fee, tx.Timestamp, tx.Data)
		if mode3.Verify(&pk, []byte(txDataWithData), sigBytes) {
			return nil
		}
	}

	// Try current format (with fee, no data)
	txDataWithFee := fmt.Sprintf("%s:%s%s%d%d%d", NetworkName, tx.From, tx.To, tx.Amount, tx.Fee, tx.Timestamp)
	if !mode3.Verify(&pk, []byte(txDataWithFee), sigBytes) {
		// Fallback to pre-fee format (chain ID but no fee) for existing chain transactions
		txDataNoFee := fmt.Sprintf("%s:%s%s%d%d", NetworkName, tx.From, tx.To, tx.Amount, tx.Timestamp)
		if !mode3.Verify(&pk, []byte(txDataNoFee), sigBytes) {
			// Fallback to legacy format (no chain ID, no fee) for very old transactions
			legacyTxData := fmt.Sprintf("%s%s%d%d", tx.From, tx.To, tx.Amount, tx.Timestamp)
			if !mode3.Verify(&pk, []byte(legacyTxData), sigBytes) {
				return fmt.Errorf("signature verification failed")
			}
		}
	}

	return nil
}

// VerifyAddressMatchesPublicKey derives the address from the public key and compares.
// Accepts both old (40-char hex) and new (dlt1-prefixed 48-char) address formats.
func VerifyAddressMatchesPublicKey(address, publicKeyHex string) error {
	// Normalize address to raw 40-char hex
	rawAddress, err := NormalizeAddress(address)
	if err != nil {
		return fmt.Errorf("invalid address format: %w", err)
	}

	// Decode hex-encoded public key
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode public key hex: %w", err)
	}

	// Derive address from public key (same algorithm as wallet.go)
	hash := sha256.Sum256(pubKeyBytes)
	derivedAddress := hex.EncodeToString(hash[:])[:40]

	if derivedAddress != rawAddress {
		return fmt.Errorf("address %s does not match public key (expected %s)", rawAddress, derivedAddress)
	}

	return nil
}

// validateTransaction checks if a transaction is valid.
// Normalizes dlt1-prefixed addresses to raw 40-char hex for internal storage.
func validateTransaction(tx *Transaction) error {
	if tx.From == "" || tx.To == "" {
		return fmt.Errorf("transaction must include from and to address")
	}

	// Normalize addresses: accept both dlt1-prefixed and raw hex
	if tx.From != "SYSTEM" {
		normalizedFrom, err := NormalizeAddress(tx.From)
		if err != nil {
			return fmt.Errorf("invalid from address: %w", err)
		}
		tx.From = normalizedFrom
	}

	normalizedTo, err := NormalizeAddress(tx.To)
	if err != nil {
		return fmt.Errorf("invalid to address: %w", err)
	}
	tx.To = normalizedTo

	if tx.Amount <= 0 {
		return fmt.Errorf("transaction amount must be positive")
	}

	if tx.Fee < 0 {
		return fmt.Errorf("transaction fee must not be negative")
	}

	if tx.Signature == "" {
		return fmt.Errorf("transaction must be signed")
	}

	// Validate Data field
	if len(tx.Data) > MaxTransactionDataSize {
		return fmt.Errorf("transaction data exceeds maximum size of %d bytes", MaxTransactionDataSize)
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

// validateTransactionForBlock validates a transaction in the context of a specific block height.
// Before DataForkHeight, transactions with non-empty Data are rejected.
func validateTransactionForBlock(tx *Transaction, blockHeight int64) error {
	if err := validateTransaction(tx); err != nil {
		return err
	}

	if tx.Data != "" && blockHeight < DataForkHeight {
		return fmt.Errorf("transaction data field not allowed before fork height %d", DataForkHeight)
	}

	return nil
}

// ============================================================================
// BALANCE & REWARD FUNCTIONS
// ============================================================================

// GetBalance calculates the confirmed balance using the cache (shannon #19).
// Uses a write lock because ensureBalanceCache may rebuild the cache.
func (bc *Blockchain) GetBalance(address string) int64 {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	return bc.getBalanceLocked(address)
}

// getBalanceLocked calculates balance using cache (must hold lock)
func (bc *Blockchain) getBalanceLocked(address string) int64 {
	bc.ensureBalanceCache()
	return bc.balanceCache[address]
}

// GetAvailableBalance returns balance minus pending outgoing transactions.
// Uses a write lock because ensureBalanceCache may rebuild the cache.
func (bc *Blockchain) GetAvailableBalance(address string) int64 {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()

	balance := bc.getBalanceLocked(address)

	// Subtract pending outgoing transactions (amount + fee)
	for _, tx := range bc.PendingTransactions {
		if tx.From == address {
			balance -= (tx.Amount + tx.Fee)
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

// ensureBalanceCache builds or refreshes the balance cache if stale
// Must be called with at least an RLock held
func (bc *Blockchain) ensureBalanceCache() {
	height := len(bc.Blocks)
	if bc.balanceCache != nil && bc.balanceCacheHeight == height {
		return // cache is fresh
	}

	bc.balanceCache = make(map[string]int64)
	bc.totalTxCache = 0
	for _, block := range bc.Blocks {
		for _, tx := range block.Transactions {
			bc.totalTxCache++
			if tx.To != "" {
				bc.balanceCache[tx.To] += tx.Amount
			}
			if tx.From != "" && tx.From != "SYSTEM" {
				// Sender pays amount + fee
				bc.balanceCache[tx.From] -= (tx.Amount + tx.Fee)
			} else if tx.From != "" {
				// SYSTEM (coinbase) — just debit Amount (fee is 0)
				bc.balanceCache[tx.From] -= tx.Amount
			}
		}
	}
	bc.balanceCacheHeight = height
}

// GetTotalTransactions returns the total transaction count using the cache.
// Uses a write lock because ensureBalanceCache may rebuild the cache.
func (bc *Blockchain) GetTotalTransactions() int {
	bc.mutex.Lock()
	bc.ensureBalanceCache()
	total := bc.totalTxCache
	bc.mutex.Unlock()
	return total
}

// GetAddressInfo returns balance and transaction history for an address
func (bc *Blockchain) GetAddressInfo(address string) (balance, received, sent int64, txs []map[string]interface{}) {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()

	for _, block := range bc.Blocks {
		for _, tx := range block.Transactions {
			isRelevant := false
			if tx.To == address {
				balance += tx.Amount
				received += tx.Amount
				isRelevant = true
			}
			if tx.From == address {
				balance -= (tx.Amount + tx.Fee)
				sent += tx.Amount + tx.Fee
				isRelevant = true
			}
			if isRelevant {
				txs = append(txs, map[string]interface{}{
					"signature":   tx.Signature,
					"from":        tx.From,
					"to":          tx.To,
					"amount":      tx.Amount,
					"amount_dlt":  FormatDLT(tx.Amount),
					"timestamp":   tx.Timestamp,
					"block_index": block.Index,
					"block_hash":  block.Hash,
				})
			}
		}
	}
	return
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

	// Build balance map and set of already-mined signatures from previous blocks
	balances := make(map[string]int64)
	minedSigs := make(map[string]bool)

	for _, b := range previousBlocks {
		for _, tx := range b.Transactions {
			if tx.To != "" {
				balances[tx.To] += tx.Amount
			}
			if tx.From != "" && tx.From != "SYSTEM" {
				balances[tx.From] -= (tx.Amount + tx.Fee)
				minedSigs[tx.Signature] = true
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

	// Sum fees from non-coinbase transactions
	var totalFees int64
	for _, tx := range block.Transactions {
		if tx.From != "SYSTEM" {
			totalFees += tx.Fee
		}
	}

	// Verify coinbase amount matches expected block reward + fees
	for _, tx := range block.Transactions {
		if tx.From == "SYSTEM" {
			expectedReward := GetBlockReward(block.Index) + totalFees
			if tx.Amount != expectedReward {
				return fmt.Errorf("block %d coinbase amount %d does not match expected reward %d (base %d + fees %d)",
					block.Index, tx.Amount, expectedReward, GetBlockReward(block.Index), totalFees)
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

		// Reject duplicate transactions — same signature must not appear in multiple blocks
		if minedSigs[tx.Signature] {
			return fmt.Errorf("block %d contains already-mined transaction (sig %s…)", block.Index, tx.Signature[:16])
		}
		minedSigs[tx.Signature] = true // also catches duplicates within the same block

		// Basic validation (includes signature verification, fee check, and fork-aware data check)
		if err := validateTransactionForBlock(tx, block.Index); err != nil {
			return fmt.Errorf("invalid transaction in block %d: %v", block.Index, err)
		}

		// Check sender has sufficient balance for amount + fee
		totalCost := tx.Amount + tx.Fee
		if balances[tx.From] < totalCost {
			return fmt.Errorf("insufficient funds in block %d: %s has %s, needs %s (amount %s + fee %s)",
				block.Index, tx.From, FormatDLT(balances[tx.From]), FormatDLT(totalCost),
				FormatDLT(tx.Amount), FormatDLT(tx.Fee))
		}

		// Update balances: sender pays amount + fee, recipient gets amount only
		balances[tx.From] -= totalCost
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

// revalidateMempool removes mempool transactions that conflict with the new chain after a reorg.
// Must be called with bc.mutex held.
func (bc *Blockchain) revalidateMempool(chain []*Block) {
	// Collect all transaction signatures that are already mined in the new chain
	minedSigs := make(map[string]bool)
	for _, block := range chain {
		for _, tx := range block.Transactions {
			if tx.From != "SYSTEM" {
				minedSigs[tx.Signature] = true
			}
		}
	}

	// Remove mined txs and rebuild pending list
	for sig := range bc.Mempool {
		if minedSigs[sig] {
			delete(bc.Mempool, sig)
		}
	}
	bc.PendingTransactions = make([]*Transaction, 0, len(bc.Mempool))
	for _, tx := range bc.Mempool {
		bc.PendingTransactions = append(bc.PendingTransactions, tx)
	}
}

// rebuildMinedTxIndex rebuilds the MinedTxIndex from the current chain.
// Must be called with bc.mutex held (or before concurrent access begins).
func (bc *Blockchain) rebuildMinedTxIndex() {
	bc.MinedTxIndex = make(map[string]bool)
	for _, block := range bc.Blocks {
		for _, tx := range block.Transactions {
			if tx.From != "SYSTEM" {
				bc.MinedTxIndex[tx.Signature] = true
			}
		}
	}
	fmt.Printf("Built mined tx index: %d confirmed transactions\n", len(bc.MinedTxIndex))
}

// RemoveTransactionsFromMempool removes specific transactions (used when receiving blocks)
func (bc *Blockchain) RemoveTransactionsFromMempool(txs []*Transaction) {
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	bc.clearMinedTransactions(txs)
}
