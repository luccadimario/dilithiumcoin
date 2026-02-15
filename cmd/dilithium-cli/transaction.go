package main

import (
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

// DLTUnit is the number of base units in 1 DLT (like satoshis)
const DLTUnit int64 = 100_000_000

// FormatDLT formats a base unit amount as a human-readable DLT string
func FormatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// ParseDLT parses a human-readable DLT string to base units
func ParseDLT(s string) (int64, error) {
	s = strings.TrimSpace(s)

	// Try parsing as a float first for user convenience
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", s)
	}
	if f <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}

	// Convert to base units with rounding
	return int64(math.Round(f * float64(DLTUnit))), nil
}

// cmdSend is the simple send command: dilithium-cli send <address> <amount>
func cmdSend(args []string) {
	// Separate flags from positional args so flags can appear anywhere
	// (Go's flag package stops at the first non-flag argument)
	var flagArgs, positionalArgs []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flagArgs = append(flagArgs, args[i])
			// If this flag takes a value, grab the next arg too
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") &&
				(args[i] == "--node" || args[i] == "-node" || args[i] == "--wallet" || args[i] == "-wallet" ||
					args[i] == "--fee" || args[i] == "-fee") {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
		} else {
			positionalArgs = append(positionalArgs, args[i])
		}
	}

	fs := flag.NewFlagSet("send", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	feeStr := fs.String("fee", "0.0001", "Transaction fee in DLT (default: 0.0001)")
	fs.Parse(flagArgs)

	if len(positionalArgs) < 2 {
		fmt.Println("Usage: dilithium-cli send <address> <amount>")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  dilithium-cli send 8a3f2b1c9d4e5f6a 25.5")
		fmt.Println("  dilithium-cli send 8a3f2b1c9d4e5f6a 100 --node http://192.168.1.10:8001")
		os.Exit(1)
	}

	toAddress := positionalArgs[0]
	amount, err := ParseDLT(positionalArgs[1])
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fee, err := ParseDLT(*feeStr)
	if err != nil {
		fmt.Printf("Error parsing fee: %v\n", err)
		os.Exit(1)
	}

	// Load wallet
	fromAddress, err := loadAddress(*walletDir)
	if err != nil {
		fmt.Println("No wallet found. Create one first:")
		fmt.Println("  dilithium-cli init")
		os.Exit(1)
	}

	privateKey, err := loadPrivateKey(*walletDir)
	if err != nil {
		fmt.Printf("Error loading wallet: %v\n", err)
		os.Exit(1)
	}

	// Get hex-encoded public key for inclusion in transaction
	pk := privateKey.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	// Create and sign transaction (includes fee in signing data)
	timestamp := time.Now().Unix()
	txData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d%d", fromAddress, toAddress, amount, fee, timestamp)

	// Dilithium signs the raw message directly
	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(privateKey, []byte(txData), sig)
	signatureHex := hex.EncodeToString(sig)

	// Show transaction details
	fmt.Println()
	fmt.Println("Sending transaction...")
	fmt.Println()
	fmt.Printf("  From:   %s\n", fromAddress)
	fmt.Printf("  To:     %s\n", toAddress)
	fmt.Printf("  Amount: %s DLT\n", FormatDLT(amount))
	fmt.Printf("  Fee:    %s DLT\n", FormatDLT(fee))
	fmt.Println()

	// Submit to node
	tx := map[string]interface{}{
		"from":       fromAddress,
		"to":         toAddress,
		"amount":     amount,
		"fee":        fee,
		"timestamp":  timestamp,
		"signature":  signatureHex,
		"public_key": publicKeyHex,
	}

	// Submit to multiple discovered nodes in parallel for reliability
	targets := DiscoverAllReachable(*nodeURL)

	type submitResult struct {
		url  string
		resp *APIResponse
		err  error
	}

	results := make(chan submitResult, len(targets))
	for _, target := range targets {
		go func(u string) {
			r, e := postJSON(u+"/transaction", tx)
			results <- submitResult{url: u, resp: r, err: e}
		}(target)
	}

	var anySuccess bool
	var lastErr string
	for i := 0; i < len(targets); i++ {
		res := <-results
		if res.err == nil && res.resp != nil && res.resp.Success {
			anySuccess = true
		} else if res.err != nil {
			lastErr = fmt.Sprintf("Could not connect to %s: %v", res.url, res.err)
		} else if res.resp != nil && !res.resp.Success {
			lastErr = fmt.Sprintf("Node %s rejected: %s", res.url, res.resp.Message)
		}
	}

	if anySuccess {
		fmt.Printf("Transaction submitted to %d node(s) successfully!\n", len(targets))
		fmt.Println()
		fmt.Println("The transaction is now in the mempool waiting to be mined.")
	} else {
		fmt.Printf("Transaction failed: %s\n", lastErr)
		fmt.Println()
		fmt.Println("Is the node running? Start it with:")
		fmt.Println("  dilithium --api-port 8001")
		os.Exit(1)
	}
}

