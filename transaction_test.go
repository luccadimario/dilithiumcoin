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
	tx := NewTransaction("addr_from", "addr_to", 500)
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
	tx := NewTransaction(w.Address, "recipient", 100)
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
	tx := NewTransaction(w.Address, "recipient", 42)
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
	tx := NewTransaction(w.Address, "recipient", 1000)
	if err := tx.Sign(w); err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("VerifyTransactionSignature failed: %v", err)
	}
}
