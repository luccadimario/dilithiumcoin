package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

// NewPool creates a new pool server
func NewPool(nodeURL, address string, stratumPort int, feePercent float64) *Pool {
	return &Pool{
		nodeURL:     nodeURL,
		address:     address,
		stratumPort: stratumPort,
		feePercent:  feePercent,
		stopCh:      make(chan struct{}),
		workers:     make(map[int64]*StratumWorker),
	}
}

// Start begins the pool server
func (p *Pool) Start() {
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
	p.workersMu.Lock()
	for _, w := range p.workers {
		w.conn.Close()
	}
	p.workersMu.Unlock()
	p.wg.Wait()
}

func (p *Pool) listen() {
	defer p.wg.Done()

	var err error
	p.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", p.stratumPort))
	if err != nil {
		fmt.Printf("Pool: Failed to listen on port %d: %v\n", p.stratumPort, err)
		return
	}
	fmt.Printf("Pool: Stratum server listening on port %d\n", p.stratumPort)

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

func (p *Pool) handleWorker(conn net.Conn) {
	defer p.wg.Done()
	defer conn.Close()

	id := atomic.AddInt64(&p.nextID, 1)
	worker := &StratumWorker{
		id:   id,
		conn: conn,
	}

	p.workersMu.Lock()
	p.workers[id] = worker
	workerCount := len(p.workers)
	p.workersMu.Unlock()

	fmt.Printf("Pool: Worker #%d connected from %s (total: %d)\n", id, conn.RemoteAddr(), workerCount)

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
			fmt.Printf("Pool: Worker #%d bad message: %v\n", id, err)
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
			p.sendResponse(worker, req.ID, nil, "unknown method")
		}
	}

	// Worker disconnected
	p.workersMu.Lock()
	delete(p.workers, id)
	remaining := len(p.workers)
	p.workersMu.Unlock()
	fmt.Printf("Pool: Worker #%d disconnected (remaining: %d)\n", id, remaining)
}

// ============================================================================
// Stratum message handlers
// ============================================================================

func (p *Pool) handleSubscribe(worker *StratumWorker, req *StratumRequest) {
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

func (p *Pool) handleAuthorize(worker *StratumWorker, req *StratumRequest) {
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

	// Ensure worker exists in database
	p.db.GetOrCreateWorker(address)

	// Send current work
	p.workMu.RLock()
	work := p.currentWork
	p.workMu.RUnlock()

	if work != nil {
		p.sendMiningWork(worker, work)
	}
}

func (p *Pool) handleSubmit(worker *StratumWorker, req *StratumRequest) {
	worker.mu.Lock()
	if !worker.authorized {
		worker.mu.Unlock()
		p.sendResponse(worker, req.ID, false, "not authorized")
		return
	}
	workerAddr := worker.address
	workerID := worker.id
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

	// Verify job ID
	if jobID != work.JobID {
		p.sendResponse(worker, req.ID, false, "stale job")
		return
	}

	// Check share difficulty
	if !meetsDifficultyBits(hash, work.ShareBits) {
		fmt.Printf("Pool: Worker #%d share REJECTED (bits: %d, hash: %s...)\n",
			workerID, work.ShareBits, hash[:16])
		p.db.RecordRejectedShare(workerAddr)
		p.sendResponse(worker, req.ID, false, "low difficulty share")
		return
	}

	// Accept share — record to database
	share := &Share{
		WorkerAddress: workerAddr,
		JobID:         jobID,
		Nonce:         nonce,
		Hash:          hash,
		Difficulty:    work.ShareBits,
		Timestamp:     time.Now().Unix(),
		BlockHeight:   work.Template.Index,
	}
	p.db.RecordShare(share)

	totalShares := p.db.data.Stats.TotalShares
	if totalShares%10 == 1 {
		fmt.Printf("Pool: Worker #%d share accepted (total: %d, hash: %s...)\n",
			workerID, totalShares, hash[:16])
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
		p.handleBlockSolution(worker, work, nonce, hash)
	}
}

// ============================================================================
// Block solution handling
// ============================================================================

func (p *Pool) handleBlockSolution(worker *StratumWorker, work *PoolWork, nonce int64, hash string) {
	// Reconstruct the block
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

	worker.mu.Lock()
	finderAddr := worker.address
	worker.mu.Unlock()

	fmt.Printf("Pool: BLOCK #%d found by worker #%d (%s)! hash: %s\n",
		block.Index, worker.id, finderAddr, hash[:16])

	// Record block
	poolBlock := &PoolBlock{
		Height:        block.Index,
		Hash:          hash,
		FinderAddress: finderAddr,
		Reward:        work.Template.Reward + totalFees,
		Timestamp:     time.Now().Unix(),
		Status:        "confirmed",
	}
	p.db.RecordBlock(poolBlock)

	// Calculate and apply PPLNS rewards
	totalReward := work.Template.Reward + totalFees
	DistributePPLNS(p.db, totalReward, p.feePercent)

	// Prune share window
	windowSize := GetPPLNSWindowSize(work.Template.DifficultyBits, work.ShareBits)
	PruneShareWindow(p.db, windowSize)

	// Immediate save
	if err := p.db.Save(); err != nil {
		fmt.Printf("Pool: Save after block failed: %v\n", err)
	}
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

// ============================================================================
// Work fetching and distribution
// ============================================================================

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

			if template.Height == lastHeight {
				continue
			}
			lastHeight = template.Height

			txs := p.fetchPendingTxs()

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
			p.workersMu.Lock()
			for _, w := range p.workers {
				p.sendMiningWork(w, work)
			}
			workerCount := len(p.workers)
			p.workersMu.Unlock()

			fmt.Printf("Pool: Distributed job %s for block #%d (diff=%d, share=%d) to %d workers\n",
				jobID, template.Index, template.DifficultyBits, shareBits, workerCount)
		}
	}
}

func (p *Pool) sendMiningWork(worker *StratumWorker, work *PoolWork) {
	// Send share difficulty
	p.sendNotification(worker, "mining.set_difficulty", []interface{}{work.ShareBits})

	// Serialize transactions
	txsJSON, _ := json.Marshal(work.Transactions)

	// Send mining.notify
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

// ============================================================================
// JSON-RPC messaging
// ============================================================================

func (p *Pool) sendResponse(worker *StratumWorker, id interface{}, result interface{}, errMsg string) {
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

func (p *Pool) sendNotification(worker *StratumWorker, method string, params []interface{}) {
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

// ============================================================================
// Stats
// ============================================================================

func (p *Pool) statsPrinter() {
	defer p.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			stats, _, activeWorkers := p.db.GetStats()

			p.workersMu.Lock()
			connectedWorkers := len(p.workers)
			p.workersMu.Unlock()

			fmt.Printf("Pool Stats: %d connected | %d active | %d blocks | %d shares\n",
				connectedWorkers, activeWorkers, stats.TotalBlocksFound, stats.TotalShares)

			// Send pool.stats to connected workers
			p.workersMu.Lock()
			for _, w := range p.workers {
				params := []interface{}{
					connectedWorkers,
					stats.TotalBlocksFound,
					stats.TotalShares,
					formatDLT(stats.TotalPaidOut),
					fmt.Sprintf("%.1f%%", p.feePercent),
				}
				p.sendNotification(w, "pool.stats", params)
			}
			p.workersMu.Unlock()
		}
	}
}

// getConnectedWorkerCount returns the number of currently connected stratum workers
func (p *Pool) getConnectedWorkerCount() int {
	p.workersMu.Lock()
	defer p.workersMu.Unlock()
	return len(p.workers)
}
