package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

// cumulativeWork calculates total proof-of-work for a chain (shannon #7)
// Each block contributes 2^difficultyBits of work
func cumulativeWork(chain []*Block) float64 {
	var total float64
	for _, b := range chain {
		bits := b.getEffectiveDifficultyBits()
		total += math.Pow(2, float64(bits))
	}
	return total
}

// ============================================================================
// MESSAGE
// ============================================================================

// Message represents data sent between nodes
type Message struct {
	Type      string      `json:"type"`      // "block", "transaction", or "chain"
	Data      interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// ============================================================================
// NODE
// ============================================================================

// Node represents a peer in the P2P network
type Node struct {
	Address      string
	Blockchain   *Blockchain
	Peers        map[string]net.Conn
	PeersMutex   sync.RWMutex
	Port         string
	MessageCh    chan Message

	// Peer management (v2.0)
	PeerManager  *PeerManager

	// Mining state
	MinerAddress   string
	miningCancel   chan struct{}
	miningMutex    sync.Mutex
	isMining       bool
	autoMineEnabled bool
}

// NewNode creates a new node with the given port and difficulty
func NewNode(port string, difficulty int) *Node {
	return &Node{
		Address:    fmt.Sprintf("localhost:%s", port),
		Blockchain: NewBlockchain(difficulty),
		Peers:      make(map[string]net.Conn),
		Port:       port,
		MessageCh:  make(chan Message, 100),
	}
}

// ============================================================================
// SERVER
// ============================================================================

// StartServer starts listening for incoming peer connections
func (n *Node) StartServer() {
	listener, err := net.Listen("tcp", "0.0.0.0:"+n.Port)
	if err != nil {
		fmt.Printf("Failed to start server on port %s: %v\n", n.Port, err)
		return
	}

	fmt.Printf("Node listening on 0.0.0.0:%s\n", n.Port)

	go n.acceptConnections(listener)
}

// acceptConnections accepts incoming peer connections
func (n *Node) acceptConnections(listener net.Listener) {
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if err.Error() != "use of closed network connection" {
				fmt.Printf("Error accepting connection: %v\n", err)
			}
			return
		}

		go n.handlePeerConnection(conn)
	}
}

// handlePeerConnection handles incoming messages from a peer
func (n *Node) handlePeerConnection(conn net.Conn) {
	defer conn.Close()

	peerAddr := conn.RemoteAddr().String()
	fmt.Printf("New peer connected: %s\n", peerAddr)

	// Store connection
	n.PeersMutex.Lock()
	n.Peers[peerAddr] = conn
	n.PeersMutex.Unlock()

	// Send our blockchain to the new peer
	n.sendBlockchain(conn)

	// Read messages from this peer
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			fmt.Printf("Error parsing message: %v\n", err)
			continue
		}

		n.handleMessage(msg, peerAddr)
	}

	// Clean up when peer disconnects
	n.PeersMutex.Lock()
	delete(n.Peers, peerAddr)
	n.PeersMutex.Unlock()
	fmt.Printf("Peer disconnected: %s\n", peerAddr)
}

// ============================================================================
// CLIENT
// ============================================================================

// ConnectToPeer connects to another node
func (n *Node) ConnectToPeer(peerAddress string) error {
	conn, err := net.Dial("tcp", peerAddress)
	if err != nil {
		fmt.Printf("Failed to connect to %s: %v\n", peerAddress, err)
		return err
	}

	fmt.Printf("Connected to peer: %s\n", peerAddress)

	// Store connection
	n.PeersMutex.Lock()
	n.Peers[peerAddress] = conn
	n.PeersMutex.Unlock()

	// Send our blockchain
	n.sendBlockchain(conn)

	// Read messages from this peer in a goroutine
	go n.readPeerMessages(conn, peerAddress)

	return nil
}

// readPeerMessages reads messages from a peer connection
func (n *Node) readPeerMessages(conn net.Conn, peerAddress string) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			fmt.Printf("Error parsing message: %v\n", err)
			continue
		}

		n.handleMessage(msg, peerAddress)
	}
}

// ============================================================================
// MESSAGE HANDLING
// ============================================================================

// handleMessage processes incoming messages based on type
func (n *Node) handleMessage(msg Message, peerAddr string) {
	switch msg.Type {
	case "transaction":
		n.handleTransactionMessage(msg, peerAddr)
	case "block":
		n.handleBlockMessage(msg, peerAddr)
	case "chain":
		n.handleChainMessage(msg, peerAddr)
	}
}

