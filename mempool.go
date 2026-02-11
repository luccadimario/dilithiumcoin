package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ============================================================================
// MEMPOOL CONFIGURATION
// ============================================================================

// MempoolConfig holds mempool configuration
type MempoolConfig struct {
	MaxSize         int           // Maximum number of transactions
	MaxTxSize       int           // Maximum transaction size in bytes
	MinFee          int64         // Minimum transaction fee
	ExpiryTime      time.Duration // Time after which transactions expire
	MaxOrphans      int           // Maximum orphan transactions
	PruneInterval   time.Duration // How often to prune expired transactions
}

// DefaultMempoolConfig returns default mempool configuration
func DefaultMempoolConfig() *MempoolConfig {
	return &MempoolConfig{
		MaxSize:       10000,
		MaxTxSize:     100 * 1024, // 100KB
		MinFee:        0,          // No minimum fee for now
		ExpiryTime:    72 * time.Hour,
		MaxOrphans:    100,
		PruneInterval: 10 * time.Minute,
	}
}

// ============================================================================
// MEMPOOL ENTRY
// ============================================================================

// MempoolEntry wraps a transaction with metadata
type MempoolEntry struct {
	Tx          *Transaction
	AddedAt     time.Time
	Size        int   // Serialized size
	Fee         int64 // Transaction fee
	FeePerByte  int64 // Fee per byte for prioritization
}

// IsExpired checks if the transaction has expired
func (e *MempoolEntry) IsExpired(expiry time.Duration) bool {
	return time.Since(e.AddedAt) > expiry
}

// ============================================================================
// ENHANCED MEMPOOL
// ============================================================================

// Mempool manages pending transactions
type Mempool struct {
	config     *MempoolConfig
	entries    map[string]*MempoolEntry // signature -> entry
	byAddress  map[string][]string      // from address -> signatures
	mutex      sync.RWMutex
	blockchain *Blockchain              // Reference for validation

	stopCh     chan struct{}
}

// NewMempool creates a new mempool
func NewMempool(config *MempoolConfig, blockchain *Blockchain) *Mempool {
	if config == nil {
		config = DefaultMempoolConfig()
	}

	mp := &Mempool{
		config:     config,
		entries:    make(map[string]*MempoolEntry),
		byAddress:  make(map[string][]string),
		blockchain: blockchain,
		stopCh:     make(chan struct{}),
	}

	return mp
}

// Start starts the mempool background tasks
func (mp *Mempool) Start() {
	go mp.pruneLoop()
}

// Stop stops the mempool background tasks
func (mp *Mempool) Stop() {
	close(mp.stopCh)
}

// ============================================================================
// TRANSACTION MANAGEMENT
// ============================================================================

// AddTransaction adds a transaction to the mempool after validation
func (mp *Mempool) AddTransaction(tx *Transaction) error {
	// Validate first
	if err := mp.ValidateTransaction(tx); err != nil {
		return err
	}

	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	// Check if already in mempool
	if _, exists := mp.entries[tx.Signature]; exists {
		return nil // Already have it
	}

	// Check size limit
	if len(mp.entries) >= mp.config.MaxSize {
		return fmt.Errorf("mempool is full")
	}

	// Create entry
	entry := &MempoolEntry{
		Tx:         tx,
		AddedAt:    time.Now(),
		Size:       len(tx.ToJSON()),
		Fee:        0, // Fee calculation would go here
		FeePerByte: 0,
	}

	mp.entries[tx.Signature] = entry

	// Index by sender address
	mp.byAddress[tx.From] = append(mp.byAddress[tx.From], tx.Signature)

	return nil
}

// AddTransactionIfNew adds a transaction and returns true if it was new
func (mp *Mempool) AddTransactionIfNew(tx *Transaction) (bool, error) {
	mp.mutex.RLock()
	_, exists := mp.entries[tx.Signature]
	mp.mutex.RUnlock()

	if exists {
		return false, nil
	}

	if err := mp.AddTransaction(tx); err != nil {
		return false, err
	}

	return true, nil
}

// RemoveTransaction removes a transaction from the mempool
func (mp *Mempool) RemoveTransaction(signature string) {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	entry, exists := mp.entries[signature]
	if !exists {
		return
	}

	// Remove from main map
	delete(mp.entries, signature)

	// Remove from address index
	if sigs, ok := mp.byAddress[entry.Tx.From]; ok {
		for i, sig := range sigs {
			if sig == signature {
				mp.byAddress[entry.Tx.From] = append(sigs[:i], sigs[i+1:]...)
				break
			}
		}
		if len(mp.byAddress[entry.Tx.From]) == 0 {
			delete(mp.byAddress, entry.Tx.From)
		}
	}
}

// RemoveTransactions removes multiple transactions
func (mp *Mempool) RemoveTransactions(txs []*Transaction) {
	for _, tx := range txs {
		mp.RemoveTransaction(tx.Signature)
	}
}

// GetTransaction returns a transaction by signature
func (mp *Mempool) GetTransaction(signature string) *Transaction {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	if entry, exists := mp.entries[signature]; exists {
		return entry.Tx
	}
	return nil
}

// HasTransaction checks if a transaction exists
func (mp *Mempool) HasTransaction(signature string) bool {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()
	_, exists := mp.entries[signature]
	return exists
}

// GetAllTransactions returns all transactions
func (mp *Mempool) GetAllTransactions() []*Transaction {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	txs := make([]*Transaction, 0, len(mp.entries))
	for _, entry := range mp.entries {
		txs = append(txs, entry.Tx)
	}
	return txs
}

