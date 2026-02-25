package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// PoolClient connects to a Stratum pool server as a worker
type PoolClient struct {
	poolAddr string
	address  string // Worker's payout address
	threads  int
	conn     net.Conn
	stopCh   chan struct{}
	wg       sync.WaitGroup

	// Stratum state
	nextReqID  int64
	jobID      string
	shareBits  int
	jobMu      sync.Mutex

	// Current mining state
	miningMu    sync.Mutex
	cancelCh    chan struct{} // close to cancel current mining
	totalHashes int64
	shares      int64
	startTime   time.Time
}

// NewPoolClient creates a new pool worker client
func NewPoolClient(poolAddr, address string, threads int) *PoolClient {
	return &PoolClient{
		poolAddr: poolAddr,
		address:  address,
		threads:  threads,
		stopCh:   make(chan struct{}),
	}
}

// Start connects to the pool and begins mining
func (pc *PoolClient) Start() {
	pc.startTime = time.Now()
	pc.wg.Add(1)
	go pc.run()
}

// Stop disconnects from the pool
func (pc *PoolClient) Stop() {
	close(pc.stopCh)
	if pc.conn != nil {
		pc.conn.Close()
	}
	pc.miningMu.Lock()
	if pc.cancelCh != nil {
		select {
		case <-pc.cancelCh:
		default:
			close(pc.cancelCh)
		}
	}
	pc.miningMu.Unlock()
	pc.wg.Wait()

	hashes := atomic.LoadInt64(&pc.totalHashes)
	shares := atomic.LoadInt64(&pc.shares)
	elapsed := time.Since(pc.startTime).Seconds()
	if elapsed > 0 {
		fmt.Printf("Pool worker stats: %d hashes, %d shares, %.0f H/s avg\n",
			hashes, shares, float64(hashes)/elapsed)
	}
}

func (pc *PoolClient) run() {
	defer pc.wg.Done()

	for {
		select {
		case <-pc.stopCh:
			return
		default:
		}

		err := pc.connectAndWork()
		if err != nil {
			fmt.Printf("Pool connection error: %v\n", err)
		}

		select {
		case <-pc.stopCh:
			return
		case <-time.After(5 * time.Second):
			fmt.Println("Reconnecting to pool...")
		}
	}
}

func (pc *PoolClient) connectAndWork() error {
	var err error
	pc.conn, err = net.DialTimeout("tcp", pc.poolAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to pool: %w", err)
	}
	defer pc.conn.Close()

	fmt.Printf("Connected to pool at %s\n", pc.poolAddr)

	// Stratum handshake: subscribe then authorize
	if err := pc.sendSubscribe(); err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}

	scanner := bufio.NewScanner(pc.conn)
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
	fmt.Printf("Stratum: Subscribed to pool\n")

	// Send authorize
	if err := pc.sendAuthorize(); err != nil {
		return fmt.Errorf("authorize failed: %w", err)
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
	fmt.Printf("Stratum: Authorized with address %s\n", pc.address)

	// Main message loop - handle notifications
	for scanner.Scan() {
		select {
		case <-pc.stopCh:
			return nil
		default:
		}

		// Try to parse as notification (has method field)
		var raw map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}

		method, _ := raw["method"].(string)
		switch method {
		case "mining.set_difficulty":
			pc.handleSetDifficulty(raw)
		case "mining.notify":
			pc.handleNotify(raw)
		case "pool.stats":
			pc.handlePoolStats(raw)
		default:
			// Could be a response to mining.submit
			if raw["result"] != nil || raw["error"] != nil {
				// Submit response - already logged on send
				if raw["error"] != nil {
					fmt.Printf("Stratum: Submit error: %v\n", raw["error"])
				}
			}
		}
	}

	return fmt.Errorf("pool connection closed")
}

func (pc *PoolClient) getNextID() int64 {
	return atomic.AddInt64(&pc.nextReqID, 1)
}

