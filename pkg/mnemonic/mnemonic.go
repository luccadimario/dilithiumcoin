package mnemonic

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"io"
	"strings"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

const (
	// hkdfSalt is the domain-separation salt for HKDF key derivation.
	// Changing this would derive different keys from the same mnemonic.
	hkdfSalt = "dilithium-v1-keypair"
)

// Generate creates a new 24-word BIP39 mnemonic (256-bit entropy).
func Generate() (string, error) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return "", fmt.Errorf("failed to generate entropy: %w", err)
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("failed to generate mnemonic: %w", err)
	}
	return mnemonic, nil
}

// Validate checks whether the given mnemonic is a valid BIP39 phrase.
func Validate(mnemonic string) bool {
	return bip39.IsMnemonicValid(mnemonic)
}

// GenerateKeyFromMnemonic deterministically derives a CRYSTALS-Dilithium
// Mode3 key pair from a BIP39 mnemonic phrase.
//
// Derivation path:
//
//	mnemonic → BIP39 seed (PBKDF2-HMAC-SHA512, 2048 rounds)
//	        → HKDF-SHA512 with salt "dilithium-v1-keypair"
//	        → deterministic io.Reader
//	        → mode3.GenerateKey(reader)
func GenerateKeyFromMnemonic(mnemonic string) (*mode3.PublicKey, *mode3.PrivateKey, error) {
	mnemonic = strings.TrimSpace(mnemonic)
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, nil, fmt.Errorf("invalid mnemonic phrase")
	}

	// BIP39: mnemonic → 64-byte seed using PBKDF2 with passphrase ""
	seed := pbkdf2.Key([]byte(mnemonic), []byte("mnemonic"), 2048, 64, sha512.New)

	// HKDF-SHA512: seed → deterministic byte stream
	reader := hkdf.New(sha256.New, seed, []byte(hkdfSalt), nil)

	// Generate Dilithium key pair deterministically
	pubKey, privKey, err := mode3.GenerateKey(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key from mnemonic: %w", err)
	}

	return pubKey, privKey, nil
}

// DeriveReader returns a deterministic io.Reader from a mnemonic.
// This is exposed for testing that the same mnemonic always produces
// the same key pair.
func DeriveReader(mnemonic string) (io.Reader, error) {
	mnemonic = strings.TrimSpace(mnemonic)
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid mnemonic phrase")
	}

	seed := pbkdf2.Key([]byte(mnemonic), []byte("mnemonic"), 2048, 64, sha512.New)
	reader := hkdf.New(sha256.New, seed, []byte(hkdfSalt), nil)
	return reader, nil
}
