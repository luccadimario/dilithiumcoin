package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// PoolMessage is the base message type for pool protocol
type PoolMessage struct {
	Type string `json:"type"` // "work", "share", "block", "stats"
}

// PoolWorkMessage is sent by the pool server with mining work
type PoolWorkMessage struct {
	Type         string         `json:"type"` // "work"
	Template     *BlockTemplate `json:"template"`
	ShareBits    int            `json:"share_bits"`
	Address      string         `json:"address"`
	Transactions []*Transaction `json:"transactions"`
}

// PoolShareMessage is sent by the worker to submit a share
type PoolShareMessage struct {
	Type  string `json:"type"` // "share"
	Nonce int64  `json:"nonce"`
	Hash  string `json:"hash"`
}

// PoolBlockMessage is sent by the worker to submit a full block
type PoolBlockMessage struct {
	Type  string `json:"type"` // "block"
	Block *Block `json:"block"`
}

// PoolStatsMessage is sent by the pool with statistics
type PoolStatsMessage struct {
	Type        string `json:"type"` // "stats"
	Workers     int    `json:"workers"`
	BlocksFound int    `json:"blocks_found"`
	YourShares  int    `json:"your_shares"`
}

// PoolWorker connects to a mining pool and submits shares
type PoolWorker struct {
	poolAddr  string
	address   string
	threads   int
	useGPU    bool
	gpuDevice int
	batchSize uint64

	// Stats
	sharesSubmitted atomic.Int64
	blocksFound     atomic.Int64
	totalHashes     atomic.Int64
	startTime       time.Time

	// Control
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewPoolWorker creates a new pool mining worker
func NewPoolWorker(poolAddr, address string, threads int, useGPU bool, gpuDevice int, batchSize uint64) *PoolWorker {
	return &PoolWorker{
		poolAddr:  poolAddr,
		address:   address,
		threads:   threads,
		useGPU:    useGPU,
		gpuDevice: gpuDevice,
		batchSize: batchSize,
		stopCh:    make(chan struct{}),
	}
}

// Start begins pool mining
func (pw *PoolWorker) Start() error {
	if pw.useGPU {
		if !GPUMiningAvailable {
			return fmt.Errorf("GPU mining not available - rebuild with CUDA support")
		}
		if err := GPUInit(pw.gpuDevice); err != nil {
			return fmt.Errorf("failed to initialize GPU: %w", err)
		}
	}

	pw.startTime = time.Now()

	// Start stats reporter
	pw.wg.Add(1)
	go pw.statsLoop()

	// Start mining loop
	pw.wg.Add(1)
	go pw.miningLoop()

	return nil
}

// Stop halts pool mining
func (pw *PoolWorker) Stop() {
	close(pw.stopCh)
	pw.wg.Wait()
	if pw.useGPU {
		GPUCleanup()
	}
}

// miningLoop connects to the pool and processes work
func (pw *PoolWorker) miningLoop() {
	defer pw.wg.Done()

	for {
		select {
		case <-pw.stopCh:
			return
		default:
		}

		if err := pw.runPoolSession(); err != nil {
			fmt.Printf("[!] Pool connection error: %v\n", err)
			fmt.Printf("[~] Reconnecting in 5 seconds...\n")
			pw.sleep(5 * time.Second)
		}
	}
}

// runPoolSession handles a single pool connection session
func (pw *PoolWorker) runPoolSession() error {
	fmt.Printf("[*] Connecting to pool: %s\n", pw.poolAddr)

	conn, err := net.DialTimeout("tcp", pw.poolAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	fmt.Printf("[+] Connected to pool\n")

	encoder := json.NewEncoder(conn)

	// Send registration with address
	if pw.address != "" {
		regMsg := map[string]interface{}{
			"type":    "register",
			"address": pw.address,
			"threads": pw.threads,
		}
		if err := encoder.Encode(regMsg); err != nil {
			return fmt.Errorf("failed to send registration: %w", err)
		}
		fmt.Printf("[+] Registered with address: %s\n", pw.address)
	}

	// Channel for incoming messages from the pool
	msgCh := make(chan []byte, 4)
	errCh := make(chan error, 1)

	// Reader goroutine: reads messages from pool connection
	go func() {
		reader := bufio.NewReader(conn)
		for {
			conn.SetReadDeadline(time.Now().Add(120 * time.Second))
			line, err := reader.ReadBytes('\n')
			if err != nil {
				errCh <- fmt.Errorf("read error: %w", err)
				return
			}
			msgCh <- line
		}
	}()

	// Mining context — cancelled when new work arrives or session ends
	var miningCancel context.CancelFunc
	var miningWg sync.WaitGroup

	cancelMining := func() {
		if miningCancel != nil {
			miningCancel()
			miningWg.Wait()
			miningCancel = nil
		}
	}
	defer cancelMining()

	// Mutex for encoder (shared between mining goroutine and main loop)
	var encoderMu sync.Mutex

	for {
		select {
		case <-pw.stopCh:
			cancelMining()
			return nil

		case err := <-errCh:
			cancelMining()
			return err

		case line := <-msgCh:
			var baseMsg PoolMessage
			if err := json.Unmarshal(line, &baseMsg); err != nil {
				fmt.Printf("[!] Invalid pool message: %v\n", err)
				continue
			}

			switch baseMsg.Type {
			case "work":
				var workMsg PoolWorkMessage
				if err := json.Unmarshal(line, &workMsg); err != nil {
					fmt.Printf("[!] Invalid work message: %v\n", err)
					continue
				}

				fmt.Printf("[*] Received work: block #%d | share difficulty: %d bits | block difficulty: %d bits\n",
					workMsg.Template.Index, workMsg.ShareBits, workMsg.Template.DifficultyBits)

				// Cancel any current mining
				cancelMining()

				// Start mining in background
				var ctx context.Context
				ctx, miningCancel = context.WithCancel(context.Background())
				miningWg.Add(1)
				go func() {
					defer miningWg.Done()
					pw.mineAndSubmitShares(ctx, &workMsg, encoder, &encoderMu)
				}()

			case "stats":
				var statsMsg PoolStatsMessage
				if err := json.Unmarshal(line, &statsMsg); err == nil {
					fmt.Printf("[i] Pool stats: workers=%d blocks=%d your_shares=%d\n",
						statsMsg.Workers, statsMsg.BlocksFound, statsMsg.YourShares)
				}

			default:
				fmt.Printf("[~] Unknown pool message type: %s\n", baseMsg.Type)
			}
		}
	}
}

// mineAndSubmitShares continuously mines and submits shares until context is cancelled.
func (pw *PoolWorker) mineAndSubmitShares(ctx context.Context, work *PoolWorkMessage, encoder *json.Encoder, encoderMu *sync.Mutex) {
	// Build block from template
	block := &Block{
		Index:          work.Template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   work.Transactions,
		PreviousHash:   work.Template.PreviousHash,
		Difficulty:     work.Template.Difficulty,
		DifficultyBits: work.Template.DifficultyBits,
	}

	// Prepare mining data
	prefix, suffix := pw.buildHashInput(block)

	// Compute midstate
	h := sha256.New()
	fullBlockLen := (len(prefix) / 64) * 64
	if fullBlockLen > 0 {
		h.Write(prefix[:fullBlockLen])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlockLen:]

	fmt.Printf("[*] Mining block #%d with %d %s...\n", block.Index, pw.threads,
		map[bool]string{true: "GPU", false: "CPU threads"}[pw.useGPU])

	// Continuously mine and submit shares
	for {
		select {
		case <-ctx.Done():
			return
		case <-pw.stopCh:
			return
		default:
		}

		result, found := pw.mineWithWorkersCtx(ctx, midstate, prefixTail, suffix, work.ShareBits, work.Template.DifficultyBits)
		if !found {
			return
		}

		// Set block nonce and hash
		block.Nonce = result.Nonce
		block.Hash = hashToHex(result.Hash)

		// Submit share
		if meetsDifficultyBytes(result.Hash, work.ShareBits) {
			encoderMu.Lock()
			err := encoder.Encode(&PoolShareMessage{
				Type:  "share",
				Nonce: result.Nonce,
				Hash:  block.Hash,
			})
			encoderMu.Unlock()
			if err != nil {
				fmt.Printf("[!] Failed to submit share: %v\n", err)
				return
			}
			pw.sharesSubmitted.Add(1)
			fmt.Printf("[+] Share submitted: %s...\n", block.Hash[:16])
		}

		// Also check if it meets full block difficulty
		if meetsDifficultyBytes(result.Hash, work.Template.DifficultyBits) {
			encoderMu.Lock()
			err := encoder.Encode(&PoolBlockMessage{
				Type:  "block",
				Block: block,
			})
			encoderMu.Unlock()
			if err != nil {
				fmt.Printf("[!] Failed to submit block: %v\n", err)
				return
			}
			pw.blocksFound.Add(1)
			fmt.Printf("[+] BLOCK FOUND! Hash: %s...\n", block.Hash[:16])
			return
		}
	}
}

// buildHashInput is the same as in miner.go
func (pw *PoolWorker) buildHashInput(block *Block) (prefix, suffix []byte) {
	txJSON, _ := json.Marshal(block.Transactions)

	prefixStr := strconv.FormatInt(block.Index, 10) +
		strconv.FormatInt(block.Timestamp, 10) +
		string(txJSON) +
		block.PreviousHash

	suffixStr := strconv.Itoa(block.Difficulty)

	return []byte(prefixStr), []byte(suffixStr)
}

// mineWithWorkersCtx launches CPU or GPU workers with an external context
func (pw *PoolWorker) mineWithWorkersCtx(ctx context.Context, midstate, prefixTail, suffix []byte, shareBits, blockBits int) (MiningResult, bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan MiningResult, 1)

	// Use share difficulty (lower) for mining
	diffBits := shareBits

	if pw.useGPU {
		// GPU mining
		worker := &GPUWorker{
			ID:        0,
			DeviceID:  pw.gpuDevice,
			BatchSize: pw.batchSize,
		}
		go func() {
			worker.Mine(ctx, midstate, prefixTail, suffix, 0, 1, diffBits, resultCh)
		}()

		select {
		case result := <-resultCh:
			pw.totalHashes.Add(worker.HashCount.Load())
			return result, true
		case <-pw.stopCh:
			cancel()
			pw.totalHashes.Add(worker.HashCount.Load())
			return MiningResult{}, false
		}
	} else {
		// CPU mining
		var workerWg sync.WaitGroup
		workers := make([]*CPUWorker, pw.threads)

		for i := 0; i < pw.threads; i++ {
			workers[i] = &CPUWorker{ID: i}
			workerWg.Add(1)
			go func(w *CPUWorker, startNonce int64) {
				defer workerWg.Done()
				w.Mine(ctx, midstate, prefixTail, suffix,
					startNonce, int64(pw.threads), diffBits, resultCh)
			}(workers[i], int64(i))
		}

		// Wait for result or cancellation
		select {
		case result := <-resultCh:
			cancel()
			workerWg.Wait()
			for _, w := range workers {
				pw.totalHashes.Add(w.HashCount.Load())
			}
			return result, true
		case <-pw.stopCh:
			cancel()
			workerWg.Wait()
			for _, w := range workers {
				pw.totalHashes.Add(w.HashCount.Load())
			}
			return MiningResult{}, false
		}
	}
}

// statsLoop prints periodic statistics
func (pw *PoolWorker) statsLoop() {
	defer pw.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(pw.startTime).Seconds()
			hashes := pw.totalHashes.Load()
			shares := pw.sharesSubmitted.Load()
			blocks := pw.blocksFound.Load()

			rate := float64(hashes) / elapsed
			if rate > 1e6 {
				fmt.Printf("[i] Hashrate: %.2f MH/s | Shares: %d | Blocks: %d | Hashes: %d\n",
					rate/1e6, shares, blocks, hashes)
			} else {
				fmt.Printf("[i] Hashrate: %.0f KH/s | Shares: %d | Blocks: %d | Hashes: %d\n",
					rate/1e3, shares, blocks, hashes)
			}

		case <-pw.stopCh:
			return
		}
	}
}

// sleep waits for duration or until stopped
func (pw *PoolWorker) sleep(d time.Duration) {
	select {
	case <-time.After(d):
	case <-pw.stopCh:
	}
}
