package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// APIResponse matches the node's response format
type APIResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// httpClient with reasonable timeout
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// postJSON sends a POST request with JSON body
func postJSON(url string, data interface{}) (*APIResponse, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(body))
	}

	return &apiResp, nil
}

// getJSON sends a GET request and returns the response
func getJSON(url string) (*APIResponse, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(body))
	}

	return &apiResp, nil
}

// cmdBalance checks balance for an address
func cmdBalance(args []string) {
	fs := flag.NewFlagSet("balance", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	fs.Parse(args)

	remaining := fs.Args()

	var address string
	if len(remaining) > 0 {
		address = remaining[0]
	} else {
		// Use wallet address
		addr, err := loadAddress(*walletDir)
		if err != nil {
			fmt.Println("No wallet found. Create one with:")
			fmt.Println("  dilithium-cli init")
			fmt.Println()
			fmt.Println("Or specify an address:")
			fmt.Println("  dilithium-cli balance <address>")
			os.Exit(1)
		}
		address = addr
	}

	// Get blockchain to calculate balance
	resp, err := getJSON(*nodeURL + "/chain")
	if err != nil {
		fmt.Printf("Error: Could not connect to node at %s\n", *nodeURL)
		fmt.Printf("       %v\n", err)
		os.Exit(1)
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}

	// Calculate balance from blockchain
	balance := calculateBalance(resp.Data, address)

	fmt.Printf("Address: %s\n", address)
	fmt.Printf("Balance: %s DLT\n", FormatDLT(balance))
}

// calculateBalance computes balance from blockchain data
func calculateBalance(data map[string]interface{}, address string) int64 {
	var balance int64

	blocks, ok := data["blocks"].([]interface{})
	if !ok {
		return 0
	}

	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}

		txs, ok := block["transactions"].([]interface{})
		if !ok {
			continue
		}

		for _, t := range txs {
			tx, ok := t.(map[string]interface{})
			if !ok {
				continue
			}

			from, _ := tx["from"].(string)
			to, _ := tx["to"].(string)
			// JSON numbers decode as float64; amounts are int64 base units
			amount := int64(tx["amount"].(float64))

			if to == address {
				balance += amount
			}
			if from == address {
				balance -= amount
			}
		}
	}

	return balance
}

// cmdStatus shows node status
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	fs.Parse(args)

	resp, err := getJSON(*nodeURL + "/status")
	if err != nil {
		fmt.Printf("Error: Could not connect to node at %s\n", *nodeURL)
		fmt.Printf("       %v\n", err)
		os.Exit(1)
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}

	fmt.Println("========== NODE STATUS ==========")

	if v, ok := resp.Data["version"]; ok {
		fmt.Printf("Version:     %v\n", v)
	}
	if v, ok := resp.Data["blockchain_height"]; ok {
		fmt.Printf("Block Height: %.0f\n", v.(float64))
	}
	if v, ok := resp.Data["pending_transactions"]; ok {
		fmt.Printf("Pending TXs:  %.0f\n", v.(float64))
	}
	if v, ok := resp.Data["difficulty"]; ok {
		fmt.Printf("Difficulty:   %.0f\n", v.(float64))
	}

	if peers, ok := resp.Data["peers"].(map[string]interface{}); ok {
		total, _ := peers["total"].(float64)
		inbound, _ := peers["inbound"].(float64)
		outbound, _ := peers["outbound"].(float64)
		fmt.Printf("Peers:        %.0f (in: %.0f, out: %.0f)\n", total, inbound, outbound)
	}

	if mining, ok := resp.Data["mining"].(map[string]interface{}); ok {
		enabled, _ := mining["enabled"].(bool)
		active, _ := mining["active"].(bool)
		if enabled {
			if active {
				fmt.Println("Mining:       ACTIVE")
			} else {
				fmt.Println("Mining:       enabled (idle)")
			}
		} else {
			fmt.Println("Mining:       disabled")
		}
	}

	fmt.Println("=================================")
}

