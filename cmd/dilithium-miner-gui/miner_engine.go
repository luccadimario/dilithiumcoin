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

const dltUnit int64 = 100_000_000

type apiResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

type blockTemplate struct {
	Index          int64
	PreviousHash   string
	Difficulty     int
	DifficultyBits int
	Height         int64
	Reward         int64
}

type transaction struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    int64  `json:"amount"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
	PublicKey string `json:"public_key,omitempty"`
}

// merkleRootForkHeight is the block at which hashes switch to using a Merkle root.
const merkleRootForkHeight = 6000

type block struct {
	Index          int64          `json:"Index"`
	Timestamp      int64          `json:"Timestamp"`
	Transactions   []*transaction `json:"transactions"`
	MerkleRoot     string         `json:"MerkleRoot,omitempty"`
	PreviousHash   string         `json:"PreviousHash"`
	Hash           string         `json:"Hash"`
	Nonce          int64          `json:"Nonce"`
	Difficulty     int            `json:"Difficulty"`
	DifficultyBits int            `json:"DifficultyBits,omitempty"`
}

type miner struct {
	nodeURL     string
	address     string
	threads     int
	stopCh      chan struct{}
	wg          sync.WaitGroup
	blocksMined int64
	totalHashes int64
	earnings    int64
	startTime   time.Time
	logFn       func(string, string)
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

func newMiner(nodeURL, address string, threads int, logFn func(string, string)) *miner {
	return &miner{
		nodeURL: nodeURL,
		address: address,
		threads: threads,
		stopCh:  make(chan struct{}),
		logFn:   logFn,
	}
}

func (m *miner) Start() {
	m.startTime = time.Now()
	m.wg.Add(1)
	go m.miningLoop()
}

func (m *miner) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func (m *miner) log(msg, typ string) {
	if m.logFn != nil {
		m.logFn(msg, typ)
	}
}

func (m *miner) miningLoop() {
	defer m.wg.Done()

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		template, err := getWork(m.nodeURL)
		if err != nil {
			m.log(fmt.Sprintf("Error getting work: %v", err), "error")
			time.Sleep(5 * time.Second)
			continue
		}

		txs := getPendingTransactions(m.nodeURL)

		blk, found := m.mineBlock(template, txs)
		if !found {
			continue
		}

		if err := submitBlock(m.nodeURL, blk); err != nil {
			m.log(fmt.Sprintf("Block rejected: %v", err), "error")
			continue
		}

		atomic.AddInt64(&m.blocksMined, 1)
		atomic.AddInt64(&m.earnings, template.Reward)
		blocks := atomic.LoadInt64(&m.blocksMined)
		m.log(fmt.Sprintf("Block #%d mined! Reward: %s DLT (total: %d blocks)", blk.Index, formatDLT(template.Reward), blocks), "success")
	}
}

func (m *miner) mineBlock(template *blockTemplate, pendingTxs []*transaction) (*block, bool) {
	coinbase := &transaction{
		From:      "SYSTEM",
		To:        m.address,
		Amount:    template.Reward,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", template.Index, time.Now().UnixNano()),
	}

	txs := make([]*transaction, 0, len(pendingTxs)+1)
	txs = append(txs, coinbase)
	txs = append(txs, pendingTxs...)

	baseBlock := &block{
		Index:          template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   txs,
		MerkleRoot:     computeMerkleRoot(txs),
		PreviousHash:   template.PreviousHash,
		Difficulty:     template.Difficulty,
		DifficultyBits: template.DifficultyBits,
	}

	useBits := template.DifficultyBits > 0
	hashPrefix := strings.Repeat("0", template.Difficulty)

	m.log(fmt.Sprintf("Mining block #%d (diff bits: %d, threads: %d)", template.Index, template.DifficultyBits, m.threads), "info")

	resultCh := make(chan *block, 1)
	cancelCh := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < m.threads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			nonce := int64(threadID)
			var localHashes int64

			tb := &block{
				Index: baseBlock.Index, Timestamp: baseBlock.Timestamp,
				Transactions: baseBlock.Transactions, MerkleRoot: baseBlock.MerkleRoot,
				PreviousHash: baseBlock.PreviousHash,
				Difficulty: baseBlock.Difficulty, DifficultyBits: baseBlock.DifficultyBits,
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

				tb.Nonce = nonce
				hash := calculateBlockHash(tb)
				localHashes++

				var meets bool
				if useBits {
					meets = meetsDifficultyBits(hash, template.DifficultyBits)
				} else {
					meets = strings.HasPrefix(hash, hashPrefix)
				}

				if meets {
					tb.Hash = hash
					atomic.AddInt64(&m.totalHashes, localHashes)
					select {
					case resultCh <- tb:
						close(cancelCh)
					default:
					}
					return
				}
				nonce += int64(m.threads)
			}
		}(i)
	}

	// Chain-advanced checker
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
				if chainAdvanced(m.nodeURL, template.Height) {
					m.log("New block from network, restarting...", "info")
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

	var result *block
	select {
	case result = <-resultCh:
	case <-m.stopCh:
		close(cancelCh)
		wg.Wait()
		return nil, false
	}
	wg.Wait()

	if result == nil {
		return nil, false
	}
	return result, true
}

// --- Network helpers ---

func getWork(nodeURL string) (*blockTemplate, error) {
	resp, err := httpClient.Get(nodeURL + "/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid response")
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("%s", apiResp.Message)
	}

	d := apiResp.Data
	height := int64(getFloat(d, "blockchain_height"))
	difficulty := int(getFloat(d, "difficulty"))
	diffBits := int(getFloat(d, "difficulty_bits"))
	lastHash, _ := d["last_block_hash"].(string)

	reward := int64(50 * dltUnit)
	halvings := int(height) / 250000
	for i := 0; i < halvings; i++ {
		reward /= 2
	}
	if reward < 1 {
		reward = 1
	}

	return &blockTemplate{
		Index: height, PreviousHash: lastHash,
		Difficulty: difficulty, DifficultyBits: diffBits,
		Height: height, Reward: reward,
	}, nil
}

func getPendingTransactions(nodeURL string) []*transaction {
	resp, err := httpClient.Get(nodeURL + "/mempool")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp apiResponse
	if json.Unmarshal(body, &apiResp) != nil {
		return nil
	}

	rawTxs, ok := apiResp.Data["transactions"].([]interface{})
	if !ok {
		return nil
	}

	var txs []*transaction
	for _, t := range rawTxs {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		txs = append(txs, &transaction{
			From:      getString(tm, "from"),
			To:        getString(tm, "to"),
			Amount:    int64(getFloat(tm, "amount")),
			Timestamp: int64(getFloat(tm, "timestamp")),
			Signature: getString(tm, "signature"),
			PublicKey: getString(tm, "public_key"),
		})
	}
	return txs
}

func submitBlock(nodeURL string, blk *block) error {
	data, _ := json.Marshal(blk)
	resp, err := httpClient.Post(nodeURL+"/block/submit", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp apiResponse
	if json.Unmarshal(body, &apiResp) != nil {
		return fmt.Errorf("invalid response")
	}
	if !apiResp.Success {
		return fmt.Errorf("%s", apiResp.Message)
	}
	return nil
}

func chainAdvanced(nodeURL string, templateHeight int64) bool {
	resp, err := httpClient.Get(nodeURL + "/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp apiResponse
	if json.Unmarshal(body, &apiResp) != nil {
		return false
	}
	currentHeight := int64(getFloat(apiResp.Data, "blockchain_height"))
	return currentHeight > templateHeight
}

func checkNode(nodeURL string) error {
	resp, err := httpClient.Get(nodeURL + "/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp apiResponse
	if json.Unmarshal(body, &apiResp) != nil {
		return fmt.Errorf("invalid response")
	}
	if !apiResp.Success {
		return fmt.Errorf("%s", apiResp.Message)
	}
	return nil
}

func computeMerkleRoot(txs []*transaction) string {
	if len(txs) == 0 {
		h := sha256.Sum256([]byte{})
		return hex.EncodeToString(h[:])
	}
	hashes := make([][]byte, len(txs))
	for i, tx := range txs {
		txJSON, _ := json.Marshal(tx)
		h := sha256.Sum256(txJSON)
		hashes[i] = h[:]
	}
	for len(hashes) > 1 {
		if len(hashes)%2 != 0 {
			hashes = append(hashes, hashes[len(hashes)-1])
		}
		var next [][]byte
		for i := 0; i < len(hashes); i += 2 {
			combined := append(hashes[i], hashes[i+1]...)
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}
		hashes = next
	}
	return hex.EncodeToString(hashes[0])
}

func calculateBlockHash(b *block) string {
	var txData string
	if b.Index >= merkleRootForkHeight {
		txData = b.MerkleRoot
	} else {
		txJSON, _ := json.Marshal(b.Transactions)
		txData = string(txJSON)
	}
	blockData := strconv.FormatInt(b.Index, 10) +
		strconv.FormatInt(b.Timestamp, 10) +
		txData + b.PreviousHash +
		strconv.FormatInt(b.Nonce, 10) +
		strconv.Itoa(b.Difficulty)
	hash := sha256.Sum256([]byte(blockData))
	return hex.EncodeToString(hash[:])
}

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

func getFloat(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func formatDLT(amount int64) string {
	whole := amount / dltUnit
	frac := amount % dltUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}
