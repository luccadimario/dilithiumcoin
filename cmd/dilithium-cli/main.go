package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	AppVersion = "3.2.1"
	AppName    = "dilithium-cli"
)

// Default paths
var (
	DefaultDataDir   string
	DefaultWalletDir string
)

func init() {
	home, _ := os.UserHomeDir()
	DefaultDataDir = filepath.Join(home, ".dilithium")
	DefaultWalletDir = filepath.Join(DefaultDataDir, "wallet")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	// Wallet commands
	case "init":
		cmdInit(args)
	case "wallet":
		handleWallet(args)
	case "address":
		cmdAddress(args)
	case "balance":
		cmdBalance(args)

	// Transaction commands
	case "send":
		cmdSend(args)
	case "transaction", "tx":
		handleTransaction(args)

	// Query commands
	case "status":
		cmdStatus(args)
	case "peers":
		cmdPeers(args)
	case "mempool":
		cmdMempool(args)
	case "block":
		cmdBlock(args)

	// Help
	case "help", "-h", "--help":
		printUsage()
	case "version", "-v", "--version":
		fmt.Printf("%s v%s\n", AppName, AppVersion)

	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func handleWallet(args []string) {
	if len(args) == 0 {
		// Show wallet info if no subcommand
		cmdAddress(nil)
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "create", "new":
		cmdInit(args[1:])
	case "info", "show":
		cmdWalletInfo(args[1:])
	case "export":
		cmdWalletExport(args[1:])
	default:
		fmt.Printf("Unknown wallet command: %s\n", subcommand)
		fmt.Println("\nWallet commands:")
		fmt.Println("  wallet create    - Create new wallet")
		fmt.Println("  wallet info      - Show wallet information")
		fmt.Println("  wallet export    - Export wallet keys")
		os.Exit(1)
	}
}

func handleTransaction(args []string) {
	if len(args) == 0 {
		fmt.Println("Transaction commands:")
		fmt.Println("  tx sign    - Sign a transaction")
		fmt.Println("  tx send    - Sign and broadcast transaction")
		fmt.Println("  tx status  - Check transaction status")
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "sign":
		cmdSignTransaction(args[1:])
	case "send":
		cmdSend(args[1:])
	default:
		fmt.Printf("Unknown transaction command: %s\n", subcommand)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`
  ██████╗ ██╗██╗     ██╗████████╗██╗  ██╗██╗██╗   ██╗███╗   ███╗
  ██╔══██╗██║██║     ██║╚══██╔══╝██║  ██║██║██║   ██║████╗ ████║
  ██║  ██║██║██║     ██║   ██║   ███████║██║██║   ██║██╔████╔██║
  ██║  ██║██║██║     ██║   ██║   ██╔══██║██║██║   ██║██║╚██╔╝██║
  ██████╔╝██║███████╗██║   ██║   ██║  ██║██║╚██████╔╝██║ ╚═╝ ██║
  ╚═════╝ ╚═╝╚══════╝╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝ ╚═════╝ ╚═╝     ╚═╝
                         CLI v%s

`, AppVersion)

	fmt.Println("USAGE:")
	fmt.Println("  dilithium-cli <command> [options]")
	fmt.Println()
	fmt.Println("WALLET COMMANDS:")
	fmt.Println("  init                      Create a new wallet (first time setup)")
	fmt.Println("  address                   Show your wallet address")
	fmt.Println("  balance [address]         Check balance (yours or specified address)")
	fmt.Println()
	fmt.Println("TRANSACTION COMMANDS:")
	fmt.Println("  send <address> <amount>   Send DLT to an address")
	fmt.Println("  tx sign [options]         Sign a transaction manually")
	fmt.Println()
	fmt.Println("NETWORK COMMANDS:")
	fmt.Println("  status                    Node status")
	fmt.Println("  peers                     List connected peers")
	fmt.Println("  mempool                   View pending transactions")
	fmt.Println("  block [index]             View block details")
	fmt.Println()
	fmt.Println("OPTIONS:")
	fmt.Println("  --node <url>              Node API URL (default: http://localhost:8001)")
	fmt.Println("  --wallet <path>           Wallet directory (default: ~/.dilithium/wallet)")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  dilithium-cli init")
	fmt.Println("  dilithium-cli send 8a3f2b1c9d4e5f6a 25.5")
	fmt.Println("  dilithium-cli balance")
	fmt.Println("  dilithium-cli status --node http://192.168.1.10:8001")
}
