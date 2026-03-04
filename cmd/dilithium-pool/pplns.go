package main

import (
	"fmt"
)

// PPLNSWindowMultiplier determines window size relative to expected shares per block.
// Window = multiplier * (2^blockBits / 2^shareBits)
const PPLNSWindowMultiplier = 2.0

// GetPPLNSWindowSize returns the number of shares to keep in the PPLNS window.
func GetPPLNSWindowSize(blockDiffBits, shareDiffBits int) int {
	if shareDiffBits <= 0 || blockDiffBits <= 0 {
		return 1000 // safe default
	}

	// Expected shares per block = 2^(blockBits - shareBits)
	diffDelta := blockDiffBits - shareDiffBits
	if diffDelta <= 0 {
		return 100
	}

	expectedShares := 1 << diffDelta
	windowSize := int(float64(expectedShares) * PPLNSWindowMultiplier)

	if windowSize < 100 {
		windowSize = 100
	}
	if windowSize > 1_000_000 {
		windowSize = 1_000_000 // cap to prevent memory issues
	}

	return windowSize
}

// DistributePPLNS calculates PPLNS rewards from the current share window
// and credits worker balances in the database.
func DistributePPLNS(db *Database, blockReward int64, feePercent float64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Calculate pool fee
	poolFee := int64(float64(blockReward) * (feePercent / 100.0))
	distributable := blockReward - poolFee

	// Count shares per worker in the window
	shareCount := make(map[string]int64)
	var totalShares int64

	for _, share := range db.data.Shares {
		shareCount[share.WorkerAddress]++
		totalShares++
	}

	if totalShares == 0 {
		fmt.Printf("PPLNS: No shares in window, cannot distribute\n")
		return
	}

	fmt.Printf("PPLNS: Distributing %s DLT (reward=%s, fee=%s at %.1f%%) across %d shares from %d workers\n",
		formatDLT(distributable), formatDLT(blockReward), formatDLT(poolFee), feePercent,
		totalShares, len(shareCount))

	// Credit each worker proportionally
	var distributed int64
	for address, shares := range shareCount {
		payout := (distributable * shares) / totalShares
		if payout <= 0 {
			continue
		}
		distributed += payout

		w, exists := db.data.Workers[address]
		if !exists {
			w = &Worker{Address: address}
			db.data.Workers[address] = w
		}
		w.Balance += payout
		w.TotalEarned += payout

		fmt.Printf("PPLNS:   %s... earned %s DLT (%d/%d shares)\n",
			address[:12], formatDLT(payout), shares, totalShares)
	}

	// Any rounding dust goes to pool operator
	dust := distributable - distributed
	if dust > 0 {
		fmt.Printf("PPLNS:   Rounding dust: %s DLT (to pool operator)\n", formatDLT(dust))
	}

	fmt.Printf("PPLNS: Pool operator earned %s DLT in fees\n", formatDLT(poolFee+dust))
}

// PruneShareWindow keeps only the most recent windowSize shares.
func PruneShareWindow(db *Database, windowSize int) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if len(db.data.Shares) > windowSize {
		pruned := len(db.data.Shares) - windowSize
		db.data.Shares = db.data.Shares[pruned:]
		fmt.Printf("PPLNS: Pruned %d old shares, window now %d\n", pruned, len(db.data.Shares))
	}
}
