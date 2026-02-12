package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DLTUnit is the number of base units in 1 DLT
const DLTUnit int64 = 100_000_000

// meetsDifficultyBits checks if a hash meets N leading zero bits
func meetsDifficultyBits(hash string, bits int) bool {
	if bits <= 0 {
		return true
	}
	hashBytes, err := hex.DecodeString(hash)
	if err != nil || len(hashBytes) < (bits+7)/8 {
		return false
	}
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
		if hashBytes[i] != 0 {
			return false
		}
	}
	remainingBits := bits % 8
	if remainingBits > 0 {
		mask := byte(0xFF) << uint(8-remainingBits)
		if hashBytes[fullBytes]&mask != 0 {
			return false
		}
	}
	return true
}

// FormatDLT formats base units as a human-readable DLT string
func FormatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// Miner handles the mining loop
type Miner struct {
	nodeURL    string
	address    string
	threads    int
	stopCh     chan struct{}
	wg         sync.WaitGroup

	// Stats
	blocksMined  int64
	totalHashes  int64
	earnings     int64
	startTime    time.Time
}

// BlockTemplate holds the data needed to mine a block
type BlockTemplate struct {
	Index          int64  `json:"Index"`
	PreviousHash   string `json:"PreviousHash"`
	Difficulty     int    `json:"difficulty"`
	DifficultyBits int    `json:"difficulty_bits"`
	Height         int64  `json:"blockchain_height"`
	Reward         int64  `json:"block_reward"`
}