func (pc *PoolClient) sendSubscribe() error {
	req := StratumRequest{
		ID:     pc.getNextID(),
		Method: "mining.subscribe",
		Params: []interface{}{"dilithium-miner/1.0"},
	}
	return pc.sendJSON(req)
}

func (pc *PoolClient) sendAuthorize() error {
	req := StratumRequest{
		ID:     pc.getNextID(),
		Method: "mining.authorize",
		Params: []interface{}{pc.address, "x"},
	}
	return pc.sendJSON(req)
}

func (pc *PoolClient) sendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	pc.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = pc.conn.Write(data)
	return err
}

func (pc *PoolClient) handleSetDifficulty(raw map[string]interface{}) {
	params, ok := raw["params"].([]interface{})
	if !ok || len(params) < 1 {
		return
	}
	bits, ok := params[0].(float64)
	if !ok {
		return
	}
	pc.jobMu.Lock()
	pc.shareBits = int(bits)
	pc.jobMu.Unlock()
	fmt.Printf("Stratum: Share difficulty set to %d bits\n", int(bits))
}

func (pc *PoolClient) handleNotify(raw map[string]interface{}) {
	params, ok := raw["params"].([]interface{})
	if !ok || len(params) < 10 {
		return
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
	timestamp := int64(params[8].(float64))
	cleanJobs, _ := params[9].(bool)

	// Parse transactions
	var txs []*Transaction
	if txsJSON != "" && txsJSON != "null" {
		json.Unmarshal([]byte(txsJSON), &txs)
	}

	pc.jobMu.Lock()
	pc.jobID = jobID
	shareBits := pc.shareBits
	pc.jobMu.Unlock()

	fmt.Printf(">> \"%s\" <<\n", trekQuote())
	fmt.Printf("Stratum: New job %s - block #%d (difficulty bits: %d, share bits: %d)\n",
		jobID, blockIndex, difficultyBits, shareBits)

	// Cancel any existing mining
	pc.miningMu.Lock()
	if pc.cancelCh != nil {
		select {
		case <-pc.cancelCh:
		default:
			close(pc.cancelCh)
		}
	}
	pc.cancelCh = make(chan struct{})
	cancelCh := pc.cancelCh
	pc.miningMu.Unlock()

	template := &BlockTemplate{
		Index:          blockIndex,
		PreviousHash:   prevHash,
		Difficulty:     difficulty,
		DifficultyBits: difficultyBits,
		Height:         blockIndex,
		Reward:         reward,
	}

	_ = timestamp
	_ = cleanJobs

	// Mine in background
	pc.wg.Add(1)
	go func() {
		defer pc.wg.Done()
		pc.mineForPool(template, txs, poolAddress, shareBits, jobID, cancelCh)
	}()
}

func (pc *PoolClient) handlePoolStats(raw map[string]interface{}) {
	params, ok := raw["params"].([]interface{})
	if !ok || len(params) < 5 {
		return
	}
	workers := int(params[0].(float64))
	blocks := int64(params[1].(float64))
	shares := int64(params[2].(float64))
	earnings, _ := params[3].(string)
	poolFee, _ := params[4].(string)

	if earnings != "" {
		fmt.Printf("Pool: %d workers | %d blocks found | your shares: %d | earnings: %s | fee: %s\n",
			workers, blocks, shares, earnings, poolFee)
	} else {
		fmt.Printf("Pool: %d workers | %d blocks found | your shares: %d\n",
			workers, blocks, shares)
	}
}

func (pc *PoolClient) mineForPool(template *BlockTemplate, txs []*Transaction, poolAddress string, shareBits int, jobID string, cancelCh chan struct{}) {
	// Sum transaction fees
	var totalFees int64
	for _, tx := range txs {
		totalFees += tx.Fee
	}

	// Build coinbase + transactions
	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        poolAddress,
		Amount:    template.Reward + totalFees,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", template.Index, time.Now().UnixNano()),
	}

	allTxs := make([]*Transaction, 0, len(txs)+1)
	allTxs = append(allTxs, coinbase)
	allTxs = append(allTxs, txs...)

	baseBlock := &Block{
		Index:          template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   allTxs,
		PreviousHash:   template.PreviousHash,
		Difficulty:     template.Difficulty,
		DifficultyBits: template.DifficultyBits,
	}

	useBits := template.DifficultyBits > 0
	hashPrefix := strings.Repeat("0", template.Difficulty)

	resultCh := make(chan *Block, 1)
	shareCh := make(chan *Block, 16)
	var mineCancel chan struct{} = make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < pc.threads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			nonce := int64(threadID)
			var localHashes int64

			threadBlock := &Block{
				Index:          baseBlock.Index,
				Timestamp:      baseBlock.Timestamp,
				Transactions:   baseBlock.Transactions,
				PreviousHash:   baseBlock.PreviousHash,
				Difficulty:     baseBlock.Difficulty,
				DifficultyBits: baseBlock.DifficultyBits,
			}

			for {
				select {
				case <-mineCancel:
					atomic.AddInt64(&pc.totalHashes, localHashes)
					return
				case <-cancelCh:
					atomic.AddInt64(&pc.totalHashes, localHashes)
					return
				case <-pc.stopCh:
					atomic.AddInt64(&pc.totalHashes, localHashes)
					return
				default:
				}

				threadBlock.Nonce = nonce
				hash := calculateBlockHash(threadBlock)
				localHashes++

				// Check full difficulty first
				var meetsFullDifficulty bool
				if useBits {
					meetsFullDifficulty = meetsDifficultyBits(hash, template.DifficultyBits)
				} else {
					meetsFullDifficulty = strings.HasPrefix(hash, hashPrefix)
				}

				if meetsFullDifficulty {
					threadBlock.Hash = hash
					atomic.AddInt64(&pc.totalHashes, localHashes)
					select {
					case resultCh <- threadBlock:
						close(mineCancel)
					default:
					}
					return
				}

				// Check share difficulty
				if meetsDifficultyBits(hash, shareBits) {
					shareBlock := &Block{
						Index: threadBlock.Index,
						Nonce: nonce,
						Hash:  hash,
					}
					select {
					case shareCh <- shareBlock:
					default:
					}
				}

				nonce += int64(pc.threads)

				if localHashes%100000 == 0 {
					atomic.AddInt64(&pc.totalHashes, localHashes)
					localHashes = 0
				}
			}
		}(i)
	}

	// Share submitter goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case share := <-shareCh:
				pc.submitWork(share.Nonce, share.Hash, jobID)
			case <-mineCancel:
				for {
					select {
					case share := <-shareCh:
						pc.submitWork(share.Nonce, share.Hash, jobID)
					default:
						return
					}
				}
			case <-cancelCh:
				return
			case <-pc.stopCh:
				return
			}
		}
	}()

	// Wait for block found or cancellation
	select {
	case block := <-resultCh:
		if block != nil {
			fmt.Printf("BLOCK FOUND! #%d hash: %s\n", block.Index, block.Hash[:16])
			pc.submitWork(block.Nonce, block.Hash, jobID)
		}
	case <-cancelCh:
		close(mineCancel)
	case <-pc.stopCh:
		close(mineCancel)
	}

	wg.Wait()
}

// submitWork sends a mining.submit request to the pool
func (pc *PoolClient) submitWork(nonce int64, hash string, jobID string) {
	req := StratumRequest{
		ID:     pc.getNextID(),
		Method: "mining.submit",
		Params: []interface{}{pc.address, jobID, nonce, hash},
	}

	if pc.conn != nil {
		pc.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		data, err := json.Marshal(req)
		if err != nil {
			return
		}
		data = append(data, '\n')
		pc.conn.Write(data)
		atomic.AddInt64(&pc.shares, 1)
	}
}
