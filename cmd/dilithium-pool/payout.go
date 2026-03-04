package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

// LoadPoolWallet loads a wallet from the given directory for payout signing.
func LoadPoolWallet(walletDir, passphrase string) (*PoolWallet, error) {
	// Load address
	address, err := loadAddress(walletDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load address: %w", err)
	}

	// Load private key
	privateKeyPath := filepath.Join(walletDir, "private.pem")
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM format")
	}

	encrypted := block.Type == "DILITHIUM ENCRYPTED PRIVATE KEY"
	var privKeyBytes []byte

	if encrypted {
		if passphrase == "" {
			return nil, fmt.Errorf("wallet is encrypted, passphrase required")
		}
		decrypted, err := decryptKey(block.Bytes, passphrase)
		if err != nil {
			return nil, fmt.Errorf("wrong passphrase: %w", err)
		}
		privKeyBytes = decrypted
	} else {
		privKeyBytes = block.Bytes
	}

	var sk mode3.PrivateKey
	if err := sk.UnmarshalBinary(privKeyBytes); err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	pk := sk.Public().(*mode3.PublicKey)

	return &PoolWallet{
		PrivateKey: &sk,
		PublicKey:  pk,
		Address:    address,
	}, nil
}

func loadAddress(walletDir string) (string, error) {
	// Try reading address file first
	addressPath := filepath.Join(walletDir, "address")
	if data, err := os.ReadFile(addressPath); err == nil {
		return string(data), nil
	}

	// Fall back to deriving from public key
	publicKeyPath := filepath.Join(walletDir, "public.pem")
	keyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return "", fmt.Errorf("no wallet found in %s", walletDir)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return "", fmt.Errorf("invalid PEM format")
	}

	hash := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(hash[:])[:40], nil
}

// --- Encryption functions (compatible with dilithium-wallet) ---

func deriveKey(passphrase string, salt []byte) []byte {
	key := sha256.Sum256(append(salt, []byte(passphrase)...))
	for i := 0; i < 100_000; i++ {
		key = sha256.Sum256(key[:])
	}
	return key[:]
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

// ============================================================================
// Payout Engine
// ============================================================================

// NewPayoutEngine creates a new payout engine
func NewPayoutEngine(pool *Pool, db *Database, wallet *PoolWallet, nodeURL string, minPayout int64, interval time.Duration) *PayoutEngine {
	return &PayoutEngine{
		pool:           pool,
		db:             db,
		wallet:         wallet,
		nodeURL:        nodeURL,
		minPayout:      minPayout,
		payoutInterval: interval,
		stopCh:         make(chan struct{}),
	}
}

// Start begins the payout processing loop
func (pe *PayoutEngine) Start() {
	pe.wg.Add(1)
	go pe.payoutLoop()
}

// Stop shuts down the payout engine
func (pe *PayoutEngine) Stop() {
	close(pe.stopCh)
	pe.wg.Wait()
}

func (pe *PayoutEngine) payoutLoop() {
	defer pe.wg.Done()

	ticker := time.NewTicker(pe.payoutInterval)
	defer ticker.Stop()

	fmt.Printf("Payouts: Engine started (min=%s DLT, interval=%s)\n",
		formatDLT(pe.minPayout), pe.payoutInterval)

	for {
		select {
		case <-pe.stopCh:
			return
		case <-ticker.C:
			pe.processPayouts()
		}
	}
}

func (pe *PayoutEngine) processPayouts() {
	workers := pe.db.GetWorkersForPayout(pe.minPayout)
	if len(workers) == 0 {
		return
	}

	fmt.Printf("Payouts: Processing %d workers with balance >= %s DLT\n",
		len(workers), formatDLT(pe.minPayout))

	for _, w := range workers {
		amount := w.Balance
		fee := MinTransactionFee

		err := pe.sendPayout(w.Address, amount, fee)
		if err != nil {
			fmt.Printf("Payouts: FAILED for %s (%s DLT): %v\n",
				w.Address[:12], formatDLT(amount), err)

			pe.db.RecordPayout(&Payout{
				TxSignature:   "",
				WorkerAddress: w.Address,
				Amount:        amount,
				Fee:           fee,
				Timestamp:     time.Now().Unix(),
				Status:        "failed",
			})
			continue
		}

		fmt.Printf("Payouts: Sent %s DLT to %s\n", formatDLT(amount), w.Address[:12])
	}

	// Save after payouts
	if err := pe.db.Save(); err != nil {
		fmt.Printf("Payouts: Save failed: %v\n", err)
	}
}

func (pe *PayoutEngine) sendPayout(toAddress string, amount int64, fee int64) error {
	// Create signing data
	timestamp := time.Now().Unix()
	txData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d%d",
		pe.wallet.Address, toAddress, amount, fee, timestamp)

	// Sign with pool wallet
	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(pe.wallet.PrivateKey, []byte(txData), sig)
	signatureHex := hex.EncodeToString(sig)

	// Get public key hex
	pubKeyBytes, _ := pe.wallet.PublicKey.MarshalBinary()
	publicKeyHex := hex.EncodeToString(pubKeyBytes)

	// Build transaction
	tx := map[string]interface{}{
		"from":       pe.wallet.Address,
		"to":         toAddress,
		"amount":     amount,
		"fee":        fee,
		"timestamp":  timestamp,
		"signature":  signatureHex,
		"public_key": publicKeyHex,
	}

	// Submit to node
	data, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	resp, err := httpClient.Post(pe.nodeURL+"/transaction", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("node connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response failed: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("invalid response: %s", string(body))
	}

	if !apiResp.Success {
		return fmt.Errorf("node rejected: %s", apiResp.Message)
	}

	// Record successful payout and deduct balance
	pe.db.RecordPayout(&Payout{
		TxSignature:   signatureHex[:64], // truncate for storage
		WorkerAddress: toAddress,
		Amount:        amount,
		Fee:           fee,
		Timestamp:     timestamp,
		Status:        "sent",
	})

	return nil
}
