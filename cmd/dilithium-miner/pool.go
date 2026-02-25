package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// Stratum V1 JSON-RPC types
// ============================================================================

// StratumRequest is a JSON-RPC request from client to server
type StratumRequest struct {
	ID     interface{}   `json:"id"`
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

// StratumResponse is a JSON-RPC response from server to client
type StratumResponse struct {
	ID     interface{} `json:"id"`
	Result interface{} `json:"result"`
	Error  interface{} `json:"error"`
}

// StratumNotification is a server-push notification (id is null)
type StratumNotification struct {
	ID     interface{}   `json:"id"`
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

// ============================================================================
// Pool server
// ============================================================================

// Pool manages a Stratum mining pool server
type Pool struct {
	nodeURL  string
	address  string
	port     int
	fee      float64
	listener net.Listener
	stopCh   chan struct{}
	wg       sync.WaitGroup

	mu      sync.Mutex
	workers map[int64]*PoolWorker
	nextID  int64

	// Current work
	currentWork *PoolWork
	currentJob  string // current job ID
	workMu      sync.RWMutex
	nextJobID   int64

	// Stats
	blocksFound int64
	totalShares int64
	startTime   time.Time
}

// PoolWorker represents a connected pool worker
type PoolWorker struct {
	id         int64
	conn       net.Conn
	address    string // Worker's payout address
	subscribed bool
	authorized bool
	agent      string // mining software agent string
	shares     int64
	earnings   int64 // Accumulated earnings in DLT units
	mu         sync.Mutex
}

// PoolWork holds the current work template for distribution
type PoolWork struct {
	Template     *BlockTemplate `json:"template"`
	ShareBits    int            `json:"share_bits"`
	Transactions []*Transaction `json:"transactions"`
	JobID        string         `json:"job_id"`
}

// NewPool creates a new pool server
func NewPool(nodeURL, address string, port int, fee float64) *Pool {
	return &Pool{
		nodeURL: nodeURL,
		address: address,
		port:    port,
		fee:     fee,
		stopCh:  make(chan struct{}),
		workers: make(map[int64]*PoolWorker),
	}
}

// Start begins the pool server
func (p *Pool) Start() {
	p.startTime = time.Now()

	// Start work fetcher
	p.wg.Add(1)
	go p.workFetcher()

	// Start TCP listener
	p.wg.Add(1)
	go p.listen()

	// Start stats printer
	p.wg.Add(1)
	go p.statsPrinter()
}

// Stop shuts down the pool
func (p *Pool) Stop() {
	close(p.stopCh)
	if p.listener != nil {
		p.listener.Close()
	}
	p.mu.Lock()
	for _, w := range p.workers {
		w.conn.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Pool) listen() {
	defer p.wg.Done()

	var err error
	p.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", p.port))
	if err != nil {
		fmt.Printf("Pool: Failed to listen on port %d: %v\n", p.port, err)
		return
	}
	fmt.Printf("Pool: Stratum server listening on port %d\n", p.port)

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.stopCh:
				return
			default:
				fmt.Printf("Pool: Accept error: %v\n", err)
				continue
			}
		}

		p.wg.Add(1)
		go p.handleWorker(conn)
	}
}

func (p *Pool) generateJobID() string {
	id := atomic.AddInt64(&p.nextJobID, 1)
	return fmt.Sprintf("%x", id)
}

func (p *Pool) handleWorker(conn net.Conn) {
	defer p.wg.Done()
	defer conn.Close()

	id := atomic.AddInt64(&p.nextID, 1)
	worker := &PoolWorker{
		id:   id,
		conn: conn,
	}

	p.mu.Lock()
	p.workers[id] = worker
	workerCount := len(p.workers)
	p.mu.Unlock()

	fmt.Printf("Pool: Worker #%d connected from %s (total: %d)\n", id, conn.RemoteAddr(), workerCount)

	// Read JSON-RPC messages from worker
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-p.stopCh:
			return
		default:
		}

		var req StratumRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			fmt.Printf("Pool: Worker #%d bad message: %v (raw: %.100s)\n", id, err, scanner.Text())
			continue
		}

		switch req.Method {
		case "mining.subscribe":
			p.handleSubscribe(worker, &req)
		case "mining.authorize":
			p.handleAuthorize(worker, &req)
		case "mining.submit":
			p.handleSubmit(worker, &req)
		default:
			// Send error for unknown methods
			p.sendResponse(worker, req.ID, nil, "unknown method")
		}
	}

	// Worker disconnected
	p.mu.Lock()
	delete(p.workers, id)
	remaining := len(p.workers)
	p.mu.Unlock()
	fmt.Printf("Pool: Worker #%d disconnected (remaining: %d)\n", id, remaining)
}

