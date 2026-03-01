package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

func handleContract(args []string) {
	if len(args) == 0 {
		printContractUsage()
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "deploy":
		cmdContractDeploy(args[1:])
	case "call":
		cmdContractCall(args[1:])
	case "query":
		cmdContractQuery(args[1:])
	case "code":
		cmdContractCode(args[1:])
	case "storage":
		cmdContractStorage(args[1:])
	default:
		fmt.Printf("Unknown contract command: %s\n", subcommand)
		printContractUsage()
		os.Exit(1)
	}
}

func printContractUsage() {
	fmt.Println("Contract commands:")
	fmt.Println("  contract deploy   --code <hex> --gas-limit N --gas-price N")
	fmt.Println("  contract call     --to <addr> --data <hex> --gas-limit N")
	fmt.Println("  contract query    --to <addr> --data <hex>")
	fmt.Println("  contract code     --address <addr>")
	fmt.Println("  contract storage  --address <addr> --key <hex>")
}

func cmdContractDeploy(args []string) {
	var flagArgs, positionalArgs []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flagArgs = append(flagArgs, args[i])
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
		} else {
			positionalArgs = append(positionalArgs, args[i])
		}
	}
	_ = positionalArgs

	fs := flag.NewFlagSet("contract deploy", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	code := fs.String("code", "", "Hex-encoded bytecode")
	gasLimit := fs.Uint64("gas-limit", 1000000, "Gas limit")
	gasPrice := fs.Int64("gas-price", 100, "Gas price per unit")
	value := fs.Int64("value", 0, "DLT to send to contract (in base units)")
	fs.Parse(flagArgs)

	if *code == "" {
		fmt.Println("Error: --code is required")
		os.Exit(1)
	}

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

	pk := privateKey.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	timestamp := time.Now().Unix()

	txData := fmt.Sprintf("dilithium-mainnet:%d%s%s%d%d%d%d%d:%s",
		1, fromAddress, "", *value, int64(0), *gasLimit, *gasPrice, timestamp, *code)

	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(privateKey, []byte(txData), sig)
	signatureHex := hex.EncodeToString(sig)

	tx := map[string]interface{}{
		"from":       fromAddress,
		"amount":     *value,
		"gas_limit":  *gasLimit,
		"gas_price":  *gasPrice,
		"data":       *code,
		"timestamp":  timestamp,
		"signature":  signatureHex,
		"public_key": publicKeyHex,
	}

	fmt.Println()
	fmt.Println("Deploying contract...")
	fmt.Printf("  From:      %s\n", fromAddress)
	fmt.Printf("  Gas Limit: %d\n", *gasLimit)
	fmt.Printf("  Gas Price: %d\n", *gasPrice)
	fmt.Printf("  Code Size: %d bytes\n", len(*code)/2)
	fmt.Println()

	resp, err := postJSON(*nodeURL+"/contract/deploy", tx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if resp.Success {
		fmt.Println("Deploy transaction submitted successfully!")
		for key, val := range resp.Data {
			fmt.Printf("  %s: %v\n", key, val)
		}
	} else {
		fmt.Printf("Deploy failed: %s\n", resp.Message)
		os.Exit(1)
	}
}

func cmdContractCall(args []string) {
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flagArgs = append(flagArgs, args[i])
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
		}
	}

	fs := flag.NewFlagSet("contract call", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	to := fs.String("to", "", "Contract address")
	data := fs.String("data", "", "Hex-encoded calldata")
	gasLimit := fs.Uint64("gas-limit", 500000, "Gas limit")
	gasPrice := fs.Int64("gas-price", 100, "Gas price per unit")
	value := fs.Int64("value", 0, "DLT to send (in base units)")
	fs.Parse(flagArgs)

	if *to == "" {
		fmt.Println("Error: --to is required")
		os.Exit(1)
	}

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

	pk := privateKey.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	timestamp := time.Now().Unix()

	txDataStr := fmt.Sprintf("dilithium-mainnet:%d%s%s%d%d%d%d%d:%s",
		2, fromAddress, *to, *value, int64(0), *gasLimit, *gasPrice, timestamp, *data)

	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(privateKey, []byte(txDataStr), sig)
	signatureHex := hex.EncodeToString(sig)

	tx := map[string]interface{}{
		"from":       fromAddress,
		"to":         *to,
		"amount":     *value,
		"gas_limit":  *gasLimit,
		"gas_price":  *gasPrice,
		"data":       *data,
		"timestamp":  timestamp,
		"signature":  signatureHex,
		"public_key": publicKeyHex,
	}

	fmt.Println()
	fmt.Println("Calling contract...")
	fmt.Printf("  From:     %s\n", fromAddress)
	fmt.Printf("  To:       %s\n", *to)
	fmt.Printf("  Gas:      %d @ %d\n", *gasLimit, *gasPrice)
	if *data != "" {
		fmt.Printf("  Data:     %s\n", truncateHex(*data, 32))
	}
	fmt.Println()

	resp, err := postJSON(*nodeURL+"/contract/call", tx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if resp.Success {
		fmt.Println("Call transaction submitted successfully!")
	} else {
		fmt.Printf("Call failed: %s\n", resp.Message)
		os.Exit(1)
	}
}

func cmdContractQuery(args []string) {
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flagArgs = append(flagArgs, args[i])
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
		}
	}

	fs := flag.NewFlagSet("contract query", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	to := fs.String("to", "", "Contract address")
	data := fs.String("data", "", "Hex-encoded calldata")
	from := fs.String("from", "", "Caller address (optional)")
	fs.Parse(flagArgs)

	if *to == "" {
		fmt.Println("Error: --to is required")
		os.Exit(1)
	}

	req := map[string]interface{}{
		"to":   *to,
		"from": *from,
		"data": *data,
	}

	resp, err := postJSON(*nodeURL+"/contract/query", req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if resp.Success {
		fmt.Println("Query result:")
		if returnData, ok := resp.Data["return_data"].(string); ok {
			fmt.Printf("  Return data: %s\n", returnData)
		}
		if gasUsed, ok := resp.Data["gas_used"].(float64); ok {
			fmt.Printf("  Gas used:    %.0f\n", gasUsed)
		}
	} else {
		fmt.Printf("Query failed: %s\n", resp.Message)
		os.Exit(1)
	}
}

func cmdContractCode(args []string) {
	fs := flag.NewFlagSet("contract code", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	address := fs.String("address", "", "Contract address")
	fs.Parse(args)

	if *address == "" {
		fmt.Println("Error: --address is required")
		os.Exit(1)
	}

	resp, err := getJSON(fmt.Sprintf("%s/contract/code?address=%s", *nodeURL, *address))
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if resp.Success {
		fmt.Printf("Contract: %s\n", *address)
		if code, ok := resp.Data["code"].(string); ok {
			fmt.Printf("Code:     %s\n", truncateHex(code, 64))
		}
		if size, ok := resp.Data["code_size"].(float64); ok {
			fmt.Printf("Size:     %.0f bytes\n", size)
		}
	} else {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}
}

func cmdContractStorage(args []string) {
	fs := flag.NewFlagSet("contract storage", flag.ExitOnError)
	nodeURL := fs.String("node", "http://localhost:8001", "Node API URL")
	address := fs.String("address", "", "Contract address")
	key := fs.String("key", "", "Storage key (hex)")
	fs.Parse(args)

	if *address == "" || *key == "" {
		fmt.Println("Error: --address and --key are required")
		os.Exit(1)
	}

	resp, err := getJSON(fmt.Sprintf("%s/contract/storage?address=%s&key=%s", *nodeURL, *address, *key))
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if resp.Success {
		if value, ok := resp.Data["value"].(string); ok {
			fmt.Printf("Storage[%s] = %s\n", *key, value)
		}
	} else {
		fmt.Printf("Error: %s\n", resp.Message)
		os.Exit(1)
	}
}

func truncateHex(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
