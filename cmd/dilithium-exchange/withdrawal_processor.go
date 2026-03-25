package main

import (
	"fmt"
	"math/big"
	"sync"
	"time"
)

// WithdrawalProcessor processes queued withdrawals on a ticker.
type WithdrawalProcessor struct {
	db        *Database
	config    *Config
	ethWallet *ETHWallet
	btcWallet *BTCWallet
	dltWallet *DLTWallet
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewWithdrawalProcessor(db *Database, config *Config, eth *ETHWallet, btc *BTCWallet, dlt *DLTWallet) *WithdrawalProcessor {
	return &WithdrawalProcessor{
		db:        db,
		config:    config,
		ethWallet: eth,
		btcWallet: btc,
		dltWallet: dlt,
		stopCh:    make(chan struct{}),
	}
}

func (p *WithdrawalProcessor) Start() {
	p.wg.Add(1)
	go p.processLoop()
}

func (p *WithdrawalProcessor) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

func (p *WithdrawalProcessor) processLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.processPending()
		}
	}
}

func (p *WithdrawalProcessor) processPending() {
	withdrawals, err := p.db.GetPendingWithdrawals()
	if err != nil || len(withdrawals) == 0 {
		return
	}

	for _, w := range withdrawals {
		// Mark as processing first (prevent double-send on crash)
		p.db.UpdateWithdrawalStatus(w.ID, "processing", "")

		var txHash string
		var sendErr error

		switch w.Currency {
		case "ETH":
			// Send from exchange ETH wallet (index 0 = exchange master wallet)
			amount := new(big.Int).SetInt64(w.Amount)
			txHash, sendErr = p.ethWallet.SendETH(0, w.Destination, amount)

		case "BTC":
			txHash, sendErr = p.btcWallet.SendBTC(0, w.Destination, w.Amount)

		case "DLT":
			txHash, sendErr = p.dltWallet.SendDLT(w.Destination, w.Amount, p.config.WithdrawFeeDLT)
		}

		if sendErr != nil {
			fmt.Printf("[withdrawal] failed to send %s withdrawal #%d: %v\n", w.Currency, w.ID, sendErr)
			// Refund user balance and mark failed
			p.db.CreditBalance(w.UserID, w.Currency, w.Amount+w.Fee)
			p.db.UpdateWithdrawalStatus(w.ID, "failed", "")
			continue
		}

		p.db.UpdateWithdrawalStatus(w.ID, "sent", txHash)
		fmt.Printf("[withdrawal] sent %s withdrawal #%d: %s → %s tx=%s\n",
			w.Currency, w.ID, formatAmount(w.Currency, w.Amount), w.Destination[:10], txHash[:16])
	}
}

func formatAmount(currency string, amount int64) string {
	switch currency {
	case "DLT":
		return FormatDLT(amount) + " DLT"
	case "ETH":
		return formatWei(amount) + " ETH"
	case "BTC":
		return formatSats(amount) + " BTC"
	}
	return fmt.Sprintf("%d", amount)
}
