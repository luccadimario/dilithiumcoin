package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const version = "1.0.0"

func main() {
	nodeURL := flag.String("node", "http://localhost:8001", "Node RPC URL")
	stratumPort := flag.Int("stratum-port", 3333, "Stratum server port")
	dashboardPort := flag.Int("dashboard-port", 8080, "Dashboard HTTP port")
	walletDir := flag.String("wallet-dir", "", "Pool wallet directory (required)")
	walletPassword := flag.String("wallet-password", "", "Pool wallet passphrase (if encrypted)")
	poolFee := flag.Float64("pool-fee", 2.0, "Pool fee percentage")
	minPayoutDLT := flag.Float64("min-payout", 1.0, "Minimum payout in DLT")
	payoutInterval := flag.Duration("payout-interval", 10*time.Minute, "Payout check interval")
	dbPath := flag.String("db", "pool_db.json", "Database file path")
	autoSaveInterval := flag.Duration("autosave", 60*time.Second, "Database auto-save interval")
	showVersion := flag.Bool("version", false, "Show version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Printf("dilithium-pool v%s\n", version)
		os.Exit(0)
	}

	if *walletDir == "" {
		fmt.Println("Error: --wallet-dir is required")
		fmt.Println("Create a wallet first: dilithium-wallet create --output /path/to/pool-wallet")
		flag.Usage()
		os.Exit(1)
	}

	// Load pool wallet
	fmt.Printf("Loading pool wallet from %s...\n", *walletDir)
	wallet, err := LoadPoolWallet(*walletDir, *walletPassword)
	if err != nil {
		fmt.Printf("Failed to load wallet: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pool wallet loaded: %s\n", wallet.Address)

	// Load database
	fmt.Printf("Loading database from %s...\n", *dbPath)
	db, err := NewDatabase(*dbPath)
	if err != nil {
		fmt.Printf("Failed to load database: %v\n", err)
		os.Exit(1)
	}
	db.data.PoolWallet = wallet.Address

	// Start auto-save
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	db.StartAutoSave(*autoSaveInterval, stopCh, &wg)

	// Create pool
	pool := NewPool(*nodeURL, wallet.Address, *stratumPort, *poolFee)
	pool.db = db
	pool.wallet = wallet

	// Create payout engine
	minPayout := int64(*minPayoutDLT * float64(DLTUnit))
	payoutEngine := NewPayoutEngine(pool, db, wallet, *nodeURL, minPayout, *payoutInterval)

	// Create dashboard
	dashboard := NewDashboard(pool, db, *dashboardPort)

	// Start everything
	pool.Start()
	payoutEngine.Start()
	go func() {
		if err := dashboard.Start(); err != nil {
			fmt.Printf("Dashboard error: %v\n", err)
		}
	}()

	fmt.Println()
	fmt.Printf("=== Dilithium Mining Pool v%s ===\n", version)
	fmt.Printf("Pool Address:  %s\n", wallet.Address)
	fmt.Printf("Pool Fee:      %.1f%%\n", *poolFee)
	fmt.Printf("Stratum:       stratum+tcp://0.0.0.0:%d\n", *stratumPort)
	fmt.Printf("Dashboard:     http://localhost:%d\n", *dashboardPort)
	fmt.Printf("Node:          %s\n", *nodeURL)
	fmt.Printf("Min Payout:    %s DLT\n", formatDLT(minPayout))
	fmt.Printf("Payout Check:  every %s\n", *payoutInterval)
	fmt.Printf("Database:      %s (auto-save every %s)\n", *dbPath, *autoSaveInterval)
	fmt.Println("Pool is running. Press Ctrl+C to stop.")
	fmt.Println()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	close(stopCh)
	pool.Stop()
	payoutEngine.Stop()
	dashboard.Stop()
	wg.Wait()

	// Final save
	if err := db.Save(); err != nil {
		fmt.Printf("Final save failed: %v\n", err)
	} else {
		fmt.Println("Database saved.")
	}

	fmt.Println("Goodbye!")
}
