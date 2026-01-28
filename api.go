package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIResponse is the standard response format for all API endpoints
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// StartAPI starts the HTTP server for the node
func (n *Node) StartAPI(apiPort string) {
	mux := http.NewServeMux()

	// Register all routes
	n.registerRoutes(mux)

	apiAddr := "0.0.0.0:" + apiPort
	n.printAPIInfo(apiPort)

	if err := http.ListenAndServe(apiAddr, mux); err != nil {
		fmt.Printf("API error: %v\n", err)
	}
}

// registerRoutes registers all API endpoints
func (n *Node) registerRoutes(mux *http.ServeMux) {
	// Blockchain endpoints
	mux.HandleFunc("/chain", n.handleGetChain)
	mux.HandleFunc("/mine", n.handleMine)
	mux.HandleFunc("/status", n.handleStatus)

	// Peer endpoints
	mux.HandleFunc("/peers", n.handleGetPeers)
	mux.HandleFunc("/add-peer", n.handleAddPeer)

	// Transaction endpoints
	mux.HandleFunc("/transaction", n.handleAddTransaction)
	mux.HandleFunc("/mempool", n.handleGetMempool)

}

// printAPIInfo prints API endpoint information
func (n *Node) printAPIInfo(apiPort string) {
	fmt.Printf("API listening on 0.0.0.0:%s\n", apiPort)
	fmt.Println("Endpoints:")
	fmt.Printf("  POST http://YOUR_IP:%s/transaction          - Submit signed transaction\n", apiPort)
	fmt.Printf("  GET  http://YOUR_IP:%s/mempool              - View pending transactions\n", apiPort)
	fmt.Printf("  POST http://YOUR_IP:%s/mine?data=X&miner=Y  - Mine a block\n", apiPort)
	fmt.Printf("  GET  http://YOUR_IP:%s/chain                - View blockchain\n", apiPort)
	fmt.Printf("  GET  http://YOUR_IP:%s/status               - Node status\n", apiPort)
	fmt.Printf("  GET  http://YOUR_IP:%s/peers                - Connected peers\n", apiPort)
	fmt.Printf("  POST http://YOUR_IP:%s/add-peer?address=X   - Connect to peer\n\n", apiPort)
}

// ============================================================================
// TRANSACTION ENDPOINTS
// ============================================================================

// handleAddTransaction receives and adds signed transactions to mempool
func (n *Node) handleAddTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	var req struct {
		From      string  `json:"from"`
		To        string  `json:"to"`
		Amount    float64 `json:"amount"`
		Timestamp int64   `json:"timestamp"`
		Signature string  `json:"signature"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tx := &Transaction{
		From:      req.From,
		To:        req.To,
		Amount:    req.Amount,
		Timestamp: req.Timestamp,
		Signature: req.Signature,
	}

	// Validate transaction
	if err := validateTransaction(tx); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Add to mempool
	if err := n.Blockchain.AddTransaction(tx); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Failed to add transaction: %v", err))
		return
	}

	// Broadcast to peers
	n.broadcastTransaction(tx)

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Transaction added to mempool",
		Data: map[string]interface{}{
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"timestamp": tx.Timestamp,
			"signature": truncateString(tx.Signature, 32),
		},
	})
}

// handleGetMempool returns all pending transactions in the mempool
func (n *Node) handleGetMempool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	txDisplay := make([]map[string]interface{}, 0, len(n.Blockchain.PendingTransactions))
	for _, tx := range n.Blockchain.PendingTransactions {
		txDisplay = append(txDisplay, map[string]interface{}{
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"timestamp": tx.Timestamp,
			"signature": truncateString(tx.Signature, 32),
		})
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Mempool retrieved",
		Data: map[string]interface{}{
			"count":        len(n.Blockchain.PendingTransactions),
			"transactions": txDisplay,
		},
	})
}

// ============================================================================
// BLOCKCHAIN ENDPOINTS
// ============================================================================

// handleGetChain returns the entire blockchain
func (n *Node) handleGetChain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Blockchain retrieved",
		Data: map[string]interface{}{
			"length": len(n.Blockchain.Blocks),
			"blocks": n.Blockchain.Blocks,
		},
	})
}

// handleMine mines a new block with pending transactions
func (n *Node) handleMine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	minerAddress := r.URL.Query().Get("miner")
	if minerAddress == "" {
		minerAddress = "anonymous"
	}

	fmt.Printf("Mining block for miner: %s\n", minerAddress)

	// Mine the block with pending transactions from mempool
	newBlock := n.Blockchain.MinePendingTransactions(minerAddress)

	// Broadcast to peers
	n.broadcastBlock(newBlock)

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Block mined and broadcast",
		Data: map[string]interface{}{
			"block_index":  newBlock.Index,
			"hash":         newBlock.Hash,
			"transactions": len(newBlock.Transactions),
		},
	})
}

// handleStatus returns the current node status
func (n *Node) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	n.PeersMutex.RLock()
	peersCount := len(n.Peers)
	n.PeersMutex.RUnlock()

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Node status",
		Data: map[string]interface{}{
			"address":            n.Address,
			"blockchain_length":  len(n.Blockchain.Blocks),
			"pending_transactions": len(n.Blockchain.PendingTransactions),
			"peers_connected":    peersCount,
			"difficulty":         n.Blockchain.Difficulty,
			"is_valid":           n.Blockchain.IsValid(),
		},
	})
}

// ============================================================================
// PEER ENDPOINTS
// ============================================================================

// handleGetPeers returns list of connected peers
func (n *Node) handleGetPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	n.PeersMutex.RLock()
	peers := make([]string, 0, len(n.Peers))
	for addr := range n.Peers {
		peers = append(peers, addr)
	}
	n.PeersMutex.RUnlock()

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Connected peers",
		Data: map[string]interface{}{
			"count": len(peers),
			"peers": peers,
		},
	})
}

// handleAddPeer connects to a new peer
func (n *Node) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	address := r.URL.Query().Get("address")
	if address == "" {
		respondError(w, http.StatusBadRequest, "Missing 'address' query parameter")
		return
	}

	if err := n.ConnectToPeer(address); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Failed to connect: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Connected to peer %s", address),
		Data: map[string]interface{}{
			"peer": address,
		},
	})
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================


// truncateString safely truncates a string and adds ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// respondJSON sends a JSON response with proper headers
func respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// respondError sends an error response
func respondError(w http.ResponseWriter, statusCode int, message string) {
	respondJSON(w, statusCode, APIResponse{
		Success: false,
		Message: message,
	})
}
