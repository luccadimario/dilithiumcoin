package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
)

const (
	NodeAPI      = "http://localhost:8001"
	PollInterval = 10 * time.Second
	RewardAmount = 5_00000000 // 5.0 DILT
	RewardFee    = 10000      // 0.0001 DLT
)

var gmDataDir string

func init() {
	rand.Seed(time.Now().UnixNano())
	home, _ := os.UserHomeDir()
	gmDataDir = filepath.Join(home, ".dilithium", "gm")
}

// WorldState holds the persistent game world.
type WorldState struct {
	DilithiumSectors map[string]bool `json:"dilithium_sectors"`
}

func loadWorldState() WorldState {
	path := filepath.Join(gmDataDir, "world.json")
	data, err := os.ReadFile(path)
	if err != nil {
		// First run: seed 10 random dilithium deposits.
		ws := WorldState{DilithiumSectors: make(map[string]bool)}
		for i := 0; i < 10; i++ {
			x, y := rand.Intn(10), rand.Intn(10)
			ws.DilithiumSectors[fmt.Sprintf("%d,%d", x, y)] = true
		}
		saveWorldState(ws)
		return ws
	}
	var ws WorldState
	if err := json.Unmarshal(data, &ws); err != nil || ws.DilithiumSectors == nil {
		ws.DilithiumSectors = make(map[string]bool)
	}
	return ws
}

func saveWorldState(ws WorldState) {
	os.MkdirAll(gmDataDir, 0700)
	data, _ := json.MarshalIndent(ws, "", "  ")
	os.WriteFile(filepath.Join(gmDataDir, "world.json"), data, 0600)
}

// loadProcessedTxs loads the set of already-handled tx signatures from disk
// so that replays are blocked across restarts.
func loadProcessedTxs() map[string]bool {
	path := filepath.Join(gmDataDir, "processed.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]bool)
	}
	var txs map[string]bool
	if err := json.Unmarshal(data, &txs); err != nil || txs == nil {
		return make(map[string]bool)
	}
	return txs
}

func saveProcessedTxs(txs map[string]bool) {
	os.MkdirAll(gmDataDir, 0700)
	data, _ := json.Marshal(txs)
	os.WriteFile(filepath.Join(gmDataDir, "processed.json"), data, 0600)
}

func main() {
	fmt.Println("Starting Dilithium Frontier Game Master...")

	sk, gmAddress := getGMKeys()
	pk := sk.Public().(*mode3.PublicKey)

	fmt.Printf("GM Address : %s\n", gmAddress)
	fmt.Println("Fund this address to enable player rewards.")

	state := loadWorldState()
	processedTxs := loadProcessedTxs()

	fmt.Printf("World state: %d dilithium deposits active.\n", len(state.DilithiumSectors))

	for {
		txs := fetchRecentTransactions(gmAddress)
		changed := false
		for _, tx := range txs {
			if processedTxs[tx.Signature] {
				continue
			}
			if strings.HasPrefix(tx.Data, "ST:") {
				handleAction(tx, sk, *pk, gmAddress, &state)
				changed = true
			}
			processedTxs[tx.Signature] = true
		}
		if changed {
			saveProcessedTxs(processedTxs)
			saveWorldState(state)
		}

		time.Sleep(PollInterval)

		// ~5% chance each tick to respawn one resource.
		if rand.Intn(100) < 5 {
			respawnResources(&state)
			saveWorldState(state)
		}
	}
}

// Transaction is a single entry from the node's address history.
type Transaction struct {
	From      string `json:"from"`
	Data      string `json:"data"`
	Signature string `json:"signature"`
	Amount    int64  `json:"amount"`
}

