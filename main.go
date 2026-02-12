package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
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

	// Load or create configuration
	config := DefaultConfig()
	config.P2PPort = flags.Port
	config.API.Port = flags.APIPort
	config.API.Host = flags.APIAddr
	config.Chain.DefaultDifficulty = flags.Difficulty
	config.MinerAddress = flags.Miner
	config.AutoMine = flags.AutoMine

	// Apply environment overrides
	config.ApplyEnvOverrides()

	// Validate configuration
	if err := config.Validate(); err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Ensure data directory exists
	if err := config.EnsureDataDir(); err != nil {
		fmt.Printf("Failed to create data directory: %v\n", err)
		os.Exit(1)
	}

	printStartupInfo(config)

	// Create node with blockchain
	node := NewNode(config.P2PPort, config.Chain.DefaultDifficulty)

	// Create peer manager
	peerConfig := &PeerConfig{
		MaxInbound:       config.Network.MaxInbound,
		MaxOutbound:      config.Network.MaxOutbound,
		HandshakeTimeout: config.Network.HandshakeTimeout,
		PingInterval:     config.Network.PingInterval,
		PingTimeout:      config.Network.PingTimeout,
		ConnectTimeout:   config.Network.ConnectTimeout,
		BanDuration:      config.Network.BanDuration,
		MinPeers:         config.Network.MinOutbound,
		MaxAddrBook:      config.Network.MaxStoredPeers,
	}
	peerManager := NewPeerManager(node, peerConfig)
	node.PeerManager = peerManager

	// Determine local address for P2P
	localAddr := NewNetAddr("0.0.0.0", parsePort(config.P2PPort), SFNodeNetwork)
	if config.AutoMine {
		localAddr.Services |= SFNodeMiner
	}
	if config.API.Enabled {
		localAddr.Services |= SFNodeAPI
	}

	// Start TCP listener for P2P connections
	listener, err := net.Listen("tcp", "0.0.0.0:"+config.P2PPort)
	if err != nil {
		fmt.Printf("Failed to start P2P server on port %s: %v\n", config.P2PPort, err)
		os.Exit(1)
	}
	fmt.Printf("P2P server listening on 0.0.0.0:%s\n", config.P2PPort)

	// Start accepting connections
	go acceptConnections(listener, peerManager)

	// Start peer manager
	peerManager.Start(localAddr)

	// Start API server
	if config.API.Enabled {
		go node.StartAPI(config.API.Host, config.API.Port, flags.TLSCert, flags.TLSKey)
	}

	// Setup UPnP port forwarding
	setupNetworkAccess(node, config.P2PPort)

	// Bootstrap network
	if flags.NoSeeds {
		// Clear seed nodes for local testing
		config.Network.SeedNodes = []string{}
		fmt.Println("Seed nodes disabled (local testing mode)")
	}

	if flags.Connect != "" {
		// If specific peer is provided, add it as a seed
		peerManager.AddSeedNode(flags.Connect)
		config.Network.SeedNodes = append([]string{flags.Connect}, config.Network.SeedNodes...)
	}

	// Bootstrap: try peers.dat, then seed nodes
	peerManager.Bootstrap(config.Network, config.PeersFilePath())

	// Wait for initial chain sync before mining
	if peerManager.PeerCount() > 0 {
		fmt.Println("Waiting for chain sync...")
		time.Sleep(3 * time.Second)
	}

	// Start auto-mining if configured
	if config.AutoMine && config.MinerAddress != "" {
		node.StartAutoMining(config.MinerAddress)
	} else if config.AutoMine && config.MinerAddress == "" {
		fmt.Println("Warning: --auto-mine requires --miner address")
	}

	fmt.Println("\nNode is running. Press Ctrl+C to stop.")
	printNodeInfo(node, peerManager)

	// Wait for shutdown signal
	waitForShutdown(node, peerManager, listener, config)
}

// Flags holds all command-line flags
type Flags struct {
	Port       string
	Difficulty int
	Connect    string
	APIPort    string
	APIAddr    string
	Version    bool
	Miner      string
	AutoMine   bool
	DataDir    string
	Testnet    bool
	NoSeeds    bool
	TLSCert    string
	TLSKey     string
}

// parseFlags parses command-line arguments
func parseFlags() Flags {
	flags := Flags{}
	flag.StringVar(&flags.Port, "port", "1701", "P2P port")
	flag.IntVar(&flags.Difficulty, "difficulty", 6, "Mining difficulty")
	flag.StringVar(&flags.Connect, "connect", "", "Initial peer to connect to")
	flag.StringVar(&flags.APIPort, "api-port", "8001", "HTTP API port")
	flag.StringVar(&flags.APIAddr, "api-addr", "127.0.0.1", "API listen address (use 0.0.0.0 for all interfaces)")
	flag.BoolVar(&flags.Version, "version", false, "Show version and exit")
	flag.StringVar(&flags.Miner, "miner", "", "Miner wallet address for rewards")
	flag.BoolVar(&flags.AutoMine, "auto-mine", false, "Enable automatic mining")
	flag.StringVar(&flags.DataDir, "data-dir", "", "Data directory path")
	flag.BoolVar(&flags.Testnet, "testnet", false, "Run on testnet")
	flag.BoolVar(&flags.NoSeeds, "no-seeds", false, "Don't connect to seed nodes (for local testing)")
	flag.StringVar(&flags.TLSCert, "tls-cert", "", "TLS certificate file for HTTPS API")
	flag.StringVar(&flags.TLSKey, "tls-key", "", "TLS key file for HTTPS API")
	flag.Parse()
	return flags
}

