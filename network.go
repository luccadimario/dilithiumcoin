package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// Message represents data sent between nodes
type Message struct {
	Type      string      `json:"type"`      // "block" or "chain"
	Data      interface{} `json:"data"`      // Block or Blockchain
	Timestamp int64       `json:"timestamp"`
}

// Node represents a peer in the network
type Node struct {
	Address    string
	Blockchain *Blockchain
	Peers      map[string]net.Conn
	PeersMutex sync.RWMutex
	Port       string
	MessageCh  chan Message
}

// NewNode creates a new node
func NewNode(port string, difficulty int) *Node {
	return &Node{
		Address:    fmt.Sprintf("localhost:%s", port),
		Blockchain: NewBlockchain(difficulty),
		Peers:      make(map[string]net.Conn),
		Port:       port,
		MessageCh:  make(chan Message, 100),
	}
}

// StartServer starts listening for incoming connections
func (n *Node) StartServer() {
	listener, err := net.Listen("tcp", ":"+n.Port)
	if err != nil {
		fmt.Printf("Failed to start server on port %s: %v\n", n.Port, err)
		return
	}

	fmt.Printf("Node listening on %s\n", n.Address)

	// Accept incoming connections in a goroutine
	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Only print error if it's not a closed connection
				if err.Error() != "use of closed network connection" {
					fmt.Printf("Error accepting connection: %v\n", err)
				}
				return
			}

			// Handle each peer connection in a separate goroutine
			go n.handlePeerConnection(conn)
		}
	}()
}

// handlePeerConnection handles incoming messages from a peer
func (n *Node) handlePeerConnection(conn net.Conn) {
	defer conn.Close()

	peerAddr := conn.RemoteAddr().String()
	fmt.Printf("New peer connected: %s\n", peerAddr)

	// Store the connection
	n.PeersMutex.Lock()
	n.Peers[peerAddr] = conn
	n.PeersMutex.Unlock()

	// Send our current blockchain to the new peer
	n.sendBlockchain(conn)

	// Read messages from this peer
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg Message
		err := json.Unmarshal(scanner.Bytes(), &msg)
		if err != nil {
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

// ConnectToPeer connects to another node
func (n *Node) ConnectToPeer(peerAddress string) error {
	conn, err := net.Dial("tcp", peerAddress)
	if err != nil {
		fmt.Printf("Failed to connect to %s: %v\n", peerAddress, err)
		return err
	}

	fmt.Printf("Connected to peer: %s\n", peerAddress)

	// Store the connection
	n.PeersMutex.Lock()
	n.Peers[peerAddress] = conn
	n.PeersMutex.Unlock()

	// Send our blockchain
	n.sendBlockchain(conn)

	// Read messages from this peer in a goroutine
	go func() {
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var msg Message
			err := json.Unmarshal(scanner.Bytes(), &msg)
			if err != nil {
				fmt.Printf("Error parsing message: %v\n", err)
				continue
			}

			n.handleMessage(msg, peerAddress)
		}
	}()

	return nil
}

// handleMessage processes incoming messages
func (n *Node) handleMessage(msg Message, peerAddr string) {
	switch msg.Type {
	case "block":
		// Peer sent us a new block
		var block Block
		blockJSON, _ := json.Marshal(msg.Data)
		json.Unmarshal(blockJSON, &block)
		
		fmt.Printf("Received block from %s\n", peerAddr)
		
		// Add to our blockchain if valid
		if len(n.Blockchain.Blocks) > 0 {
			lastBlock := n.Blockchain.Blocks[len(n.Blockchain.Blocks)-1]
			if block.PreviousHash == lastBlock.Hash {
				n.Blockchain.Blocks = append(n.Blockchain.Blocks, &block)
				fmt.Printf("Block added to chain\n")
				
				// Broadcast to other peers
				n.broadcastBlock(&block)
			}
		}

	case "chain":
		// Peer sent us their entire blockchain
		var chain []*Block
		chainJSON, _ := json.Marshal(msg.Data)
		json.Unmarshal(chainJSON, &chain)
		
		// If their chain is longer and valid, replace ours
		if len(chain) > len(n.Blockchain.Blocks) && n.isValidChain(chain) {
			fmt.Printf("Syncing blockchain from %s (length: %d)\n", peerAddr, len(chain))
			n.Blockchain.Blocks = chain
		} else if len(chain) == len(n.Blockchain.Blocks) && len(chain) == 1 {
			// If both have just genesis block, sync the genesis block hash
			if chain[0].Hash != n.Blockchain.Blocks[0].Hash {
				fmt.Printf("Syncing genesis block from %s\n", peerAddr)
				n.Blockchain.Blocks[0] = chain[0]
			}
		}
	}
}

// isValidChain validates a blockchain
func (n *Node) isValidChain(chain []*Block) bool {
	for i := 1; i < len(chain); i++ {
		currentBlock := chain[i]
		previousBlock := chain[i-1]

		if currentBlock.Hash != currentBlock.CalculateHash() {
			return false
		}

		if currentBlock.PreviousHash != previousBlock.Hash {
			return false
		}

		target := ""
		for j := 0; j < n.Blockchain.Difficulty; j++ {
			target += "0"
		}
		if currentBlock.Hash[:n.Blockchain.Difficulty] != target {
			return false
		}
	}
	return true
}

// sendBlockchain sends the entire blockchain to a peer
func (n *Node) sendBlockchain(conn net.Conn) {
	msg := Message{
		Type:      "chain",
		Data:      n.Blockchain.Blocks,
		Timestamp: time.Now().Unix(),
	}

	data, _ := json.Marshal(msg)
	fmt.Fprintf(conn, "%s\n", string(data))
}

// broadcastBlock sends a block to all peers
func (n *Node) broadcastBlock(block *Block) {
	msg := Message{
		Type:      "block",
		Data:      block,
		Timestamp: time.Now().Unix(),
	}

	data, _ := json.Marshal(msg)

	n.PeersMutex.RLock()
	defer n.PeersMutex.RUnlock()

	for addr, conn := range n.Peers {
		_, err := fmt.Fprintf(conn, "%s\n", string(data))
		if err != nil {
			fmt.Printf("Failed to send block to %s: %v\n", addr, err)
		}
	}
}

// MinePendingTransactions mines pending transactions and broadcasts
func (n *Node) MinePendingTransactions(minerAddress string) {
	block := n.Blockchain.MinePendingTransactions(minerAddress)
	n.broadcastBlock(block)
}

// PrintStatus prints the node's blockchain
func (n *Node) PrintStatus() {
	fmt.Printf("\n========== NODE %s ==========\n", n.Address)
	fmt.Printf("Peers connected: %d\n", len(n.Peers))
	fmt.Printf("Blockchain length: %d\n", len(n.Blockchain.Blocks))
	fmt.Println("\nBlocks:")
	for _, block := range n.Blockchain.Blocks {
		fmt.Printf("  Block #%d: %s\n", block.Index, block.Hash[:16]+"...")
	}
	fmt.Println("================================\n")
}

