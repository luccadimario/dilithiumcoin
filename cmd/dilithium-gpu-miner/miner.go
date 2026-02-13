package main

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Miner coordinates CPU or GPU workers to mine dilithium blocks.
type Miner struct {
	client  *NodeClient
	address string
	threads int

	// GPU settings
	useGPU    bool
	gpuDevice int
	batchSize uint64

	// Stats
	blocksMined atomic.Int64
	totalHashes atomic.Int64
	earnings    atomic.Int64
	startTime   time.Time

	// Control
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewMiner creates a new mining coordinator.
func NewMiner(nodeURL, address string, threads int) *Miner {
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	return &Miner{
		client:  NewNodeClient(nodeURL),
		address: address,
		threads: threads,
		stopCh:  make(chan struct{}),
	}
}

// Start begins the mining loop.
func (m *Miner) Start() error {
	if err := m.client.CheckConnection(); err != nil {
		return err
	}

	// Initialize GPU if needed
	if m.useGPU {
		if !GPUMiningAvailable {
			return fmt.Errorf("GPU mining not available - rebuild with CUDA support")
		}
		if err := GPUInit(m.gpuDevice); err != nil {
			return fmt.Errorf("failed to initialize GPU: %w", err)
		}
	}

	m.startTime = time.Now()

	// Start stats reporter
	m.wg.Add(1)
	go m.statsLoop()

	// Start main mining loop
	m.wg.Add(1)
	go m.miningLoop()

	return nil
}

// Stop halts all mining activity.
func (m *Miner) Stop() {
	close(m.stopCh)
	m.wg.Wait()
	if m.useGPU {
		GPUCleanup()
	}
}

// waitForSync blocks until the node is synced with the real network.
// Requires: (1) at least one peer connected, (2) height > 0, (3) height stable for 10 seconds.
func (m *Miner) waitForSync() {
	fmt.Println("[*] Waiting for node to connect to peers and sync...")

	// Phase 1: Wait for at least one peer
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		peers, err := m.client.GetPeerCount()
		if err == nil && peers > 0 {
			fmt.Printf("[+] Connected to %d peer(s)\n", peers)
			break
		}
		fmt.Println("[~] No peers yet, waiting...")
		m.sleep(3 * time.Second)
	}

	// Phase 2: Wait for height > 0 (chain data received from peers)
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		height, err := m.client.GetCurrentHeight()
		if err == nil && height > 0 {
			fmt.Printf("[~] Chain height: %d, waiting for sync to complete...\n", height)
			break
		}
		m.sleep(2 * time.Second)
	}

	// Phase 3: Wait for height to stabilize (not advancing for 5 checks = 10 seconds)
	var lastHeight int64
	stableCount := 0

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		height, err := m.client.GetCurrentHeight()
		if err != nil {
			m.sleep(2 * time.Second)
			continue
		}

		if height == lastHeight {
			stableCount++
		} else {
			if lastHeight > 0 {
				fmt.Printf("[~] Syncing... height: %d (+%d)\n", height, height-lastHeight)
			}
			stableCount = 0
			lastHeight = height
		}

		// Height stable for 5 checks (10 seconds) = likely synced
		if stableCount >= 5 {
			fmt.Printf("[+] Node synced at height %d\n", height)
			return
		}

		m.sleep(2 * time.Second)
	}
}

