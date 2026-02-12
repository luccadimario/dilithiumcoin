package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

// APIResponse is the standard response format for all API endpoints
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// StartAPI starts the HTTP server for the node
// If tlsCert and tlsKey are provided, it starts an HTTPS server (shannon #4)
func (n *Node) StartAPI(apiHost, apiPort, tlsCert, tlsKey string) {
	mux := http.NewServeMux()

	// Register all routes
	n.registerRoutes(mux)

	apiAddr := apiHost + ":" + apiPort
	n.printAPIInfo(apiHost, apiPort)

	// Use http.Server with timeouts (shannon #18)
	server := &http.Server{
		Addr:         apiAddr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if tlsCert != "" && tlsKey != "" {
		fmt.Printf("TLS enabled (cert: %s)\n", tlsCert)
		if err := server.ListenAndServeTLS(tlsCert, tlsKey); err != nil {
			fmt.Printf("API TLS error: %v\n", err)
		}
	} else {
		if err := server.ListenAndServe(); err != nil {
			fmt.Printf("API error: %v\n", err)
		}
	}
}

// corsMiddleware adds CORS headers to responses
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Restrict CORS to known origins (shannon #2)
		origin := r.Header.Get("Origin")
		allowedOrigins := map[string]bool{
			"https://dilithiumcoin.com":     true,
			"https://www.dilithiumcoin.com": true,
			"http://localhost:3000":          true,
			"http://localhost:3001":          true,
		}
		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		// Always allow same-origin requests (no Origin header)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// registerRoutes registers all API endpoints
func (n *Node) registerRoutes(mux *http.ServeMux) {
	// Rate limiters for different endpoint categories
	writeLimiter := NewRateLimiter(30, time.Minute)   // 30 req/min for state-changing ops
	mineLimiter := NewRateLimiter(10, time.Minute)     // 10 req/min for mining
	readLimiter := NewRateLimiter(120, time.Minute)    // 120 req/min for reads

	// Node info (read-limited)
	mux.HandleFunc("/", rateLimitMiddleware(readLimiter, n.handleRoot))
	mux.HandleFunc("/status", rateLimitMiddleware(readLimiter, n.handleStatus))
	mux.HandleFunc("/version", rateLimitMiddleware(readLimiter, n.handleVersion))

	// Blockchain endpoints
	mux.HandleFunc("/chain", rateLimitMiddleware(readLimiter, n.handleGetChain))
	mux.HandleFunc("/block", rateLimitMiddleware(readLimiter, n.handleGetBlock))
	mux.HandleFunc("/block/submit", rateLimitMiddleware(writeLimiter, n.handleBlockSubmit))
	mux.HandleFunc("/mine", rateLimitMiddleware(mineLimiter, n.handleMine))

	// Transaction endpoints
	mux.HandleFunc("/transaction", rateLimitMiddleware(writeLimiter, n.handleTransaction))
	mux.HandleFunc("/mempool", rateLimitMiddleware(readLimiter, n.handleGetMempool))
	mux.HandleFunc("/mempool/stats", rateLimitMiddleware(readLimiter, n.handleMempoolStats))

	// Peer endpoints (write-limited for sensitive ops)
	mux.HandleFunc("/peers", rateLimitMiddleware(readLimiter, n.handlePeers))
	mux.HandleFunc("/peers/add", rateLimitMiddleware(writeLimiter, n.handleAddPeer))
	mux.HandleFunc("/peers/ban", rateLimitMiddleware(writeLimiter, n.handleBanPeer))
	mux.HandleFunc("/peers/unban", rateLimitMiddleware(writeLimiter, n.handleUnbanPeer))
	mux.HandleFunc("/peers/banned", rateLimitMiddleware(readLimiter, n.handleGetBanned))

	// Network endpoints
	mux.HandleFunc("/network", rateLimitMiddleware(readLimiter, n.handleNetworkStats))
	mux.HandleFunc("/network/addresses", rateLimitMiddleware(readLimiter, n.handleGetAddresses))

	// Explorer endpoints (for block explorer / dashboard)
	mux.HandleFunc("/stats", rateLimitMiddleware(readLimiter, n.handleExplorerStats))
	mux.HandleFunc("/explorer/tx", rateLimitMiddleware(readLimiter, n.handleExplorerTx))
	mux.HandleFunc("/explorer/address", rateLimitMiddleware(readLimiter, n.handleGetAddress))

	// Legacy endpoints (backwards compatibility)
	mux.HandleFunc("/add-peer", rateLimitMiddleware(writeLimiter, n.handleAddPeerLegacy))
}

// printAPIInfo prints API endpoint information
func (n *Node) printAPIInfo(apiHost, apiPort string) {
	fmt.Printf("API listening on %s:%s\n", apiHost, apiPort)
	fmt.Println("API Endpoints:")
	fmt.Printf("  GET  /status              - Node status\n")
	fmt.Printf("  GET  /version             - Version info\n")
	fmt.Printf("  GET  /chain               - Full blockchain\n")
	fmt.Printf("  GET  /block?index=N       - Get block by index or hash\n")
	fmt.Printf("  POST /block/submit        - Submit a mined block\n")
	fmt.Printf("  POST /mine?miner=ADDR     - Mine a block\n")
	fmt.Printf("  POST /transaction         - Submit transaction\n")
	fmt.Printf("  GET  /mempool             - Pending transactions\n")
	fmt.Printf("  GET  /mempool/stats       - Mempool statistics\n")
	fmt.Printf("  GET  /peers               - Connected peers\n")
	fmt.Printf("  POST /peers/add?addr=X    - Connect to peer\n")
	fmt.Printf("  POST /peers/ban?addr=X    - Ban a peer\n")
	fmt.Printf("  POST /peers/unban?addr=X  - Unban a peer\n")
	fmt.Printf("  GET  /peers/banned        - List banned peers\n")
	fmt.Printf("  GET  /network             - Network statistics\n")
	fmt.Printf("  GET  /network/addresses   - Known addresses\n")
	fmt.Printf("  GET  /stats               - Explorer stats (for dashboards)\n")
	fmt.Printf("  GET  /explorer/tx?hash=X  - Get transaction by hash\n")
	fmt.Printf("  GET  /explorer/address?addr=X - Get address info\n")
	fmt.Println()
}

// ============================================================================
// NODE INFO ENDPOINTS
// ============================================================================

// handleRoot returns basic node info
func (n *Node) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		respondError(w, http.StatusNotFound, "Not found")
		return
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Dilithium Node API",
		Data: map[string]interface{}{
			"name":     NetworkName,
			"version":  Version,
			"protocol": ProtocolVersion,
		},
	})
}

