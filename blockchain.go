package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// ============================================================================
// BLOCK
// ============================================================================

// Block represents a single block in the blockchain
type Block struct {
	Index        int64
	Timestamp    int64
	Transactions []*Transaction `json:"transactions"`
	PreviousHash string
	Hash         string
	Nonce        int64
}

// CalculateHash creates a SHA-256 hash of the block
func (b *Block) CalculateHash() string {
	txJSON, _ := json.Marshal(b.Transactions)

	blockData := strconv.FormatInt(b.Index, 10) +
		strconv.FormatInt(b.Timestamp, 10) +
		string(txJSON) +
		b.PreviousHash +
		strconv.FormatInt(b.Nonce, 10)

	hash := sha256.Sum256([]byte(blockData))
	return hex.EncodeToString(hash[:])
}

// MineBlock performs proof of work by finding a hash with required leading zeros
func (b *Block) MineBlock(difficulty int) bool {
	return b.MineBlockWithCancel(difficulty, nil)
}

// MineBlockWithCancel performs proof of work with cancellation support
// Returns true if mining succeeded, false if cancelled
func (b *Block) MineBlockWithCancel(difficulty int, cancel <-chan struct{}) bool {
	target := createTarget(difficulty)
	fmt.Printf("Mining block %d...\n", b.Index)
	start := time.Now()

	b.Hash = b.CalculateHash()

	for b.Hash[:difficulty] != target {
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
	Difficulty          int
	PendingTransactions []*Transaction
	MiningReward        float64
	Mempool             map[string]*Transaction
	mutex               sync.RWMutex
}

// NewBlockchain initializes a new blockchain with genesis block
func NewBlockchain(difficulty int) *Blockchain {
	return &Blockchain{
		Blocks:              []*Block{createGenesisBlock(difficulty)},
		Difficulty:          difficulty,
		PendingTransactions: make([]*Transaction, 0),
		MiningReward:        10.0,
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
func (bc *Blockchain) MinePendingTransactionsWithCancel(minerAddress string, cancel <-chan struct{}) (*Block, bool) {
	bc.mutex.Lock()

	// Check if there are transactions to mine
	if len(bc.PendingTransactions) == 0 {
		bc.mutex.Unlock()
		return nil, false
	}

	// Add mining reward transaction
	rewardTx := &Transaction{
		From:      "SYSTEM",
		To:        minerAddress,
		Amount:    bc.MiningReward,
		Timestamp: time.Now().Unix(),
		Signature: "SYSTEM",
	}

	// Copy pending transactions and add reward
	txToMine := make([]*Transaction, len(bc.PendingTransactions), len(bc.PendingTransactions)+1)
	copy(txToMine, bc.PendingTransactions)
	txToMine = append(txToMine, rewardTx)

	// Create new block with pending transactions
	previousBlock := bc.Blocks[len(bc.Blocks)-1]
	newBlock := &Block{
		Index:        previousBlock.Index + 1,
		Timestamp:    time.Now().Unix(),
		Transactions: txToMine,
		PreviousHash: previousBlock.Hash,
		Nonce:        0,
	}

	bc.mutex.Unlock()

	// Mine the block (this can be cancelled)
	if !newBlock.MineBlockWithCancel(bc.Difficulty, cancel) {
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
func (bc *Blockchain) GetBlockCount() int {
	bc.mutex.RLock()
	defer bc.mutex.RUnlock()
	return len(bc.Blocks)
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
	target := createTarget(bc.Difficulty)
	if currentBlock.Hash[:bc.Difficulty] != target {
		fmt.Printf("Block %d has invalid proof of work\n", currentBlock.Index)
		return false
	}

	return true
}

// PrintBlockchain displays all blocks in the chain
func (bc *Blockchain) PrintBlockchain() {
	fmt.Println("\n========== BLOCKCHAIN ==========")
	for _, block := range bc.Blocks {
		bc.printBlock(block)
	}
	fmt.Println("================================\n")
}

// printBlock displays a single block's information
func (bc *Blockchain) printBlock(block *Block) {
	fmt.Printf("\nBlock #%d\n", block.Index)
	fmt.Printf("Timestamp: %d\n", block.Timestamp)
	fmt.Printf("Transactions: %d\n", len(block.Transactions))

	for j, tx := range block.Transactions {
		fromDisplay := tx.From
		toDisplay := tx.To
		if len(tx.From) > 8 {
			fromDisplay = tx.From[:8]
		}
		if len(tx.To) > 8 {
			toDisplay = tx.To[:8]
		}
		fmt.Printf("  TX %d: %s -> %s: %.2f\n", j, fromDisplay, toDisplay, tx.Amount)
	}

	fmt.Printf("Previous Hash: %s\n", block.PreviousHash)
	fmt.Printf("Hash: %s\n", block.Hash)
	fmt.Printf("Nonce: %d\n", block.Nonce)
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// createGenesisBlock creates the first block in the blockchain
func createGenesisBlock(difficulty int) *Block {
	genesisBlock := &Block{
		Index:        0,
		Timestamp:    time.Now().Unix(),
		Transactions: make([]*Transaction, 0),
		PreviousHash: "0",
		Nonce:        0,
	}
	genesisBlock.MineBlock(difficulty)
	return genesisBlock
}

// createTarget creates a target string with the required number of leading zeros
func createTarget(difficulty int) string {
	target := ""
	for i := 0; i < difficulty; i++ {
		target += "0"
	}
	return target
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

	return nil
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
