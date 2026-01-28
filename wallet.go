package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Wallet represents a user's wallet with public/private keys
type Wallet struct {
	Address    string
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
}

// NewWallet creates a new wallet with RSA key pair
func NewWallet() (*Wallet, error) {
	// Generate 2048-bit RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %v", err)
	}

	publicKey := &privateKey.PublicKey

	// Create address from public key hash
	pubKeyBytes := publicKey.N.Bytes()
	hash := sha256.Sum256(pubKeyBytes)
	address := hex.EncodeToString(hash[:])[:16] // First 16 chars of hash

	wallet := &Wallet{
		Address:    address,
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}

	return wallet, nil
}

// SignTransaction signs a transaction with the wallet's private key
func (w *Wallet) SignTransaction(txData string) (string, error) {
	// Hash the transaction data
	hash := sha256.Sum256([]byte(txData))

	// Sign the hash with private key
	signature, err := rsa.SignPKCS1v15(rand.Reader, w.PrivateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Return signature as hex string
	return hex.EncodeToString(signature), nil
}

// VerifySignature verifies a transaction signature
func VerifySignature(publicKey *rsa.PublicKey, txData string, signatureHex string) bool {
	// Decode signature from hex
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}

	// Hash the transaction data
	hash := sha256.Sum256([]byte(txData))

	// Verify the signature
	err = rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], signature)
	return err == nil
}

