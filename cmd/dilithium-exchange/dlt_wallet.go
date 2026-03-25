package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

const (
	DLTUnit    int64 = 100_000_000
	DefaultFee int64 = 10_000 // 0.0001 DLT
)

// DLTWallet holds the exchange's Dilithium hot wallet.
type DLTWallet struct {
	address    string
	privateKey *mode3.PrivateKey
	publicKey  *mode3.PublicKey
	nodeURL    string
}

// NewDLTWallet loads the exchange's DLT wallet from walletDir.
func NewDLTWallet(walletDir, nodeURL string) (*DLTWallet, error) {
	// Load address
	addrData, err := os.ReadFile(filepath.Join(walletDir, "address"))
	if err != nil {
		return nil, fmt.Errorf("read address: %w", err)
	}
	address := strings.TrimSpace(string(addrData))

	// Load private key (unencrypted only for exchange hot wallet)
	keyData, err := os.ReadFile(filepath.Join(walletDir, "private.pem"))
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in private key file")
	}
	if block.Type == "DILITHIUM ENCRYPTED PRIVATE KEY" {
		return nil, fmt.Errorf("exchange wallet must be unencrypted (remove passphrase first)")
	}

	var sk mode3.PrivateKey
	if err := sk.UnmarshalBinary(block.Bytes); err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	pk := sk.Public().(*mode3.PublicKey)
	return &DLTWallet{
		address:    address,
		privateKey: &sk,
		publicKey:  pk,
		nodeURL:    strings.TrimRight(nodeURL, "/"),
	}, nil
}

func (w *DLTWallet) Address() string { return w.address }

// GetBalance returns the exchange DLT wallet balance in base units.
func (w *DLTWallet) GetBalance() (int64, error) {
	info, err := w.GetAddressInfo(w.address)
	if err != nil {
		return 0, err
	}
	return info.Balance, nil
}

type dltAddrInfo struct {
	Balance          int64       `json:"balance"`
	TotalReceived    int64       `json:"total_received"`
	TotalSent        int64       `json:"total_sent"`
	TransactionCount int         `json:"transaction_count"`
	Transactions     []DLTTxInfo `json:"transactions"`
}

// DLTTxInfo is a transaction from the node's /explorer/address response.
type DLTTxInfo struct {
	Signature  string `json:"signature"`
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     int64  `json:"amount"`
	Fee        int64  `json:"fee"`
	Timestamp  int64  `json:"timestamp"`
	BlockIndex int64  `json:"block_index"`
}

type nodeResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// GetAddressInfo fetches address info from the Dilithium node.
func (w *DLTWallet) GetAddressInfo(addr string) (*dltAddrInfo, error) {
	resp, err := http.Get(w.nodeURL + "/explorer/address?addr=" + addr)
	if err != nil {
		return nil, fmt.Errorf("node request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var nr nodeResponse
	if err := json.Unmarshal(body, &nr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !nr.Success {
		return nil, fmt.Errorf("node error: %s", nr.Message)
	}

	var info dltAddrInfo
	if err := json.Unmarshal(nr.Data, &info); err != nil {
		return nil, fmt.Errorf("parse address data: %w", err)
	}
	return &info, nil
}

// SendDLT signs and submits a DLT transaction to the node.
// Returns the hex-encoded signature (used as tx ID).
func (w *DLTWallet) SendDLT(toAddr string, amount, fee int64) (string, error) {
	if fee <= 0 {
		fee = DefaultFee
	}
	timestamp := time.Now().Unix()

	// Build signing data — matches format in cmd/dilithium-cli/transaction.go
	txData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d%d", w.address, toAddr, amount, fee, timestamp)

	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(w.privateKey, []byte(txData), sig)
	sigHex := hex.EncodeToString(sig)

	pk := w.privateKey.Public().(*mode3.PublicKey)
	pubBytes, _ := pk.MarshalBinary()

	tx := map[string]interface{}{
		"from":       w.address,
		"to":         toAddr,
		"amount":     amount,
		"fee":        fee,
		"timestamp":  timestamp,
		"signature":  sigHex,
		"public_key": hex.EncodeToString(pubBytes),
	}

	payload, _ := json.Marshal(tx)
	resp, err := http.Post(w.nodeURL+"/transaction", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		return "", fmt.Errorf("submit transaction: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var nr nodeResponse
	json.Unmarshal(body, &nr)
	if !nr.Success {
		return "", fmt.Errorf("node rejected tx: %s", nr.Message)
	}
	return sigHex, nil
}

// FormatDLT converts base units to human-readable string.
func FormatDLT(amount int64) string {
	whole := amount / DLTUnit
	frac := amount % DLTUnit
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// VerifyDLTOwnership checks that a Dilithium signature over a challenge was made
// by the private key corresponding to dltAddr. This proves ownership.
//
// challenge format: "link-dlt:{dltAddr}:{ethAddr}:{nonce}"
// The user signs this with: dilithium-cli tx sign --to exchange-addr --amount 0
// (or we provide a dedicated signing UI in the wallet app)
func VerifyDLTOwnership(dltAddr, challenge, sigHex, pubKeyHex string) (bool, error) {
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}
	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return false, fmt.Errorf("decode public key: %w", err)
	}

	var pk mode3.PublicKey
	if err := pk.UnmarshalBinary(pubKeyBytes); err != nil {
		return false, fmt.Errorf("parse public key: %w", err)
	}

	// Verify the public key matches the claimed DLT address
	hash := sha256.Sum256(pubKeyBytes)
	derivedAddr := hex.EncodeToString(hash[:])[:40]
	if !strings.EqualFold(derivedAddr, dltAddr) {
		return false, fmt.Errorf("public key does not match DLT address")
	}

	// Verify the signature
	valid := mode3.Verify(&pk, []byte(challenge), sigBytes)
	return valid, nil
}