// cmdSignTransaction signs a transaction without submitting
func cmdSignTransaction(args []string) {
	fs := flag.NewFlagSet("transaction sign", flag.ExitOnError)
	from := fs.String("from", "", "Sender address")
	to := fs.String("to", "", "Recipient address")
	amountStr := fs.String("amount", "", "Amount to send (in DLT)")
	keyFile := fs.String("key", "", "Path to private key file")
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory (if --key not specified)")

	fs.Parse(args)

	// If no from address, try to load from wallet
	if *from == "" {
		addr, err := loadAddress(*walletDir)
		if err == nil {
			*from = addr
		}
	}

	// Parse amount
	var amount int64
	if *amountStr != "" {
		var err error
		amount, err = ParseDLT(*amountStr)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Validate inputs
	if *from == "" || *to == "" || amount <= 0 {
		fmt.Println("Error: Missing required flags")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  dilithium-cli tx sign --to <address> --amount <number>")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --from <address>   Sender address (auto-detected from wallet)")
		fmt.Println("  --to <address>     Recipient address (required)")
		fmt.Println("  --amount <number>  Amount to send in DLT (required)")
		fmt.Println("  --key <file>       Private key file")
		fmt.Println("  --wallet <dir>     Wallet directory")
		os.Exit(1)
	}

	// Load private key
	var privateKey *mode3.PrivateKey
	var err error

	if *keyFile != "" {
		privateKey, err = loadPrivateKeyFromFile(*keyFile)
	} else {
		privateKey, err = loadPrivateKey(*walletDir)
	}

	if err != nil {
		fmt.Printf("Error loading private key: %v\n", err)
		os.Exit(1)
	}

	// Get hex-encoded public key
	pk := privateKey.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	// Default fee for signed transactions
	var fee int64 = 10000 // 0.0001 DLT

	// Create and sign transaction (includes fee)
	timestamp := time.Now().Unix()
	txData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d%d", *from, *to, amount, fee, timestamp)

	// Dilithium signs the raw message directly
	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(privateKey, []byte(txData), sig)
	signatureHex := hex.EncodeToString(sig)

	// Display the signed transaction
	fmt.Println()
	fmt.Println("========== SIGNED TRANSACTION ==========")
	fmt.Printf("From:      %s\n", *from)
	fmt.Printf("To:        %s\n", *to)
	fmt.Printf("Amount:    %s DLT (%d base units)\n", FormatDLT(amount), amount)
	fmt.Printf("Fee:       %s DLT (%d base units)\n", FormatDLT(fee), fee)
	fmt.Printf("Timestamp: %d\n", timestamp)
	fmt.Printf("Signature: %s...\n", signatureHex[:32])
	fmt.Println("=========================================")
	fmt.Println()

	// Show JSON for manual submission
	fmt.Println("Transaction JSON:")
	fmt.Printf(`{
  "from": "%s",
  "to": "%s",
  "amount": %d,
  "fee": %d,
  "timestamp": %d,
  "signature": "%s",
  "public_key": "%s"
}
`, *from, *to, amount, fee, timestamp, signatureHex, publicKeyHex)
	fmt.Println()

	// Show curl command
	fmt.Println("Submit with curl:")
	fmt.Printf("curl -X POST http://localhost:8001/transaction \\\n")
	fmt.Printf("  -H 'Content-Type: application/json' \\\n")
	fmt.Printf("  -d '{\"from\":\"%s\",\"to\":\"%s\",\"amount\":%d,\"fee\":%d,\"timestamp\":%d,\"signature\":\"%s\",\"public_key\":\"%s\"}'\n",
		*from, *to, amount, fee, timestamp, signatureHex, publicKeyHex)
}

// loadPrivateKeyFromFile loads a private key from a specific file
func loadPrivateKeyFromFile(keyFile string) (*mode3.PrivateKey, error) {
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("could not read key file: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM format")
	}

	var sk mode3.PrivateKey
	if err := sk.UnmarshalBinary(block.Bytes); err != nil {
		return nil, fmt.Errorf("could not parse Dilithium private key: %w", err)
	}

	return &sk, nil
}
