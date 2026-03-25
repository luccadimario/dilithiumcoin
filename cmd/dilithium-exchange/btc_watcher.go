package main

import (
	"fmt"
	"sync"
	"time"
)

// BTCWatcher polls BlockCypher for deposits to user BTC deposit addresses.
type BTCWatcher struct {
	wallet *BTCWallet
	db     *Database
	config *Config
	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewBTCWatcher(wallet *BTCWallet, db *Database, config *Config) *BTCWatcher {
	return &BTCWatcher{
		wallet: wallet,
		db:     db,
		config: config,
		stopCh: make(chan struct{}),
	}
}

func (w *BTCWatcher) Start() {
	w.wg.Add(1)
	go w.watchLoop()
}

func (w *BTCWatcher) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

func (w *BTCWatcher) watchLoop() {
	defer w.wg.Done()

	// BTC blocks are ~10 min; poll every 60s
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			if err := w.scan(); err != nil {
				fmt.Printf("[btc-watcher] error: %v\n", err)
			}
		}
	}
}

func (w *BTCWatcher) scan() error {
	users, err := w.db.GetAllUsers()
	if err != nil || len(users) == 0 {
		return err
	}

	currentHeight, err := w.wallet.GetCurrentBlockHeight()
	if err != nil {
		return fmt.Errorf("get block height: %w", err)
	}

	for _, user := range users {
		addr, err := w.wallet.DeriveDepositAddress(user.BTCDepositIndex)
		if err != nil {
			continue
		}

		txs, err := w.wallet.GetAddressTxs(addr)
		if err != nil {
			fmt.Printf("[btc-watcher] get txs for %s: %v\n", addr[:10], err)
			continue
		}

		for _, tx := range txs {
			exists, _ := w.db.DepositExists("BTC", tx.Hash)
			if exists {
				// Update confirmations for pending deposits
				w.db.UpdateDepositConfirmations("BTC", tx.Hash, tx.Confirmations)
				continue
			}

			dep := &Deposit{
				UserID:        user.ID,
				Currency:      "BTC",
				Amount:        tx.AmountSats,
				TxHash:        tx.Hash,
				Confirmations: tx.Confirmations,
				Status:        "pending",
			}
			if tx.Confirmations >= w.config.BTCConfirmations {
				dep.Status = "confirmed"
			}

			if err := w.db.CreateDeposit(dep); err != nil {
				fmt.Printf("[btc-watcher] save deposit %s: %v\n", tx.Hash[:16], err)
			} else {
				fmt.Printf("[btc-watcher] detected BTC deposit: user=%d tx=%s amount=%d sats\n",
					user.ID, tx.Hash[:16], tx.AmountSats)
			}
		}
	}

	// Update existing pending deposits with new confirmation counts
	w.updatePendingConfirmations(currentHeight)

	// Credit confirmed deposits
	w.creditConfirmed()
	return nil
}

func (w *BTCWatcher) updatePendingConfirmations(currentHeight int64) {
	// BlockCypher gives us confirmation counts directly, so this is handled in scan()
	// This would be used if we needed to poll individual tx statuses
}

func (w *BTCWatcher) creditConfirmed() {
	deps, err := w.db.GetConfirmedDeposits()
	if err != nil {
		return
	}
	for _, dep := range deps {
		if dep.Currency != "BTC" {
			continue
		}
		if err := w.db.CreditDeposit(dep.ID, dep.UserID, dep.Currency, dep.Amount); err != nil {
			fmt.Printf("[btc-watcher] credit deposit %d: %v\n", dep.ID, err)
		} else if dep.Amount > 0 {
			fmt.Printf("[btc-watcher] credited BTC deposit: user=%d amount=%d sats\n",
				dep.UserID, dep.Amount)
		}
	}
}