// handleVersion returns version information
func (n *Node) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Version info",
		Data: map[string]interface{}{
			"name":       NetworkName,
			"version":    Version,
			"protocol":   ProtocolVersion,
			"user_agent": UserAgent,
		},
	})
}

// handleStatus returns the current node status
func (n *Node) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	// Get peer count from peer manager if available, otherwise use legacy
	var peersCount, inboundCount, outboundCount int
	if n.PeerManager != nil {
		stats := n.PeerManager.GetNetworkStats()
		peersCount = stats.PeerCount
		inboundCount = stats.InboundCount
		outboundCount = stats.OutboundCount
	} else {
		n.PeersMutex.RLock()
		peersCount = len(n.Peers)
		n.PeersMutex.RUnlock()
	}

	// Mining status
	n.miningMutex.Lock()
	isMining := n.isMining
	autoMine := n.autoMineEnabled
	minerAddr := n.MinerAddress
	n.miningMutex.Unlock()

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Node status",
		Data: map[string]interface{}{
			"version":              Version,
			"protocol":             ProtocolVersion,
			"address":              n.Address,
			"blockchain_height":    n.Blockchain.GetBlockCount(),
			"pending_transactions": n.Blockchain.GetPendingCount(),
			"peers": map[string]interface{}{
				"total":    peersCount,
				"inbound":  inboundCount,
				"outbound": outboundCount,
			},
			"mining": map[string]interface{}{
				"enabled":  autoMine,
				"active":   isMining,
				"address":  minerAddr,
			},
			"difficulty":      n.Blockchain.GetCurrentDifficulty(),
			"difficulty_bits": n.Blockchain.GetCurrentDifficultyBits(),
			"valid":      n.Blockchain.IsValid(),
			"uptime":     time.Now().Unix(),
		},
	})
}

