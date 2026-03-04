package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// PoolDatabase is the root structure persisted to pool_db.json
type PoolDatabase struct {
	Version    int                `json:"version"`
	Timestamp  int64              `json:"timestamp"`
	PoolWallet string             `json:"pool_wallet"`
	Workers    map[string]*Worker `json:"workers"`
	Shares     []*Share           `json:"shares"`
	Blocks     []*PoolBlock       `json:"blocks"`
	Payouts    []*Payout          `json:"payouts"`
	Stats      *PoolStats         `json:"stats"`
}

// Database manages persistent storage with atomic writes
type Database struct {
	path string
	data *PoolDatabase
	mu   sync.RWMutex
}

// NewDatabase creates or loads a database from the given path
func NewDatabase(path string) (*Database, error) {
	d := &Database{
		path: path,
		data: &PoolDatabase{
			Version: 1,
			Workers: make(map[string]*Worker),
			Shares:  make([]*Share, 0),
			Blocks:  make([]*PoolBlock, 0),
			Payouts: make([]*Payout, 0),
			Stats:   &PoolStats{PoolStartTime: time.Now().Unix()},
		},
	}

	if err := d.Load(); err != nil {
		return nil, err
	}

	return d, nil
}

// Load reads database from disk
func (d *Database) Load() error {
	data, err := os.ReadFile(d.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // New database
		}
		return fmt.Errorf("read failed: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := json.Unmarshal(data, d.data); err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	// Ensure maps are initialized
	if d.data.Workers == nil {
		d.data.Workers = make(map[string]*Worker)
	}
	if d.data.Stats == nil {
		d.data.Stats = &PoolStats{PoolStartTime: time.Now().Unix()}
	}

	fmt.Printf("Database: Loaded %d workers, %d shares, %d blocks, %d payouts\n",
		len(d.data.Workers), len(d.data.Shares), len(d.data.Blocks), len(d.data.Payouts))

	return nil
}

// Save writes database to disk atomically (temp file + rename)
func (d *Database) Save() error {
	d.mu.RLock()
	d.data.Timestamp = time.Now().Unix()
	data, err := json.MarshalIndent(d.data, "", "  ")
	d.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	tmpPath := d.path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp failed: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write failed: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync failed: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, d.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename failed: %w", err)
	}

	return nil
}

// StartAutoSave begins automatic periodic saves
func (d *Database) StartAutoSave(interval time.Duration, stopCh chan struct{}, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				d.Save() // Final save on shutdown
				return
			case <-ticker.C:
				if err := d.Save(); err != nil {
					fmt.Printf("Database: Auto-save failed: %v\n", err)
				}
			}
		}
	}()
}

// ============================================================================
// Worker operations
// ============================================================================

// GetOrCreateWorker returns an existing worker or creates a new one
func (d *Database) GetOrCreateWorker(address string) *Worker {
	d.mu.Lock()
	defer d.mu.Unlock()

	w, exists := d.data.Workers[address]
	if !exists {
		w = &Worker{
			Address:  address,
			JoinedAt: time.Now().Unix(),
		}
		d.data.Workers[address] = w
	}
	return w
}

// RecordShare adds a share and updates worker stats
func (d *Database) RecordShare(share *Share) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.data.Shares = append(d.data.Shares, share)
	d.data.Stats.TotalShares++

	w, exists := d.data.Workers[share.WorkerAddress]
	if !exists {
		w = &Worker{
			Address:  share.WorkerAddress,
			JoinedAt: time.Now().Unix(),
		}
		d.data.Workers[share.WorkerAddress] = w
	}
	w.SharesAccepted++
	w.LastSeen = share.Timestamp
}

// RecordRejectedShare increments the rejected share count
func (d *Database) RecordRejectedShare(address string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	w, exists := d.data.Workers[address]
	if exists {
		w.SharesRejected++
	}
}

// RecordBlock adds a found block to history
func (d *Database) RecordBlock(block *PoolBlock) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.data.Blocks = append(d.data.Blocks, block)
	d.data.Stats.TotalBlocksFound++
}

