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

// Pool manages a mining pool server
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
	currentWork   *PoolWork
	workMu        sync.RWMutex

	// Stats
	blocksFound   int64
	totalShares   int64
	startTime     time.Time
}

// PoolWorker represents a connected pool worker
type PoolWorker struct {
	id       int64
	conn     net.Conn
	encoder  *json.Encoder
	address  string  // Worker's payout address
	shares   int64
	earnings int64   // Accumulated earnings in DLT units
	hashrate float64
	mu       sync.Mutex
}

// PoolWork holds the current work template for distribution
type PoolWork struct {
	Template     *BlockTemplate `json:"template"`
	ShareBits    int            `json:"share_bits"`
	Transactions []*Transaction `json:"transactions"`
}

// PoolMessage is the JSON-over-TCP protocol message
type PoolMessage struct {
	Type     string          `json:"type"`
	Template *BlockTemplate  `json:"template,omitempty"`
	ShareBits int            `json:"share_bits,omitempty"`
	Nonce    int64           `json:"nonce,omitempty"`
	Hash     string          `json:"hash,omitempty"`
	Block    *Block          `json:"block,omitempty"`
	Txs      []*Transaction  `json:"transactions,omitempty"`
	Workers  int             `json:"workers,omitempty"`
	Hashrate string          `json:"hashrate,omitempty"`
	Found    int64           `json:"blocks_found,omitempty"`
	Shares   int64           `json:"your_shares,omitempty"`
	Earnings string          `json:"your_earnings,omitempty"`
	PoolFee  string          `json:"pool_fee,omitempty"`
	Address  string          `json:"address,omitempty"`
	Threads  int             `json:"threads,omitempty"`
	Error    string          `json:"error,omitempty"`
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
	fmt.Printf("Pool: Listening on port %d\n", p.port)

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
	worker := &PoolWorker{
		id:      id,
		conn:    conn,
		encoder: json.NewEncoder(conn),
	}

	p.mu.Lock()
	p.workers[id] = worker
	workerCount := len(p.workers)
	p.mu.Unlock()

	fmt.Printf("Pool: Worker #%d connected from %s (total: %d)\n", id, conn.RemoteAddr(), workerCount)

	// Read messages from worker
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-p.stopCh:
			return
		default:
		}

		var msg PoolMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			fmt.Printf("Pool: Worker #%d bad message: %v (raw: %.100s)\n", id, err, scanner.Text())
			continue
		}

		switch msg.Type {
		case "register":
			p.handleRegister(worker, &msg)
		case "share":
			p.handleShare(worker, &msg)
		case "block":
			p.handleBlockFound(worker, &msg)
		}
	}

	// Worker disconnected
	p.mu.Lock()
	delete(p.workers, id)
	remaining := len(p.workers)
	p.mu.Unlock()
	fmt.Printf("Pool: Worker #%d disconnected (remaining: %d)\n", id, remaining)
}

func (p *Pool) handleRegister(worker *PoolWorker, msg *PoolMessage) {
	if msg.Address == "" {
		fmt.Printf("Pool: Worker #%d registration failed: no address provided\n", worker.id)
		return
	}

	worker.mu.Lock()
	worker.address = msg.Address
	worker.mu.Unlock()

	fmt.Printf("Pool: Worker #%d registered with address %s\n", worker.id, msg.Address)

	// Send current work after registration
	p.workMu.RLock()
	work := p.currentWork
	p.workMu.RUnlock()

	if work != nil {
		p.sendWork(worker, work)
	}
}

func (p *Pool) handleShare(worker *PoolWorker, msg *PoolMessage) {
	// Verify worker is registered
	worker.mu.Lock()
	if worker.address == "" {
		worker.mu.Unlock()
		fmt.Printf("Pool: Worker #%d share rejected: not registered\n", worker.id)
		return
	}
	worker.mu.Unlock()

	// Verify share meets share difficulty
	p.workMu.RLock()
	work := p.currentWork
	p.workMu.RUnlock()

	if work == nil {
		return
	}

	if meetsDifficultyBits(msg.Hash, work.ShareBits) {
		count := atomic.AddInt64(&worker.shares, 1)
		atomic.AddInt64(&p.totalShares, 1)
		if count%10 == 1 {
			fmt.Printf("Pool: Worker #%d share accepted (total: %d, hash: %s...)\n",
				worker.id, count, msg.Hash[:16])
		}
	} else {
		fmt.Printf("Pool: Worker #%d share REJECTED (bits: %d, hash: %s...)\n",
			worker.id, work.ShareBits, msg.Hash[:16])
	}
}

func (p *Pool) handleBlockFound(worker *PoolWorker, msg *PoolMessage) {
	if msg.Block == nil {
		return
	}

	// Verify the block meets full difficulty
	p.workMu.RLock()
	work := p.currentWork
	p.workMu.RUnlock()

	if work == nil {
		return
	}

	useBits := work.Template.DifficultyBits > 0
	var valid bool
	if useBits {
		valid = meetsDifficultyBits(msg.Block.Hash, work.Template.DifficultyBits)
	} else {
		hashPrefix := strings.Repeat("0", work.Template.Difficulty)
		valid = strings.HasPrefix(msg.Block.Hash, hashPrefix)
	}

	if !valid {
		fmt.Printf("Pool: Worker #%d submitted invalid block\n", worker.id)
		return
	}

	// Submit to node
	if err := p.submitBlock(msg.Block); err != nil {
		fmt.Printf("Pool: Block submission failed: %v\n", err)
		return
	}

	atomic.AddInt64(&p.blocksFound, 1)
	fmt.Printf("Pool: Block #%d found by worker #%d!\n", msg.Block.Index, worker.id)

	// Calculate and distribute rewards
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

	// Calculate each worker's payout
	for _, w := range p.workers {
		workerShares := atomic.LoadInt64(&w.shares)
		if workerShares == 0 {
			continue
		}

		// Proportional payout
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

			work := &PoolWork{
				Template:     template,
				ShareBits:    shareBits,
				Transactions: txs,
			}

			p.workMu.Lock()
			p.currentWork = work
			p.workMu.Unlock()

			// Broadcast to all workers
			p.mu.Lock()
			for _, w := range p.workers {
				p.sendWork(w, work)
			}
			p.mu.Unlock()

			fmt.Printf("Pool: Distributed work for block #%d to %d workers\n", template.Index, len(p.workers))
		}
	}
}

func (p *Pool) sendWork(worker *PoolWorker, work *PoolWork) {
	msg := PoolMessage{
		Type:      "work",
		Template:  work.Template,
		ShareBits: work.ShareBits,
		Txs:       work.Transactions,
		Address:   p.address,
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	data = append(data, '\n')
	worker.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	worker.conn.Write(data)
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

			// Send stats to all workers
			p.mu.Lock()
			for _, w := range p.workers {
				w.mu.Lock()
				workerShares := atomic.LoadInt64(&w.shares)
				workerEarnings := w.earnings
				w.mu.Unlock()

				msg := PoolMessage{
					Type:     "stats",
					Workers:  workerCount,
					Found:    blocks,
					Shares:   workerShares,
					Earnings: formatDLT(workerEarnings),
					PoolFee:  fmt.Sprintf("%.1f%%", p.fee),
				}
				w.mu.Lock()
				data, _ := json.Marshal(msg)
				data = append(data, '\n')
				w.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				w.conn.Write(data)
				w.mu.Unlock()
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
