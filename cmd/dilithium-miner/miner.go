package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DLTUnit is the number of base units in 1 DLT
const DLTUnit int64 = 100_000_000

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
	Index        int    `json:"Index"`
	PreviousHash string `json:"PreviousHash"`
	Difficulty   int    `json:"difficulty"`
	Height       int    `json:"blockchain_height"`
	Reward       int64  `json:"block_reward"`
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
	Index        int            `json:"Index"`
	Timestamp    int64          `json:"Timestamp"`
	Transactions []*Transaction `json:"transactions"`
	PreviousHash string         `json:"PreviousHash"`
	Hash         string         `json:"Hash"`
	Nonce        int64          `json:"Nonce"`
	Difficulty   int            `json:"Difficulty"`
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

	height := int(apiResp.Data["blockchain_height"].(float64))
	difficulty := int(apiResp.Data["difficulty"].(float64))

	// Get the last block hash from the chain
	chainResp, err := httpClient.Get(m.nodeURL + "/chain")
	if err != nil {
		return nil, fmt.Errorf("cannot get chain: %w", err)
	}
	defer chainResp.Body.Close()

	chainBody, err := io.ReadAll(chainResp.Body)
	if err != nil {
		return nil, err
	}

	var chainAPI APIResponse
	if err := json.Unmarshal(chainBody, &chainAPI); err != nil {
		return nil, fmt.Errorf("invalid chain response")
	}

	blocks, ok := chainAPI.Data["blocks"].([]interface{})
	if !ok || len(blocks) == 0 {
		return nil, fmt.Errorf("no blocks in chain")
	}

	lastBlock := blocks[len(blocks)-1].(map[string]interface{})
	lastHash := lastBlock["Hash"].(string)

	// Calculate block reward
	var reward int64 = 50 * DLTUnit // Default initial reward
	halvings := height / 250000
	for i := 0; i < halvings; i++ {
		reward /= 2
	}
	if reward < 1 {
		reward = 1
	}

	return &BlockTemplate{
		Index:        height,
		PreviousHash: lastHash,
		Difficulty:   difficulty,
		Height:       height,
		Reward:       reward,
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

// mineBlock performs proof of work
func (m *Miner) mineBlock(template *BlockTemplate, pendingTxs []*Transaction) (*Block, bool) {
	// Build transactions list: coinbase + pending
	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        m.address,
		Amount:    template.Reward,
		Timestamp: time.Now().Unix(),
		Signature: "coinbase",
	}

	txs := make([]*Transaction, 0, len(pendingTxs)+1)
	txs = append(txs, coinbase)
	txs = append(txs, pendingTxs...)

	block := &Block{
		Index:        template.Index,
		Timestamp:    time.Now().Unix(),
		Transactions: txs,
		PreviousHash: template.PreviousHash,
		Difficulty:   template.Difficulty,
	}

	// Create target
	target := new(big.Int)
	target.SetBit(target, 256-template.Difficulty*4, 1)
	target.Sub(target, big.NewInt(1))

	// Mine with cancellation
	var nonce int64
	hashPrefix := strings.Repeat("0", template.Difficulty)
	startTime := time.Now()
	var localHashes int64

	for {
		select {
		case <-m.stopCh:
			return nil, false
		default:
		}

		block.Nonce = nonce
		hash := calculateBlockHash(block)
		localHashes++

		if strings.HasPrefix(hash, hashPrefix) {
			block.Hash = hash
			atomic.AddInt64(&m.totalHashes, localHashes)
			elapsed := time.Since(startTime).Seconds()
			if elapsed > 0 {
				hashrate := float64(localHashes) / elapsed
				fmt.Printf("Found valid hash after %d hashes (%.0f H/s)\n", localHashes, hashrate)
			}
			return block, true
		}

		nonce++

		// Periodically check if chain has advanced
		if nonce%100000 == 0 {
			atomic.AddInt64(&m.totalHashes, localHashes)
			localHashes = 0

			if m.chainAdvanced(template.Height) {
				fmt.Println("New block detected from network, restarting...")
				return nil, false
			}
		}
	}
}

// chainAdvanced checks if the chain has moved past our template height
func (m *Miner) chainAdvanced(templateHeight int) bool {
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

	currentHeight := int(apiResp.Data["blockchain_height"].(float64))
	return currentHeight > templateHeight
}

// calculateBlockHash computes the SHA-256 hash of a block
func calculateBlockHash(block *Block) string {
	txHashes := ""
	for _, tx := range block.Transactions {
		txHashes += fmt.Sprintf("%s%s%d%d%s", tx.From, tx.To, tx.Amount, tx.Timestamp, tx.Signature)
	}
	record := fmt.Sprintf("%d%d%s%s%d%d", block.Index, block.Timestamp, txHashes, block.PreviousHash, block.Nonce, block.Difficulty)
	h := sha256.Sum256([]byte(record))
	return fmt.Sprintf("%x", h)
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