// RecordPayout adds a payout to history and updates worker balance
func (d *Database) RecordPayout(payout *Payout) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.data.Payouts = append(d.data.Payouts, payout)

	w, exists := d.data.Workers[payout.WorkerAddress]
	if exists {
		w.TotalPaid += payout.Amount
		w.Balance -= payout.Amount
		if w.Balance < 0 {
			w.Balance = 0
		}
	}
	d.data.Stats.TotalPaidOut += payout.Amount
}

// GetWorkersForPayout returns workers with balance >= minPayout
func (d *Database) GetWorkersForPayout(minPayout int64) []*Worker {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*Worker
	for _, w := range d.data.Workers {
		if w.Balance >= minPayout {
			// Return a copy
			copy := *w
			result = append(result, &copy)
		}
	}
	return result
}

// DeductBalance subtracts amount from a worker's balance (called after successful payout)
func (d *Database) DeductBalance(address string, amount int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	w, exists := d.data.Workers[address]
	if exists {
		w.Balance -= amount
		if w.Balance < 0 {
			w.Balance = 0
		}
		w.TotalPaid += amount
	}
	d.data.Stats.TotalPaidOut += amount
}

// ============================================================================
// Read-only accessors for dashboard
// ============================================================================

// GetStats returns a copy of pool stats plus computed values
func (d *Database) GetStats() (PoolStats, int, int) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := *d.data.Stats
	totalWorkers := len(d.data.Workers)

	// Count active workers (seen in last 10 minutes)
	activeWorkers := 0
	cutoff := time.Now().Unix() - 600
	for _, w := range d.data.Workers {
		if w.LastSeen >= cutoff {
			activeWorkers++
		}
	}

	return stats, totalWorkers, activeWorkers
}

// GetWorkers returns a copy of all workers
func (d *Database) GetWorkers() []*Worker {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*Worker, 0, len(d.data.Workers))
	for _, w := range d.data.Workers {
		copy := *w
		result = append(result, &copy)
	}
	return result
}

// GetWorkerByAddress returns a copy of a specific worker
func (d *Database) GetWorkerByAddress(address string) (*Worker, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	w, exists := d.data.Workers[address]
	if !exists {
		return nil, false
	}
	copy := *w
	return &copy, true
}

// GetRecentBlocks returns the last n blocks
func (d *Database) GetRecentBlocks(n int) []*PoolBlock {
	d.mu.RLock()
	defer d.mu.RUnlock()

	blocks := d.data.Blocks
	if len(blocks) > n {
		blocks = blocks[len(blocks)-n:]
	}
	result := make([]*PoolBlock, len(blocks))
	for i, b := range blocks {
		copy := *b
		result[i] = &copy
	}
	return result
}

// GetRecentPayouts returns the last n payouts
func (d *Database) GetRecentPayouts(n int) []*Payout {
	d.mu.RLock()
	defer d.mu.RUnlock()

	payouts := d.data.Payouts
	if len(payouts) > n {
		payouts = payouts[len(payouts)-n:]
	}
	result := make([]*Payout, len(payouts))
	for i, p := range payouts {
		copy := *p
		result[i] = &copy
	}
	return result
}

// GetSharesInWindow returns the current share count
func (d *Database) GetSharesInWindow() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.data.Shares)
}

// GetWorkerSharesInWindow counts a specific worker's shares in the PPLNS window
func (d *Database) GetWorkerSharesInWindow(address string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	count := 0
	for _, s := range d.data.Shares {
		if s.WorkerAddress == address {
			count++
		}
	}
	return count
}

// GetWorkerPayouts returns all payouts for a specific worker
func (d *Database) GetWorkerPayouts(address string) []*Payout {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*Payout
	for _, p := range d.data.Payouts {
		if p.WorkerAddress == address {
			copy := *p
			result = append(result, &copy)
		}
	}
	return result
}