// cmdPeers shows connected peers
func cmdPeers(args []string) {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	fs.Parse(args)

	resp, err := getJSON(*nodeURL + "/peers")
	if err != nil {
		fmt.Printf("Error: Could not connect to node at %s\n", *nodeURL)
		fmt.Printf("       %v\n", err)
		os.Exit(1)
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}

	count, _ := resp.Data["count"].(float64)
	fmt.Printf("Connected Peers: %.0f\n\n", count)

	peers, ok := resp.Data["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		fmt.Println("No peers connected.")
		return
	}

	for i, p := range peers {
		if peer, ok := p.(map[string]interface{}); ok {
			addr, _ := peer["address"].(string)
			state, _ := peer["state"].(string)
			inbound, _ := peer["inbound"].(bool)

			direction := "outbound"
			if inbound {
				direction = "inbound"
			}

			fmt.Printf("%d. %s [%s] (%s)\n", i+1, addr, state, direction)
		} else if peerStr, ok := p.(string); ok {
			// Legacy format
			fmt.Printf("%d. %s\n", i+1, peerStr)
		}
	}
}

// cmdMempool shows pending transactions
func cmdMempool(args []string) {
	fs := flag.NewFlagSet("mempool", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	fs.Parse(args)

	resp, err := getJSON(*nodeURL + "/mempool")
	if err != nil {
		fmt.Printf("Error: Could not connect to node at %s\n", *nodeURL)
		fmt.Printf("       %v\n", err)
		os.Exit(1)
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}

	count, _ := resp.Data["count"].(float64)
	fmt.Printf("Pending Transactions: %.0f\n\n", count)

	txs, ok := resp.Data["transactions"].([]interface{})
	if !ok || len(txs) == 0 {
		fmt.Println("Mempool is empty.")
		return
	}

	for i, t := range txs {
		if tx, ok := t.(map[string]interface{}); ok {
			from, _ := tx["from"].(string)
			to, _ := tx["to"].(string)
			amount := int64(tx["amount"].(float64))

			fmt.Printf("%d. %s -> %s: %s DLT\n", i+1, from, to, FormatDLT(amount))
		}
	}
}

// cmdBlock shows block details
func cmdBlock(args []string) {
	fs := flag.NewFlagSet("block", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	fs.Parse(args)

	remaining := fs.Args()

	// If no index specified, show latest block
	url := *nodeURL + "/chain"
	resp, err := getJSON(url)
	if err != nil {
		fmt.Printf("Error: Could not connect to node at %s\n", *nodeURL)
		fmt.Printf("       %v\n", err)
		os.Exit(1)
	}

	if !resp.Success {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}

	blocks, ok := resp.Data["blocks"].([]interface{})
	if !ok || len(blocks) == 0 {
		fmt.Println("No blocks found.")
		return
	}

	var blockIndex int
	if len(remaining) > 0 {
		fmt.Sscanf(remaining[0], "%d", &blockIndex)
		if blockIndex < 0 || blockIndex >= len(blocks) {
			fmt.Printf("Error: Block %d not found (chain height: %d)\n", blockIndex, len(blocks))
			os.Exit(1)
		}
	} else {
		blockIndex = len(blocks) - 1
	}

	block, ok := blocks[blockIndex].(map[string]interface{})
	if !ok {
		fmt.Println("Error: Invalid block data")
		os.Exit(1)
	}

	fmt.Printf("========== BLOCK #%.0f ==========\n", block["Index"])
	fmt.Printf("Hash:         %s\n", block["Hash"])
	fmt.Printf("Previous:     %s\n", block["PreviousHash"])
	fmt.Printf("Timestamp:    %.0f\n", block["Timestamp"])
	fmt.Printf("Nonce:        %.0f\n", block["Nonce"])

	txs, ok := block["transactions"].([]interface{})
	if ok {
		fmt.Printf("Transactions: %d\n", len(txs))
		fmt.Println()

		for i, t := range txs {
			if tx, ok := t.(map[string]interface{}); ok {
				from, _ := tx["from"].(string)
				to, _ := tx["to"].(string)
				amount := int64(tx["amount"].(float64))
				fmt.Printf("  %d. %s -> %s: %s DLT\n", i+1, from, to, FormatDLT(amount))
			}
		}
	}
	fmt.Println("=================================")
}
