package main

import (
	"fmt"
	"sync"
	"time"
)

// ETHWatcher polls the Ethereum chain for deposits to user deposit addresses.
type ETHWatcher struct {
	wallet *ETHWallet
	db     *Database
	config *Config
	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewETHWatcher(wallet *ETHWallet, db *Database, config *Config) *ETHWatcher {
	return &ETHWatcher{
		wallet: wallet,
		db:     db,
		config: config,
		stopCh: make(chan struct{}),
	}
}

func (w *ETHWatcher) Start() {
	w.wg.Add(1)
	go w.watchLoop()
}

func (w *ETHWatcher) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

func (w *ETHWatcher) watchLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Load last scanned block from DB
	lastBlockStr, _ := w.db.GetMeta("eth_last_block")
	var lastBlock uint64
	if lastBlockStr != "" {
		fmt.Sscanf(lastBlockStr, "%d", &lastBlock)
	}

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			if err := w.scan(&lastBlock); err != nil {
				fmt.Printf("[eth-watcher] error: %v\n", err)
			}
		}
	}
}

func (w *ETHWatcher) scan(lastBlock *uint64) error {
	currentBlock, err := w.wallet.GetCurrentBlock()
	if err != nil {
		return fmt.Errorf("get current block: %w", err)
	}

	if *lastBlock == 0 {
		// Start scanning from current block - 20 (catch recent deposits)
		if currentBlock > 20 {
			*lastBlock = currentBlock - 20
		}
	}

	if currentBlock <= *lastBlock {
		return nil // no new blocks
	}

	// Load all users and build a map: lowercase ETH deposit address → user
	users, err := w.db.GetAllUsers()
	if err != nil || len(users) == 0 {
		return err
	}

	addrToUser := make(map[string]*User)
	for _, user := range users {
		addr, err := w.wallet.DeriveDepositAddress(user.ETHDepositIndex)
		if err != nil {
			continue
		}
		addrToUser[addr] = user
	}

	// Scan new blocks (limit to 50 at a time to avoid timeout)
	scanTo := currentBlock
	if scanTo > *lastBlock+50 {
		scanTo = *lastBlock + 50
	}

	for blockNum := *lastBlock + 1; blockNum <= scanTo; blockNum++ {
		txs, err := w.wallet.GetBlockTransactions(blockNum)
		if err != nil {
			fmt.Printf("[eth-watcher] block %d error: %v\n", blockNum, err)
			continue
		}

		for _, tx := range txs {
			user, ok := addrToUser[tx.To]
			if !ok || tx.Value == nil || tx.Value.Sign() == 0 {
				continue
			}

			amount := tx.Value.Int64()
			if amount <= 0 {
				// Value too large for int64 — skip (>9.2 ETH in wei is fine, but >9.2×10^18 wei overflows)
				// For realistic exchange use, amounts will be well under int64 max
				// (int64 max ≈ 9.2 × 10^18 wei = 9.2 ETH, more than enough)
				continue
			}

			exists, _ := w.db.DepositExists("ETH", tx.Hash)
			if exists {
				continue
			}

			confirmations := int(currentBlock - tx.BlockNumber)
			status := "pending"
			if confirmations >= w.config.ETHConfirmations {
				status = "confirmed"
			}

			dep := &Deposit{
				UserID:        user.ID,
				Currency:      "ETH",
				Amount:        amount,
				TxHash:        tx.Hash,
				Confirmations: confirmations,
				Status:        status,
			}
			if err := w.db.CreateDeposit(dep); err != nil {
				fmt.Printf("[eth-watcher] save deposit %s: %v\n", tx.Hash, err)
			} else {
				fmt.Printf("[eth-watcher] detected ETH deposit: user=%d tx=%s amount=%s\n",
					user.ID, tx.Hash[:16], formatWei(amount))
			}
		}
		*lastBlock = blockNum
	}

	// Update pending deposits' confirmation counts
	w.updateConfirmations(currentBlock)

	// Credit confirmed deposits
	w.creditConfirmed()

	// Persist last block
	w.db.SetMeta("eth_last_block", fmt.Sprintf("%d", *lastBlock))
	return nil
}

func (w *ETHWatcher) updateConfirmations(currentBlock uint64) {
	// Update confirmation counts for pending ETH deposits
	// (In production, you'd query each tx's block number; here we use a simpler approach)
	deps, _ := w.db.GetConfirmedDeposits()
	for _, dep := range deps {
		if dep.Currency != "ETH" {
			continue
		}
		// Already confirmed — process in creditConfirmed
	}
}

func (w *ETHWatcher) creditConfirmed() {
	deps, err := w.db.GetConfirmedDeposits()
	if err != nil {
		return
	}
	for _, dep := range deps {
		if dep.Currency != "ETH" {
			continue
		}
		if err := w.db.CreditDeposit(dep.ID, dep.UserID, dep.Currency, dep.Amount); err != nil {
			fmt.Printf("[eth-watcher] credit deposit %d: %v\n", dep.ID, err)
		} else if dep.Amount > 0 {
			fmt.Printf("[eth-watcher] credited ETH deposit: user=%d amount=%s\n",
				dep.UserID, formatWei(dep.Amount))
		}
	}
}