func fetchRecentTransactions(gmAddress string) []Transaction {
	resp, err := http.Get(NodeAPI + "/explorer/address?addr=" + gmAddress)
	if err != nil {
		fmt.Printf("[WARN] Could not reach node: %v\n", err)
		return nil
	}
	defer resp.Body.Close()

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Transactions []Transaction `json:"transactions"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&apiResp)
	return apiResp.Data.Transactions
}

func handleAction(tx Transaction, sk *mode3.PrivateKey, pk mode3.PublicKey, gmAddress string, state *WorldState) {
	// Command format: ST:[SHIP]:[ACTION]:[TARGET]
	parts := strings.Split(tx.Data, ":")
	if len(parts) < 4 {
		return
	}

	ship := parts[1]
	action := parts[2]
	target := parts[3]

	fmt.Printf("[GAME] %s (%s) → %s @ %s\n", ship, tx.From, action, target)

	if action == "SCAN" {
		if state.DilithiumSectors[target] {
			fmt.Printf("[REWARD] Dilithium found at %s by %s! Sending 5 DILT.\n", target, tx.From)
			sendReward(tx.From, gmAddress, sk, pk)
			delete(state.DilithiumSectors, target)
		} else {
			fmt.Printf("[GAME] No dilithium at %s.\n", target)
		}
	}
}

func sendReward(to, fromAddress string, sk *mode3.PrivateKey, pk mode3.PublicKey) {
	timestamp := time.Now().Unix()
	txSignData := fmt.Sprintf("dilithium-mainnet:%s%s%d%d%d", fromAddress, to, RewardAmount, RewardFee, timestamp)

	sig := make([]byte, mode3.SignatureSize)
	mode3.SignTo(*sk, []byte(txSignData), sig)

	pubKeyBytes, _ := pk.MarshalBinary()

	rewardTx := map[string]interface{}{
		"from":       fromAddress,
		"to":         to,
		"amount":     RewardAmount,
		"fee":        RewardFee,
		"timestamp":  timestamp,
		"signature":  hex.EncodeToString(sig),
		"public_key": hex.EncodeToString(pubKeyBytes),
	}

	body, _ := json.Marshal(rewardTx)
	resp, err := http.Post(NodeAPI+"/transaction", "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("[ERROR] Failed to send reward to %s: %v\n", to, err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("[REWARD] 5 DILT sent to %s\n", to)
}

// getGMKeys loads the persistent GM keypair from disk, generating it on first run.
// The derived address is saved to ~/.dilithium/gm/address so the wallet can read it.
func getGMKeys() (*mode3.PrivateKey, string) {
	os.MkdirAll(gmDataDir, 0700)
	keyPath := filepath.Join(gmDataDir, "private.pem")
	addrPath := filepath.Join(gmDataDir, "address")

	// Try loading existing keypair.
	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			var sk mode3.PrivateKey
			if err := sk.UnmarshalBinary(block.Bytes); err == nil {
				addrBytes, _ := os.ReadFile(addrPath)
				return &sk, strings.TrimSpace(string(addrBytes))
			}
		}
	}

	// First run: generate a new keypair.
	fmt.Println("No GM keypair found — generating new one...")
	pk, sk, err := mode3.GenerateKey(cryptorand.Reader)
	if err != nil {
		fmt.Printf("FATAL: failed to generate GM keypair: %v\n", err)
		os.Exit(1)
	}

	// Derive checksummed address (same algorithm as the wallet).
	pubKeyBytes, _ := pk.MarshalBinary()
	hash := sha256.Sum256(pubKeyBytes)
	rawHex := hex.EncodeToString(hash[:])[:40]
	checksumHash := sha256.Sum256([]byte("dlt1" + rawHex))
	address := "dlt1" + rawHex + hex.EncodeToString(checksumHash[:])[:4]

	// Save private key PEM.
	privKeyBytes, _ := sk.MarshalBinary()
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "DILITHIUM PRIVATE KEY", Bytes: privKeyBytes})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		fmt.Printf("FATAL: failed to save GM private key: %v\n", err)
		os.Exit(1)
	}

	// Save address so the wallet can pick it up automatically.
	os.WriteFile(addrPath, []byte(address), 0644)

	fmt.Printf("GM keypair saved to %s\n", gmDataDir)
	return sk, address
}

func respawnResources(state *WorldState) {
	x, y := rand.Intn(10), rand.Intn(10)
	key := fmt.Sprintf("%d,%d", x, y)
	state.DilithiumSectors[key] = true
	fmt.Printf("[WORLD] Dilithium anomaly detected at sector %s.\n", key)
}
