package main

import (
	"encoding/json"
	"testing"
)

func TestFormatDLT(t *testing.T) {
	t.Parallel()
	tests := []struct {
		amount int64
		want   string
	}{
		{0, "0.00000000"},
		{100_000_000, "1.00000000"},
		{150_000_000, "1.50000000"},
		{50_000_000, "0.50000000"},
		{1, "0.00000001"},
		{-100_000_000, "-1.00000000"},
		{-150_000_000, "-1.50000000"},
	}
	for _, tt := range tests {
		if got := FormatDLT(tt.amount); got != tt.want {
			t.Errorf("FormatDLT(%d) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

func TestParseDLT(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int64
	}{
		{"1.0", 100_000_000},
		{"0.5", 50_000_000},
		{"100", 100 * 100_000_000},
		{"0.00000001", 1},
		{"1.5", 150_000_000},
		{"0", 0},
	}
	for _, tt := range tests {
		got, err := ParseDLT(tt.input)
		if err != nil {
			t.Errorf("ParseDLT(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseDLT(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseDLTInvalid(t *testing.T) {
	t.Parallel()
	invalids := []string{"", "abc", "hello"}
	for _, s := range invalids {
		_, err := ParseDLT(s)
		if err == nil {
			t.Errorf("ParseDLT(%q) expected error, got nil", s)
		}
	}
}

func TestNewTransaction(t *testing.T) {
	t.Parallel()
	tx := NewTransaction("addr_from", "addr_to", 500, 0)
	if tx.From != "addr_from" {
		t.Errorf("From = %q, want %q", tx.From, "addr_from")
	}
	if tx.To != "addr_to" {
		t.Errorf("To = %q, want %q", tx.To, "addr_to")
	}
	if tx.Amount != 500 {
		t.Errorf("Amount = %d, want 500", tx.Amount)
	}
	if tx.Signature != "" {
		t.Errorf("Signature should be empty, got %q", tx.Signature)
	}
	if tx.Timestamp == 0 {
		t.Error("Timestamp should be non-zero")
	}
}

func TestTransactionSign(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100, 0)
	if err := tx.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if tx.Signature == "" {
		t.Fatal("Signature should not be empty after signing")
	}
	if tx.PublicKey == "" {
		t.Fatal("PublicKey should not be empty after signing")
	}
}

func TestTransactionToJSON(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 42, 0)
	tx.Sign(w)

	jsonStr := tx.ToJSON()
	var decoded Transaction
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if decoded.From != tx.From {
		t.Errorf("From mismatch: %q vs %q", decoded.From, tx.From)
	}
	if decoded.To != tx.To {
		t.Errorf("To mismatch: %q vs %q", decoded.To, tx.To)
	}
	if decoded.Amount != tx.Amount {
		t.Errorf("Amount mismatch: %d vs %d", decoded.Amount, tx.Amount)
	}
	if decoded.Signature != tx.Signature {
		t.Error("Signature mismatch after JSON roundtrip")
	}
	if decoded.PublicKey != tx.PublicKey {
		t.Error("PublicKey mismatch after JSON roundtrip")
	}
}

func TestTransactionSignVerifyRoundtrip(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 1000, 0)
	if err := tx.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("VerifyTransactionSignature failed: %v", err)
	}
}

func TestTransactionWithData(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 500, 10000)
	tx.Data = "Hello from Dilithium!"
	if err := tx.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("VerifyTransactionSignature failed for tx with data: %v", err)
	}
}

func TestTransactionWithEmptyData(t *testing.T) {
	// Transactions with empty Data should produce the same signature as before (backward compat)
	t.Parallel()
	w, _ := NewWallet()

	tx1 := NewTransaction(w.Address, "recipient", 500, 10000)
	tx1.Timestamp = 1234567890
	if err := tx1.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}

	tx2 := NewTransaction(w.Address, "recipient", 500, 10000)
	tx2.Timestamp = 1234567890
	tx2.Data = "" // explicitly empty
	if err := tx2.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}

	if tx1.Signature != tx2.Signature {
		t.Error("Transactions with empty Data should produce identical signatures")
	}
}

func TestTransactionDataMaxLength(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()

	// 256 bytes should be fine
	tx := NewTransaction(w.Address, "recipient", 100, 10000)
	tx.Data = string(make([]byte, 256))
	if err := tx.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if err := validateTransaction(tx); err != nil {
		t.Fatalf("validateTransaction should accept 256 byte data: %v", err)
	}

	// 257 bytes should be rejected
	tx2 := NewTransaction(w.Address, "recipient", 100, 10000)
	tx2.Data = string(make([]byte, 257))
	if err := tx2.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if err := validateTransaction(tx2); err == nil {
		t.Fatal("validateTransaction should reject data > 256 bytes")
	}
}

func TestTransactionDataInJSON(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100, 10000)
	tx.Data = "test memo"
	tx.Sign(w)

	jsonStr := tx.ToJSON()
	var decoded Transaction
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if decoded.Data != "test memo" {
		t.Errorf("Data mismatch: got %q, want %q", decoded.Data, "test memo")
	}
}

func TestTransactionDataOmitEmptyJSON(t *testing.T) {
	// Data should be omitted from JSON when empty (omitempty)
	t.Parallel()
	tx := NewTransaction("from", "to", 100, 0)
	jsonStr := tx.ToJSON()
	var raw map[string]interface{}
	json.Unmarshal([]byte(jsonStr), &raw)
	if _, exists := raw["data"]; exists {
		t.Error("empty Data field should be omitted from JSON")
	}
}
