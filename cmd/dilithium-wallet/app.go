package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const WalletAppVersion = "4.2.2"

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

// MnemonicResult is returned when creating a wallet with mnemonic
type MnemonicResult struct {
	Mnemonic  string `json:"mnemonic"`
	Address   string `json:"address"`
	Encrypted bool   `json:"encrypted"`
	Error     string `json:"error,omitempty"`
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
	shipName  string
}

// NewApp creates a new App instance
func NewApp() *App {
	return &App{
		wallet:   newWalletService(defaultWalletDir),
		api:      newAPIService("http://localhost:8001"),
		shipName: "USS Dilithium",
	}
}

// SetShipName updates the player's ship name and persists it to disk.
func (a *App) SetShipName(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == "" {
		name = "USS Dilithium"
	}
	a.shipName = name
	a.saveShipName(name)
}

// GetShipName returns the current ship name
func (a *App) GetShipName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.shipName
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Load persisted ship name if one was saved.
	if name := a.loadShipName(); name != "" {
		a.mu.Lock()
		a.shipName = name
		a.mu.Unlock()
	}

	// Discover the best available node using multi-tier resolution.
	go a.resolveNode()
}

func (a *App) loadShipName() string {
	data, err := os.ReadFile(filepath.Join(defaultWalletDir, "shipname"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (a *App) saveShipName(name string) {
	os.MkdirAll(defaultWalletDir, 0700)
	os.WriteFile(filepath.Join(defaultWalletDir, "shipname"), []byte(name), 0644)
}

// getGMAddress returns the Game Master address. It reads from ~/.dilithium/gm/address
// (written by dilithium-gm on first run) so the wallet automatically targets the
// correct address without any manual configuration.
func getGMAddress() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".dilithium", "gm", "address"))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// resolveNode discovers the best reachable node using multi-tier discovery:
// cached nodes → localhost → hardcoded seeds
func (a *App) resolveNode() {
	best := discoverBestNode()
	a.api.setNodeURL(best)
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
		Address:   addressToChecksummed(address),
		Encrypted: passphrase != "",
	}
}

// CreateWalletWithMnemonic creates a new wallet and returns the mnemonic phrase
func (a *App) CreateWalletWithMnemonic(passphrase string) MnemonicResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	mnemonicPhrase, address, err := a.wallet.createFromMnemonic(passphrase)
	if err != nil {
		return MnemonicResult{Error: fmt.Sprintf("ERROR: %v", err)}
	}
	return MnemonicResult{
		Mnemonic:  mnemonicPhrase,
		Address:   addressToChecksummed(address),
		Encrypted: passphrase != "",
	}
}

// RestoreFromMnemonic restores a wallet from a BIP39 mnemonic phrase
func (a *App) RestoreFromMnemonic(mnemonicPhrase, passphrase string) WalletInfo {
	a.mu.Lock()
	defer a.mu.Unlock()

	address, err := a.wallet.restoreFromMnemonic(mnemonicPhrase, passphrase)
	if err != nil {
		return WalletInfo{Address: fmt.Sprintf("ERROR: %v", err)}
	}
	return WalletInfo{
		Address:   addressToChecksummed(address),
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
		Address:   addressToChecksummed(address),
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
func (a *App) SendTransaction(to string, amountDLT string, feeDLT string, data string) TxResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.wallet.isUnlocked() {
		return TxResult{Success: false, Message: "wallet is locked"}
	}

	return a.api.sendTransaction(a.wallet, to, amountDLT, feeDLT, data)
}

// ExecuteGameAction performs a Star Trek themed game action via a transaction
func (a *App) ExecuteGameAction(action string, target string) TxResult {
	a.mu.Lock()
	ship := a.shipName
	unlocked := a.wallet.isUnlocked()
	a.mu.Unlock()

	if !unlocked {
		return TxResult{Success: false, Message: "Your station is locked, Captain. Please unlock your wallet."}
	}

	// Resolve GM address: prefer the address file written by dilithium-gm.
	gmAddress := getGMAddress()
	if gmAddress == "" {
		return TxResult{Success: false, Message: "Game Master not found. Run dilithium-gm first."}
	}

	// Create the command string: ST:[SHIP]:[ACTION]:[TARGET]
	gameData := fmt.Sprintf("ST:%s:%s:%s", ship, action, target)
	
	return a.api.sendTransaction(a.wallet, gmAddress, "0.001", "0.0001", gameData)
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