// sendResponse sends a JSON-RPC response to a worker
func (p *Pool) sendResponse(worker *PoolWorker, id interface{}, result interface{}, errMsg string) {
	resp := StratumResponse{
		ID:     id,
		Result: result,
	}
	if errMsg != "" {
		resp.Error = []interface{}{20, errMsg, nil}
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	worker.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	worker.conn.Write(data)
}

// sendNotification sends a JSON-RPC notification to a worker
func (p *Pool) sendNotification(worker *PoolWorker, method string, params []interface{}) {
	notif := StratumNotification{
		ID:     nil,
		Method: method,
		Params: params,
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	data, err := json.Marshal(notif)
	if err != nil {
		return
	}
	data = append(data, '\n')
	worker.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	worker.conn.Write(data)
}

func (p *Pool) handleSubscribe(worker *PoolWorker, req *StratumRequest) {
	// Parse agent string if provided
	if len(req.Params) > 0 {
		if agent, ok := req.Params[0].(string); ok {
			worker.mu.Lock()
			worker.agent = agent
			worker.mu.Unlock()
		}
	}

	worker.mu.Lock()
	worker.subscribed = true
	worker.mu.Unlock()

	// Response: [[["mining.set_difficulty", "sub1"], ["mining.notify", "sub2"]], "extranonce1", extranonce2_size]
	subscriptions := []interface{}{
		[]interface{}{"mining.set_difficulty", fmt.Sprintf("%x", worker.id)},
		[]interface{}{"mining.notify", fmt.Sprintf("%x", worker.id)},
	}
	extranonce1 := fmt.Sprintf("%08x", worker.id)
	extranonce2Size := 4

	result := []interface{}{subscriptions, extranonce1, extranonce2Size}
	p.sendResponse(worker, req.ID, result, "")

	fmt.Printf("Pool: Worker #%d subscribed\n", worker.id)
}

func (p *Pool) handleAuthorize(worker *PoolWorker, req *StratumRequest) {
	// Params: ["worker_address", "password"]
	if len(req.Params) < 1 {
		p.sendResponse(worker, req.ID, false, "missing worker address")
		return
	}

	address, ok := req.Params[0].(string)
	if !ok || address == "" {
		p.sendResponse(worker, req.ID, false, "invalid worker address")
		return
	}

	worker.mu.Lock()
	worker.address = address
	worker.authorized = true
	worker.mu.Unlock()

	p.sendResponse(worker, req.ID, true, "")

	fmt.Printf("Pool: Worker #%d authorized with address %s\n", worker.id, address)

	// Send current work after authorization
	p.workMu.RLock()
	work := p.currentWork
	p.workMu.RUnlock()

	if work != nil {
		p.sendMiningWork(worker, work)
	}
}

func (p *Pool) handleSubmit(worker *PoolWorker, req *StratumRequest) {
	// Params: ["worker_address", "job_id", nonce, "hash"]
	worker.mu.Lock()
	if !worker.authorized {
		worker.mu.Unlock()
		p.sendResponse(worker, req.ID, false, "not authorized")
		return
	}
	worker.mu.Unlock()

	if len(req.Params) < 4 {
		p.sendResponse(worker, req.ID, false, "invalid params")
		return
	}

	// Parse params
	jobID, _ := req.Params[1].(string)
	var nonce int64
	switch v := req.Params[2].(type) {
	case float64:
		nonce = int64(v)
	case json.Number:
		n, _ := v.Int64()
		nonce = n
	}
	hash, _ := req.Params[3].(string)

	if hash == "" {
		p.sendResponse(worker, req.ID, false, "missing hash")
		return
	}

	// Get current work
	p.workMu.RLock()
	work := p.currentWork
	p.workMu.RUnlock()

	if work == nil {
		p.sendResponse(worker, req.ID, false, "no current work")
		return
	}

	// Verify job ID matches current work
	if jobID != work.JobID {
		p.sendResponse(worker, req.ID, false, "stale job")
		return
	}

	// Check if hash meets share difficulty
	if !meetsDifficultyBits(hash, work.ShareBits) {
		fmt.Printf("Pool: Worker #%d share REJECTED (bits: %d, hash: %s...)\n",
			worker.id, work.ShareBits, hash[:16])
		p.sendResponse(worker, req.ID, false, "low difficulty share")
		return
	}

	// Accept share
	count := atomic.AddInt64(&worker.shares, 1)
	atomic.AddInt64(&p.totalShares, 1)
	if count%10 == 1 {
		fmt.Printf("Pool: Worker #%d share accepted (total: %d, hash: %s...)\n",
			worker.id, count, hash[:16])
	}
	p.sendResponse(worker, req.ID, true, "")

	// Check if hash also meets full block difficulty
	useBits := work.Template.DifficultyBits > 0
	var meetsBlock bool
	if useBits {
		meetsBlock = meetsDifficultyBits(hash, work.Template.DifficultyBits)
	} else {
		hashPrefix := strings.Repeat("0", work.Template.Difficulty)
		meetsBlock = strings.HasPrefix(hash, hashPrefix)
	}

	if meetsBlock {
		// Worker found a block! Reconstruct and submit it.
		p.handleBlockSolution(worker, work, nonce, hash)
	}
}

// handleBlockSolution reconstructs and submits a block when a worker finds one
func (p *Pool) handleBlockSolution(worker *PoolWorker, work *PoolWork, nonce int64, hash string) {
	// Reconstruct the block from the work template
	var totalFees int64
	for _, tx := range work.Transactions {
		totalFees += tx.Fee
	}

	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        p.address,
		Amount:    work.Template.Reward + totalFees,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", work.Template.Index, time.Now().UnixNano()),
	}

	txs := make([]*Transaction, 0, len(work.Transactions)+1)
	txs = append(txs, coinbase)
	txs = append(txs, work.Transactions...)

	block := &Block{
		Index:          work.Template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   txs,
		PreviousHash:   work.Template.PreviousHash,
		Hash:           hash,
		Nonce:          nonce,
		Difficulty:     work.Template.Difficulty,
		DifficultyBits: work.Template.DifficultyBits,
	}

	// Submit to node
	if err := p.submitBlock(block); err != nil {
		fmt.Printf("Pool: Block submission failed: %v\n", err)
		return
	}

	atomic.AddInt64(&p.blocksFound, 1)
	fmt.Printf("Pool: BLOCK #%d found by worker #%d! hash: %s\n", block.Index, worker.id, hash[:16])

	// Distribute rewards
	p.distributeRewards(work.Template.Reward)

	// Reset shares for next round
	p.mu.Lock()
	for _, w := range p.workers {
		atomic.StoreInt64(&w.shares, 0)
	}
	p.mu.Unlock()
	atomic.StoreInt64(&p.totalShares, 0)
}

func (p *Pool) distributeRewards(blockReward int64) {
	totalShares := atomic.LoadInt64(&p.totalShares)
	if totalShares == 0 {
		fmt.Printf("Pool: No shares to distribute\n")
		return
	}

	// Calculate pool fee
	poolFee := int64(float64(blockReward) * (p.fee / 100.0))
	distributable := blockReward - poolFee

	fmt.Printf("Pool: Distributing reward: total=%s, fee=%s (%.1f%%), distributable=%s\n",
		formatDLT(blockReward), formatDLT(poolFee), p.fee, formatDLT(distributable))

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, w := range p.workers {
		workerShares := atomic.LoadInt64(&w.shares)
		if workerShares == 0 {
			continue
		}

		workerPayout := (distributable * workerShares) / totalShares

		w.mu.Lock()
		w.earnings += workerPayout
		workerAddr := w.address
		totalEarnings := w.earnings
		w.mu.Unlock()

		fmt.Printf("Pool: Worker #%d (%s) earned %s (shares: %d/%d, total earnings: %s)\n",
			w.id, workerAddr, formatDLT(workerPayout), workerShares, totalShares, formatDLT(totalEarnings))
	}

	fmt.Printf("Pool: Pool operator earned %s in fees\n", formatDLT(poolFee))
}

func (p *Pool) submitBlock(block *Block) error {
	data, err := json.Marshal(block)
	if err != nil {
		return err
	}

	resp, err := httpClient.Post(p.nodeURL+"/block/submit", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("cannot connect to node: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("invalid response: %s", string(body))
	}

	if !apiResp.Success {
		return fmt.Errorf("block rejected: %s", apiResp.Message)
	}
	return nil
}

func (p *Pool) workFetcher() {
	defer p.wg.Done()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastHeight int64

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			template, err := p.fetchTemplate()
			if err != nil {
				continue
			}

			// Only distribute new work when height changes
			if template.Height == lastHeight {
				continue
			}
			lastHeight = template.Height

			txs := p.fetchPendingTxs()

			// Calculate share difficulty: full difficulty minus 8 bits (easier)
			shareBits := template.DifficultyBits - 8
			if shareBits < 4 {
				shareBits = 4
			}

			jobID := p.generateJobID()
			work := &PoolWork{
				Template:     template,
				ShareBits:    shareBits,
				Transactions: txs,
				JobID:        jobID,
			}

			p.workMu.Lock()
			p.currentWork = work
			p.currentJob = jobID
			p.workMu.Unlock()

			// Broadcast to all workers
			p.mu.Lock()
			for _, w := range p.workers {
				p.sendMiningWork(w, work)
			}
			p.mu.Unlock()

			fmt.Printf("Pool: Distributed job %s for block #%d to %d workers\n", jobID, template.Index, len(p.workers))
		}
	}
}

