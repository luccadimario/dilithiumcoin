package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// Transaction represents a transaction in the blockchain
type Transaction struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Amount    float64   `json:"amount"`
	Timestamp int64     `json:"timestamp"`
	Signature string    `json:"signature"`
}

// NewTransaction creates a new transaction
func NewTransaction(from string, to string, amount float64) *Transaction {
	return &Transaction{
		From:      from,
		To:        to,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
		Signature: "",
	}
}

// Sign signs the transaction with a wallet
func (t *Transaction) Sign(wallet *Wallet) error {
	// Create transaction data string (without signature)
	txData := fmt.Sprintf("%s%s%f%d", t.From, t.To, t.Amount, t.Timestamp)

	// Sign the transaction
	signature, err := wallet.SignTransaction(txData)
	if err != nil {
		return err
	}

	t.Signature = signature
	return nil
}

// IsValid validates the transaction signature
func (t *Transaction) IsValid() bool {
	if t.Signature == "" {
		return false
	}

	return true
}

// ToJSON converts transaction to JSON
func (t *Transaction) ToJSON() string {
	data, _ := json.Marshal(t)
	return string(data)
}

