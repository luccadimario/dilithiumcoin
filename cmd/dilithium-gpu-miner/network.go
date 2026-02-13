package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DLTUnit is the number of base units in 1 DLT (8 decimal places).
const DLTUnit int64 = 100_000_000

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// APIResponse is the standard response format from the dilithium node.
type APIResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// BlockTemplate holds the data needed to mine a block.
type BlockTemplate struct {
	Index          int64  `json:"Index"`
	PreviousHash   string `json:"PreviousHash"`
	Difficulty     int    `json:"difficulty"`
	DifficultyBits int    `json:"difficulty_bits"`
	Height         int64  `json:"blockchain_height"`
	Reward         int64  `json:"block_reward"`
}

// Transaction represents a dilithium transaction.
type Transaction struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    int64  `json:"amount"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
	PublicKey string `json:"public_key,omitempty"`
}

// Block represents a mined block for submission to the node.
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

// NodeClient communicates with a dilithium node via HTTP API.
type NodeClient struct {
	baseURL string
}

// NewNodeClient creates a client for the given node URL.
func NewNodeClient(url string) *NodeClient {
	return &NodeClient{baseURL: url}
}

// CheckConnection verifies the node is reachable.
func (c *NodeClient) CheckConnection() error {
	resp, err := httpClient.Get(c.baseURL + "/status")
	if err != nil {
		return fmt.Errorf("cannot connect to node at %s: %w", c.baseURL, err)
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
		return fmt.Errorf("node error: %s", apiResp.Message)
	}

	return nil
}

// GetWork fetches the current block template from the node.
func (c *NodeClient) GetWork() (*BlockTemplate, error) {
	// Get status for difficulty, height, and last block hash
	resp, err := httpClient.Get(c.baseURL + "/status")
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
		return nil, fmt.Errorf("invalid status response: %s", string(body))
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("node error: %s", apiResp.Message)
	}

	height := int64(apiResp.Data["blockchain_height"].(float64))
	difficulty := int(apiResp.Data["difficulty"].(float64))
	var difficultyBits int
	if db, ok := apiResp.Data["difficulty_bits"].(float64); ok {
		difficultyBits = int(db)
	}

	// Get last block hash from status (fixed: /chain is paginated!)
	lastHash, ok := apiResp.Data["last_block_hash"].(string)
	if !ok || lastHash == "" {
		return nil, fmt.Errorf("no last_block_hash in status response")
	}

	// Calculate block reward with halving
	reward := int64(50) * DLTUnit
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

// GetCurrentHeight returns the current blockchain height (for stale work detection).
func (c *NodeClient) GetCurrentHeight() (int64, error) {
	resp, err := httpClient.Get(c.baseURL + "/status")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return 0, err
	}
	if !apiResp.Success {
		return 0, fmt.Errorf("node error")
	}

	return int64(apiResp.Data["blockchain_height"].(float64)), nil
}

// GetPeerCount returns the number of connected peers.
func (c *NodeClient) GetPeerCount() (int, error) {
	resp, err := httpClient.Get(c.baseURL + "/status")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return 0, err
	}

	peers, ok := apiResp.Data["peers"].(map[string]interface{})
	if !ok {
		return 0, nil
	}
	total, _ := peers["total"].(float64)
	return int(total), nil
}

// SubmitBlock posts a mined block to the node.
func (c *NodeClient) SubmitBlock(block *Block) error {
	data, err := json.Marshal(block)
	if err != nil {
		return err
	}

	resp, err := httpClient.Post(c.baseURL+"/block/submit", "application/json", bytes.NewBuffer(data))
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

// GetPendingTransactions fetches mempool transactions from the node.
func (c *NodeClient) GetPendingTransactions() []*Transaction {
	resp, err := httpClient.Get(c.baseURL + "/mempool")
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
			From:      getStr(txMap, "from"),
			To:        getStr(txMap, "to"),
			Amount:    int64(getNum(txMap, "amount")),
			Timestamp: int64(getNum(txMap, "timestamp")),
			Signature: getStr(txMap, "signature"),
			PublicKey: getStr(txMap, "public_key"),
		}
		txs = append(txs, tx)
	}

	return txs
}

func getStr(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func getNum(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

// FormatDLT formats base units as human-readable DLT string.
func FormatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}
