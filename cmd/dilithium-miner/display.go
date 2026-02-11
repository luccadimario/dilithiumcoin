package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

// PrintStats displays final mining statistics
func (m *Miner) PrintStats() {
	elapsed := time.Since(m.startTime)
	blocks := atomic.LoadInt64(&m.blocksMined)
	hashes := atomic.LoadInt64(&m.totalHashes)
	earnings := atomic.LoadInt64(&m.earnings)

	fmt.Println()
	fmt.Println("========== MINING STATS ==========")
	fmt.Printf("Duration:      %s\n", elapsed.Round(time.Second))
	fmt.Printf("Blocks Mined:  %d\n", blocks)
	fmt.Printf("Total Hashes:  %d\n", hashes)
	if elapsed.Seconds() > 0 {
		fmt.Printf("Avg Hashrate:  %.0f H/s\n", float64(hashes)/elapsed.Seconds())
	}
	fmt.Printf("Earnings:      %s DLT\n", FormatDLT(earnings))
	fmt.Println("==================================")
}