// sendMiningWork sends mining.set_difficulty + mining.notify to a worker
func (p *Pool) sendMiningWork(worker *PoolWorker, work *PoolWork) {
	// Send mining.set_difficulty first
	p.sendNotification(worker, "mining.set_difficulty", []interface{}{work.ShareBits})

	// Serialize transactions for the notify message
	txsJSON, _ := json.Marshal(work.Transactions)

	// Send mining.notify
	// Params: [job_id, block_index, prev_hash, difficulty, difficulty_bits, reward, txs_json, pool_address, timestamp, clean_jobs]
	params := []interface{}{
		work.JobID,
		work.Template.Index,
		work.Template.PreviousHash,
		work.Template.Difficulty,
		work.Template.DifficultyBits,
		work.Template.Reward,
		string(txsJSON),
		p.address,
		time.Now().Unix(),
		true, // clean_jobs
	}
	p.sendNotification(worker, "mining.notify", params)
}

func (p *Pool) fetchTemplate() (*BlockTemplate, error) {
	resp, err := httpClient.Get(p.nodeURL + "/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("node error: %s", apiResp.Message)
	}

	height := int64(apiResp.Data["blockchain_height"].(float64))
	difficulty := int(apiResp.Data["difficulty"].(float64))
	difficultyBits := 0
	if db, ok := apiResp.Data["difficulty_bits"].(float64); ok {
		difficultyBits = int(db)
	}

	lastHash, ok := apiResp.Data["last_block_hash"].(string)
	if !ok || lastHash == "" {
		return nil, fmt.Errorf("node did not return last_block_hash")
	}

	var reward int64 = 50 * DLTUnit
	halvings := int(height) / 250000
	for i := 0; i < halvings; i++ {
		reward /= 2
	}
	if reward < 1 {
		reward = 1
	}

	return &BlockTemplate{
		Index:          height,
		PreviousHash:   lastHash,
		Difficulty:     difficulty,
		DifficultyBits: difficultyBits,
		Height:         height,
		Reward:         reward,
	}, nil
}