// ============================================================================
// BLOCKCHAIN ENDPOINTS
// ============================================================================

// handleGetChain returns the blockchain with pagination (shannon #5)
func (n *Node) handleGetChain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	blocks := n.Blockchain.GetBlocks()
	totalHeight := len(blocks)

	// Parse pagination params (default: last 50 blocks)
	limitStr := r.URL.Query().Get("limit")
	pageStr := r.URL.Query().Get("page")

	limit := 50 // default
	if limitStr != "" {
		if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 {
			limit = int(l)
		}
	}
	if limit > 500 {
		limit = 500 // hard cap
	}

	page := 0
	if pageStr != "" {
		if p, err := strconv.ParseInt(pageStr, 10, 64); err == nil && p >= 0 {
			page = int(p)
		}
	}

	start := page * limit
	if start >= totalHeight {
		start = totalHeight
	}
	end := start + limit
	if end > totalHeight {
		end = totalHeight
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Blockchain retrieved",
		Data: map[string]interface{}{
			"height":     totalHeight,
			"page":       page,
			"limit":      limit,
			"total_pages": (totalHeight + limit - 1) / limit,
			"blocks":     blocks[start:end],
		},
	})
}

// handleGetBlock returns a specific block
func (n *Node) handleGetBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	// Try by index first
	indexStr := r.URL.Query().Get("index")
	if indexStr != "" {
		// Use strconv.ParseInt instead of fmt.Sscanf (shannon #15)
		index, err := strconv.ParseInt(indexStr, 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, "Invalid index parameter")
			return
		}
		block := n.Blockchain.GetBlockByIndex(index)
		if block == nil {
			respondError(w, http.StatusNotFound, "Block not found")
			return
		}
		respondJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Block retrieved",
			Data:    block,
		})
		return
	}

	// Try by hash
	hash := r.URL.Query().Get("hash")
	if hash != "" {
		block := n.Blockchain.GetBlock(hash)
		if block == nil {
			respondError(w, http.StatusNotFound, "Block not found")
			return
		}
		respondJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Block retrieved",
			Data:    block,
		})
		return
	}

	respondError(w, http.StatusBadRequest, "Must provide 'index' or 'hash' parameter")
}

