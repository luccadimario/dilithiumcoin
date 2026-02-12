package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	AppVersion = "3.0.2"
	AppName    = "dilithium-miner"
)

func main() {
	nodeURL := flag.String("node", "", "Node API URL (if not set, an embedded node is started)")
	minerAddr := flag.String("miner", "", "Miner wallet address")
	threads := flag.Int("threads", 1, "Number of mining threads")
	walletDir := flag.String("wallet", "", "Wallet directory (auto-detect address)")
	showVersion := flag.Bool("version", false, "Show version")
	noNode := flag.Bool("no-node", false, "Disable embedded node (requires --node)")

	flag.Parse()

	if *showVersion {
		fmt.Printf("%s v%s\n", AppName, AppVersion)
		os.Exit(0)
	}

	printBanner()

	// Resolve miner address
	address := *minerAddr
	if address == "" && *walletDir != "" {
		addr, err := loadMinerAddress(*walletDir)
		if err != nil {
			fmt.Printf("Error loading wallet: %v\n", err)
			os.Exit(1)
		}
		address = addr
	}
	if address == "" {
		// Try default wallet location
		home, _ := os.UserHomeDir()
		defaultDir := filepath.Join(home, ".dilithium", "wallet")
		if addr, err := loadMinerAddress(defaultDir); err == nil {
			address = addr
		}
	}
	if address == "" {
		fmt.Println("Error: No miner address specified.")
		fmt.Println()
		fmt.Println("Provide an address with --miner or --wallet:")
		fmt.Println("  dilithium-miner --miner <address>")
		fmt.Println("  dilithium-miner --wallet ~/.dilithium/wallet")
		os.Exit(1)
	}

	// Determine node URL: start embedded node or use provided URL
	var nodeRunner *NodeRunner
	activeNodeURL := *nodeURL

	if activeNodeURL == "" && !*noNode {
		// No explicit node URL — start an embedded node
		var err error
		nodeRunner, err = StartEmbeddedNode()
		if err != nil {
			fmt.Printf("Error starting embedded node: %v\n", err)
			fmt.Println()
			fmt.Println("You can run a node separately and use --node to connect:")
			fmt.Println("  dilithium-miner --node http://localhost:8001 --miner <address>")
			os.Exit(1)
		}
		activeNodeURL = nodeRunner.APIURL()
	} else if activeNodeURL == "" {
		// --no-node set but no --node provided
		fmt.Println("Error: --no-node requires --node <url>")
		os.Exit(1)
	}

	// Clean up node URL
	activeNodeURL = strings.TrimRight(activeNodeURL, "/")

	fmt.Printf("Node:    %s\n", activeNodeURL)
	fmt.Printf("Miner:   %s\n", address)
	fmt.Printf("Threads: %d\n", *threads)
	fmt.Println()

	// Verify connectivity
	if err := checkNode(activeNodeURL); err != nil {
		fmt.Printf("Error: Cannot connect to node at %s\n", activeNodeURL)
		fmt.Printf("       %v\n", err)
		fmt.Println()
		fmt.Println("Is the node running?")
		if nodeRunner != nil {
			nodeRunner.Stop()
		}
		os.Exit(1)
	}

	fmt.Println("Connected to node successfully!")
	fmt.Println()
	fmt.Println("Mining started. Press Ctrl+C to stop.")
	fmt.Println()

	// Start mining
	miner := NewMiner(activeNodeURL, address, *threads)
	miner.Start()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nStopping miner...")
	miner.Stop()
	miner.PrintStats()

	// Stop embedded node if we started one
	if nodeRunner != nil {
		nodeRunner.Stop()
	}
}

func loadMinerAddress(walletDir string) (string, error) {
	addressPath := filepath.Join(walletDir, "address")
	data, err := os.ReadFile(addressPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func printBanner() {
	fmt.Printf(`
  ██████╗ ██╗██╗     ██╗████████╗██╗  ██╗██╗██╗   ██╗███╗   ███╗
  ██╔══██╗██║██║     ██║╚══██╔══╝██║  ██║██║██║   ██║████╗ ████║
  ██║  ██║██║██║     ██║   ██║   ███████║██║██║   ██║██╔████╔██║
  ██║  ██║██║██║     ██║   ██║   ██╔══██║██║██║   ██║██║╚██╔╝██║
  ██████╔╝██║███████╗██║   ██║   ██║  ██║██║╚██████╔╝██║ ╚═╝ ██║
  ╚═════╝ ╚═╝╚══════╝╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝ ╚═════╝ ╚═╝     ╚═╝
                       Miner v%s

`, AppVersion)
}
