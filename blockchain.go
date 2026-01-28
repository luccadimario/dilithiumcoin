package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Block represents a single block in the blockchain
type Block struct {
	Index        int64
	Timestamp    int64
	Transactions []*Transaction `json:"transactions"`
	PreviousHash string
	Hash         string
	Nonce        int64
}

// CalculateHash creates a SHA-256 hash of the block
func (b *Block) CalculateHash() string {
	// Convert transactions to JSON
	txJSON, _ := json.Marshal(b.Transactions)

	blockData := strconv.FormatInt(b.Index, 10) +
		strconv.FormatInt(b.Timestamp, 10) +
		string(txJSON) +
		b.PreviousHash +
		strconv.FormatInt(b.Nonce, 10)

	hash := sha256.Sum256([]byte(blockData))
	return hex.EncodeToString(hash[:])
}

// MineBlock performs proof of work
func (b *Block) MineBlock(difficulty int) {
	target := ""
	for i := 0; i < difficulty; i++ {
		target += "0"
	}

	fmt.Printf("Mining block %d...\n", b.Index)
	start := time.Now()

	b.Hash = b.CalculateHash()

	for b.Hash[:difficulty] != target {
		b.Nonce++
		b.Hash = b.CalculateHash()
	}

	elapsed := time.Since(start)
	fmt.Printf("Block %d mined! Hash: %s (took %v)\n", b.Index, b.Hash, elapsed)
}

// Blockchain represents the entire chain
type Blockchain struct {
	Blocks              []*Block
	Difficulty          int
	PendingTransactions []*Transaction
	MiningReward        float64
}

// CreateGenesisBlock creates the first block
func CreateGenesisBlock(difficulty int) *Block {
	genesisBlock := &Block{
		Index:        0,
		Timestamp:    time.Now().Unix(),
		Transactions: make([]*Transaction, 0),
		PreviousHash: "0",
		Nonce:        0,
	}
	genesisBlock.MineBlock(difficulty)
	return genesisBlock
}

// NewBlockchain initializes a new blockchain
func NewBlockchain(difficulty int) *Blockchain {
	return &Blockchain{
		Blocks:              []*Block{CreateGenesisBlock(difficulty)},
		Difficulty:          difficulty,
		PendingTransactions: make([]*Transaction, 0),
		MiningReward:        10.0,
	}
}

// AddTransaction adds a pending transaction
func (bc *Blockchain) AddTransaction(tx *Transaction) error {
	if tx.From == "" || tx.To == "" {
		return fmt.Errorf("transaction must include from and to address")
	}

	if tx.Amount <= 0 {
		return fmt.Errorf("transaction amount must be positive")
	}

	if tx.Signature == "" {
		return fmt.Errorf("transaction must be signed")
	}

	bc.PendingTransactions = append(bc.PendingTransactions, tx)
	return nil
}

// MinePendingTransactions mines all pending transactions
func (bc *Blockchain) MinePendingTransactions(minerAddress string) *Block {
	// Add mining reward transaction
	rewardTx := &Transaction{
		From:      "SYSTEM",
		To:        minerAddress,
		Amount:    bc.MiningReward,
		Timestamp: time.Now().Unix(),
		Signature: "SYSTEM",
	}

	bc.PendingTransactions = append(bc.PendingTransactions, rewardTx)

	// Create new block with pending transactions
	previousBlock := bc.Blocks[len(bc.Blocks)-1]
	newBlock := &Block{
		Index:        previousBlock.Index + 1,
		Timestamp:    time.Now().Unix(),
		Transactions: bc.PendingTransactions,
		PreviousHash: previousBlock.Hash,
		Nonce:        0,
	}

	// Mine the block
	newBlock.MineBlock(bc.Difficulty)
	bc.Blocks = append(bc.Blocks, newBlock)

	// Clear pending transactions
	bc.PendingTransactions = make([]*Transaction, 0)

	return newBlock
}