// handleBlockSubmit accepts a mined block from an external miner
func (n *Node) handleBlockSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	// Limit request body size to 1MB for block submissions (shannon #3)
	r.Body = http.MaxBytesReader(w, r.Body, 1*1024*1024)

	var block Block
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid block data")
		return
	}

	// Verify hash is correct
	if block.Hash != block.CalculateHash() {
		respondError(w, http.StatusBadRequest, "Block hash does not match calculated hash")
		return
	}

	// Acquire mutex BEFORE difficulty validation to avoid race condition (shannon #10)
	n.Blockchain.mutex.Lock()

	// Verify difficulty matches expected (bit-based or legacy)
	expectedBits := n.Blockchain.GetCurrentDifficultyBitsLocked()
	expectedHex := difficultyBitsToHexDigits(expectedBits)

	// All blocks must meet bit-based PoW target regardless of version
	if !meetsDifficultyBits(block.Hash, expectedBits) {
		n.Blockchain.mutex.Unlock()
		respondError(w, http.StatusBadRequest, fmt.Sprintf(
			"Block does not meet difficulty target (%d bits required)", expectedBits))
		return
	}

	if block.DifficultyBits > 0 {
		// Bit-based block — validate DifficultyBits field matches expected
		if block.DifficultyBits != expectedBits {
			n.Blockchain.mutex.Unlock()
			respondError(w, http.StatusBadRequest, fmt.Sprintf(
				"Block difficulty bits %d does not match expected %d",
				block.DifficultyBits, expectedBits))
			return
		}
	} else {
		// Legacy block — also check hex-digit difficulty is reasonable
		if block.Difficulty != expectedHex {
			n.Blockchain.mutex.Unlock()
			respondError(w, http.StatusBadRequest, fmt.Sprintf(
				"Block difficulty %d does not match expected difficulty %d",
				block.Difficulty, expectedHex))
			return
		}
	}

	// Verify block connects to our chain
	lastBlock := n.Blockchain.Blocks[len(n.Blockchain.Blocks)-1]
	if block.PreviousHash != lastBlock.Hash {
		n.Blockchain.mutex.Unlock()
		respondError(w, http.StatusBadRequest, "Block does not connect to chain tip")
		return
	}

	if block.Index != lastBlock.Index+1 {
		n.Blockchain.mutex.Unlock()
		respondError(w, http.StatusBadRequest, "Invalid block index")
		return
	}

	// Validate block timestamp (shannon #12)
	now := time.Now().Unix()
	maxFutureTime := now + 2*60*60 // 2 hours in the future
	if block.Timestamp > maxFutureTime {
		n.Blockchain.mutex.Unlock()
		respondError(w, http.StatusBadRequest, "Block timestamp too far in the future")
		return
	}
	if block.Timestamp < lastBlock.Timestamp {
		n.Blockchain.mutex.Unlock()
		respondError(w, http.StatusBadRequest, "Block timestamp is before previous block")
		return
	}

	// Validate transactions
	if err := n.Blockchain.ValidateBlockTransactions(&block, n.Blockchain.Blocks); err != nil {
		n.Blockchain.mutex.Unlock()
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid transactions: %v", err))
		return
	}

	// Add block to chain
	n.Blockchain.Blocks = append(n.Blockchain.Blocks, &block)
	n.Blockchain.clearMinedTransactions(block.Transactions)
	n.Blockchain.mutex.Unlock()

	// Broadcast to peers
	if n.PeerManager != nil {
		n.PeerManager.BroadcastBlock(&block)
	} else {
		n.broadcastBlock(&block)
	}

	fmt.Printf("Block #%d submitted via API and accepted\n", block.Index)

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Block accepted",
		Data: map[string]interface{}{
			"index":        block.Index,
			"hash":         block.Hash,
			"transactions": len(block.Transactions),
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
		respondError(w, http.StatusBadRequest, "Missing 'miner' query parameter")
		return
	}

	pendingCount := n.Blockchain.GetPendingCount()
	if pendingCount > 0 {
		fmt.Printf("API: Mining block with %d transactions for %s\n", pendingCount, minerAddress)
	} else {
		fmt.Printf("API: Mining empty block (coinbase only) for %s\n", minerAddress)
	}

	// Mine the block
	newBlock := n.Blockchain.MinePendingTransactions(minerAddress)
	if newBlock == nil {
		respondError(w, http.StatusInternalServerError, "Mining failed")
		return
	}

	// Broadcast using peer manager if available
	if n.PeerManager != nil {
		n.PeerManager.BroadcastBlock(newBlock)
	} else {
		n.broadcastBlock(newBlock)
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Block mined and broadcast",
		Data: map[string]interface{}{
			"index":        newBlock.Index,
			"hash":         newBlock.Hash,
			"transactions": len(newBlock.Transactions),
			"nonce":        newBlock.Nonce,
		},
	})
}

// ============================================================================
// TRANSACTION ENDPOINTS
// ============================================================================

// handleTransaction handles GET and POST for transactions
func (n *Node) handleTransaction(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		n.handleGetTransaction(w, r)
	case http.MethodPost:
		n.handleAddTransaction(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "Only GET and POST allowed")
	}
}

// handleGetTransaction gets a transaction by signature
func (n *Node) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	sig := r.URL.Query().Get("signature")
	if sig == "" {
		respondError(w, http.StatusBadRequest, "Missing 'signature' parameter")
		return
	}

	tx := n.Blockchain.GetTransaction(sig)
	if tx == nil {
		respondError(w, http.StatusNotFound, "Transaction not found")
		return
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Transaction found",
		Data:    tx,
	})
}

