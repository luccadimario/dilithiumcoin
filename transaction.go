package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DLTUnit is the number of base units per 1 DLT (like satoshis per bitcoin)
const DLTUnit int64 = 100_000_000

// FormatDLT formats a base-unit int64 amount as a human-readable DLT string
func FormatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// ParseDLT parses a human-readable DLT string (e.g. "1.5") into base units
func ParseDLT(s string) (int64, error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ".", 2)

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", s)
	}

	var frac int64
	if len(parts) == 2 {
		fracStr := parts[1]
		// Pad or truncate to 8 decimal places
		if len(fracStr) > 8 {
			fracStr = fracStr[:8]
		}
		for len(fracStr) < 8 {
			fracStr += "0"
		}
		frac, err = strconv.ParseInt(fracStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid fractional amount: %s", s)
		}
	}

	result := whole*DLTUnit + frac
	if whole < 0 {
		result = whole*DLTUnit - frac
	}
	return result, nil
}

// Transaction represents a transaction in the blockchain
type Transaction struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    int64  `json:"amount"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
	PublicKey string `json:"public_key,omitempty"`
}

// NewTransaction creates a new transaction
func NewTransaction(from string, to string, amount int64) *Transaction {
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
	txData := fmt.Sprintf("%s%s%d%d", t.From, t.To, t.Amount, t.Timestamp)

	// Sign the transaction
	signature, err := wallet.SignTransaction(txData)
	if err != nil {
		return err
	}

	t.Signature = signature

	// Attach public key PEM
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(wallet.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %v", err)
	}
	t.PublicKey = string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}))

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