// handleTransactionMessage processes a transaction from a peer
func (n *Node) handleTransactionMessage(msg Message, peerAddr string) {
	var tx Transaction
	txJSON, _ := json.Marshal(msg.Data)
	if err := json.Unmarshal(txJSON, &tx); err != nil {
		fmt.Printf("Error parsing transaction: %v\n", err)
		return
	}

	// Add to mempool - returns true if this is a new transaction
	added, err := n.Blockchain.AddTransactionIfNew(&tx)
	if err != nil {
		fmt.Printf("Failed to add transaction to mempool: %v\n", err)
		return
	}

	// Only broadcast if this was a NEW transaction (prevents infinite loops)
	if added {
		fmt.Printf("Received new transaction from %s\n", peerAddr)
		n.broadcastTransaction(&tx)
	}
}

// handleBlockMessage processes a block from a peer
func (n *Node) handleBlockMessage(msg Message, peerAddr string) {
	var block Block
	blockJSON, _ := json.Marshal(msg.Data)
	if err := json.Unmarshal(blockJSON, &block); err != nil {
		fmt.Printf("Error parsing block: %v\n", err)
		return
	}

	n.Blockchain.mutex.Lock()

	if len(n.Blockchain.Blocks) == 0 {
		n.Blockchain.mutex.Unlock()
		return
	}

	lastBlock := n.Blockchain.Blocks[len(n.Blockchain.Blocks)-1]

	// Quick dedup: silently ignore blocks we already have
	if block.Index <= lastBlock.Index {
		n.Blockchain.mutex.Unlock()
		return
	}

	fmt.Printf("Received block #%d from %s\n", block.Index, peerAddr)

	// Check if block connects to our chain
	if block.PreviousHash != lastBlock.Hash {
		// Block doesn't connect — request sync if it's ahead
		fmt.Printf("Block #%d doesn't connect - requesting full chain sync\n", block.Index)
		if n.PeerManager != nil {
			n.Blockchain.mutex.Unlock()
			peer := n.PeerManager.GetPeerByAddr(peerAddr)
			if peer != nil {
				n.PeerManager.requestChainSync(peer)
			}
			return
		}
		n.Blockchain.mutex.Unlock()
		return
	}

	// Validate block difficulty matches chain rules (shannon #8)
	expectedBits := n.Blockchain.GetCurrentDifficultyBitsLocked()
	expectedHex := difficultyBitsToHexDigits(expectedBits)

	// All blocks must meet bit-based PoW target regardless of version
	if !meetsDifficultyBits(block.Hash, expectedBits) {
		fmt.Printf("Block #%d does not meet difficulty target (%d bits required)\n", block.Index, expectedBits)
		n.Blockchain.mutex.Unlock()
		return
	}

	if block.DifficultyBits > 0 {
		if block.DifficultyBits != expectedBits {
			fmt.Printf("Block #%d has wrong difficulty bits %d (expected %d)\n", block.Index, block.DifficultyBits, expectedBits)
			n.Blockchain.mutex.Unlock()
			return
		}
	} else {
		if block.Difficulty != expectedHex {
			fmt.Printf("Block #%d has wrong difficulty %d (expected %d)\n", block.Index, block.Difficulty, expectedHex)
			n.Blockchain.mutex.Unlock()
			return
		}
	}

	// Validate block timestamp (shannon #12)
	now := time.Now().Unix()
	if block.Timestamp > now+2*60*60 {
		fmt.Printf("Block #%d timestamp too far in the future\n", block.Index)
		n.Blockchain.mutex.Unlock()
		return
	}
	if block.Timestamp < lastBlock.Timestamp {
		fmt.Printf("Block #%d timestamp before previous block\n", block.Index)
		n.Blockchain.mutex.Unlock()
		return
	}

	// Verify hash is correct
	if block.Hash != block.CalculateHash() {
		fmt.Printf("Block #%d has invalid hash\n", block.Index)
		n.Blockchain.mutex.Unlock()
		return
	}

	// Validate all transactions in the block have sufficient funds
	if err := n.Blockchain.ValidateBlockTransactions(&block, n.Blockchain.Blocks); err != nil {
		fmt.Printf("Block #%d has invalid transactions: %v\n", block.Index, err)
		n.Blockchain.mutex.Unlock()
		return
	}

	// Cancel our current mining - someone else won this round
	n.cancelMining()

	// Add block to chain
	n.Blockchain.Blocks = append(n.Blockchain.Blocks, &block)

	// Remove mined transactions from our mempool
	n.Blockchain.clearMinedTransactions(block.Transactions)

	n.Blockchain.mutex.Unlock()

	fmt.Printf("Block #%d added to chain (from peer)\n", block.Index)

	// Restart mining if auto-mining is enabled and there are pending transactions
	if n.autoMineEnabled && n.Blockchain.GetPendingCount() > 0 {
		go n.startMiningRound()
	}
}

