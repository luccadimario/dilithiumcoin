package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	bip39 "github.com/tyler-smith/go-bip39"
)

const version = "1.0.0"

func main() {
	config := parseConfig()

	fmt.Printf("=== Dilithium Exchange v%s ===\n", version)

	// --- Database ---
	fmt.Printf("[init] opening database: %s\n", config.DatabasePath)
	db, err := NewDatabase(config.DatabasePath)
	if err != nil {
		fatalf("open database: %v", err)
	}
	defer db.Close()

	// --- DLT Wallet ---
	fmt.Printf("[init] loading DLT wallet from: %s\n", config.DLTWalletDir)
	dltWallet, err := NewDLTWallet(config.DLTWalletDir, config.DLTNodeURL)
	if err != nil {
		fatalf("load DLT wallet: %v", err)
	}
	fmt.Printf("[init] exchange DLT address: %s\n", dltWallet.Address())

	// --- HD Seed (ETH + BTC wallets) ---
	mnemonic, err := loadOrCreateSeed(config.ETHSeedPath)
	if err != nil {
		fatalf("load HD seed: %v", err)
	}

	// --- ETH Wallet ---
	fmt.Printf("[init] connecting to ETH RPC: %s\n", config.ETHRPCURL)
	ethWallet, err := NewETHWallet(mnemonic, config.ETHRPCURL)
	if err != nil {
		// Non-fatal — exchange can run without ETH deposits for now
		fmt.Printf("[warn] ETH wallet init failed: %v (ETH deposits/withdrawals disabled)\n", err)
		ethWallet = nil
	} else {
		addr0, _ := ethWallet.DeriveDepositAddress(0)
		fmt.Printf("[init] ETH wallet ready (index 0 = %s)\n", addr0)
	}

	// --- BTC Wallet ---
	btcWallet, err := NewBTCWallet(mnemonic, config.BTCAPIEndpoint)
	if err != nil {
		fmt.Printf("[warn] BTC wallet init failed: %v (BTC deposits/withdrawals disabled)\n", err)
		btcWallet = nil
	} else {
		addr0, _ := btcWallet.DeriveDepositAddress(0)
		fmt.Printf("[init] BTC wallet ready (index 0 = %s)\n", addr0)
	}

	// --- Order Book ---
	fmt.Printf("[init] loading order book from database...\n")
	ob, err := NewOrderBook(db, config)
	if err != nil {
		fatalf("init order book: %v", err)
	}

	// --- HTTP Server ---
	srv := NewServer(config, db, ob, ethWallet, btcWallet, dltWallet)

	// --- Deposit Watchers ---
	var ethWatcher *ETHWatcher
	var btcWatcher *BTCWatcher
	if ethWallet != nil {
		ethWatcher = NewETHWatcher(ethWallet, db, config)
		ethWatcher.Start()
		fmt.Printf("[init] ETH deposit watcher started (every 15s, %d confirms)\n", config.ETHConfirmations)
	}
	if btcWallet != nil {
		btcWatcher = NewBTCWatcher(btcWallet, db, config)
		btcWatcher.Start()
		fmt.Printf("[init] BTC deposit watcher started (every 60s, %d confirms)\n", config.BTCConfirmations)
	}
	dltWatcher := NewDLTWatcher(dltWallet, db, config)
	dltWatcher.Start()
	fmt.Printf("[init] DLT deposit watcher started (every 10s)\n")

	// --- Withdrawal Processor ---
	processor := NewWithdrawalProcessor(db, config, ethWallet, btcWallet, dltWallet)
	processor.Start()
	fmt.Printf("[init] withdrawal processor started (every 30s)\n")

	// --- Print summary ---
	fmt.Println()
	fmt.Printf("  Exchange DLT address: %s\n", dltWallet.Address())
	fmt.Printf("  HTTP API:             http://localhost:%d\n", config.HTTPPort)
	fmt.Printf("  DLT node:             %s\n", config.DLTNodeURL)
	fmt.Printf("  Trading pairs:        DLT/ETH, DLT/BTC\n")
	fmt.Printf("  Maker fee:            %.2f%%\n", config.MakerFee*100)
	fmt.Printf("  Taker fee:            %.2f%%\n", config.TakerFee*100)
	fmt.Println()
	fmt.Println("Exchange is running. Press Ctrl+C to stop.")
	fmt.Println()

	// --- Signal handling ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine (blocks until stopped)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Start()
	}()

	select {
	case sig := <-sigCh:
		fmt.Printf("\n[shutdown] received %s, shutting down...\n", sig)
	case err := <-serverErr:
		if err != nil {
			fmt.Printf("[server] error: %v\n", err)
		}
	}

	// Graceful shutdown
	fmt.Println("[shutdown] stopping components...")
	srv.Stop()
	if ethWatcher != nil {
		ethWatcher.Stop()
	}
	if btcWatcher != nil {
		btcWatcher.Stop()
	}
	dltWatcher.Stop()
	processor.Stop()

	fmt.Println("[shutdown] goodbye!")
}

// loadOrCreateSeed loads an existing BIP39 mnemonic from a file, or creates one.
// The file stores the mnemonic in plain text — in production, encrypt this file.
func loadOrCreateSeed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		mnemonic := string(data)
		if bip39.IsMnemonicValid(mnemonic) {
			fmt.Printf("[init] loaded HD seed from: %s\n", path)
			return mnemonic, nil
		}
	}

	// Create new mnemonic
	entropy, err := bip39.NewEntropy(256) // 24 words
	if err != nil {
		return "", fmt.Errorf("generate entropy: %w", err)
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("generate mnemonic: %w", err)
	}

	if err := os.WriteFile(path, []byte(mnemonic), 0600); err != nil {
		return "", fmt.Errorf("save seed: %w", err)
	}

	fmt.Printf("[init] *** NEW HD SEED CREATED: %s ***\n", path)
	fmt.Printf("[init] *** BACK THIS FILE UP — it controls all ETH/BTC deposit wallets ***\n")
	fmt.Printf("[init] Seed words: %s\n", mnemonic)
	return mnemonic, nil
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[fatal] "+format+"\n", args...)
	os.Exit(1)
}
