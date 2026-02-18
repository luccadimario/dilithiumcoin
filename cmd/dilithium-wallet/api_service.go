package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

const DLTUnit int64 = 100_000_000

// hardcodedSeeds is the list of seed node API endpoints for discovery.
var hardcodedSeeds = []string{
	"http://seed.dilithiumcoin.com:8001",
	"http://seed1.dilithiumcoin.com:8001",
	"http://seed2.dilithiumcoin.com:8001",
	"http://seed3.dilithiumcoin.com:8001",
	"http://seed4.dilithiumcoin.com:8001",
	"http://seed5.dilithiumcoin.com:8001",
	"http://node1.dilithiumcoin.com:8001",
	"http://node2.dilithiumcoin.com:8001",
}

// nodeCandidate represents a discovered node with quality metrics.
type nodeCandidate struct {
	URL       string  `json:"url"`
	Height    float64 `json:"height"`
	Latency   int64   `json:"latency_ms"`
	Reachable bool    `json:"reachable"`
	LastSeen  int64   `json:"last_seen"`
}

// walletNodeCacheFile is the path to the cached known-good nodes file.
var walletNodeCacheFile string

func init() {
	home, _ := os.UserHomeDir()
	walletNodeCacheFile = filepath.Join(home, ".dilithium", "nodes.dat")
}

// discoverBestNode finds the best reachable node using multi-tier resolution:
// 1. Cached known-good nodes (nodes.dat)
// 2. localhost:8001 (local node)
// 3. Hardcoded seed list
func discoverBestNode() string {
	var candidates []nodeCandidate
	seen := make(map[string]bool)

	// 1. Load cached nodes
	cached := loadWalletNodeCache()
	for _, c := range cached {
		if !seen[c.URL] {
			candidates = append(candidates, c)
			seen[c.URL] = true
		}
	}

	// 2. Try localhost
	localURL := "http://localhost:8001"
	if !seen[localURL] {
		candidates = append(candidates, nodeCandidate{URL: localURL})
		seen[localURL] = true
	}

	// 3. Hardcoded seeds
	for _, seed := range hardcodedSeeds {
		if !seen[seed] {
			candidates = append(candidates, nodeCandidate{URL: seed})
			seen[seed] = true
		}
	}

	// Probe all candidates in parallel
	probed := probeWalletNodes(candidates)

	// Rank: reachable first, then by height desc, then latency asc
	sort.Slice(probed, func(i, j int) bool {
		if probed[i].Reachable != probed[j].Reachable {
			return probed[i].Reachable
		}
		if probed[i].Height != probed[j].Height {
			return probed[i].Height > probed[j].Height
		}
		return probed[i].Latency < probed[j].Latency
	})

	// Save reachable nodes to cache
	var reachable []nodeCandidate
	for _, c := range probed {
		if c.Reachable {
			reachable = append(reachable, c)
		}
	}
	if len(reachable) > 0 {
		saveWalletNodeCache(reachable)
		return reachable[0].URL
	}

	// Nothing reachable — return localhost and let commands fail with a useful error
	return "http://localhost:8001"
}

// probeWalletNodes concurrently checks each candidate's /status endpoint.
func probeWalletNodes(candidates []nodeCandidate) []nodeCandidate {
	client := &http.Client{Timeout: 3 * time.Second}
	result := make([]nodeCandidate, len(candidates))
	var wg sync.WaitGroup

	for i, c := range candidates {
		wg.Add(1)
		go func(idx int, candidate nodeCandidate) {
			defer wg.Done()

			start := time.Now()
			resp, err := client.Get(candidate.URL + "/status")
			latency := time.Since(start).Milliseconds()

			result[idx] = candidate
			result[idx].Latency = latency

			if err != nil {
				result[idx].Reachable = false
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				result[idx].Reachable = false
				return
			}

			var apiResp apiResponse
			if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
				result[idx].Reachable = false
				return
			}

			result[idx].Reachable = true
			result[idx].LastSeen = time.Now().Unix()

			if apiResp.Data != nil {
				if h, ok := apiResp.Data["blockchain_height"].(float64); ok {
					result[idx].Height = h
				}
			}
		}(i, c)
	}

	wg.Wait()
	return result
}

// saveWalletNodeCache writes known-good nodes to ~/.dilithium/nodes.dat.
func saveWalletNodeCache(nodes []nodeCandidate) {
	dir := filepath.Dir(walletNodeCacheFile)
	os.MkdirAll(dir, 0700)

	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(walletNodeCacheFile, data, 0600)
}

// loadWalletNodeCache reads cached nodes, skipping entries older than 1 hour.
func loadWalletNodeCache() []nodeCandidate {
	data, err := os.ReadFile(walletNodeCacheFile)
	if err != nil {
		return nil
	}

	var nodes []nodeCandidate
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil
	}

	cutoff := time.Now().Unix() - 3600
	var fresh []nodeCandidate
	for _, n := range nodes {
		if n.LastSeen >= cutoff {
			fresh = append(fresh, n)
		}
	}
	return fresh
}

type apiResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

type apiService struct {
	nodeURL string
	client  *http.Client
	mu      sync.RWMutex
}

func newAPIService(nodeURL string) *apiService {
	return &apiService{
		nodeURL: nodeURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (api *apiService) getNodeURL() string {
	api.mu.RLock()
	defer api.mu.RUnlock()
	return api.nodeURL
}

func (api *apiService) setNodeURL(url string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.nodeURL = strings.TrimRight(url, "/")
}

func (api *apiService) getJSON(url string) (*apiResponse, error) {
	resp, err := api.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(body))
	}
	return &apiResp, nil
}

