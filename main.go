package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const (
	AppName    = "Rebellion"
	AppVersion = "1.0.0"
	DefaultDifficulty = 6
)

func main() {
	// Parse command-line flags
	flags := parseFlags()

	if flags.Version {
		printVersion()
		os.Exit(0)
	}

	// Print startup banner
	printBanner()
	printStartupInfo(flags)

	// Create and start node
	node := NewNode(flags.Port, flags.Difficulty)
	node.StartServer()
	go node.StartAPI(flags.APIPort)

	// Setup UPnP port forwarding
	setupNetworkAccess(node, flags.Port)

	// Connect to peer if specified
	if flags.Connect != "" {
		connectToPeer(node, flags.Connect)
	}

	// Start auto-mining if configured
	if flags.AutoMine && flags.Miner != "" {
		node.StartAutoMining(flags.Miner)
	} else if flags.AutoMine && flags.Miner == "" {
		fmt.Println("Warning: --auto-mine requires --miner address")
	}

	fmt.Println("Node is running. Press Ctrl+C to stop.\n")

	// Wait for shutdown signal
	waitForShutdown()
}

// Flags holds all command-line flags
type Flags struct {
	Port       string
	Difficulty int
	Connect    string
	APIPort    string
	Version    bool
	Miner      string
	AutoMine   bool
}

// parseFlags parses command-line arguments
func parseFlags() Flags {
	flags := Flags{}
	flag.StringVar(&flags.Port, "port", "5001", "Port to run the node on")
	flag.IntVar(&flags.Difficulty, "difficulty", DefaultDifficulty, "Mining difficulty")
	flag.StringVar(&flags.Connect, "connect", "", "Peer address to connect to")
	flag.StringVar(&flags.APIPort, "api-port", "8001", "API port")
	flag.BoolVar(&flags.Version, "version", false, "Show version")
	flag.StringVar(&flags.Miner, "miner", "", "Miner address for mining rewards (enables mining)")
	flag.BoolVar(&flags.AutoMine, "auto-mine", false, "Enable automatic mining when transactions are pending")
	flag.Parse()
	return flags
}

// printVersion prints the application version
func printVersion() {
	fmt.Printf("%s v%s\n", AppName, AppVersion)
}

// printBanner prints the startup banner
func printBanner() {
	fmt.Println("========================================")
	fmt.Printf("%s Node - RBL Cryptocurrency\n", AppName)
	fmt.Println("========================================\n")
}

// printStartupInfo prints startup configuration
func printStartupInfo(flags Flags) {
	fmt.Printf("Starting node on port %s...\n", flags.Port)
	fmt.Printf("Mining difficulty: %d\n", flags.Difficulty)
	fmt.Printf("API port: %s\n", flags.APIPort)
	if flags.AutoMine && flags.Miner != "" {
		fmt.Printf("Auto-mining: ENABLED (rewards to %s)\n", flags.Miner)
	} else {
		fmt.Println("Auto-mining: disabled (use --auto-mine --miner <address>)")
	}
	fmt.Println()
}

// setupNetworkAccess attempts to setup UPnP port forwarding
func setupNetworkAccess(node *Node, port string) {
	externalIP, err := SetupUPnP(port)
	if err != nil {
		fmt.Println("UPnP not available - manual port forwarding required")
		fmt.Printf("To connect from internet, forward port %s to your machine\n", port)
		return
	}

	fmt.Printf("\nYour public address: %s:%s\n", externalIP, port)
	fmt.Printf("Share this with others to connect!\n\n")
}

// connectToPeer connects to a peer node
func connectToPeer(node *Node, address string) {
	fmt.Printf("Connecting to peer: %s\n", address)
	if err := node.ConnectToPeer(address); err != nil {
		fmt.Printf("Failed to connect to peer: %v\n", err)
	}
}

// waitForShutdown waits for a shutdown signal and exits gracefully
func waitForShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down node...")
	os.Exit(0)
}