// miningLoop continuously fetches work and mines blocks.
// Includes pending mempool transactions alongside the coinbase reward.
func (m *Miner) miningLoop() {
	defer m.wg.Done()

	// Wait for node to finish syncing before mining
	m.waitForSync()

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		// Fetch work template
		template, err := m.client.GetWork()
		if err != nil {
			fmt.Printf("[!] Error getting work: %v\n", err)
			m.sleep(5 * time.Second)
			continue
		}

		// Fetch pending transactions from mempool
		pendingTxs := m.client.GetPendingTransactions()
		if len(pendingTxs) > 0 {
			fmt.Printf("[*] Including %d mempool transaction(s) in block\n", len(pendingTxs))
		}

		// Build block with coinbase + pending transactions
		block := m.constructBlock(template, pendingTxs)

		// Prepare mining data: compute prefix, suffix, and midstate
		prefix, suffix := m.buildHashInput(block)

		// Determine difficulty
		diffBits := template.DifficultyBits
		if diffBits <= 0 {
			diffBits = template.Difficulty * 4
		}

		if m.useGPU {
			fmt.Printf("[*] Mining block #%d | difficulty: %d bits (%d hex) | GPU device %d\n",
				template.Index, template.DifficultyBits, template.Difficulty, m.gpuDevice)
		} else {
			if template.DifficultyBits > 0 {
				fmt.Printf("[*] Mining block #%d | difficulty: %d bits (%d hex) | %d threads\n",
					template.Index, template.DifficultyBits, template.Difficulty, m.threads)
			} else {
				fmt.Printf("[*] Mining block #%d | difficulty: %d hex digits | %d threads\n",
					template.Index, template.Difficulty, m.threads)
			}
		}

		// Mine!
		result, found := m.mineWithWorkers(prefix, suffix, diffBits, template.Height)
		if !found {
			continue
		}

		// Set block fields from result
		block.Nonce = result.Nonce
		block.Hash = hashToHex(result.Hash)

		// Verify hash matches what the node expects (sanity check)
		expectedHash := m.calculateBlockHashGo(block)
		if block.Hash != expectedHash {
			fmt.Printf("[!] HASH MISMATCH: computed=%s expected=%s\n", block.Hash, expectedHash)
			fmt.Printf("[!] This is a bug — skipping submission\n")
			continue
		}

		// Submit block
		if err := m.client.SubmitBlock(block); err != nil {
			fmt.Printf("[!] Block rejected: %v\n", err)
			continue
		}

		// Block accepted!
		m.blocksMined.Add(1)
		m.earnings.Add(template.Reward)
		fmt.Printf("[+] BLOCK #%d MINED AND ACCEPTED! Hash: %s... | Reward: %s DLT | Total: %d blocks, %s DLT\n",
			block.Index, block.Hash[:16],
			FormatDLT(template.Reward),
			m.blocksMined.Load(), FormatDLT(m.earnings.Load()))
	}
}

// constructBlock builds a block with coinbase + pending transactions, ready for mining.
func (m *Miner) constructBlock(template *BlockTemplate, pendingTxs []*Transaction) *Block {
	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        m.address,
		Amount:    template.Reward,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", template.Index, time.Now().UnixNano()),
	}

	txs := make([]*Transaction, 0, 1+len(pendingTxs))
	txs = append(txs, coinbase)
	txs = append(txs, pendingTxs...)

	return &Block{
		Index:          template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   txs,
		PreviousHash:   template.PreviousHash,
		Difficulty:     template.Difficulty,
		DifficultyBits: template.DifficultyBits,
	}
}

// buildHashInput computes the fixed prefix and suffix for hash computation.
// Block hash = SHA256(Index + Timestamp + txJSON + PreviousHash + Nonce + Difficulty)
// prefix = everything before Nonce, suffix = everything after Nonce.
func (m *Miner) buildHashInput(block *Block) (prefix, suffix []byte) {
	txJSON, _ := json.Marshal(block.Transactions)

	prefixStr := strconv.FormatInt(block.Index, 10) +
		strconv.FormatInt(block.Timestamp, 10) +
		string(txJSON) +
		block.PreviousHash

	suffixStr := strconv.Itoa(block.Difficulty)

	return []byte(prefixStr), []byte(suffixStr)
}

// calculateBlockHashGo computes the block hash using the same method as the node.
// Used only for verification — not in the hot loop.
func (m *Miner) calculateBlockHashGo(block *Block) string {
	txJSON, _ := json.Marshal(block.Transactions)
	blockData := strconv.FormatInt(block.Index, 10) +
		strconv.FormatInt(block.Timestamp, 10) +
		string(txJSON) +
		block.PreviousHash +
		strconv.FormatInt(block.Nonce, 10) +
		strconv.Itoa(block.Difficulty)

	state := InitSHA256()
	data := []byte(blockData)
	fullBlocks := (len(data) / 64) * 64
	if fullBlocks > 0 {
		state.ProcessBlocks(data[:fullBlocks])
	}
	hash := MineHash(state.H, data[fullBlocks:], state.Len)
	return hashToHex(hash)
}

