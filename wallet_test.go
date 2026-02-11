package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNewWallet(t *testing.T) {
	t.Parallel()
	w, err := NewWallet()
	if err != nil {
		t.Fatalf("NewWallet() error: %v", err)
	}
	if w.PrivateKey == nil {
		t.Fatal("PrivateKey is nil")
	}
	if w.PublicKey == nil {
		t.Fatal("PublicKey is nil")
	}
	if len(w.Address) != 40 {
		t.Fatalf("expected address length 40, got %d (%s)", len(w.Address), w.Address)
	}
	for _, c := range w.Address {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("address contains non-hex char: %c", c)
		}
	}
}

func TestWalletAddressDeterministic(t *testing.T) {
	t.Parallel()
	w, err := NewWallet()
	if err != nil {
		t.Fatalf("NewWallet() error: %v", err)
	}
	pubKeyBytes, _ := w.PublicKey.MarshalBinary()
	h := sha256.Sum256(pubKeyBytes)
	derived := hex.EncodeToString(h[:])[:40]
	if derived != w.Address {
		t.Fatalf("address not deterministic: got %s, want %s", derived, w.Address)
	}
}

func TestSignAndVerify(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	data := "hello blockchain"
	sig, err := w.SignTransaction(data)
	if err != nil {
		t.Fatalf("SignTransaction error: %v", err)
	}
	if !VerifySignature(w.PublicKey, data, sig) {
		t.Fatal("VerifySignature returned false for valid signature")
	}
}

func TestVerifyWrongKey(t *testing.T) {
	t.Parallel()
	w1, _ := NewWallet()
	w2, _ := NewWallet()
	data := "test data"
	sig, _ := w1.SignTransaction(data)
	if VerifySignature(w2.PublicKey, data, sig) {
		t.Fatal("VerifySignature should fail with wrong public key")
	}
}

func TestVerifyTamperedData(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	data := "original data"
	sig, _ := w.SignTransaction(data)
	if VerifySignature(w.PublicKey, "tampered data", sig) {
		t.Fatal("VerifySignature should fail with tampered data")
	}
}

func TestVerifyInvalidSignatureHex(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	if VerifySignature(w.PublicKey, "data", "not-valid-hex!") {
		t.Fatal("VerifySignature should return false for invalid hex")
	}
}

func TestMultipleWalletsUniqueAddresses(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		w, err := NewWallet()
		if err != nil {
			t.Fatalf("NewWallet() error on iteration %d: %v", i, err)
		}
		if seen[w.Address] {
			t.Fatalf("duplicate address on iteration %d: %s", i, w.Address)
		}
		seen[w.Address] = true
	}
}
