package main

import (
	"encoding/json"
	"fmt"
	"math"
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

// BlockError represents a block validation failure with an associated misbehavior penalty.
type BlockError struct {
	Reason  string
	Penalty int
}

func (e *BlockError) Error() string { return e.Reason }

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
	Address    string
	Blockchain *Blockchain
	Port       string
	MessageCh  chan Message

	// Peer management (v2.0)
	PeerManager   *PeerManager
	CensusManager *CensusManager
	AutoUpdater   *AutoUpdater

	// Mining state
	MinerAddress    string
	miningCancel    chan struct{}
	miningMutex     sync.Mutex
	isMining        bool
	autoMineEnabled bool
}

// NewNode creates a new node with the given port and difficulty
func NewNode(port string, difficulty int) *Node {
	return &Node{
		Address:    fmt.Sprintf("localhost:%s", port),
		Blockchain: NewBlockchain(difficulty),
		Port:       port,
		MessageCh:  make(chan Message, 100),
	}
}

// ============================================================================
// MESSAGE HANDLING
// ============================================================================

// handleMessage processes incoming messages based on type.
// Used by PeerManager to delegate block processing to the Node.
func (n *Node) handleMessage(msg Message, peerAddr string) {
	switch msg.Type {
	case "block":
		n.handleBlockMessage(msg, peerAddr)
	}
}

// AddBlock validates and accepts a block directly (no JSON round-trip).
// Called by peer.go handleBlocks and handleBlock for efficient block processing.
// Returns a *BlockError for validation failures (with misbehavior penalty), or nil on success/dedup.
func (n *Node) AddBlock(block *Block, peerAddr string) error {
	n.Blockchain.mutex.Lock()

	if len(n.Blockchain.Blocks) == 0 {
		n.Blockchain.mutex.Unlock()
		return nil
	}

	lastBlock := n.Blockchain.Blocks[len(n.Blockchain.Blocks)-1]

	// Quick dedup: silently ignore blocks we already have
	if block.Index <= lastBlock.Index {
		n.Blockchain.mutex.Unlock()
		return nil
	}

	fmt.Printf("Received block #%d from %s\n", block.Index, peerAddr)

	// Check if block connects to our chain (not penalized — could be legit reorg)
	if block.PreviousHash != lastBlock.Hash {
		fmt.Printf("Block #%d doesn't connect - requesting full chain sync\n", block.Index)
		if n.PeerManager != nil {
			n.Blockchain.mutex.Unlock()
			peer := n.PeerManager.GetPeerByAddr(peerAddr)
			if peer != nil {
				n.PeerManager.requestChainSync(peer)
			}
			return nil
		}
		n.Blockchain.mutex.Unlock()
		return nil
	}

	// Validate block difficulty
	// Check block meets its own stated PoW target (actual cheating = penalize)
	blockBits := block.DifficultyBits
	if blockBits == 0 {
		blockBits = hexDigitsToDifficultyBits(block.Difficulty)
	}
	if !meetsDifficultyBits(block.Hash, blockBits) {
		n.Blockchain.mutex.Unlock()
		return &BlockError{Reason: fmt.Sprintf("block #%d does not meet its own difficulty target (%d bits)", block.Index, blockBits), Penalty: PenaltyInvalidBlock}
	}

	// Difficulty mismatch with our expectation = possible fork, not cheating
	expectedBits := n.Blockchain.GetCurrentDifficultyBitsLocked()
	if blockBits != expectedBits {
		fmt.Printf("Block #%d difficulty mismatch (block: %d bits, expected: %d bits) - requesting sync\n",
			block.Index, blockBits, expectedBits)
		if n.PeerManager != nil {
			n.Blockchain.mutex.Unlock()
			peer := n.PeerManager.GetPeerByAddr(peerAddr)
			if peer != nil {
				n.PeerManager.requestChainSync(peer)
			}
			return nil
		}
		n.Blockchain.mutex.Unlock()
		return nil
	}

	// Validate block timestamp
	now := time.Now().Unix()
	if block.Timestamp > now+2*60*60 {
		n.Blockchain.mutex.Unlock()
		return &BlockError{Reason: fmt.Sprintf("block #%d timestamp too far in the future", block.Index), Penalty: PenaltyInvalidBlock}
	}
	if block.Timestamp < lastBlock.Timestamp {
		n.Blockchain.mutex.Unlock()
		return &BlockError{Reason: fmt.Sprintf("block #%d timestamp before previous block", block.Index), Penalty: PenaltyInvalidBlock}
	}

	// Verify hash is correct
	if block.Hash != block.CalculateHash() {
		n.Blockchain.mutex.Unlock()
		return &BlockError{Reason: fmt.Sprintf("block #%d has invalid hash", block.Index), Penalty: PenaltyInvalidBlock}
	}

	// Validate all transactions in the block
	if err := n.Blockchain.ValidateBlockTransactions(block, n.Blockchain.Blocks); err != nil {
		n.Blockchain.mutex.Unlock()
		return &BlockError{Reason: fmt.Sprintf("block #%d invalid transactions: %v", block.Index, err), Penalty: PenaltyInvalidBlock}
	}

	// Cancel our current mining - someone else won this round
	n.cancelMining()

	// Add block to chain (AppendBlock persists to disk and clears mined txs)
	if err := n.Blockchain.AppendBlock(block); err != nil {
		n.Blockchain.mutex.Unlock()
		fmt.Printf("ERROR: %v — block #%d from peer discarded\n", err, block.Index)
		return nil
	}

	n.Blockchain.mutex.Unlock()

	fmt.Printf("Block #%d added to chain (from peer)\n", block.Index)

	// Restart mining if auto-mining is enabled and there are pending transactions
	if n.autoMineEnabled && n.Blockchain.GetPendingCount() > 0 {
		go n.startMiningRound()
	}
	return nil
}