// handleAddTransaction receives and adds signed transactions to mempool
func (n *Node) handleAddTransaction(w http.ResponseWriter, r *http.Request) {
	// Limit request body size (shannon #3)
	r.Body = http.MaxBytesReader(w, r.Body, 100*1024) // 100KB max for transactions

	var req struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Amount    int64  `json:"amount"`
		Timestamp int64  `json:"timestamp"`
		Signature string `json:"signature"`
		PublicKey string `json:"public_key"`
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
		PublicKey:  req.PublicKey,
	}

	// Validate transaction
	if err := validateTransaction(tx); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Add to mempool
	added, err := n.Blockchain.AddTransactionIfNew(tx)
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Failed to add transaction: %v", err))
		return
	}

	if !added {
		respondJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Transaction already in mempool",
			Data: map[string]interface{}{
				"signature": truncateString(tx.Signature, 32),
			},
		})
		return
	}

	// Broadcast to peers
	if n.PeerManager != nil {
		n.PeerManager.BroadcastTransaction(tx)
	} else {
		n.broadcastTransaction(tx)
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Transaction added to mempool",
		Data: map[string]interface{}{
			"from":       tx.From,
			"to":         tx.To,
			"amount":     tx.Amount,
			"amount_dlt": FormatDLT(tx.Amount),
			"timestamp":  tx.Timestamp,
			"signature":  truncateString(tx.Signature, 32),
		},
	})
}

// handleGetMempool returns all pending transactions in the mempool
func (n *Node) handleGetMempool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	txs := n.Blockchain.GetPendingTransactions()
	txDisplay := make([]map[string]interface{}, 0, len(txs))

	for _, tx := range txs {
		txDisplay = append(txDisplay, map[string]interface{}{
			"from":       tx.From,
			"to":         tx.To,
			"amount":     tx.Amount,
			"amount_dlt": FormatDLT(tx.Amount),
			"timestamp":  tx.Timestamp,
			"signature":  truncateString(tx.Signature, 32),
			"public_key": tx.PublicKey,
		})
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Mempool retrieved",
		Data: map[string]interface{}{
			"count":        len(txs),
			"transactions": txDisplay,
		},
	})
}

// handleMempoolStats returns mempool statistics
func (n *Node) handleMempoolStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	txs := n.Blockchain.GetPendingTransactions()
	totalBytes := 0
	for _, tx := range txs {
		totalBytes += len(tx.ToJSON())
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Mempool statistics",
		Data: map[string]interface{}{
			"size":  len(txs),
			"bytes": totalBytes,
		},
	})
}

// ============================================================================
// PEER ENDPOINTS
// ============================================================================

// handlePeers returns connected peers
func (n *Node) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	if n.PeerManager != nil {
		peers := n.PeerManager.GetPeers()
		respondJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Connected peers",
			Data: map[string]interface{}{
				"count": len(peers),
				"peers": peers,
			},
		})
		return
	}

	// Legacy fallback
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

	address := r.URL.Query().Get("addr")
	if address == "" {
		address = r.URL.Query().Get("address") // Legacy support
	}
	if address == "" {
		respondError(w, http.StatusBadRequest, "Missing 'addr' query parameter")
		return
	}

	var err error
	if n.PeerManager != nil {
		err = n.PeerManager.Connect(address)
	} else {
		err = n.ConnectToPeer(address)
	}

	if err != nil {
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

// handleAddPeerLegacy handles legacy /add-peer endpoint
func (n *Node) handleAddPeerLegacy(w http.ResponseWriter, r *http.Request) {
	n.handleAddPeer(w, r)
}

// handleBanPeer bans a peer
func (n *Node) handleBanPeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	if n.PeerManager == nil {
		respondError(w, http.StatusNotImplemented, "Peer manager not available")
		return
	}

	address := r.URL.Query().Get("addr")
	if address == "" {
		respondError(w, http.StatusBadRequest, "Missing 'addr' query parameter")
		return
	}

	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "Manual ban via API"
	}

	n.PeerManager.Ban(address, reason)

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Banned peer %s", address),
		Data: map[string]interface{}{
			"peer":   address,
			"reason": reason,
		},
	})
}

