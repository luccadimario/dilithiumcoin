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

// ============================================================================
// Stratum V1 JSON-RPC types
// ============================================================================

// StratumRequest is a JSON-RPC request
type StratumRequest struct {
	ID     interface{}   `json:"id"`
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

// StratumResponse is a JSON-RPC response
type StratumResponse struct {
	ID     interface{} `json:"id"`
	Result interface{} `json:"result"`
	Error  interface{} `json:"error"`
}

// ============================================================================
// Pool worker (Stratum client)
// ============================================================================

// PoolWorker connects to a Stratum mining pool and submits shares
type PoolWorker struct {
	poolAddr  string
	address   string
	threads   int
	useGPU    bool
	gpuDevice int
	batchSize uint64

	// Stratum state
	nextReqID atomic.Int64
	jobID     string
	shareBits int
	jobMu     sync.Mutex

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

	pw.wg.Add(1)
	go pw.statsLoop()

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

func (pw *PoolWorker) getNextID() int64 {
	return pw.nextReqID.Add(1)
}

func (pw *PoolWorker) sendJSON(conn net.Conn, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(data)
	return err
}

// runPoolSession handles a single Stratum pool connection session
func (pw *PoolWorker) runPoolSession() error {
	fmt.Printf("[*] Connecting to pool: %s\n", pw.poolAddr)

	conn, err := net.DialTimeout("tcp", pw.poolAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	fmt.Printf("[+] Connected to pool\n")

	// Stratum handshake: subscribe
	subReq := StratumRequest{
		ID:     pw.getNextID(),
		Method: "mining.subscribe",
		Params: []interface{}{"dilithium-cpu-gpu-miner/1.0"},
	}
	if err := pw.sendJSON(conn, subReq); err != nil {
		return fmt.Errorf("failed to send subscribe: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Read subscribe response
	if !scanner.Scan() {
		return fmt.Errorf("no subscribe response")
	}
	var subResp StratumResponse
	if err := json.Unmarshal(scanner.Bytes(), &subResp); err != nil {
		return fmt.Errorf("bad subscribe response: %w", err)
	}
	if subResp.Error != nil {
		return fmt.Errorf("subscribe error: %v", subResp.Error)
	}
	fmt.Printf("[+] Stratum: Subscribed\n")

	// Stratum handshake: authorize
	authReq := StratumRequest{
		ID:     pw.getNextID(),
		Method: "mining.authorize",
		Params: []interface{}{pw.address, "x"},
	}
	if err := pw.sendJSON(conn, authReq); err != nil {
		return fmt.Errorf("failed to send authorize: %w", err)
	}

	// Read authorize response
	if !scanner.Scan() {
		return fmt.Errorf("no authorize response")
	}
	var authResp StratumResponse
	if err := json.Unmarshal(scanner.Bytes(), &authResp); err != nil {
		return fmt.Errorf("bad authorize response: %w", err)
	}
	if authResp.Error != nil {
		return fmt.Errorf("authorize error: %v", authResp.Error)
	}
	fmt.Printf("[+] Stratum: Authorized with address: %s\n", pw.address)

	// Channel for incoming messages
	msgCh := make(chan []byte, 4)
	errCh := make(chan error, 1)

	go func() {
		for scanner.Scan() {
			line := make([]byte, len(scanner.Bytes()))
			copy(line, scanner.Bytes())
			msgCh <- line
		}
		errCh <- fmt.Errorf("read error: connection closed")
	}()

	// Mining context
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

	var connMu sync.Mutex

	for {
		select {
		case <-pw.stopCh:
			cancelMining()
			return nil

		case err := <-errCh:
			cancelMining()
			return err

		case line := <-msgCh:
			var raw map[string]interface{}
			if err := json.Unmarshal(line, &raw); err != nil {
				fmt.Printf("[!] Invalid pool message: %v\n", err)
				continue
			}

			method, _ := raw["method"].(string)
			switch method {
			case "mining.set_difficulty":
				params, ok := raw["params"].([]interface{})
				if ok && len(params) >= 1 {
					if bits, ok := params[0].(float64); ok {
						pw.jobMu.Lock()
						pw.shareBits = int(bits)
						pw.jobMu.Unlock()
						fmt.Printf("[*] Stratum: Share difficulty set to %d bits\n", int(bits))
					}
				}

			case "mining.notify":
				params, ok := raw["params"].([]interface{})
				if !ok || len(params) < 10 {
					fmt.Printf("[!] Invalid notify message\n")
					continue
				}

				// Params: [job_id, block_index, prev_hash, difficulty, difficulty_bits, reward, txs_json, pool_address, timestamp, clean_jobs]
				jobID, _ := params[0].(string)
				blockIndex := int64(params[1].(float64))
				prevHash, _ := params[2].(string)
				difficulty := int(params[3].(float64))
				difficultyBits := int(params[4].(float64))
				reward := int64(params[5].(float64))
				txsJSON, _ := params[6].(string)
				poolAddress, _ := params[7].(string)

				pw.jobMu.Lock()
				pw.jobID = jobID
				shareBits := pw.shareBits
				pw.jobMu.Unlock()

				// Parse transactions
				var txs []*Transaction
				if txsJSON != "" && txsJSON != "null" {
					json.Unmarshal([]byte(txsJSON), &txs)
				}

				template := &BlockTemplate{
					Index:          blockIndex,
					PreviousHash:   prevHash,
					Difficulty:     difficulty,
					DifficultyBits: difficultyBits,
					Height:         blockIndex,
					Reward:         reward,
				}

				fmt.Printf("[*] Stratum: Job %s - block #%d | share difficulty: %d bits | block difficulty: %d bits\n",
					jobID, blockIndex, shareBits, difficultyBits)

				// Cancel current mining
				cancelMining()

				// Build work message for mining
				workMsg := &PoolWorkMessage{
					Template:     template,
					ShareBits:    shareBits,
					Address:      poolAddress,
					Transactions: txs,
					JobID:        jobID,
				}

				var ctx context.Context
				ctx, miningCancel = context.WithCancel(context.Background())
				miningWg.Add(1)
				go func() {
					defer miningWg.Done()
					pw.mineAndSubmitShares(ctx, workMsg, conn, &connMu)
				}()

			case "pool.stats":
				params, ok := raw["params"].([]interface{})
				if ok && len(params) >= 3 {
					workers := int(params[0].(float64))
					blocks := int64(params[1].(float64))
					shares := int64(params[2].(float64))
					fmt.Printf("[i] Pool stats: workers=%d blocks=%d your_shares=%d\n",
						workers, blocks, shares)
				}

			default:
				// Might be a submit response
				if raw["result"] != nil || raw["error"] != nil {
					if raw["error"] != nil {
						fmt.Printf("[!] Stratum submit error: %v\n", raw["error"])
					}
				}
			}
		}
	}
}

// PoolWorkMessage holds work data from a mining.notify
type PoolWorkMessage struct {
	Template     *BlockTemplate
	ShareBits    int
	Address      string
	Transactions []*Transaction
	JobID        string
}

// mineAndSubmitShares continuously mines and submits shares until context is cancelled
func (pw *PoolWorker) mineAndSubmitShares(ctx context.Context, work *PoolWorkMessage, conn net.Conn, connMu *sync.Mutex) {
	var totalFees int64
	for _, tx := range work.Transactions {
		totalFees += tx.Fee
	}

	coinbaseAddr := work.Address
	if coinbaseAddr == "" {
		coinbaseAddr = pw.address
	}
	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        coinbaseAddr,
		Amount:    work.Template.Reward + totalFees,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", work.Template.Index, time.Now().UnixNano()),
	}

	txs := make([]*Transaction, 0, 1+len(work.Transactions))
	txs = append(txs, coinbase)
	txs = append(txs, work.Transactions...)

	block := &Block{
		Index:          work.Template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   txs,
		MerkleRoot:     computeMerkleRoot(txs),
		PreviousHash:   work.Template.PreviousHash,
		Difficulty:     work.Template.Difficulty,
		DifficultyBits: work.Template.DifficultyBits,
	}

	prefix, suffix := pw.buildHashInput(block)

	h := sha256.New()
	fullBlockLen := (len(prefix) / 64) * 64
	if fullBlockLen > 0 {
		h.Write(prefix[:fullBlockLen])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlockLen:]

	fmt.Printf(">> \"%s\" <<\n", trekQuote())
	fmt.Printf("[*] Mining block #%d with %d %s...\n", block.Index, pw.threads,
		map[bool]string{true: "GPU", false: "CPU threads"}[pw.useGPU])

	var nonceOffset int64

	for {
		select {
		case <-ctx.Done():
			return
		case <-pw.stopCh:
			return
		default:
		}

		result, found := pw.mineWithWorkersCtx(ctx, midstate, prefixTail, suffix, work.ShareBits, work.Template.DifficultyBits, nonceOffset)
		if !found {
			return
		}

		// Advance nonce past the found result so next iteration explores new hashes
		nonceOffset = result.Nonce + 1

		block.Nonce = result.Nonce
		block.Hash = hashToHex(result.Hash)

		// Submit via Stratum mining.submit
		if meetsDifficultyBytes(result.Hash, work.ShareBits) {
			pw.submitWork(conn, connMu, work.JobID, result.Nonce, block.Hash)
			pw.sharesSubmitted.Add(1)
			fmt.Printf("[+] Share submitted: %s...\n", block.Hash[:16])
		}

		if meetsDifficultyBytes(result.Hash, work.Template.DifficultyBits) {
			pw.blocksFound.Add(1)
			fmt.Printf("[+] BLOCK FOUND! Hash: %s...\n", block.Hash[:16])
			return
		}
	}
}

// submitWork sends a mining.submit request
func (pw *PoolWorker) submitWork(conn net.Conn, connMu *sync.Mutex, jobID string, nonce int64, hash string) {
	req := StratumRequest{
		ID:     pw.getNextID(),
		Method: "mining.submit",
		Params: []interface{}{pw.address, jobID, nonce, hash},
	}

	data, err := json.Marshal(req)
	if err != nil {
		fmt.Printf("[!] Failed to marshal submit: %v\n", err)
		return
	}
	data = append(data, '\n')

	connMu.Lock()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(data)
	connMu.Unlock()

	if err != nil {
		fmt.Printf("[!] Failed to submit share: %v\n", err)
	}
}

// buildHashInput is the same as in miner.go
func (pw *PoolWorker) buildHashInput(block *Block) (prefix, suffix []byte) {
	var txData string
	if block.Index >= MerkleRootForkHeight {
		txData = block.MerkleRoot
	} else {
		txJSON, _ := json.Marshal(block.Transactions)
		txData = string(txJSON)
	}

	prefixStr := strconv.FormatInt(block.Index, 10) +
		strconv.FormatInt(block.Timestamp, 10) +
		txData +
		block.PreviousHash

	suffixStr := strconv.Itoa(block.Difficulty)

	return []byte(prefixStr), []byte(suffixStr)
}

// mineWithWorkersCtx launches CPU or GPU workers with an external context
func (pw *PoolWorker) mineWithWorkersCtx(ctx context.Context, midstate, prefixTail, suffix []byte, shareBits, blockBits int, nonceOffset int64) (MiningResult, bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan MiningResult, 1)

	diffBits := shareBits

	if pw.useGPU {
		worker := &GPUWorker{
			ID:        0,
			DeviceID:  pw.gpuDevice,
			BatchSize: pw.batchSize,
		}
		go func() {
			worker.Mine(ctx, midstate, prefixTail, suffix, nonceOffset, 1, diffBits, resultCh)
		}()

		select {
		case result := <-resultCh:
			pw.totalHashes.Add(worker.HashCount.Load())
			return result, true
		case <-ctx.Done():
			pw.totalHashes.Add(worker.HashCount.Load())
			return MiningResult{}, false
		case <-pw.stopCh:
			cancel()
			pw.totalHashes.Add(worker.HashCount.Load())
			return MiningResult{}, false
		}
	} else {
		var workerWg sync.WaitGroup
		workers := make([]*CPUWorker, pw.threads)

		for i := 0; i < pw.threads; i++ {
			workers[i] = &CPUWorker{ID: i}
			workerWg.Add(1)
			go func(w *CPUWorker, startNonce int64) {
				defer workerWg.Done()
				w.Mine(ctx, midstate, prefixTail, suffix,
					startNonce, int64(pw.threads), diffBits, resultCh)
			}(workers[i], nonceOffset+int64(i))
		}

		select {
		case result := <-resultCh:
			cancel()
			workerWg.Wait()
			for _, w := range workers {
				pw.totalHashes.Add(w.HashCount.Load())
			}
			return result, true
		case <-ctx.Done():
			workerWg.Wait()
			for _, w := range workers {
				pw.totalHashes.Add(w.HashCount.Load())
			}
			return MiningResult{}, false
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