// handleChainMessage processes a blockchain from a peer
func (n *Node) handleChainMessage(msg Message, peerAddr string) {
	var chain []*Block
	chainJSON, _ := json.Marshal(msg.Data)
	if err := json.Unmarshal(chainJSON, &chain); err != nil {
		fmt.Printf("Error parsing chain: %v\n", err)
		return
	}

	n.Blockchain.mutex.Lock()
	defer n.Blockchain.mutex.Unlock()

	// Use cumulative work (not just length) for chain selection (shannon #7)
	if cumulativeWork(chain) > cumulativeWork(n.Blockchain.Blocks) && n.isValidChain(chain) {
		fmt.Printf("Syncing blockchain from %s (length: %d -> %d)\n", peerAddr, len(n.Blockchain.Blocks), len(chain))

		// Cancel current mining - chain is being replaced
		n.cancelMining()

		n.Blockchain.Blocks = chain

		// Clear mempool - transactions may have been included in the new chain
		n.Blockchain.clearMempool()
	}
	// If chains are same length, keep our own (first miner to extend wins)
}

// ============================================================================
// BROADCASTING
// ============================================================================

// broadcastTransaction sends a transaction to all connected peers
func (n *Node) broadcastTransaction(tx *Transaction) {
	// Use PeerManager if available (v2.0)
	if n.PeerManager != nil {
		n.PeerManager.BroadcastTransaction(tx)
		return
	}

	// Legacy fallback
	msg := Message{
		Type:      "transaction",
		Data:      tx,
		Timestamp: time.Now().Unix(),
	}

	n.broadcastMessage(msg)
}

// broadcastBlock sends a block to all connected peers
func (n *Node) broadcastBlock(block *Block) {
	// Use PeerManager if available (v2.0)
	if n.PeerManager != nil {
		n.PeerManager.BroadcastBlock(block)
		return
	}

	// Legacy fallback
	msg := Message{
		Type:      "block",
		Data:      block,
		Timestamp: time.Now().Unix(),
	}

	n.broadcastMessage(msg)
}

// sendBlockchain sends the entire blockchain to a specific peer
func (n *Node) sendBlockchain(conn net.Conn) {
	msg := Message{
		Type:      "chain",
		Data:      n.Blockchain.Blocks,
		Timestamp: time.Now().Unix(),
	}

	data, _ := json.Marshal(msg)
	fmt.Fprintf(conn, "%s\n", string(data))
}

// broadcastMessage sends a message to all connected peers
func (n *Node) broadcastMessage(msg Message) {
	data, _ := json.Marshal(msg)

	n.PeersMutex.RLock()
	defer n.PeersMutex.RUnlock()

	for addr, conn := range n.Peers {
		_, err := fmt.Fprintf(conn, "%s\n", string(data))
		if err != nil {
			fmt.Printf("Failed to send message to %s: %v\n", addr, err)
		}
	}
}

// ============================================================================
// MINING
// ============================================================================

// MinePendingTransactions mines pending transactions and broadcasts the block
func (n *Node) MinePendingTransactions(minerAddress string) {
	block := n.Blockchain.MinePendingTransactions(minerAddress)
	if block != nil {
		n.broadcastBlock(block)
	}
}

// StartAutoMining starts the automatic mining loop
func (n *Node) StartAutoMining(minerAddress string) {
	n.miningMutex.Lock()
	if n.autoMineEnabled {
		n.miningMutex.Unlock()
		return
	}
	n.MinerAddress = minerAddress
	n.autoMineEnabled = true
	n.miningMutex.Unlock()

	fmt.Printf("Auto-mining enabled for address: %s\n", minerAddress)
	go n.autoMiningLoop()
}

// StopAutoMining stops the automatic mining loop
func (n *Node) StopAutoMining() {
	n.miningMutex.Lock()
	n.autoMineEnabled = false
	n.miningMutex.Unlock()
	n.cancelMining()
	fmt.Println("Auto-mining disabled")
}

