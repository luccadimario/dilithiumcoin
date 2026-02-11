package main

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func TestGenesisBlock(t *testing.T) {
	t.Parallel()
	calc := GenesisBlock.CalculateHash()
	if calc != GenesisBlock.Hash {
		t.Fatalf("genesis hash mismatch: calculated %s, stored %s", calc, GenesisBlock.Hash)
	}
	if GenesisBlock.Index != 0 {
		t.Fatalf("genesis index = %d, want 0", GenesisBlock.Index)
	}
	if GenesisBlock.PreviousHash != "0" {
		t.Fatalf("genesis previous hash = %q, want %q", GenesisBlock.PreviousHash, "0")
	}
}

func TestNewBlockchain(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	if len(bc.Blocks) != 1 {
		t.Fatalf("new blockchain should have 1 block, got %d", len(bc.Blocks))
	}
	if !bc.IsValid() {
		t.Fatal("new blockchain should be valid")
	}
}

func TestBlockCalculateHash(t *testing.T) {
	t.Parallel()
	b := &Block{
		Index:        1,
		Timestamp:    1000,
		Transactions: []*Transaction{},
		PreviousHash: "abc",
		Nonce:        42,
		Difficulty:   2,
	}
	h1 := b.CalculateHash()
	h2 := b.CalculateHash()
	if h1 != h2 {
		t.Fatalf("CalculateHash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64", len(h1))
	}
}

func TestVerifyTransactionSignature(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100)
	tx.Sign(w)
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("VerifyTransactionSignature failed: %v", err)
	}
}

func TestVerifyTransactionSignatureTampered(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100)
	tx.Sign(w)

	// Tamper with amount
	tx.Amount = 999999
	if err := VerifyTransactionSignature(tx); err == nil {
		t.Fatal("expected error for tampered transaction, got nil")
	}
}

func TestVerifyTransactionSignatureMissingKey(t *testing.T) {
	t.Parallel()
	tx := &Transaction{
		From:      "someone",
		To:        "other",
		Amount:    100,
		Timestamp: 1000,
		Signature: "aabbccdd",
		PublicKey: "",
	}
	if err := VerifyTransactionSignature(tx); err == nil {
		t.Fatal("expected error for missing public key")
	}
}

func TestVerifyAddressMatchesPublicKey(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	pubKeyBytes, _ := w.PublicKey.MarshalBinary()
	pubKeyHex := hex.EncodeToString(pubKeyBytes)
	if err := VerifyAddressMatchesPublicKey(w.Address, pubKeyHex); err != nil {
		t.Fatalf("VerifyAddressMatchesPublicKey failed: %v", err)
	}
}

func TestVerifyAddressMatchesPublicKeyWrong(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	pubKeyBytes, _ := w.PublicKey.MarshalBinary()
	pubKeyHex := hex.EncodeToString(pubKeyBytes)
	if err := VerifyAddressMatchesPublicKey("0000000000000000000000000000000000000000", pubKeyHex); err == nil {
		t.Fatal("expected error for mismatched address")
	}
}

func TestGetBlockReward(t *testing.T) {
	t.Parallel()
	tests := []struct {
		height int64
		want   int64
	}{
		{0, 50 * DLTUnit},
		{250_000, 25 * DLTUnit},
		{500_000, int64(12.5 * float64(DLTUnit))},
		{64 * 250_000, 0},
	}
	for _, tt := range tests {
		got := GetBlockReward(tt.height)
		if got != tt.want {
			t.Errorf("GetBlockReward(%d) = %d, want %d", tt.height, got, tt.want)
		}
	}
}

func TestAddTransactionValidation(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Missing from
	err := bc.AddTransaction(&Transaction{From: "", To: "b", Amount: 1, Signature: "sig"})
	if err == nil {
		t.Error("expected error for missing from")
	}

	// Missing to
	err = bc.AddTransaction(&Transaction{From: "a", To: "", Amount: 1, Signature: "sig"})
	if err == nil {
		t.Error("expected error for missing to")
	}

	// Zero amount
	err = bc.AddTransaction(&Transaction{From: "a", To: "b", Amount: 0, Signature: "sig"})
	if err == nil {
		t.Error("expected error for zero amount")
	}

	// Missing signature
	err = bc.AddTransaction(&Transaction{From: "a", To: "b", Amount: 1, Signature: ""})
	if err == nil {
		t.Error("expected error for missing signature")
	}

	// Bad signature (non-SYSTEM tx with invalid sig)
	err = bc.AddTransaction(&Transaction{
		From: "a", To: "b", Amount: 1,
		Signature: "deadbeef",
		PublicKey: "deadbeef",
	})
	if err == nil {
		t.Error("expected error for bad signature")
	}
}

func TestAddTransactionDuplicate(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Use a SYSTEM tx to bypass signature verification
	tx := &Transaction{
		From:      "SYSTEM",
		To:        "miner",
		Amount:    100,
		Timestamp: 1000,
		Signature: fmt.Sprintf("test-sig-%d", 1),
	}

	added1, err := bc.AddTransactionIfNew(tx)
	if err != nil {
		t.Fatalf("first add error: %v", err)
	}
	if !added1 {
		t.Fatal("first add should return true")
	}

	added2, err := bc.AddTransactionIfNew(tx)
	if err != nil {
		t.Fatalf("second add error: %v", err)
	}
	if added2 {
		t.Fatal("second add should return false (duplicate)")
	}
}

func TestMiningIntegration(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Create wallets
	miner, _ := NewWallet()
	recipient, _ := NewWallet()

	// Mine first block to give miner some coins
	bc.MinePendingTransactions(miner.Address)

	minerBalance := bc.GetBalance(miner.Address)
	if minerBalance != 50*DLTUnit {
		t.Fatalf("miner balance = %d, want %d", minerBalance, 50*DLTUnit)
	}

	// Create and sign a transaction
	sendAmount := int64(10 * DLTUnit)
	tx := NewTransaction(miner.Address, recipient.Address, sendAmount)
	if err := tx.Sign(miner); err != nil {
		t.Fatalf("Sign error: %v", err)
	}

	if err := bc.AddTransaction(tx); err != nil {
		t.Fatalf("AddTransaction error: %v", err)
	}

	// Mine the block containing the transaction
	bc.MinePendingTransactions(miner.Address)

	// Check balances: miner got another block reward minus send, recipient got send
	recipientBalance := bc.GetBalance(recipient.Address)
	if recipientBalance != sendAmount {
		t.Fatalf("recipient balance = %d, want %d", recipientBalance, sendAmount)
	}

	minerExpected := 2*50*DLTUnit - sendAmount // two block rewards minus send
	minerActual := bc.GetBalance(miner.Address)
	if minerActual != minerExpected {
		t.Fatalf("miner balance = %d, want %d", minerActual, minerExpected)
	}
}

func TestChainValidation(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	miner, _ := NewWallet()

	// Mine a few blocks
	bc.MinePendingTransactions(miner.Address)
	bc.MinePendingTransactions(miner.Address)
	bc.MinePendingTransactions(miner.Address)

	if !bc.IsValid() {
		t.Fatal("chain should be valid after mining")
	}

	// Corrupt a block hash
	bc.Blocks[2].Hash = "corrupted_hash"
	if bc.IsValid() {
		t.Fatal("chain should be invalid after corruption")
	}
}
