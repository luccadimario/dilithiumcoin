package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	bip32 "github.com/tyler-smith/go-bip32"
	bip39 "github.com/tyler-smith/go-bip39"
)

// BTCWallet manages Bitcoin HD wallet operations for the exchange.
type BTCWallet struct {
	masterKey   *bip32.Key
	chainParams *chaincfg.Params
	apiBase     string // BlockCypher base URL
	httpClient  *http.Client
}

// NewBTCWallet creates a BTC wallet from a BIP39 mnemonic.
// Uses BIP44 path m/44'/0'/0'/0/{index}.
func NewBTCWallet(mnemonic, apiBase string) (*BTCWallet, error) {
	seed := bip39.NewSeed(mnemonic, "")
	masterKey, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("derive master key: %w", err)
	}

	return &BTCWallet{
		masterKey:   masterKey,
		chainParams: &chaincfg.MainNetParams,
		apiBase:     strings.TrimRight(apiBase, "/"),
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// derivePath derives the key at BIP44 path m/44'/0'/0'/0/{index}.
func (w *BTCWallet) derivePath(index uint32) (*bip32.Key, error) {
	const hardened = bip32.FirstHardenedChild
	purpose, err := w.masterKey.NewChildKey(hardened + 44)
	if err != nil {
		return nil, err
	}
	coinType, err := purpose.NewChildKey(hardened + 0) // BTC = 0
	if err != nil {
		return nil, err
	}
	account, err := coinType.NewChildKey(hardened + 0)
	if err != nil {
		return nil, err
	}
	change, err := account.NewChildKey(0) // external chain
	if err != nil {
		return nil, err
	}
	return change.NewChildKey(index)
}

// DeriveDepositAddress returns the P2PKH Bitcoin address at the given user index.
func (w *BTCWallet) DeriveDepositAddress(index int) (string, error) {
	child, err := w.derivePath(uint32(index))
	if err != nil {
		return "", fmt.Errorf("derive key at index %d: %w", index, err)
	}

	// child.PublicKey().Key is 33 bytes (compressed secp256k1 public key)
	pubKey, err := btcec.ParsePubKey(child.PublicKey().Key)
	if err != nil {
		return "", fmt.Errorf("parse public key: %w", err)
	}

	// P2PKH address from compressed public key hash
	pkHash := btcutil.Hash160(pubKey.SerializeCompressed())
	addr, err := btcutil.NewAddressPubKeyHash(pkHash, w.chainParams)
	if err != nil {
		return "", fmt.Errorf("build address: %w", err)
	}
	return addr.EncodeAddress(), nil
}

// --- BlockCypher API types ---

type bcAddressResponse struct {
	Address     string   `json:"address"`
	Balance     int64    `json:"balance"`
	TotalReceived int64  `json:"total_received"`
	TotalSent   int64    `json:"total_sent"`
	Txrefs      []bcTxRef `json:"txrefs"`
	UnconfirmedTxrefs []bcTxRef `json:"unconfirmed_txrefs"`
}

type bcTxRef struct {
	TxHash        string `json:"tx_hash"`
	BlockHeight   int64  `json:"block_height"`
	Confirmations int    `json:"confirmations"`
	Value         int64  `json:"value"`    // satoshis
	Spent         bool   `json:"spent"`
	Received      string `json:"received"` // ISO8601
}

type bcTxResponse struct {
	Hash          string    `json:"hash"`
	BlockHeight   int64     `json:"block_height"`
	Confirmations int       `json:"confirmations"`
	Outputs       []bcTxOutput `json:"outputs"`
}

type bcTxOutput struct {
	Value     int64    `json:"value"`
	Addresses []string `json:"addresses"`
}

// BTCTx is a simplified BTC transaction for deposit detection.
type BTCTx struct {
	Hash          string
	ToAddress     string
	AmountSats    int64
	Confirmations int
	BlockHeight   int64
}

// GetAddressTxs returns recent transactions for a BTC address via BlockCypher.
func (w *BTCWallet) GetAddressTxs(addr string) ([]BTCTx, error) {
	url := fmt.Sprintf("%s/addrs/%s?limit=50&unspentOnly=true&includeScript=false", w.apiBase, addr)
	resp, err := w.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("blockcypher request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil // no transactions yet
	}

	body, _ := io.ReadAll(resp.Body)
	var bcResp bcAddressResponse
	if err := json.Unmarshal(body, &bcResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var txs []BTCTx
	for _, ref := range bcResp.Txrefs {
		if ref.Value > 0 && !ref.Spent {
			txs = append(txs, BTCTx{
				Hash:          ref.TxHash,
				ToAddress:     addr,
				AmountSats:    ref.Value,
				Confirmations: ref.Confirmations,
				BlockHeight:   ref.BlockHeight,
			})
		}
	}
	for _, ref := range bcResp.UnconfirmedTxrefs {
		if ref.Value > 0 && !ref.Spent {
			txs = append(txs, BTCTx{
				Hash:          ref.TxHash,
				ToAddress:     addr,
				AmountSats:    ref.Value,
				Confirmations: 0,
			})
		}
	}
	return txs, nil
}

// GetCurrentBlockHeight returns the current BTC block height via BlockCypher.
func (w *BTCWallet) GetCurrentBlockHeight() (int64, error) {
	url := w.apiBase // GET /v1/btc/main returns chain info
	resp, err := w.httpClient.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var chain struct {
		Height int64 `json:"height"`
	}
	json.Unmarshal(body, &chain)
	return chain.Height, nil
}

// SendBTC broadcasts a BTC withdrawal via BlockCypher's transaction API.
// This uses BlockCypher's "newtx" → "sendtx" flow (for small amounts only;
// for production, use a full Bitcoin node instead).
func (w *BTCWallet) SendBTC(fromIndex int, toAddr string, satoshis int64) (string, error) {
	// Get sender address
	fromAddr, err := w.DeriveDepositAddress(fromIndex)
	if err != nil {
		return "", err
	}

	// Step 1: Build unsigned tx
	newTxReq := map[string]interface{}{
		"inputs":  []map[string]interface{}{{"addresses": []string{fromAddr}}},
		"outputs": []map[string]interface{}{{"addresses": []string{toAddr}, "value": satoshis}},
	}
	reqBody, _ := json.Marshal(newTxReq)
	resp, err := w.httpClient.Post(w.apiBase+"/txs/new", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("newtx: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var newTxResp struct {
		Tx          map[string]interface{} `json:"tx"`
		ToSign      []string               `json:"tosign"`
		Errors      []struct{ Error string } `json:"errors"`
	}
	json.Unmarshal(body, &newTxResp)
	if len(newTxResp.Errors) > 0 {
		return "", fmt.Errorf("build tx error: %s", newTxResp.Errors[0].Error)
	}

	// Step 2: Sign each hash
	child, err := w.derivePath(uint32(fromIndex))
	if err != nil {
		return "", err
	}
	privKeyBytes := child.Key
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	var signatures []string
	var pubkeys []string
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	pubKeyHex := fmt.Sprintf("%x", pubKeyBytes)

	for _, toSign := range newTxResp.ToSign {
		hashBytes := mustDecodeHex(toSign)
		sig := btcec.S256() // not directly, use ecdsa
		_ = sig
		// Sign with btcec
		btcSig := signBTCHash(privKey, hashBytes)
		signatures = append(signatures, fmt.Sprintf("%x", btcSig))
		pubkeys = append(pubkeys, pubKeyHex)
	}

	// Step 3: Send signed tx
	sendReq := map[string]interface{}{
		"tx":         newTxResp.Tx,
		"tosign":     newTxResp.ToSign,
		"signatures": signatures,
		"pubkeys":    pubkeys,
	}
	sendBody, _ := json.Marshal(sendReq)
	sendResp, err := w.httpClient.Post(w.apiBase+"/txs/send", "application/json", bytes.NewReader(sendBody))
	if err != nil {
		return "", fmt.Errorf("sendtx: %w", err)
	}
	defer sendResp.Body.Close()
	sendBody2, _ := io.ReadAll(sendResp.Body)

	var sent struct {
		Tx     map[string]interface{} `json:"tx"`
		Errors []struct{ Error string } `json:"errors"`
	}
	json.Unmarshal(sendBody2, &sent)
	if len(sent.Errors) > 0 {
		return "", fmt.Errorf("send error: %s", sent.Errors[0].Error)
	}

	hash, _ := sent.Tx["hash"].(string)
	return hash, nil
}

// signBTCHash signs a 32-byte hash with a btcec private key (DER-encoded).
func signBTCHash(privKey *btcec.PrivateKey, hash []byte) []byte {
	sig := btcecdsa.Sign(privKey, hash)
	return sig.Serialize()
}

func mustDecodeHex(s string) []byte {
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var v byte
		fmt.Sscanf(s[i:i+2], "%02x", &v)
		b[i/2] = v
	}
	return b
}