// IsValid checks if the blockchain is valid
func (bc *Blockchain) IsValid() bool {
	for i := 1; i < len(bc.Blocks); i++ {
		currentBlock := bc.Blocks[i]
		previousBlock := bc.Blocks[i-1]

		// Verify current block's hash
		if currentBlock.Hash != currentBlock.CalculateHash() {
			fmt.Printf("Block %d has invalid hash\n", i)
			return false
		}

		// Verify previous hash matches
		if currentBlock.PreviousHash != previousBlock.Hash {
			fmt.Printf("Block %d has invalid previous hash\n", i)
			return false
		}

		// Verify proof of work
		target := ""
		for j := 0; j < bc.Difficulty; j++ {
			target += "0"
		}
		if currentBlock.Hash[:bc.Difficulty] != target {
			fmt.Printf("Block %d has invalid proof of work\n", i)
			return false
		}
	}
	return true
}

// PrintBlockchain displays all blocks
func (bc *Blockchain) PrintBlockchain() {
	fmt.Println("\n========== BLOCKCHAIN ==========")
	for _, block := range bc.Blocks {
		fmt.Printf("\nBlock #%d\n", block.Index)
		fmt.Printf("Timestamp: %d\n", block.Timestamp)
		fmt.Printf("Transactions: %d\n", len(block.Transactions))
		for j, tx := range block.Transactions {
			fmt.Printf("  TX %d: %s -> %s: %.2f\n", j, tx.From[:8], tx.To[:8], tx.Amount)
		}
		fmt.Printf("Previous Hash: %s\n", block.PreviousHash)
		fmt.Printf("Hash: %s\n", block.Hash)
		fmt.Printf("Nonce: %d\n", block.Nonce)
	}
	fmt.Println("\n================================\n")
}

func main() {
	fmt.Println("Starting P2P Blockchain Network with Transactions\n")

	// Create wallets
	alice, _ := NewWallet()
	bob, _ := NewWallet()
	charlie, _ := NewWallet()

	fmt.Printf("Alice address: %s\n", alice.Address)
	fmt.Printf("Bob address: %s\n", bob.Address)
	fmt.Printf("Charlie address: %s\n\n", charlie.Address)

	// Create 3 nodes
	node1 := NewNode("5001", 3)
	node2 := NewNode("5002", 3)
	node3 := NewNode("5003", 3)

	genesisHash := node1.Blockchain.Blocks[0].Hash
	node2.Blockchain.Blocks[0].Hash = genesisHash
	node3.Blockchain.Blocks[0].Hash = genesisHash

	// Start P2P servers
	node1.StartServer()
	node2.StartServer()
	node3.StartServer()

	// Start HTTP APIs
	go node1.StartAPI("8001")
	go node2.StartAPI("8002")
	go node3.StartAPI("8003")

	time.Sleep(1 * time.Second)

	fmt.Println("Connecting nodes...\n")
	node2.ConnectToPeer("localhost:5001")
	node3.ConnectToPeer("localhost:5001")

	time.Sleep(1 * time.Second)

	// Create and sign transactions
	fmt.Println("Creating transactions...\n")

	tx1 := NewTransaction(alice.Address, bob.Address, 50)
	tx1.Sign(alice)
	node1.Blockchain.AddTransaction(tx1)

	tx2 := NewTransaction(bob.Address, charlie.Address, 25)
	tx2.Sign(bob)
	node1.Blockchain.AddTransaction(tx2)

	// Mine the block
	fmt.Println("Mining block 1...\n")
	node1.MinePendingTransactions(alice.Address)
	time.Sleep(2 * time.Second)

	// Create another transaction
	tx3 := NewTransaction(charlie.Address, alice.Address, 10)
	tx3.Sign(charlie)
	node1.Blockchain.AddTransaction(tx3)

	fmt.Println("Mining block 2...\n")
	node1.MinePendingTransactions(bob.Address)
	time.Sleep(2 * time.Second)

	fmt.Println("\nFinal Status:\n")
	node1.PrintStatus()
	node2.PrintStatus()
	node3.PrintStatus()

	fmt.Printf("Node1 valid: %v\n", node1.Blockchain.IsValid())
	fmt.Printf("Node2 valid: %v\n", node2.Blockchain.IsValid())
	fmt.Printf("Node3 valid: %v\n", node3.Blockchain.IsValid())

	select {}
}