// autoMiningLoop continuously mines when there are pending transactions
func (n *Node) autoMiningLoop() {
	for {
		n.miningMutex.Lock()
		if !n.autoMineEnabled {
			n.miningMutex.Unlock()
			return
		}
		n.miningMutex.Unlock()

		// Check if there are pending transactions
		if n.Blockchain.GetPendingCount() > 0 {
			n.startMiningRound()
		}

		// Small delay before checking again
		time.Sleep(1 * time.Second)
	}
}

// startMiningRound starts a single mining attempt
func (n *Node) startMiningRound() {
	n.miningMutex.Lock()

	// Don't start if already mining or auto-mine disabled
	if n.isMining || !n.autoMineEnabled {
		n.miningMutex.Unlock()
		return
	}

	if n.MinerAddress == "" {
		n.miningMutex.Unlock()
		return
	}

	n.isMining = true
	n.miningCancel = make(chan struct{})
	cancel := n.miningCancel

	n.miningMutex.Unlock()

	// Mine (this blocks until complete or cancelled)
	block, success := n.Blockchain.MinePendingTransactionsWithCancel(n.MinerAddress, cancel)

	n.miningMutex.Lock()
	n.isMining = false
	n.miningMutex.Unlock()

	if success && block != nil {
		fmt.Printf("Successfully mined block #%d!\n", block.Index)
		n.broadcastBlock(block)
	}
}

// cancelMining cancels any in-progress mining
func (n *Node) cancelMining() {
	n.miningMutex.Lock()
	defer n.miningMutex.Unlock()

	if n.isMining && n.miningCancel != nil {
		close(n.miningCancel)
		n.miningCancel = nil
	}
}

// ============================================================================
// VALIDATION
// ============================================================================

// isValidChain validates an entire blockchain
func (n *Node) isValidChain(chain []*Block) bool {
	if len(chain) == 0 {
		return false
	}

	// Verify genesis block matches
	if chain[0].Hash != GenesisBlock.Hash {
		fmt.Printf("Chain has invalid genesis block hash: %s (expected %s)\n", chain[0].Hash, GenesisBlock.Hash)
		return false
	}

	for i := 1; i < len(chain); i++ {
		if !n.isBlockValid(chain[i], chain[i-1]) {
			return false
		}

		// Validate transactions in this block have sufficient funds
		// Use blocks 0 to i-1 as the state before this block
		if err := n.Blockchain.ValidateBlockTransactions(chain[i], chain[:i]); err != nil {
			fmt.Printf("Chain validation failed at block %d: %v\n", i, err)
			return false
		}
	}
	return true
}

// isBlockValid validates a single block against its predecessor
func (n *Node) isBlockValid(currentBlock, previousBlock *Block) bool {
	// Verify current block's hash
	if currentBlock.Hash != currentBlock.CalculateHash() {
		return false
	}

	// Verify previous hash matches
	if currentBlock.PreviousHash != previousBlock.Hash {
		return false
	}

	// Verify proof of work
	if currentBlock.DifficultyBits > 0 {
		// New bit-based validation
		if !meetsDifficultyBits(currentBlock.Hash, currentBlock.DifficultyBits) {
			fmt.Printf("Block %d fails bit-based PoW (bits=%d)\n", currentBlock.Index, currentBlock.DifficultyBits)
			return false
		}
	} else {
		// Legacy hex-digit validation
		blockDifficulty := currentBlock.Difficulty
		if blockDifficulty == 0 {
			blockDifficulty = n.Blockchain.Difficulty
		}
		target := createTarget(blockDifficulty)
		if len(currentBlock.Hash) < blockDifficulty || currentBlock.Hash[:blockDifficulty] != target {
			return false
		}
	}

	return true
}

// ============================================================================
// STATUS
// ============================================================================

// PrintStatus prints the node's status and blockchain info
func (n *Node) PrintStatus() {
	fmt.Printf("\n========== NODE %s ==========\n", n.Address)
	fmt.Printf("Peers connected: %d\n", len(n.Peers))
	fmt.Printf("Blockchain length: %d\n", len(n.Blockchain.Blocks))
	fmt.Println("\nBlocks:")
	for _, block := range n.Blockchain.Blocks {
		hashDisplay := block.Hash
		if len(block.Hash) > 16 {
			hashDisplay = block.Hash[:16] + "..."
		}
		fmt.Printf("  Block #%d: %s\n", block.Index, hashDisplay)
	}
	fmt.Println("================================")
}