// handleUnbanPeer unbans a peer
func (n *Node) handleUnbanPeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Only POST allowed")
		return
	}

	if n.PeerManager == nil {
		respondError(w, http.StatusNotImplemented, "Peer manager not available")
		return
	}

	address := r.URL.Query().Get("addr")
	if address == "" {
		respondError(w, http.StatusBadRequest, "Missing 'addr' query parameter")
		return
	}

	n.PeerManager.Unban(address)

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Unbanned peer %s", address),
		Data: map[string]interface{}{
			"peer": address,
		},
	})
}

// handleGetBanned returns list of banned peers
func (n *Node) handleGetBanned(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	if n.PeerManager == nil {
		respondError(w, http.StatusNotImplemented, "Peer manager not available")
		return
	}

	banned := n.PeerManager.GetBannedPeers()
	bannedList := make([]map[string]interface{}, 0, len(banned))

	for addr, expiry := range banned {
		bannedList = append(bannedList, map[string]interface{}{
			"address": addr,
			"expires": expiry.Unix(),
		})
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Banned peers",
		Data: map[string]interface{}{
			"count":  len(banned),
			"banned": bannedList,
		},
	})
}

// ============================================================================
// NETWORK ENDPOINTS
// ============================================================================

// handleNetworkStats returns network statistics
func (n *Node) handleNetworkStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	if n.PeerManager == nil {
		respondError(w, http.StatusNotImplemented, "Peer manager not available")
		return
	}

	stats := n.PeerManager.GetNetworkStats()

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Network statistics",
		Data:    stats,
	})
}

// handleGetAddresses returns known network addresses
func (n *Node) handleGetAddresses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	if n.PeerManager == nil {
		respondError(w, http.StatusNotImplemented, "Peer manager not available")
		return
	}

	addrs := n.PeerManager.GetAddresses(100)
	addrList := make([]map[string]interface{}, 0, len(addrs))

	for _, addr := range addrs {
		addrList = append(addrList, map[string]interface{}{
			"ip":        addr.IP,
			"port":      addr.Port,
			"services":  addr.Services,
			"last_seen": addr.Timestamp,
		})
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Known addresses",
		Data: map[string]interface{}{
			"count":     len(addrList),
			"addresses": addrList,
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

// ============================================================================
// EXPLORER ENDPOINTS (for dashboards and block explorers)
// ============================================================================

// handleExplorerStats returns comprehensive stats for block explorers
func (n *Node) handleExplorerStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	blocks := n.Blockchain.GetBlocks()
	height := len(blocks)

	// Use cached total transaction count (shannon #19)
	totalTxs := n.Blockchain.GetTotalTransactions()

	// Get current difficulty (both formats)
	currentDifficulty := n.Blockchain.GetCurrentDifficulty()
	currentDifficultyBits := n.Blockchain.GetCurrentDifficultyBits()

	// Calculate average block time (last 10 blocks)
	var avgBlockTime float64
	if height > 1 {
		numBlocks := 10
		if height < numBlocks {
			numBlocks = height - 1
		}
		if numBlocks > 0 {
			startBlock := blocks[height-numBlocks-1]
			endBlock := blocks[height-1]
			timeDiff := endBlock.Timestamp - startBlock.Timestamp
			avgBlockTime = float64(timeDiff) / float64(numBlocks)
		}
	}

	// Estimate hashrate using bit-based difficulty: 2^bits / avgBlockTime
	var estimatedHashrate float64
	if avgBlockTime > 0 {
		hashesPerBlock := math.Pow(2, float64(currentDifficultyBits))
		estimatedHashrate = hashesPerBlock / avgBlockTime
	}

	// Get peer info
	var peerCount, inboundCount, outboundCount, knownAddresses int
	if n.PeerManager != nil {
		stats := n.PeerManager.GetNetworkStats()
		peerCount = stats.PeerCount
		inboundCount = stats.InboundCount
		outboundCount = stats.OutboundCount
		knownAddresses = stats.KnownAddresses
	}

	// Last block info
	var lastBlock map[string]interface{}
	if height > 0 {
		lb := blocks[height-1]
		lastBlock = map[string]interface{}{
			"index":           lb.Index,
			"hash":            lb.Hash,
			"timestamp":       lb.Timestamp,
			"tx_count":        len(lb.Transactions),
			"difficulty":      lb.Difficulty,
			"difficulty_bits": lb.getEffectiveDifficultyBits(),
		}
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Explorer statistics",
		Data: map[string]interface{}{
			"network": map[string]interface{}{
				"name":     NetworkName,
				"version":  Version,
				"protocol": ProtocolVersion,
			},
			"blockchain": map[string]interface{}{
				"height":           height,
				"difficulty":       currentDifficulty,
				"difficulty_bits":  currentDifficultyBits,
				"total_txs":        totalTxs,
				"avg_block_time":   avgBlockTime,
				"hashrate_estimate": estimatedHashrate,
				"genesis_hash":     blocks[0].Hash,
			},
			"mempool": map[string]interface{}{
				"size": n.Blockchain.GetPendingCount(),
			},
			"peers": map[string]interface{}{
				"connected":       peerCount,
				"inbound":         inboundCount,
				"outbound":        outboundCount,
				"known_addresses": knownAddresses,
			},
			"last_block": lastBlock,
			"difficulty_adjustment": map[string]interface{}{
				"blocks_per_adjustment": BlocksPerAdjustment,
				"target_block_time":     TargetBlockTime,
				"blocks_until_adjust":   BlocksPerAdjustment - (height % BlocksPerAdjustment),
			},
			"supply": n.Blockchain.GetSupplyInfo(),
		},
	})
}