// handleBlockMessage processes a block from a peer (legacy path via Message)
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

	if err := n.Blockchain.AppendBlock(&block); err != nil {
		fmt.Printf("ERROR: %v — block #%d from peer discarded\n", err, block.Index)
		n.Blockchain.mutex.Unlock()
		return
	}

	n.Blockchain.mutex.Unlock()

	fmt.Printf("Block #%d added to chain (from peer)\n", block.Index)

	// Restart mining if auto-mining is enabled and there are pending transactions
	if n.autoMineEnabled && n.Blockchain.GetPendingCount() > 0 {
		go n.startMiningRound()
	}
}

// ============================================================================
// BROADCASTING
// ============================================================================

// broadcastTransaction sends a transaction to all connected peers via PeerManager
func (n *Node) broadcastTransaction(tx *Transaction) {
	n.PeerManager.BroadcastTransaction(tx)
}

// broadcastBlock sends a block to all connected peers via PeerManager
func (n *Node) broadcastBlock(block *Block) {
	n.PeerManager.BroadcastBlock(block)
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

// isValidChain validates an entire blockchain, including difficulty at each height
func (n *Node) isValidChain(chain []*Block) bool {
	if len(chain) == 0 {
		return false
	}

	// Verify genesis block matches
	if chain[0].Hash != GenesisBlock.Hash {
		fmt.Printf("Chain has invalid genesis block hash: %s (expected %s)\n", chain[0].Hash, GenesisBlock.Hash)
		return false
	}

	// Track running difficulty state for validation (like Bitcoin does during IBD)
	runningBits := chain[0].getEffectiveDifficultyBits()

	for i := 1; i < len(chain); i++ {
		if !n.isBlockValid(chain[i], chain[i-1]) {
			return false
		}

		// Recompute expected difficulty at this height
		blockBits := chain[i].getEffectiveDifficultyBits()
		if i >= DAAForkHeight+DAAWindow {
			// Strict difficulty validation only after DAA fork + full LWMA window,
			// so the algorithm has a complete set of post-fork blocks to work with.
			// Pre-fork and transition blocks used varying legacy algorithms.
			expectedBits := computeExpectedDifficultyBits(chain, i, runningBits)
			if blockBits != expectedBits {
				fmt.Printf("Chain validation failed at block %d: difficulty %d bits, expected %d bits\n",
					i, blockBits, expectedBits)
				return false
			}
			runningBits = expectedBits
		} else {
			runningBits = blockBits
		}

		// Validate transactions in this block have sufficient funds
		if err := n.Blockchain.ValidateBlockTransactions(chain[i], chain[:i]); err != nil {
			fmt.Printf("Chain validation failed at block %d: %v\n", i, err)
			return false
		}
	}
	return true
}

// computeExpectedDifficultyBits recomputes the expected difficulty for a block at the given
// height, using only the chain data. This is used during chain sync validation (like Bitcoin's
// IBD) to ensure no block has artificially low difficulty.
func computeExpectedDifficultyBits(chain []*Block, height int, currentBits int) int {
	// Before the DAA fork: legacy 50-block adjustment
	if height < DAAForkHeight {
		// Difficulty only changes at multiples of BlocksPerAdjustment
		if height < BlocksPerAdjustment || height%BlocksPerAdjustment != 0 {
			return currentBits
		}

		// Legacy algorithm: compare actual vs expected time
		startBlock := chain[height-BlocksPerAdjustment]
		endBlock := chain[height-1]
		actualTime := endBlock.Timestamp - startBlock.Timestamp
		if actualTime <= 0 {
			actualTime = 1
		}
		expectedTime := int64(BlocksPerAdjustment * TargetBlockTime)

		ratio := float64(expectedTime) / float64(actualTime)
		if ratio > MaxAdjustmentFactor {
			ratio = MaxAdjustmentFactor
		} else if ratio < 1.0/MaxAdjustmentFactor {
			ratio = 1.0 / MaxAdjustmentFactor
		}

		logRatio := math.Log2(ratio)
		var adjustment int
		if math.Abs(logRatio) < 0.25 {
			adjustment = 0
		} else {
			adjustment = int(math.Round(logRatio))
		}

		newBits := currentBits + adjustment
		if newBits < MinDifficultyBits {
			newBits = MinDifficultyBits
		} else if newBits > MaxDifficultyBits {
			newBits = MaxDifficultyBits
		}
		return newBits
	}

	// Post-DAA fork: per-block LWMA
	if height < DAAWindow {
		return currentBits
	}

	useV2 := height >= DAAv2ForkHeight
	maxSolveTime := int64(TargetBlockTime) * 10

	var weightedSum float64
	var totalWeight float64

	for i := 0; i < DAAWindow; i++ {
		blockIndex := height - DAAWindow + i
		if blockIndex <= 0 || blockIndex < DAAForkHeight {
			continue
		}

		solveTime := chain[blockIndex].Timestamp - chain[blockIndex-1].Timestamp
		if solveTime <= 0 {
			solveTime = 1
		}
		if solveTime > maxSolveTime {
			solveTime = maxSolveTime
		}

		adjustedTime := float64(solveTime)

		if useV2 {
			blockBits := chain[blockIndex].getEffectiveDifficultyBits()
			diffDelta := currentBits - blockBits
			if diffDelta > 0 {
				adjustedTime *= float64(int64(1) << diffDelta)
			} else if diffDelta < 0 {
				adjustedTime /= float64(int64(1) << (-diffDelta))
			}
			if adjustedTime > float64(maxSolveTime) {
				adjustedTime = float64(maxSolveTime)
			}
			if adjustedTime < 1 {
				adjustedTime = 1
			}
		}

		weight := float64(i + 1)
		weightedSum += adjustedTime * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return currentBits
	}

	weightedAvg := weightedSum / totalWeight

	var adjustment int
	if useV2 {
		if weightedAvg < float64(TargetBlockTime)*0.7 {
			adjustment = 1
		} else if weightedAvg > float64(TargetBlockTime)*1.3 {
			adjustment = -1
		}
	} else {
		if weightedAvg < float64(TargetBlockTime)*0.7 {
			adjustment = 1
		} else if weightedAvg > float64(TargetBlockTime)*5.0 {
			adjustment = -3
		} else if weightedAvg > float64(TargetBlockTime)*3.0 {
			adjustment = -2
		} else if weightedAvg > float64(TargetBlockTime)*1.3 {
			adjustment = -1
		}
	}

	newBits := currentBits + adjustment
	if newBits < MinDifficultyBits {
		newBits = MinDifficultyBits
	} else if newBits > MaxDifficultyBits {
		newBits = MaxDifficultyBits
	}
	return newBits
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

	// Verify proof of work — every block must meet at least MinDifficultyBits
	// This prevents accepting chains with artificially low difficulty
	effectiveBits := currentBlock.getEffectiveDifficultyBits()
	if effectiveBits < MinDifficultyBits {
		effectiveBits = MinDifficultyBits
	}
	if !meetsDifficultyBits(currentBlock.Hash, effectiveBits) {
		fmt.Printf("Block %d fails PoW check (required %d bits)\n", currentBlock.Index, effectiveBits)
		return false
	}

	return true
}

// ============================================================================
// STATUS
// ============================================================================

// PrintStatus prints the node's status and blockchain info
func (n *Node) PrintStatus() {
	fmt.Printf("\n========== NODE %s ==========\n", n.Address)
	if n.PeerManager != nil {
		fmt.Printf("Peers connected: %d\n", n.PeerManager.PeerCount())
	}
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
