package main

import (
	"fmt"
	"sync"
	"time"
)

// DLTWatcher polls the Dilithium node for DLT deposits to the exchange address.
// It matches deposits to users via their linked DLT address (the "from" field).
type DLTWatcher struct {
	wallet  *DLTWallet
	db      *Database
	config  *Config
	stopCh  chan struct{}
	wg      sync.WaitGroup
	seenTxs map[string]bool
}

func NewDLTWatcher(wallet *DLTWallet, db *Database, config *Config) *DLTWatcher {
	return &DLTWatcher{
		wallet:  wallet,
		db:      db,
		config:  config,
		stopCh:  make(chan struct{}),
		seenTxs: make(map[string]bool),
	}
}

func (w *DLTWatcher) Start() {
	w.wg.Add(1)
	go w.watchLoop()
}

func (w *DLTWatcher) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

func (w *DLTWatcher) watchLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Seed seenTxs from DB on startup to avoid re-processing known deposits
	w.seedSeen()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			if err := w.scan(); err != nil {
				fmt.Printf("[dlt-watcher] error: %v\n", err)
			}
		}
	}
}

func (w *DLTWatcher) seedSeen() {
	// Load already-known DLT deposit tx hashes so we don't double-credit on restart
	deps, _ := w.db.GetConfirmedDeposits()
	for _, dep := range deps {
		if dep.Currency == "DLT" {
			w.seenTxs[dep.TxHash] = true
		}
	}
}

func (w *DLTWatcher) scan() error {
	// Fetch all incoming transactions to the exchange DLT address
	info, err := w.wallet.GetAddressInfo(w.wallet.Address())
	if err != nil {
		return fmt.Errorf("fetch address info: %w", err)
	}

	// Build map: dlt_address → user for matching deposits
	users, err := w.db.GetAllUsers()
	if err != nil {
		return fmt.Errorf("load users: %w", err)
	}
	dltToUser := make(map[string]*User)
	for _, user := range users {
		if user.DLTAddress != "" {
			dltToUser[user.DLTAddress] = user
		}
	}

	for _, tx := range info.Transactions {
		// Only process transactions TO the exchange (incoming)
		if tx.To != w.wallet.Address() {
			continue
		}
		if tx.Amount <= 0 {
			continue
		}

		// Use tx signature as unique ID
		txID := tx.Signature
		if w.seenTxs[txID] {
			continue
		}

		// Check DB for existence
		exists, _ := w.db.DepositExists("DLT", txID)
		if exists {
			w.seenTxs[txID] = true
			continue
		}

		// Match sender to a user by their linked DLT address
		user, ok := dltToUser[tx.From]
		if !ok {
			// Deposit from unknown address — log and skip (cannot attribute to a user)
			fmt.Printf("[dlt-watcher] unattributable DLT deposit from %s: %s DLT (no user linked)\n",
				tx.From[:10], FormatDLT(tx.Amount))
			w.seenTxs[txID] = true
			continue
		}

		// DLT deposits are confirmed after 1 block (block_index > 0 means mined)
		confirmations := 1
		if tx.BlockIndex <= 0 {
			confirmations = 0
		}
		status := "pending"
		if confirmations >= w.config.DLTConfirmations {
			status = "confirmed"
		}

		dep := &Deposit{
			UserID:        user.ID,
			Currency:      "DLT",
			Amount:        tx.Amount,
			TxHash:        txID,
			Confirmations: confirmations,
			Status:        status,
		}

		if err := w.db.CreateDeposit(dep); err != nil {
			fmt.Printf("[dlt-watcher] save deposit: %v\n", err)
		} else {
			fmt.Printf("[dlt-watcher] detected DLT deposit: user=%d amount=%s from=%s\n",
				user.ID, FormatDLT(tx.Amount), tx.From[:10])
		}

		w.seenTxs[txID] = true
	}

	// Credit confirmed deposits
	w.creditConfirmed()
	return nil
}

func (w *DLTWatcher) creditConfirmed() {
	deps, err := w.db.GetConfirmedDeposits()
	if err != nil {
		return
	}
	for _, dep := range deps {
		if dep.Currency != "DLT" {
			continue
		}
		if err := w.db.CreditDeposit(dep.ID, dep.UserID, dep.Currency, dep.Amount); err != nil {
			fmt.Printf("[dlt-watcher] credit deposit %d: %v\n", dep.ID, err)
		} else if dep.Amount > 0 {
			fmt.Printf("[dlt-watcher] credited DLT deposit: user=%d amount=%s\n",
				dep.UserID, FormatDLT(dep.Amount))
		}
	}
}
