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
