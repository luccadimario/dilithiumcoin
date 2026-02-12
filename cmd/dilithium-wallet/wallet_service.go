package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

type walletService struct {
	walletDir  string
	privateKey *mode3.PrivateKey
	publicKey  *mode3.PublicKey
	address    string
	encrypted  bool
}

func newWalletService(walletDir string) *walletService {
	return &walletService{walletDir: walletDir}
}

// exists checks if a wallet exists on disk
func (ws *walletService) exists() bool {
	privateKeyPath := filepath.Join(ws.walletDir, "private.pem")
	_, err := os.Stat(privateKeyPath)
	return err == nil
}

// create generates a new wallet and saves it to disk
func (ws *walletService) create(passphrase string) (string, error) {
	// Generate CRYSTALS-Dilithium Mode3 key pair
	publicKey, privateKey, err := mode3.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to generate key: %v", err)
	}

	// Create address from public key hash
	pubKeyBytes, _ := publicKey.MarshalBinary()
	hash := sha256.Sum256(pubKeyBytes)
	address := hex.EncodeToString(hash[:])[:40]

	// Create wallet directory
	if err := os.MkdirAll(ws.walletDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create directory: %v", err)
	}

	privKeyBytes, _ := privateKey.MarshalBinary()

	privateKeyPath := filepath.Join(ws.walletDir, "private.pem")
	if passphrase != "" {
		encrypted, err := encryptKey(privKeyBytes, passphrase)
		if err != nil {
			return "", fmt.Errorf("failed to encrypt key: %v", err)
		}
		privateKeyPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "DILITHIUM ENCRYPTED PRIVATE KEY",
			Bytes: encrypted,
		})
		if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
			return "", fmt.Errorf("failed to save private key: %v", err)
		}
		ws.encrypted = true
	} else {
		privateKeyPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "DILITHIUM PRIVATE KEY",
			Bytes: privKeyBytes,
		})
		if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
			return "", fmt.Errorf("failed to save private key: %v", err)
		}
	}

	// Save public key
	publicKeyPath := filepath.Join(ws.walletDir, "public.pem")
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "DILITHIUM PUBLIC KEY",
		Bytes: pubKeyBytes,
	})
	if err := os.WriteFile(publicKeyPath, publicKeyPEM, 0644); err != nil {
		return "", fmt.Errorf("failed to save public key: %v", err)
	}

	// Save address
	addressPath := filepath.Join(ws.walletDir, "address")
	if err := os.WriteFile(addressPath, []byte(address), 0644); err != nil {
		return "", fmt.Errorf("failed to save address: %v", err)
	}

	// Keep keys in memory
	ws.privateKey = privateKey
	ws.publicKey = publicKey
	ws.address = address

	return address, nil
}

// load loads an existing wallet from disk
func (ws *walletService) load(passphrase string) (string, bool, error) {
	// Load address
	address, err := ws.loadAddressFromDisk()
	if err != nil {
		return "", false, fmt.Errorf("failed to load address: %v", err)
	}

	// Load private key
	privateKeyPath := filepath.Join(ws.walletDir, "private.pem")
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", false, fmt.Errorf("failed to read private key: %v", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return "", false, fmt.Errorf("invalid PEM format")
	}

	encrypted := block.Type == "DILITHIUM ENCRYPTED PRIVATE KEY"
	var privKeyBytes []byte

	if encrypted {
		if passphrase == "" {
			return "", true, fmt.Errorf("wallet is encrypted, passphrase required")
		}
		decrypted, err := decryptKey(block.Bytes, passphrase)
		if err != nil {
			return "", true, fmt.Errorf("wrong passphrase")
		}
		privKeyBytes = decrypted
	} else {
		privKeyBytes = block.Bytes
	}

	var sk mode3.PrivateKey
	if err := sk.UnmarshalBinary(privKeyBytes); err != nil {
		return "", encrypted, fmt.Errorf("failed to parse private key: %v", err)
	}

	ws.privateKey = &sk
	ws.publicKey = sk.Public().(*mode3.PublicKey)
	ws.address = address
	ws.encrypted = encrypted

	return address, encrypted, nil
}

// lock clears keys from memory
func (ws *walletService) lock() {
	ws.privateKey = nil
	ws.publicKey = nil
	ws.address = ""
}

// getAddress returns the current address
func (ws *walletService) getAddress() string {
	return ws.address
}

// isUnlocked returns whether keys are loaded in memory
func (ws *walletService) isUnlocked() bool {
	return ws.privateKey != nil
}

// isEncrypted returns whether the wallet file is encrypted
func (ws *walletService) isEncrypted() bool {
	if ws.encrypted {
		return true
	}
	// Check the file on disk
	privateKeyPath := filepath.Join(ws.walletDir, "private.pem")
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(keyData)
	if block == nil {
		return false
	}
	return block.Type == "DILITHIUM ENCRYPTED PRIVATE KEY"
}

// exportPrivateKey returns the private key PEM string
func (ws *walletService) exportPrivateKey() string {
	privateKeyPath := filepath.Join(ws.walletDir, "private.pem")
	data, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "ERROR: " + err.Error()
	}
	return string(data)
}

// loadAddressFromDisk loads the address from the wallet directory
func (ws *walletService) loadAddressFromDisk() (string, error) {
	// Try address file first
	addressPath := filepath.Join(ws.walletDir, "address")
	if data, err := os.ReadFile(addressPath); err == nil {
		return string(data), nil
	}

	// Fall back to deriving from public key
	publicKeyPath := filepath.Join(ws.walletDir, "public.pem")
	keyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return "", fmt.Errorf("no wallet found")
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return "", fmt.Errorf("invalid PEM format")
	}

	hash := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(hash[:])[:40], nil
}

// --- Encryption functions (same as CLI for compatibility) ---

func deriveKey(passphrase string, salt []byte) []byte {
	key := sha256.Sum256(append(salt, []byte(passphrase)...))
	for i := 0; i < 100_000; i++ {
		key = sha256.Sum256(key[:])
	}
	return key[:]
}

func encryptKey(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	result := make([]byte, 0, len(salt)+len(nonce)+len(ciphertext))
	result = append(result, salt...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	return result, nil
}

func decryptKey(data []byte, passphrase string) ([]byte, error) {
	if len(data) < 28 {
		return nil, fmt.Errorf("encrypted data too short")
	}

	salt := data[:16]
	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < 16+nonceSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	nonce := data[16 : 16+nonceSize]
	ciphertext := data[16+nonceSize:]

	return gcm.Open(nil, nonce, ciphertext, nil)
}
