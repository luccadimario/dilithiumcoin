package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

const DLTUnit int64 = 100_000_000

// SeedNodeAPI is the public seed node API — works without running a local node
const SeedNodeAPI = "https://api.dilithiumcoin.com"

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
func (api *apiService) sendTransaction(ws *walletService, to string, amountDLT string) TxResult {
	if ws.privateKey == nil {
		return TxResult{Success: false, Message: "wallet is locked"}
	}

	amount, err := parseDLT(amountDLT)
	if err != nil {
		return TxResult{Success: false, Message: err.Error()}
	}

	fromAddress := ws.address

	// Get hex-encoded public key
	pk := ws.privateKey.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	// Create and sign transaction
	timestamp := time.Now().Unix()
	txData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d", fromAddress, to, amount, timestamp)

	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(ws.privateKey, []byte(txData), sig)
	signatureHex := hex.EncodeToString(sig)

	// Submit to node
	tx := map[string]interface{}{
		"from":       fromAddress,
		"to":         to,
		"amount":     amount,
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
