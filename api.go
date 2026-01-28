package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIResponse is the standard response format
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// StartAPI starts the HTTP server for the node
func (n *Node) StartAPI(apiPort string) {
	// Create a new mux for this node (instead of using the default one)
	mux := http.NewServeMux()

	// Routes
	mux.HandleFunc("/chain", n.handleGetChain)
	mux.HandleFunc("/mine", n.handleMine)
	mux.HandleFunc("/status", n.handleStatus)
	mux.HandleFunc("/peers", n.handleGetPeers)
	mux.HandleFunc("/add-peer", n.handleAddPeer)

	apiAddr := ":" + apiPort
	fmt.Printf("API listening on http://localhost:%s\n", apiPort)
	fmt.Printf("   GET  http://localhost:%s/chain\n", apiPort)
	fmt.Printf("   POST http://localhost:%s/mine?data=<message>\n", apiPort)
	fmt.Printf("   GET  http://localhost:%s/status\n", apiPort)
	fmt.Printf("   GET  http://localhost:%s/peers\n", apiPort)
	fmt.Printf("   POST http://localhost:%s/add-peer?address=<address>\n\n", apiPort)

	if err := http.ListenAndServe(apiAddr, mux); err != nil {
		fmt.Printf("API error: %v\n", err)
	}
}

// handleGetChain returns the entire blockchain
func (n *Node) handleGetChain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	n.Blockchain.Blocks = n.Blockchain.Blocks // Make sure we have latest

	response := APIResponse{
		Success: true,
		Message: "Blockchain retrieved",
		Data: map[string]interface{}{
			"length": len(n.Blockchain.Blocks),
			"blocks": n.Blockchain.Blocks,
		},
	}

	respondJSON(w, http.StatusOK, response)
}

// handleMine mines a new block
func (n *Node) handleMine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	// Get data from query parameter
	data := r.URL.Query().Get("data")
	if data == "" {
		respondError(w, http.StatusBadRequest, "Missing 'data' query parameter")
		return
	}

	minerAddress := r.URL.Query().Get("miner")
	if minerAddress == "" {
		minerAddress = "anonymous"
	}

	fmt.Printf("Mining block with data: %s\n", data)

	// Create a transaction from the data
	tx := NewTransaction("API", minerAddress, 0)
	tx.Signature = data // Store the data in signature field for now
	n.Blockchain.AddTransaction(tx)

	// Mine the block
	newBlock := n.Blockchain.MinePendingTransactions(minerAddress)

	// Broadcast to peers
	n.broadcastBlock(newBlock)

	response := APIResponse{
		Success: true,
		Message: "Block mined and broadcast",
		Data: map[string]interface{}{
			"block_index":  newBlock.Index,
			"hash":         newBlock.Hash,
			"transactions": len(newBlock.Transactions),
		},
	}

	respondJSON(w, http.StatusOK, response)
}

// handleStatus returns node status
func (n *Node) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	n.PeersMutex.RLock()
	peersCount := len(n.Peers)
	n.PeersMutex.RUnlock()

	response := APIResponse{
		Success: true,
		Message: "Node status",
		Data: map[string]interface{}{
			"address":           n.Address,
			"blockchain_length": len(n.Blockchain.Blocks),
			"peers_connected":   peersCount,
			"difficulty":        n.Blockchain.Difficulty,
			"is_valid":          n.Blockchain.IsValid(),
		},
	}

	respondJSON(w, http.StatusOK, response)
}

// handleGetPeers returns connected peers
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

	response := APIResponse{
		Success: true,
		Message: "Connected peers",
		Data: map[string]interface{}{
			"count": len(peers),
			"peers": peers,
		},
	}

	respondJSON(w, http.StatusOK, response)
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

	err := n.ConnectToPeer(address)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Failed to connect: %v", err))
		return
	}

	response := APIResponse{
		Success: true,
		Message: fmt.Sprintf("Connected to peer %s", address),
		Data: map[string]interface{}{
			"peer": address,
		},
	}

	respondJSON(w, http.StatusOK, response)
}

// respondJSON sends a JSON response
func respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// respondError sends an error response
func respondError(w http.ResponseWriter, statusCode int, message string) {
	response := APIResponse{
		Success: false,
		Message: message,
	}
	respondJSON(w, statusCode, response)
}