func (api *apiService) postJSON(url string, data interface{}) (*apiResponse, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	resp, err := api.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(body))
	}
	return &apiResp, nil
}

// getBalance fetches balance info from the node
func (api *apiService) getBalance(address string) BalanceInfo {
	api.mu.RLock()
	nodeURL := api.nodeURL
	api.mu.RUnlock()

	resp, err := api.getJSON(nodeURL + "/explorer/address?addr=" + address)
	if err != nil {
		return BalanceInfo{Address: address, Error: fmt.Sprintf("Could not connect: %v", err)}
	}

	if !resp.Success {
		return BalanceInfo{Address: address, Error: resp.Message}
	}

	balanceDLT, _ := resp.Data["balance_dlt"].(string)
	txCount := int(getFloat64(resp.Data, "transaction_count"))
	receivedDLT, _ := resp.Data["total_received_dlt"].(string)
	sentDLT, _ := resp.Data["total_sent_dlt"].(string)

	return BalanceInfo{
		Address:          address,
		BalanceDLT:       balanceDLT,
		TotalReceivedDLT: receivedDLT,
		TotalSentDLT:     sentDLT,
		TxCount:          txCount,
	}
}

// sendTransaction signs and submits a transaction to the node
func (api *apiService) sendTransaction(ws *walletService, to string, amountDLT string, feeDLT string) TxResult {
	if ws.privateKey == nil {
		return TxResult{Success: false, Message: "wallet is locked"}
	}

	amount, err := parseDLT(amountDLT)
	if err != nil {
		return TxResult{Success: false, Message: err.Error()}
	}

	fee, err := parseDLTFee(feeDLT)
	if err != nil {
		return TxResult{Success: false, Message: err.Error()}
	}

	fromAddress := ws.address

	// Get hex-encoded public key
	pk := ws.privateKey.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	// Create and sign transaction (includes fee in signing data)
	timestamp := time.Now().Unix()
	txData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d%d", fromAddress, to, amount, fee, timestamp)

	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(ws.privateKey, []byte(txData), sig)
	signatureHex := hex.EncodeToString(sig)

	// Submit to node
	tx := map[string]interface{}{
		"from":       fromAddress,
		"to":         to,
		"amount":     amount,
		"fee":        fee,
		"timestamp":  timestamp,
		"signature":  signatureHex,
		"public_key": publicKeyHex,
	}

	api.mu.RLock()
	nodeURL := api.nodeURL
	api.mu.RUnlock()

	resp, err := api.postJSON(nodeURL+"/transaction", tx)
	if err != nil {
		return TxResult{Success: false, Message: fmt.Sprintf("Could not connect to node: %v", err)}
	}

	return TxResult{Success: resp.Success, Message: resp.Message}
}

// getTransactionHistory fetches transaction history from the node
func (api *apiService) getTransactionHistory(address string) []TransactionInfo {
	api.mu.RLock()
	nodeURL := api.nodeURL
	api.mu.RUnlock()

	resp, err := api.getJSON(nodeURL + "/explorer/address?addr=" + address)
	if err != nil {
		return nil
	}

	if !resp.Success {
		return nil
	}

	var txs []TransactionInfo

	rawTxs, ok := resp.Data["transactions"].([]interface{})
	if !ok {
		return txs
	}

	for _, raw := range rawTxs {
		txMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		from, _ := txMap["from"].(string)
		to, _ := txMap["to"].(string)
		amount := int64(getFloat64(txMap, "amount"))
		timestamp := int64(getFloat64(txMap, "timestamp"))

		direction := "received"
		if from == address {
			direction = "sent"
		}

		txs = append(txs, TransactionInfo{
			From:      from,
			To:        to,
			AmountDLT: formatDLT(amount),
			Timestamp: timestamp,
			Direction: direction,
		})
	}

	return txs
}

// testConnection tests the connection to the node and returns status
func (api *apiService) testConnection() NodeStatus {
	api.mu.RLock()
	nodeURL := api.nodeURL
	api.mu.RUnlock()

	quickClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := quickClient.Get(nodeURL + "/status")
	if err != nil {
		return NodeStatus{Connected: false, Error: fmt.Sprintf("Could not connect: %v", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeStatus{Connected: false, Error: "Failed to read response"}
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return NodeStatus{Connected: false, Error: "Invalid response from node"}
	}

	if !apiResp.Success {
		return NodeStatus{Connected: false, Error: apiResp.Message}
	}

	version, _ := apiResp.Data["version"].(string)
	blockHeight := int(getFloat64(apiResp.Data, "blockchain_height"))
	pendingTxs := int(getFloat64(apiResp.Data, "pending_transactions"))
	difficulty := int(getFloat64(apiResp.Data, "difficulty"))

	peerCount := 0
	if peers, ok := apiResp.Data["peers"].(map[string]interface{}); ok {
		peerCount = int(getFloat64(peers, "total"))
	}

	return NodeStatus{
		Connected:   true,
		Version:     version,
		BlockHeight: blockHeight,
		PendingTxs:  pendingTxs,
		Difficulty:  difficulty,
		PeerCount:   peerCount,
	}
}

// --- Helpers ---

func getFloat64(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func formatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

func parseDLT(s string) (int64, error) {
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", s)
	}
	if f <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	return int64(math.Round(f * float64(DLTUnit))), nil
}

func parseDLTFee(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 10000, nil // default: 0.0001 DLT
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid fee: %s", s)
	}
	if f < 0 {
		return 0, fmt.Errorf("fee cannot be negative")
	}
	fee := int64(math.Round(f * float64(DLTUnit)))
	if fee < 10000 {
		return 0, fmt.Errorf("minimum fee is 0.0001 DLT")
	}
	return fee, nil
}
