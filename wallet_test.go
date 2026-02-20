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

func TestAddressChecksum(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	checksummed := AddressToChecksummed(w.Address)

	// Should be 48 chars: "dlt1" (4) + address (40) + checksum (4)
	if len(checksummed) != 48 {
		t.Fatalf("expected checksummed address length 48, got %d: %s", len(checksummed), checksummed)
	}
	if checksummed[:4] != "dlt1" {
		t.Fatalf("expected dlt1 prefix, got %s", checksummed[:4])
	}
	// The middle 40 chars should be the raw address
	if checksummed[4:44] != w.Address {
		t.Fatalf("address portion mismatch: got %s, want %s", checksummed[4:44], w.Address)
	}
}

func TestAddressChecksumDeterministic(t *testing.T) {
	t.Parallel()
	addr := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	cs1 := AddressToChecksummed(addr)
	cs2 := AddressToChecksummed(addr)
	if cs1 != cs2 {
		t.Fatalf("checksum not deterministic: %s vs %s", cs1, cs2)
	}
}

func TestAddressChecksumInvalid(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	checksummed := AddressToChecksummed(w.Address)

	// Tamper with checksum
	tampered := checksummed[:44] + "0000"
	_, err := NormalizeAddress(tampered)
	if err == nil {
		t.Fatal("expected error for invalid checksum, got nil")
	}
}

func TestAddressBackwardCompat(t *testing.T) {
	t.Parallel()
	// Old 40-char hex address should still work
	oldAddr := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	normalized, err := NormalizeAddress(oldAddr)
	if err != nil {
		t.Fatalf("NormalizeAddress failed for old format: %v", err)
	}
	if normalized != oldAddr {
		t.Fatalf("NormalizeAddress changed old address: got %s, want %s", normalized, oldAddr)
	}
}

func TestAddressRoundtrip(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()

	// Encode to checksummed
	checksummed := AddressToChecksummed(w.Address)

	// Decode back to raw
	rawAddr, err := NormalizeAddress(checksummed)
	if err != nil {
		t.Fatalf("NormalizeAddress failed: %v", err)
	}
	if rawAddr != w.Address {
		t.Fatalf("roundtrip failed: got %s, want %s", rawAddr, w.Address)
	}

	// Re-encode
	reEncoded := AddressToChecksummed(rawAddr)
	if reEncoded != checksummed {
		t.Fatalf("re-encode mismatch: got %s, want %s", reEncoded, checksummed)
	}
}

func TestChecksummedAddressMethod(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	checksummed := w.ChecksummedAddress()
	if checksummed != AddressToChecksummed(w.Address) {
		t.Fatalf("ChecksummedAddress() mismatch: got %s", checksummed)
	}
}

func TestNormalizeAddressSystem(t *testing.T) {
	t.Parallel()
	normalized, err := NormalizeAddress("SYSTEM")
	if err != nil {
		t.Fatalf("NormalizeAddress failed for SYSTEM: %v", err)
	}
	if normalized != "SYSTEM" {
		t.Fatalf("expected SYSTEM, got %s", normalized)
	}
}
