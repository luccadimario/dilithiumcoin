package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const NodeGUIVersion = "4.2.0"

// apiResponse matches the node's response format
type apiResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// NodeStatus holds the full node status
type NodeStatus struct {
	Connected       bool    `json:"connected"`
	Version         string  `json:"version"`
	BlockHeight     int     `json:"block_height"`
	PendingTxs      int     `json:"pending_txs"`
	Difficulty      int     `json:"difficulty"`
	DifficultyBits  int     `json:"difficulty_bits"`
	PeerCount       int     `json:"peer_count"`
	InboundPeers    int     `json:"inbound_peers"`
	OutboundPeers   int     `json:"outbound_peers"`
	MiningEnabled   bool    `json:"mining_enabled"`
	MiningActive    bool    `json:"mining_active"`
	MinerAddress    string  `json:"miner_address"`
	LastBlockHash   string  `json:"last_block_hash"`
	Error           string  `json:"error,omitempty"`
}

// PeerInfo holds info about a connected peer
type PeerInfo struct {
	Address   string `json:"address"`
	State     string `json:"state"`
	Inbound   bool   `json:"inbound"`
	Direction string `json:"direction"`
}

// BlockInfo holds block data for display
type BlockInfo struct {
	Index        int64  `json:"index"`
	Hash         string `json:"hash"`
	PreviousHash string `json:"previous_hash"`
	Timestamp    int64  `json:"timestamp"`
	Nonce        int64  `json:"nonce"`
	Difficulty   int    `json:"difficulty"`
	TxCount      int    `json:"tx_count"`
}