// handleExplorerTx returns a transaction by its hash/signature (searches mempool and blockchain)
func (n *Node) handleExplorerTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	hash := r.URL.Query().Get("hash")
	if hash == "" {
		respondError(w, http.StatusBadRequest, "Missing 'hash' query parameter")
		return
	}

	// Search in mempool first
	tx := n.Blockchain.GetTransaction(hash)
	if tx != nil {
		respondJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Message: "Transaction found in mempool",
			Data: map[string]interface{}{
				"transaction": tx,
				"confirmed":   false,
				"block_index": nil,
			},
		})
		return
	}

	// Search in blockchain
	blocks := n.Blockchain.GetBlocks()
	for _, block := range blocks {
		for _, btx := range block.Transactions {
			if btx.Signature == hash {
				respondJSON(w, http.StatusOK, APIResponse{
					Success: true,
					Message: "Transaction found",
					Data: map[string]interface{}{
						"transaction": btx,
						"confirmed":   true,
						"block_index": block.Index,
						"block_hash":  block.Hash,
					},
				})
				return
			}
		}
	}

	respondError(w, http.StatusNotFound, "Transaction not found")
}

// handleGetAddress returns address balance and transaction history
func (n *Node) handleGetAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "Only GET allowed")
		return
	}

	address := r.URL.Query().Get("addr")
	if address == "" {
		respondError(w, http.StatusBadRequest, "Missing 'addr' query parameter")
		return
	}

	// Use blockchain method to get address info (shannon #19)
	balance, received, sent, transactions := n.Blockchain.GetAddressInfo(address)
	txCount := len(transactions)

	// Check pending transactions
	var pendingTxs []map[string]interface{}
	for _, tx := range n.Blockchain.GetPendingTransactions() {
		if tx.To == address || tx.From == address {
			pendingTxs = append(pendingTxs, map[string]interface{}{
				"signature":  tx.Signature,
				"from":       tx.From,
				"to":         tx.To,
				"amount":     tx.Amount,
				"amount_dlt": FormatDLT(tx.Amount),
				"timestamp":  tx.Timestamp,
			})
		}
	}

	respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Address info",
		Data: map[string]interface{}{
			"address":              address,
			"balance":              balance,
			"balance_dlt":          FormatDLT(balance),
			"total_received":       received,
			"total_received_dlt":   FormatDLT(received),
			"total_sent":           sent,
			"total_sent_dlt":       FormatDLT(sent),
			"transaction_count":    txCount,
			"transactions":         transactions,
			"pending_transactions": pendingTxs,
		},
	})
}
