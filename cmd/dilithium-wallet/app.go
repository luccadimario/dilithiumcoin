package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const WalletAppVersion = "1.1.0"

// Default paths
var (
	defaultDataDir   string
	defaultWalletDir string
)

func init() {
	home, _ := os.UserHomeDir()
	defaultDataDir = filepath.Join(home, ".dilithium")
	defaultWalletDir = filepath.Join(defaultDataDir, "wallet")
}

// WalletInfo is returned after wallet creation/loading
type WalletInfo struct {
	Address   string `json:"address"`
	Encrypted bool   `json:"encrypted"`
}

// BalanceInfo holds balance data from the node
type BalanceInfo struct {
	Address          string `json:"address"`
	BalanceDLT       string `json:"balance_dlt"`
	TotalReceivedDLT string `json:"total_received_dlt"`
	TotalSentDLT     string `json:"total_sent_dlt"`
	TxCount          int    `json:"tx_count"`
	Error            string `json:"error,omitempty"`
}

// TxResult holds the result of a submitted transaction
type TxResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// TransactionInfo holds a single transaction for display
type TransactionInfo struct {
	From      string `json:"from"`
	To        string `json:"to"`
	AmountDLT string `json:"amount_dlt"`
	Timestamp int64  `json:"timestamp"`
	Direction string `json:"direction"` // "sent" or "received"
}

// NodeStatus holds node connection test results
type NodeStatus struct {
	Connected       bool   `json:"connected"`
	Version         string `json:"version"`
	BlockHeight     int    `json:"block_height"`
	PendingTxs      int    `json:"pending_txs"`
	Difficulty      int    `json:"difficulty"`
	PeerCount       int    `json:"peer_count"`
	Error           string `json:"error,omitempty"`
}

// App is the main application struct bound to Wails
type App struct {
	ctx       context.Context
	wallet    *walletService
	api       *apiService
	mu        sync.Mutex
}

// NewApp creates a new App instance
func NewApp() *App {
	return &App{
		wallet: newWalletService(defaultWalletDir),
		api:    newAPIService(SeedNodeAPI),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Try local node first — if reachable, use it (faster + supports tx submission)
	go a.resolveNode()
}

// resolveNode checks if a local node is running and switches to it if so
func (a *App) resolveNode() {
	localURL := "http://localhost:8001"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(localURL + "/status")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			a.api.setNodeURL(localURL)
		}
	}
}

// --- Wallet lifecycle ---

// HasWallet checks if a wallet exists on disk
func (a *App) HasWallet() bool {
	return a.wallet.exists()
}

// CreateWallet creates a new wallet with the given passphrase
func (a *App) CreateWallet(passphrase string) WalletInfo {
	a.mu.Lock()
	defer a.mu.Unlock()

	address, err := a.wallet.create(passphrase)
	if err != nil {
		return WalletInfo{Address: fmt.Sprintf("ERROR: %v", err)}
	}
	return WalletInfo{
		Address:   address,
		Encrypted: passphrase != "",
	}
}

// LoadWallet loads an existing wallet with the given passphrase
func (a *App) LoadWallet(passphrase string) WalletInfo {
	a.mu.Lock()
	defer a.mu.Unlock()

	address, encrypted, err := a.wallet.load(passphrase)
	if err != nil {
		return WalletInfo{Address: fmt.Sprintf("ERROR: %v", err)}
	}
	return WalletInfo{
		Address:   address,
		Encrypted: encrypted,
	}
}

// LockWallet clears keys from memory
func (a *App) LockWallet() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wallet.lock()
}

// ExportPrivateKey exports the private key PEM
func (a *App) ExportPrivateKey() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wallet.exportPrivateKey()
}

// GetAddress returns the current wallet address
func (a *App) GetAddress() string {
	return a.wallet.getAddress()
}

// IsUnlocked returns whether the wallet is currently unlocked
func (a *App) IsUnlocked() bool {
	return a.wallet.isUnlocked()
}

// IsEncrypted returns whether the wallet file is encrypted
func (a *App) IsEncrypted() bool {
	return a.wallet.isEncrypted()
}

// --- Balance & transactions ---

// GetBalance fetches balance from the node
func (a *App) GetBalance() BalanceInfo {
	address := a.wallet.getAddress()
	if address == "" {
		return BalanceInfo{Error: "wallet not loaded"}
	}
	return a.api.getBalance(address)
}

// SendTransaction signs and submits a transaction
func (a *App) SendTransaction(to string, amountDLT string) TxResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.wallet.isUnlocked() {
		return TxResult{Success: false, Message: "wallet is locked"}
	}

	return a.api.sendTransaction(a.wallet, to, amountDLT)
}

// GetTransactionHistory gets transaction history from the node
func (a *App) GetTransactionHistory() []TransactionInfo {
	address := a.wallet.getAddress()
	if address == "" {
		return nil
	}
	return a.api.getTransactionHistory(address)
}

// --- Node connection ---

// GetNodeURL returns the current node URL
func (a *App) GetNodeURL() string {
	return a.api.getNodeURL()
}

// SetNodeURL sets the node URL
func (a *App) SetNodeURL(url string) {
	a.api.setNodeURL(url)
}

// TestConnection tests the connection to the node
func (a *App) TestConnection() NodeStatus {
	return a.api.testConnection()
}

// GetVersion returns the wallet app version
func (a *App) GetVersion() string {
	return WalletAppVersion
}