// GetTransactionsForBlock returns transactions for mining, ordered by fee
func (mp *Mempool) GetTransactionsForBlock(maxCount int) []*Transaction {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	// Get all entries
	entries := make([]*MempoolEntry, 0, len(mp.entries))
	for _, entry := range mp.entries {
		entries = append(entries, entry)
	}

	// Sort by fee per byte (highest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].FeePerByte > entries[j].FeePerByte
	})

	// Return up to maxCount transactions
	count := len(entries)
	if count > maxCount {
		count = maxCount
	}

	txs := make([]*Transaction, count)
	for i := 0; i < count; i++ {
		txs[i] = entries[i].Tx
	}

	return txs
}

// GetTransactionsByAddress returns all transactions from an address
func (mp *Mempool) GetTransactionsByAddress(address string) []*Transaction {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	sigs, ok := mp.byAddress[address]
	if !ok {
		return nil
	}

	txs := make([]*Transaction, 0, len(sigs))
	for _, sig := range sigs {
		if entry, exists := mp.entries[sig]; exists {
			txs = append(txs, entry.Tx)
		}
	}
	return txs
}

// Size returns the number of transactions in the mempool
func (mp *Mempool) Size() int {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()
	return len(mp.entries)
}

// ============================================================================
// TRANSACTION VALIDATION
// ============================================================================

// ValidationResult contains validation details
type ValidationResult struct {
	Valid   bool
	Code    RejectCode
	Reason  string
}

// ValidateTransaction performs full transaction validation
func (mp *Mempool) ValidateTransaction(tx *Transaction) error {
	// Basic field validation
	if tx.From == "" {
		return fmt.Errorf("missing sender address")
	}
	if tx.To == "" {
		return fmt.Errorf("missing recipient address")
	}
	if tx.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if tx.Signature == "" {
		return fmt.Errorf("missing signature")
	}

	// Size validation
	txSize := len(tx.ToJSON())
	if txSize > mp.config.MaxTxSize {
		return fmt.Errorf("transaction too large: %d bytes (max %d)", txSize, mp.config.MaxTxSize)
	}

	// Timestamp validation (not too far in future)
	if tx.Timestamp > time.Now().Add(2*time.Hour).Unix() {
		return fmt.Errorf("transaction timestamp too far in future")
	}

	// System transactions bypass signature verification
	if tx.From == "SYSTEM" {
		return nil
	}

	// Use shared signature verification from blockchain.go
	if err := VerifyTransactionSignature(tx); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// Use shared address verification from blockchain.go
	if err := VerifyAddressMatchesPublicKey(tx.From, tx.PublicKey); err != nil {
		return fmt.Errorf("address verification failed: %w", err)
	}

	return nil
}

// ============================================================================
// MEMPOOL STATISTICS
// ============================================================================

// MempoolStats contains mempool statistics
type MempoolStats struct {
	Size          int     `json:"size"`
	Bytes         int     `json:"bytes"`
	MaxSize       int     `json:"max_size"`
	Usage         float64 `json:"usage_percent"`
	OldestTxAge   int64   `json:"oldest_tx_age_seconds"`
}

// GetStats returns mempool statistics
func (mp *Mempool) GetStats() *MempoolStats {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	totalBytes := 0
	var oldestTime time.Time

	for _, entry := range mp.entries {
		totalBytes += entry.Size
		if oldestTime.IsZero() || entry.AddedAt.Before(oldestTime) {
			oldestTime = entry.AddedAt
		}
	}

	var oldestAge int64
	if !oldestTime.IsZero() {
		oldestAge = int64(time.Since(oldestTime).Seconds())
	}

	usage := float64(len(mp.entries)) / float64(mp.config.MaxSize) * 100

	return &MempoolStats{
		Size:        len(mp.entries),
		Bytes:       totalBytes,
		MaxSize:     mp.config.MaxSize,
		Usage:       usage,
		OldestTxAge: oldestAge,
	}
}

// ============================================================================
// MAINTENANCE
// ============================================================================

// pruneLoop periodically removes expired transactions
func (mp *Mempool) pruneLoop() {
	ticker := time.NewTicker(mp.config.PruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-mp.stopCh:
			return
		case <-ticker.C:
			mp.PruneExpired()
		}
	}
}

// PruneExpired removes expired transactions
func (mp *Mempool) PruneExpired() int {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	pruned := 0
	for sig, entry := range mp.entries {
		if entry.IsExpired(mp.config.ExpiryTime) {
			delete(mp.entries, sig)

			// Clean up address index
			if sigs, ok := mp.byAddress[entry.Tx.From]; ok {
				for i, s := range sigs {
					if s == sig {
						mp.byAddress[entry.Tx.From] = append(sigs[:i], sigs[i+1:]...)
						break
					}
				}
			}

			pruned++
		}
	}

	if pruned > 0 {
		fmt.Printf("Pruned %d expired transactions from mempool\n", pruned)
	}

	return pruned
}

// Clear removes all transactions from the mempool
func (mp *Mempool) Clear() {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	mp.entries = make(map[string]*MempoolEntry)
	mp.byAddress = make(map[string][]string)
}

// ============================================================================
// MEMPOOL SYNC
// ============================================================================

// GetInventory returns inventory vectors for all mempool transactions
func (mp *Mempool) GetInventory() []*InvVector {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	inv := make([]*InvVector, 0, len(mp.entries))
	for sig := range mp.entries {
		inv = append(inv, NewInvVector(InvTypeTx, sig))
	}
	return inv
}

// GetMissingTransactions returns signatures of transactions we don't have
func (mp *Mempool) GetMissingTransactions(inventory []*InvVector) []string {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	var missing []string
	for _, inv := range inventory {
		if inv.Type == InvTypeTx {
			if _, exists := mp.entries[inv.Hash]; !exists {
				missing = append(missing, inv.Hash)
			}
		}
	}
	return missing
}