// Transaction represents a transaction for block building
type Transaction struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    int64  `json:"amount"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
	PublicKey string `json:"public_key,omitempty"`
}

// Block represents a mined block to submit
type Block struct {
	Index          int64          `json:"Index"`
	Timestamp      int64          `json:"Timestamp"`
	Transactions   []*Transaction `json:"transactions"`
	PreviousHash   string         `json:"PreviousHash"`
	Hash           string         `json:"Hash"`
	Nonce          int64          `json:"Nonce"`
	Difficulty     int            `json:"Difficulty"`
	DifficultyBits int            `json:"DifficultyBits,omitempty"`
}

// APIResponse matches the node's response format
type APIResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// NewMiner creates a new miner instance
func NewMiner(nodeURL, address string, threads int) *Miner {
	return &Miner{
		nodeURL: nodeURL,
		address: address,
		threads: threads,
		stopCh:  make(chan struct{}),
	}
}

// Start begins the mining loop
func (m *Miner) Start() {
	m.startTime = time.Now()
	m.wg.Add(1)
	go m.miningLoop()
}

// Stop halts the miner
func (m *Miner) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// miningLoop continuously fetches work and mines
func (m *Miner) miningLoop() {
	defer m.wg.Done()

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		// Get work from node
		template, err := m.getWork()
		if err != nil {
			fmt.Printf("Error getting work: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Get pending transactions
		txs := m.getPendingTransactions()

		// Mine block
		block, found := m.mineBlock(template, txs)
		if !found {
			continue // Cancelled or new block detected
		}

		// Submit block
		if err := m.submitBlock(block); err != nil {
			fmt.Printf("Error submitting block: %v\n", err)
			continue
		}

		atomic.AddInt64(&m.blocksMined, 1)
		atomic.AddInt64(&m.earnings, template.Reward)
		fmt.Printf("Block #%d mined! Reward: %s DLT | Total blocks: %d | Earnings: %s DLT\n",
			block.Index, FormatDLT(template.Reward),
			atomic.LoadInt64(&m.blocksMined), FormatDLT(atomic.LoadInt64(&m.earnings)))
	}
}

// getWork fetches the current chain state from the node
func (m *Miner) getWork() (*BlockTemplate, error) {
	resp, err := httpClient.Get(m.nodeURL + "/status")
	if err != nil {
		return nil, fmt.Errorf("cannot connect to node: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(body))
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
		return nil, fmt.Errorf("node did not return last_block_hash in /status")
	}

	// Calculate block reward
	var reward int64 = 50 * DLTUnit // Default initial reward
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

// getPendingTransactions fetches mempool transactions
func (m *Miner) getPendingTransactions() []*Transaction {
	resp, err := httpClient.Get(m.nodeURL + "/mempool")
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

func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func getFloat(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

// mineBlock performs proof of work using multiple threads
func (m *Miner) mineBlock(template *BlockTemplate, pendingTxs []*Transaction) (*Block, bool) {
	// Build transactions list: coinbase + pending
	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        m.address,
		Amount:    template.Reward,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", template.Index, time.Now().UnixNano()),
	}

	txs := make([]*Transaction, 0, len(pendingTxs)+1)
	txs = append(txs, coinbase)
	txs = append(txs, pendingTxs...)

	baseBlock := &Block{
		Index:          template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   txs,
		PreviousHash:   template.PreviousHash,
		Difficulty:     template.Difficulty,
		DifficultyBits: template.DifficultyBits,
	}

	useBits := template.DifficultyBits > 0
	if useBits {
		fmt.Printf("Mining block #%d (difficulty bits: %d, hex: %d, threads: %d)\n",
			template.Index, template.DifficultyBits, template.Difficulty, m.threads)
	} else {
		fmt.Printf("Mining block #%d (difficulty: %d, threads: %d)\n",
			template.Index, template.Difficulty, m.threads)
	}

	hashPrefix := strings.Repeat("0", template.Difficulty)
	startTime := time.Now()

	resultCh := make(chan *Block, 1)
	cancelCh := make(chan struct{})
	var wg sync.WaitGroup

	// Spawn mining threads with interleaved nonce ranges
	for i := 0; i < m.threads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			nonce := int64(threadID)
			var localHashes int64

			// Each thread gets its own block copy
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
				case <-cancelCh:
					atomic.AddInt64(&m.totalHashes, localHashes)
					return
				case <-m.stopCh:
					atomic.AddInt64(&m.totalHashes, localHashes)
					return
				default:
				}

				threadBlock.Nonce = nonce
				hash := calculateBlockHash(threadBlock)
				localHashes++

				var meets bool
				if useBits {
					meets = meetsDifficultyBits(hash, template.DifficultyBits)
				} else {
					meets = strings.HasPrefix(hash, hashPrefix)
				}

				if meets {
					threadBlock.Hash = hash
					atomic.AddInt64(&m.totalHashes, localHashes)
					select {
					case resultCh <- threadBlock:
						close(cancelCh)
					default:
					}
					return
				}

				nonce += int64(m.threads)
			}
		}(i)
	}

	// Chain-advanced checker goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-cancelCh:
				return
			case <-m.stopCh:
				return
			case <-ticker.C:
				if m.chainAdvanced(template.Height) {
					fmt.Println("New block detected from network, restarting...")
					select {
					case resultCh <- nil:
						close(cancelCh)
					default:
					}
					return
				}
			}
		}
	}()

	// Wait for result
	var result *Block
	select {
	case result = <-resultCh:
	case <-m.stopCh:
		close(cancelCh)
		wg.Wait()
		return nil, false
	}

	wg.Wait()

	if result == nil {
		// Chain advanced, no valid block
		return nil, false
	}

	elapsed := time.Since(startTime).Seconds()
	totalH := atomic.LoadInt64(&m.totalHashes)
	if elapsed > 0 {
		hashrate := float64(totalH) / elapsed
		fmt.Printf("Found valid hash (%.0f H/s across %d threads)\n", hashrate, m.threads)
	}
	return result, true
}

// chainAdvanced checks if the chain has moved past our template height
func (m *Miner) chainAdvanced(templateHeight int64) bool {
	resp, err := httpClient.Get(m.nodeURL + "/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return false
	}

	if !apiResp.Success {
		return false
	}

	currentHeight := int64(apiResp.Data["blockchain_height"].(float64))
	return currentHeight > templateHeight
}

// calculateBlockHash computes the SHA-256 hash of a block
// Must match the node's Block.CalculateHash() exactly
func calculateBlockHash(block *Block) string {
	txJSON, _ := json.Marshal(block.Transactions)

	blockData := strconv.FormatInt(block.Index, 10) +
		strconv.FormatInt(block.Timestamp, 10) +
		string(txJSON) +
		block.PreviousHash +
		strconv.FormatInt(block.Nonce, 10) +
		strconv.Itoa(block.Difficulty)

	hash := sha256.Sum256([]byte(blockData))
	return hex.EncodeToString(hash[:])
}

// submitBlock posts a mined block to the node
func (m *Miner) submitBlock(block *Block) error {
	data, err := json.Marshal(block)
	if err != nil {
		return err
	}

	resp, err := httpClient.Post(m.nodeURL+"/block/submit", "application/json", bytes.NewBuffer(data))
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

// checkNode verifies connectivity to the node
func checkNode(nodeURL string) error {
	resp, err := httpClient.Get(nodeURL + "/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("invalid response from node")
	}

	if !apiResp.Success {
		return fmt.Errorf("node returned error: %s", apiResp.Message)
	}

	return nil
}
