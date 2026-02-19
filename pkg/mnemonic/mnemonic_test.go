package mnemonic

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestDeterministic(t *testing.T) {
	m, err := Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !Validate(m) {
		t.Fatal("Generated mnemonic is not valid")
	}

	pub1, priv1, err := GenerateKeyFromMnemonic(m)
	if err != nil {
		t.Fatalf("First derivation failed: %v", err)
	}
	pub2, priv2, err := GenerateKeyFromMnemonic(m)
	if err != nil {
		t.Fatalf("Second derivation failed: %v", err)
	}

	b1, _ := pub1.MarshalBinary()
	b2, _ := pub2.MarshalBinary()
	h1 := sha256.Sum256(b1)
	h2 := sha256.Sum256(b2)
	addr1 := hex.EncodeToString(h1[:])[:40]
	addr2 := hex.EncodeToString(h2[:])[:40]

	if addr1 != addr2 {
		t.Fatalf("Addresses differ: %s vs %s", addr1, addr2)
	}

	pk1, _ := priv1.MarshalBinary()
	pk2, _ := priv2.MarshalBinary()
	if hex.EncodeToString(pk1) != hex.EncodeToString(pk2) {
		t.Fatal("Private keys differ")
	}

	t.Logf("Mnemonic: %s", m)
	t.Logf("Address: %s", addr1)
	t.Logf("Deterministic: OK")
}

func TestInvalidMnemonic(t *testing.T) {
	if Validate("not a valid mnemonic phrase") {
		t.Fatal("Expected invalid mnemonic to fail validation")
	}

	_, _, err := GenerateKeyFromMnemonic("not a valid mnemonic phrase")
	if err == nil {
		t.Fatal("Expected error for invalid mnemonic")
	}
}