// printVersion prints the application version
func printVersion() {
	fmt.Printf("%s v%s\n", NetworkName, Version)
	fmt.Printf("Protocol version: %d\n", ProtocolVersion)
	fmt.Printf("User agent: %s\n", UserAgent)
}

// printBanner prints the startup banner
func printBanner() {
	fmt.Println()
	fmt.Println("  ██████╗ ██╗██╗     ██╗████████╗██╗  ██╗██╗██╗   ██╗███╗   ███╗")
	fmt.Println("  ██╔══██╗██║██║     ██║╚══██╔══╝██║  ██║██║██║   ██║████╗ ████║")
	fmt.Println("  ██║  ██║██║██║     ██║   ██║   ███████║██║██║   ██║██╔████╔██║")
	fmt.Println("  ██║  ██║██║██║     ██║   ██║   ██╔══██║██║██║   ██║██║╚██╔╝██║")
	fmt.Println("  ██████╔╝██║███████╗██║   ██║   ██║  ██║██║╚██████╔╝██║ ╚═╝ ██║")
	fmt.Println("  ╚═════╝ ╚═╝╚══════╝╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝ ╚═════╝ ╚═╝     ╚═╝")
	fmt.Printf("                       v%s - DLT Cryptocurrency\n", Version)
	fmt.Println()
}

// printStartupInfo prints startup configuration
func printStartupInfo(config *Config) {
	fmt.Println("Configuration:")
	fmt.Printf("  P2P Port:     %s\n", config.P2PPort)
	fmt.Printf("  API Port:     %s\n", config.API.Port)
	fmt.Printf("  Difficulty:   %d\n", config.Chain.DefaultDifficulty)
	fmt.Printf("  Data Dir:     %s\n", config.DataDir)

	if config.AutoMine && config.MinerAddress != "" {
		minerDisplay := config.MinerAddress
		if len(minerDisplay) > 16 {
			minerDisplay = minerDisplay[:8] + "..." + minerDisplay[len(minerDisplay)-8:]
		}
		fmt.Printf("  Mining:       ENABLED (rewards to %s)\n", minerDisplay)
	} else {
		fmt.Println("  Mining:       disabled")
	}

	fmt.Printf("  Seed Nodes:   %d configured\n", len(config.Network.SeedNodes))
	fmt.Println()
}

// printNodeInfo prints node information after startup
func printNodeInfo(node *Node, pm *PeerManager) {
	stats := pm.GetNetworkStats()
	fmt.Println()
	fmt.Printf("Network Status:\n")
	fmt.Printf("  Connected Peers: %d (in: %d, out: %d)\n",
		stats.PeerCount, stats.InboundCount, stats.OutboundCount)
	fmt.Printf("  Known Addresses: %d\n", stats.KnownAddresses)
	fmt.Printf("  Block Height:    %d\n", node.Blockchain.GetBlockCount())
	fmt.Printf("  Pending TXs:     %d\n", node.Blockchain.GetPendingCount())
	fmt.Println()
}

// acceptConnections accepts incoming P2P connections
func acceptConnections(listener net.Listener, pm *PeerManager) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Check if listener was closed
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			fmt.Printf("Error accepting connection: %v\n", err)
			continue
		}

		pm.HandleInbound(conn)
	}
}

// setupNetworkAccess attempts to setup UPnP port forwarding
func setupNetworkAccess(node *Node, port string) {
	externalIP, err := SetupUPnP(port)
	if err != nil {
		fmt.Println("UPnP not available - manual port forwarding may be required")
		return
	}

	fmt.Printf("Public address: %s:%s (UPnP configured)\n", externalIP, port)
}

// waitForShutdown waits for a shutdown signal and exits gracefully
func waitForShutdown(node *Node, pm *PeerManager, listener net.Listener, config *Config) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")

	// Stop mining
	node.StopAutoMining()

	// Save peer database
	fmt.Println("Saving peer database...")
	pm.SavePeerDatabase(config.PeersFilePath())

	// Close listener
	listener.Close()

	// Stop peer manager
	pm.Stop()

	fmt.Println("Shutdown complete.")
	os.Exit(0)
}

// parsePort parses a port string to uint16
func parsePort(port string) uint16 {
	var p uint16
	// Use strconv instead of fmt.Sscanf (shannon #15)
	if n, err := strconv.ParseUint(port, 10, 16); err == nil {
		p = uint16(n)
	}
	return p
}
