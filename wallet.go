package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

// Wallet represents a user's wallet with public/private keys
type Wallet struct {
	Address    string
	PrivateKey *mode3.PrivateKey
	PublicKey  *mode3.PublicKey
}

// NewWallet creates a new wallet with CRYSTALS-Dilithium key pair
func NewWallet() (*Wallet, error) {
	// Generate CRYSTALS-Dilithium Mode3 key pair (192-bit quantum-safe)
	publicKey, privateKey, err := mode3.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Dilithium key: %v", err)
	}

	// Create address from public key hash
	pubKeyBytes, _ := publicKey.MarshalBinary()
	hash := sha256.Sum256(pubKeyBytes)
	address := hex.EncodeToString(hash[:])[:40] // First 40 chars of hash (20 bytes)

	wallet := &Wallet{
		Address:    address,
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}

	return wallet, nil
}

// ChecksummedAddress returns the dlt1-prefixed checksummed address
func (w *Wallet) ChecksummedAddress() string {
	return AddressToChecksummed(w.Address)
}

// AddressToChecksummed converts a raw 40-char hex address to dlt1-prefixed checksummed format.
// Format: "dlt1" + 40-char hex + 4-char checksum = 48 chars total
func AddressToChecksummed(rawHex string) string {
	checksum := computeAddressChecksum(rawHex)
	return "dlt1" + rawHex + checksum
}

// computeAddressChecksum returns the first 4 hex chars of SHA256("dlt1" + address_hex)
func computeAddressChecksum(rawHex string) string {
	hash := sha256.Sum256([]byte("dlt1" + rawHex))
	return hex.EncodeToString(hash[:])[:4]
}

// NormalizeAddress accepts both old (40-char hex) and new (dlt1-prefixed 48-char) formats.
// Returns the raw 40-char hex address. For dlt1-prefixed addresses, validates the checksum.
// Other formats are passed through unchanged for backward compatibility.
func NormalizeAddress(address string) (string, error) {
	// New dlt1-prefixed checksummed format
	if len(address) == 48 && address[:4] == "dlt1" {
		rawHex := address[4:44]
		checksum := address[44:48]
		expected := computeAddressChecksum(rawHex)
		if checksum != expected {
			return "", fmt.Errorf("invalid address checksum: expected %s, got %s", expected, checksum)
		}
		return rawHex, nil
	}
	// Standard 40-char hex address — pass through
	if len(address) == 40 {
		return address, nil
	}
	// Other addresses (SYSTEM, legacy, etc.) — pass through
	return address, nil
}

// SignTransaction signs a transaction with the wallet's private key
func (w *Wallet) SignTransaction(txData string) (string, error) {
	// Dilithium signs the raw message directly (no separate hashing needed)
	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(w.PrivateKey, []byte(txData), sig)

	// Return signature as hex string
	return hex.EncodeToString(sig), nil
}

// VerifySignature verifies a transaction signature
func VerifySignature(publicKey *mode3.PublicKey, txData string, signatureHex string) bool {
	// Decode signature from hex
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}

	// Verify the signature
	return mode3.Verify(publicKey, []byte(txData), signature)
}