func (p *Pool) fetchPendingTxs() []*Transaction {
	resp, err := httpClient.Get(p.nodeURL + "/mempool")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil
	}

	txsRaw, ok := apiResp.Data["transactions"].([]interface{})
	if !ok {
		return nil
	}

	var txs []*Transaction
	for _, t := range txsRaw {
		txMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		tx := &Transaction{
			From:      getString(txMap, "from"),
			To:        getString(txMap, "to"),
			Amount:    int64(getFloat(txMap, "amount")),
			Fee:       int64(getFloat(txMap, "fee")),
			Data:      getString(txMap, "data"),
			Timestamp: int64(getFloat(txMap, "timestamp")),
			Signature: getString(txMap, "signature"),
			PublicKey: getString(txMap, "public_key"),
		}
		txs = append(txs, tx)
	}
	return txs
}

func (p *Pool) statsPrinter() {
	defer p.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.mu.Lock()
			workerCount := len(p.workers)
			p.mu.Unlock()

			blocks := atomic.LoadInt64(&p.blocksFound)
			shares := atomic.LoadInt64(&p.totalShares)
			uptime := time.Since(p.startTime).Round(time.Second)

			fmt.Printf("Pool Stats: %d workers | %d blocks found | %d shares | uptime %s\n",
				workerCount, blocks, shares, uptime)

			// Send pool.stats notification to all workers
			p.mu.Lock()
			for _, w := range p.workers {
				w.mu.Lock()
				workerShares := atomic.LoadInt64(&w.shares)
				workerEarnings := w.earnings
				w.mu.Unlock()

				params := []interface{}{
					workerCount,
					blocks,
					workerShares,
					formatDLT(workerEarnings),
					fmt.Sprintf("%.1f%%", p.fee),
				}
				p.sendNotification(w, "pool.stats", params)
			}
			p.mu.Unlock()
		}
	}
}

// getWorkerCount returns the number of connected workers
func (p *Pool) getWorkerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

// formatDLT formats an amount in DLT units to a human-readable string
func formatDLT(amount int64) string {
	whole := amount / DLTUnit
	fraction := amount % DLTUnit
	return fmt.Sprintf("%d.%08d DLT", whole, fraction)
}
