package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

// DLTUnit is the number of base units in 1 DLT
const DLTUnit int64 = 100_000_000

// MinTransactionFee is the minimum transaction fee (0.0001 DLT)
const MinTransactionFee int64 = 10_000

// MerkleRootForkHeight is the block at which hashes switch to using a Merkle root.
const MerkleRootForkHeight = 6000

// ============================================================================
// Blockchain types (mirrored from node/miner)
// ============================================================================

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
	Fee       int64  `json:"fee,omitempty"`
	Data      string `json:"data,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
	PublicKey string `json:"public_key,omitempty"`
}

// Block represents a mined block to submit
type Block struct {
	Index          int64          `json:"Index"`
	Timestamp      int64          `json:"Timestamp"`
	Transactions   []*Transaction `json:"transactions"`
	MerkleRoot     string         `json:"MerkleRoot,omitempty"`
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
// Pool types
// ============================================================================

// Share represents a single accepted share from a worker
type Share struct {
	WorkerAddress string `json:"worker_address"`
	JobID         string `json:"job_id"`
	Nonce         int64  `json:"nonce"`
	Hash          string `json:"hash"`
	Difficulty    int    `json:"difficulty"`
	Timestamp     int64  `json:"timestamp"`
	BlockHeight   int64  `json:"block_height"`
}

// Worker represents a pool participant (persistent)
type Worker struct {
	Address        string `json:"address"`
	Balance        int64  `json:"balance"`
	TotalEarned    int64  `json:"total_earned"`
	TotalPaid      int64  `json:"total_paid"`
	SharesAccepted int64  `json:"shares_accepted"`
	SharesRejected int64  `json:"shares_rejected"`
	LastSeen       int64  `json:"last_seen"`
	JoinedAt       int64  `json:"joined_at"`
}

// PoolBlock tracks a block found by the pool
type PoolBlock struct {
	Height        int64  `json:"height"`
	Hash          string `json:"hash"`
	FinderAddress string `json:"finder_address"`
	Reward        int64  `json:"reward"`
	Timestamp     int64  `json:"timestamp"`
	Status        string `json:"status"` // "confirmed", "orphaned"
}

// Payout represents a payment sent to a worker
type Payout struct {
	TxSignature   string `json:"tx_signature"`
	WorkerAddress string `json:"worker_address"`
	Amount        int64  `json:"amount"`
	Fee           int64  `json:"fee"`
	Timestamp     int64  `json:"timestamp"`
	Status        string `json:"status"` // "sent", "failed"
}

// PoolStats holds aggregate pool statistics
type PoolStats struct {
	TotalShares      int64 `json:"total_shares"`
	TotalBlocksFound int64 `json:"total_blocks_found"`
	TotalPaidOut     int64 `json:"total_paid_out"`
	PoolStartTime    int64 `json:"pool_start_time"`
}

// ============================================================================
// Pool wallet
// ============================================================================

// PoolWallet holds the pool operator's wallet for signing payouts
type PoolWallet struct {
	PrivateKey *mode3.PrivateKey
	PublicKey  *mode3.PublicKey
	Address    string
}

// ============================================================================
// Pool server (runtime state)
// ============================================================================

// PoolWork holds the current work template for distribution
type PoolWork struct {
	Template     *BlockTemplate `json:"template"`
	ShareBits    int            `json:"share_bits"`
	Transactions []*Transaction `json:"transactions"`
	JobID        string         `json:"job_id"`
}

// StratumWorker represents a connected Stratum worker (runtime, not persisted)
type StratumWorker struct {
	id         int64
	conn       net.Conn
	address    string // payout address
	subscribed bool
	authorized bool
	agent      string
	mu         sync.Mutex
}

// Pool manages the Stratum mining pool server
type Pool struct {
	nodeURL    string
	address    string // pool operator address
	stratumPort int
	feePercent float64

	listener net.Listener
	stopCh   chan struct{}
	wg       sync.WaitGroup

	// Connected workers (runtime)
	workersMu sync.Mutex
	workers   map[int64]*StratumWorker
	nextID    int64

	// Current work
	workMu      sync.RWMutex
	currentWork *PoolWork
	currentJob  string
	nextJobID   int64

	// Persistent database
	db *Database

	// Pool wallet for payouts
	wallet *PoolWallet
}

// PayoutEngine handles automatic payouts to workers
type PayoutEngine struct {
	pool           *Pool
	db             *Database
	wallet         *PoolWallet
	nodeURL        string
	minPayout      int64
	payoutInterval time.Duration
	stopCh         chan struct{}
	wg             sync.WaitGroup
}

// Dashboard serves the web UI and JSON API
type Dashboard struct {
	pool   *Pool
	db     *Database
	port   int
	server *http.Server
}

// ============================================================================
// Helpers
// ============================================================================

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

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

// formatDLT formats base units as a human-readable DLT string
func formatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// getString safely extracts a string from a map
func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// getFloat safely extracts a float64 from a map
func getFloat(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

// generateJobID returns a unique job ID
func (p *Pool) generateJobID() string {
	id := atomic.AddInt64(&p.nextJobID, 1)
	return fmt.Sprintf("%x", id)
}