// mineWithWorkers launches CPU or GPU workers to search for a valid nonce.
func (m *Miner) mineWithWorkers(prefix, suffix []byte, diffBits int, templateHeight int64) (MiningResult, bool) {
	// Compute SHA-256 midstate using stdlib (hardware-accelerated)
	h := sha256.New()
	fullBlockLen := (len(prefix) / 64) * 64
	if fullBlockLen > 0 {
		h.Write(prefix[:fullBlockLen])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlockLen:]

	fmt.Printf("[*] Prefix: %d bytes | Midstate: %d blocks skipped | Tail: %d bytes\n",
		len(prefix), fullBlockLen/64, len(prefixTail))

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan MiningResult, 1)

	if m.useGPU {
		// Launch single GPU worker
		worker := &GPUWorker{
			ID:        0,
			DeviceID:  m.gpuDevice,
			BatchSize: m.batchSize,
		}
		go func() {
			worker.Mine(ctx, midstate, prefixTail, suffix, 0, 1, diffBits, resultCh)
		}()

		// Monitor for results or stale work
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		hashStart := m.totalHashes.Load()
		startTime := time.Now()

		for {
			select {
			case result := <-resultCh:
				cancel()
				m.totalHashes.Add(worker.HashCount.Load())

				elapsed := time.Since(startTime).Seconds()
				hashes := m.totalHashes.Load() - hashStart
				if elapsed > 0 {
					fmt.Printf("[+] Found hash after %d hashes (%.2f MH/s)\n",
						hashes, float64(hashes)/elapsed/1e6)
				}
				return result, true

			case <-ticker.C:
				currentHeight, err := m.client.GetCurrentHeight()
				if err == nil && currentHeight > templateHeight {
					fmt.Printf("[~] Chain advanced to %d, restarting with new work...\n", currentHeight)
					cancel()
					m.totalHashes.Add(worker.HashCount.Load())
					return MiningResult{}, false
				}

				hashes := worker.HashCount.Load()
				elapsed := time.Since(startTime).Seconds()
				if elapsed > 0 {
					rate := float64(hashes) / elapsed
					if rate > 1e6 {
						fmt.Printf("[~] Hashrate: %.2f MH/s | Hashes: %d\n", rate/1e6, hashes)
					} else {
						fmt.Printf("[~] Hashrate: %.0f KH/s | Hashes: %d\n", rate/1e3, hashes)
					}
				}

			case <-m.stopCh:
				cancel()
				m.totalHashes.Add(worker.HashCount.Load())
				return MiningResult{}, false
			}
		}
	}

	// CPU mining
	var workerWg sync.WaitGroup
	workers := make([]*CPUWorker, m.threads)

	for i := 0; i < m.threads; i++ {
		workers[i] = &CPUWorker{ID: i}
		workerWg.Add(1)
		go func(w *CPUWorker, startNonce int64) {
			defer workerWg.Done()
			w.Mine(ctx, midstate, prefixTail, suffix,
				startNonce, int64(m.threads), diffBits, resultCh)
		}(workers[i], int64(i))
	}

	// Monitor for results, stale work, or shutdown
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	hashStart := m.totalHashes.Load()
	startTime := time.Now()

	for {
		select {
		case result := <-resultCh:
			cancel()
			workerWg.Wait()

			// Accumulate hash counts
			for _, w := range workers {
				m.totalHashes.Add(w.HashCount.Load())
			}

			elapsed := time.Since(startTime).Seconds()
			hashes := m.totalHashes.Load() - hashStart
			if elapsed > 0 {
				fmt.Printf("[+] Found hash after %d hashes (%.2f MH/s)\n",
					hashes, float64(hashes)/elapsed/1e6)
			}
			return result, true

		case <-ticker.C:
			// Check if chain has advanced (stale work detection)
			currentHeight, err := m.client.GetCurrentHeight()
			if err == nil && currentHeight > templateHeight {
				fmt.Printf("[~] Chain advanced to %d, restarting with new work...\n", currentHeight)
				cancel()
				workerWg.Wait()
				for _, w := range workers {
					m.totalHashes.Add(w.HashCount.Load())
				}
				return MiningResult{}, false
			}

			// Print live hashrate
			var totalWorkerHashes int64
			for _, w := range workers {
				totalWorkerHashes += w.HashCount.Load()
			}
			elapsed := time.Since(startTime).Seconds()
			if elapsed > 0 {
				rate := float64(totalWorkerHashes) / elapsed
				if rate > 1e6 {
					fmt.Printf("[~] Hashrate: %.2f MH/s | Hashes: %d\n", rate/1e6, totalWorkerHashes)
				} else {
					fmt.Printf("[~] Hashrate: %.0f KH/s | Hashes: %d\n", rate/1e3, totalWorkerHashes)
				}
			}

		case <-m.stopCh:
			cancel()
			workerWg.Wait()
			for _, w := range workers {
				m.totalHashes.Add(w.HashCount.Load())
			}
			return MiningResult{}, false
		}
	}
}

// statsLoop prints periodic statistics.
func (m *Miner) statsLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(m.startTime).Seconds()
			totalH := m.totalHashes.Load()
			blocks := m.blocksMined.Load()
			earn := m.earnings.Load()

			fmt.Printf("[i] Session: %.0fs | Total hashes: %d | Blocks: %d | Earnings: %s DLT\n",
				elapsed, totalH, blocks, FormatDLT(earn))

		case <-m.stopCh:
			return
		}
	}
}

// sleep waits for duration or until stopped.
func (m *Miner) sleep(d time.Duration) {
	select {
	case <-time.After(d):
	case <-m.stopCh:
	}
}