// MempoolTx holds a mempool transaction
type MempoolTx struct {
	From      string `json:"from"`
	To        string `json:"to"`
	AmountDLT string `json:"amount_dlt"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

// ExplorerStats holds full explorer stats
type ExplorerStats struct {
	Height          int     `json:"height"`
	TotalTxs        int     `json:"total_txs"`
	Difficulty      int     `json:"difficulty"`
	DifficultyBits  int     `json:"difficulty_bits"`
	AvgBlockTime    float64 `json:"avg_block_time"`
	HashRate        float64 `json:"hash_rate"`
	MempoolSize     int     `json:"mempool_size"`
	PeerCount       int     `json:"peer_count"`
	KnownAddresses  int     `json:"known_addresses"`
	BlockReward     string  `json:"block_reward"`
	Error           string  `json:"error,omitempty"`
}

// NetworkInfo holds network address info
type NetworkInfo struct {
	PeerCount      int        `json:"peer_count"`
	InboundCount   int        `json:"inbound_count"`
	OutboundCount  int        `json:"outbound_count"`
	KnownAddresses int        `json:"known_addresses"`
	BannedCount    int        `json:"banned_count"`
	Peers          []PeerInfo `json:"peers"`
}

// App is the main application struct
type App struct {
	ctx     context.Context
	nodeURL string
	client  *http.Client
	mu      sync.RWMutex
}

func NewApp() *App {
	return &App{
		nodeURL: "http://localhost:8001",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(ctx context.Context) {}

// --- API helpers ---

func (a *App) getJSON(path string) (*apiResponse, error) {
	a.mu.RLock()
	url := a.nodeURL + path
	a.mu.RUnlock()

	resp, err := a.client.Get(url)
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
		return nil, fmt.Errorf("invalid response")
	}
	return &apiResp, nil
}

func (a *App) postJSON(path string, data interface{}) (*apiResponse, error) {
	a.mu.RLock()
	url := a.nodeURL + path
	a.mu.RUnlock()

	jsonData, _ := json.Marshal(data)
	resp, err := a.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
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
		return nil, fmt.Errorf("invalid response")
	}
	return &apiResp, nil
}

// --- Node connection ---

func (a *App) GetNodeURL() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.nodeURL
}

func (a *App) SetNodeURL(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nodeURL = url
}

func (a *App) GetVersion() string {
	return NodeGUIVersion
}

// --- Status ---

func (a *App) GetStatus() NodeStatus {
	resp, err := a.getJSON("/status")
	if err != nil {
		return NodeStatus{Connected: false, Error: err.Error()}
	}
	if !resp.Success {
		return NodeStatus{Connected: false, Error: resp.Message}
	}

	d := resp.Data
	version, _ := d["version"].(string)
	blockHeight := int(getFloat(d, "blockchain_height"))
	pendingTxs := int(getFloat(d, "pending_transactions"))
	difficulty := int(getFloat(d, "difficulty"))
	diffBits := int(getFloat(d, "difficulty_bits"))
	lastHash, _ := d["last_block_hash"].(string)

	var peerCount, inbound, outbound int
	if peers, ok := d["peers"].(map[string]interface{}); ok {
		peerCount = int(getFloat(peers, "total"))
		inbound = int(getFloat(peers, "inbound"))
		outbound = int(getFloat(peers, "outbound"))
	}

	var miningEnabled, miningActive bool
	var minerAddr string
	if mining, ok := d["mining"].(map[string]interface{}); ok {
		miningEnabled, _ = mining["enabled"].(bool)
		miningActive, _ = mining["active"].(bool)
		minerAddr, _ = mining["address"].(string)
	}

	return NodeStatus{
		Connected:      true,
		Version:        version,
		BlockHeight:    blockHeight,
		PendingTxs:     pendingTxs,
		Difficulty:      difficulty,
		DifficultyBits:  diffBits,
		PeerCount:       peerCount,
		InboundPeers:    inbound,
		OutboundPeers:   outbound,
		MiningEnabled:   miningEnabled,
		MiningActive:    miningActive,
		MinerAddress:    minerAddr,
		LastBlockHash:   lastHash,
	}
}

// --- Explorer Stats ---

func (a *App) GetExplorerStats() ExplorerStats {
	resp, err := a.getJSON("/stats")
	if err != nil {
		return ExplorerStats{Error: err.Error()}
	}
	if !resp.Success {
		return ExplorerStats{Error: resp.Message}
	}

	d := resp.Data
	var stats ExplorerStats

	if bc, ok := d["blockchain"].(map[string]interface{}); ok {
		stats.Height = int(getFloat(bc, "height"))
		stats.TotalTxs = int(getFloat(bc, "total_txs"))
		stats.Difficulty = int(getFloat(bc, "difficulty"))
		stats.DifficultyBits = int(getFloat(bc, "difficulty_bits"))
		stats.AvgBlockTime = getFloat(bc, "avg_block_time")
		stats.HashRate = getFloat(bc, "hashrate_estimate")
	}
	if mp, ok := d["mempool"].(map[string]interface{}); ok {
		stats.MempoolSize = int(getFloat(mp, "size"))
	}
	if peers, ok := d["peers"].(map[string]interface{}); ok {
		stats.PeerCount = int(getFloat(peers, "connected"))
		stats.KnownAddresses = int(getFloat(peers, "known_addresses"))
	}
	if supply, ok := d["supply"].(map[string]interface{}); ok {
		if reward, ok := supply["block_reward_dlt"].(string); ok {
			stats.BlockReward = reward
		}
	}

	return stats
}

// --- Peers ---

func (a *App) GetPeers() NetworkInfo {
	resp, err := a.getJSON("/peers")
	if err != nil {
		return NetworkInfo{}
	}
	if !resp.Success {
		return NetworkInfo{}
	}

	var info NetworkInfo
	info.PeerCount = int(getFloat(resp.Data, "count"))

	rawPeers, ok := resp.Data["peers"].([]interface{})
	if ok {
		for _, p := range rawPeers {
			if pm, ok := p.(map[string]interface{}); ok {
				addr, _ := pm["address"].(string)
				state, _ := pm["state"].(string)
				inbound, _ := pm["inbound"].(bool)
				direction := "outbound"
				if inbound {
					direction = "inbound"
				}
				info.Peers = append(info.Peers, PeerInfo{
					Address:   addr,
					State:     state,
					Inbound:   inbound,
					Direction: direction,
				})
			}
		}
	}

	// Get banned count
	bannedResp, err := a.getJSON("/peers/banned")
	if err == nil && bannedResp.Success {
		info.BannedCount = int(getFloat(bannedResp.Data, "count"))
	}

	// Get network stats for inbound/outbound
	netResp, err := a.getJSON("/network")
	if err == nil && netResp.Success {
		info.InboundCount = int(getFloat(netResp.Data, "InboundCount"))
		info.OutboundCount = int(getFloat(netResp.Data, "OutboundCount"))
		info.KnownAddresses = int(getFloat(netResp.Data, "KnownAddresses"))
	}

	return info
}

func (a *App) AddPeer(addr string) string {
	resp, err := a.postJSON("/peers/add?addr="+addr, nil)
	if err != nil {
		return "Error: " + err.Error()
	}
	if !resp.Success {
		return "Error: " + resp.Message
	}
	return resp.Message
}

func (a *App) BanPeer(addr string) string {
	resp, err := a.postJSON("/peers/ban?addr="+addr, nil)
	if err != nil {
		return "Error: " + err.Error()
	}
	return resp.Message
}

func (a *App) UnbanPeer(addr string) string {
	resp, err := a.postJSON("/peers/unban?addr="+addr, nil)
	if err != nil {
		return "Error: " + err.Error()
	}
	return resp.Message
}

// --- Blocks ---

func (a *App) GetRecentBlocks(limit int) []BlockInfo {
	if limit <= 0 {
		limit = 20
	}
	resp, err := a.getJSON(fmt.Sprintf("/chain?limit=%d", limit))
	if err != nil {
		return nil
	}
	if !resp.Success {
		return nil
	}

	rawBlocks, ok := resp.Data["blocks"].([]interface{})
	if !ok {
		return nil
	}

	var blocks []BlockInfo
	for _, b := range rawBlocks {
		bm, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		txs, _ := bm["transactions"].([]interface{})
		blocks = append(blocks, BlockInfo{
			Index:        int64(getFloat(bm, "Index")),
			Hash:         getString(bm, "Hash"),
			PreviousHash: getString(bm, "PreviousHash"),
			Timestamp:    int64(getFloat(bm, "Timestamp")),
			Nonce:        int64(getFloat(bm, "Nonce")),
			Difficulty:   int(getFloat(bm, "Difficulty")),
			TxCount:      len(txs),
		})
	}

	// Reverse to show newest first
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
	}

	return blocks
}

// --- Mempool ---

func (a *App) GetMempool() []MempoolTx {
	resp, err := a.getJSON("/mempool")
	if err != nil {
		return nil
	}
	if !resp.Success {
		return nil
	}

	rawTxs, ok := resp.Data["transactions"].([]interface{})
	if !ok {
		return nil
	}

	var txs []MempoolTx
	for _, t := range rawTxs {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		amountDLT, _ := tm["amount_dlt"].(string)
		if amountDLT == "" {
			amount := int64(getFloat(tm, "amount"))
			amountDLT = formatDLT(amount)
		}
		sig, _ := tm["signature"].(string)
		if len(sig) > 16 {
			sig = sig[:16] + "..."
		}

		txs = append(txs, MempoolTx{
			From:      getString(tm, "from"),
			To:        getString(tm, "to"),
			AmountDLT: amountDLT,
			Timestamp: int64(getFloat(tm, "timestamp")),
			Signature: sig,
		})
	}

	return txs
}

// --- Helpers ---

func getFloat(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

const dltUnit int64 = 100_000_000

func formatDLT(amount int64) string {
	whole := amount / dltUnit
	frac := amount % dltUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}
