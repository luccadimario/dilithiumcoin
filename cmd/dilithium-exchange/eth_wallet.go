package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	bip32 "github.com/tyler-smith/go-bip32"
	bip39 "github.com/tyler-smith/go-bip39"
)

// ETHWallet manages Ethereum HD wallet operations for the exchange.
type ETHWallet struct {
	masterKey  *bip32.Key
	client     *ethclient.Client
	chainID    *big.Int
	rpcURL     string
}

// NewETHWallet creates an ETH wallet from a BIP39 mnemonic.
// The mnemonic is used for HD key derivation (BIP44: m/44'/60'/0'/0/{index}).
func NewETHWallet(mnemonic, rpcURL string) (*ETHWallet, error) {
	seed := bip39.NewSeed(mnemonic, "")
	masterKey, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("derive master key: %w", err)
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("connect to ETH RPC: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chainID, err := client.ChainID(ctx)
	if err != nil {
		// Default to mainnet if RPC unavailable at start
		chainID = big.NewInt(1)
	}

	return &ETHWallet{
		masterKey: masterKey,
		client:    client,
		chainID:   chainID,
		rpcURL:    rpcURL,
	}, nil
}

// derivePath derives the key at BIP44 path m/44'/60'/0'/0/{index}.
func (w *ETHWallet) derivePath(index uint32) (*bip32.Key, error) {
	const hardened = bip32.FirstHardenedChild
	purpose, err := w.masterKey.NewChildKey(hardened + 44)
	if err != nil {
		return nil, err
	}
	coinType, err := purpose.NewChildKey(hardened + 60) // ETH = 60
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

// DeriveDepositAddress returns the ETH deposit address for a given user index.
func (w *ETHWallet) DeriveDepositAddress(index int) (string, error) {
	child, err := w.derivePath(uint32(index))
	if err != nil {
		return "", fmt.Errorf("derive key at index %d: %w", index, err)
	}
	privKey, err := crypto.ToECDSA(child.Key)
	if err != nil {
		return "", fmt.Errorf("parse ECDSA key: %w", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	return strings.ToLower(addr.Hex()), nil
}

// DerivePrivateKey returns the ECDSA private key at the given index (for signing).
func (w *ETHWallet) DerivePrivateKey(index int) (*ecdsa.PrivateKey, error) {
	child, err := w.derivePath(uint32(index))
	if err != nil {
		return nil, err
	}
	return crypto.ToECDSA(child.Key)
}

// GetBalance returns the ETH balance of an address in wei.
func (w *ETHWallet) GetBalance(addr string) (*big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	balance, err := w.client.BalanceAt(ctx, common.HexToAddress(addr), nil)
	if err != nil {
		return nil, err
	}
	return balance, nil
}

// GetCurrentBlock returns the current Ethereum block number.
func (w *ETHWallet) GetCurrentBlock() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return w.client.BlockNumber(ctx)
}

// ETHTx represents a simplified Ethereum transaction for deposit detection.
type ETHTx struct {
	Hash        string
	From        string
	To          string
	Value       *big.Int
	BlockNumber uint64
}

// GetBlockTransactions returns all transactions in a given block.
func (w *ETHWallet) GetBlockTransactions(blockNum uint64) ([]ETHTx, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	block, err := w.client.BlockByNumber(ctx, new(big.Int).SetUint64(blockNum))
	if err != nil {
		return nil, err
	}

	var txs []ETHTx
	signer := types.LatestSignerForChainID(w.chainID)
	for _, tx := range block.Transactions() {
		if tx.To() == nil {
			continue // contract creation, skip
		}
		from, err := types.Sender(signer, tx)
		if err != nil {
			continue
		}
		txs = append(txs, ETHTx{
			Hash:        tx.Hash().Hex(),
			From:        strings.ToLower(from.Hex()),
			To:          strings.ToLower(tx.To().Hex()),
			Value:       tx.Value(),
			BlockNumber: blockNum,
		})
	}
	return txs, nil
}

// SendETH sends ETH from the wallet at fromIndex to toAddr.
// amount is in wei. Returns the transaction hash.
func (w *ETHWallet) SendETH(fromIndex int, toAddr string, amount *big.Int) (string, error) {
	privKey, err := w.DerivePrivateKey(fromIndex)
	if err != nil {
		return "", err
	}

	fromAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	nonce, err := w.client.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return "", fmt.Errorf("get nonce: %w", err)
	}

	gasPrice, err := w.client.SuggestGasPrice(ctx)
	if err != nil {
		gasPrice = big.NewInt(20e9) // 20 gwei fallback
	}

	gasLimit := uint64(21000) // standard ETH transfer
	to := common.HexToAddress(toAddr)

	tx := types.NewTransaction(nonce, to, amount, gasLimit, gasPrice, nil)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(w.chainID), privKey)
	if err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}

	if err := w.client.SendTransaction(ctx, signedTx); err != nil {
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}

	return signedTx.Hash().Hex(), nil
}

// VerifySIWESignature recovers the Ethereum address from a SIWE message + signature.
// Returns the lowercase Ethereum address that signed the message.
func VerifySIWESignature(message, sigHex string) (string, error) {
	sigHex = strings.TrimPrefix(sigHex, "0x")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 65 {
		return "", fmt.Errorf("invalid signature length: %d", len(sigBytes))
	}

	// Ethereum personal_sign uses V = 27 or 28; convert to recovery ID 0/1
	if sigBytes[64] >= 27 {
		sigBytes[64] -= 27
	}

	// Hash the message with Ethereum personal sign prefix
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	hash := crypto.Keccak256Hash([]byte(prefix + message))

	pubKey, err := crypto.SigToPub(hash.Bytes(), sigBytes)
	if err != nil {
		return "", fmt.Errorf("recover public key: %w", err)
	}

	addr := crypto.PubkeyToAddress(*pubKey)
	return strings.ToLower(addr.Hex()), nil
}

// --- ETH RPC fallback via HTTP JSON-RPC (for block number polling) ---

type ethRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type ethRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func ethRPCCall(rpcURL, method string, params []interface{}) (json.RawMessage, error) {
	req := ethRPCRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1}
	body, _ := json.Marshal(req)
	resp, err := http.Post(rpcURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var rpcResp ethRPCResponse
	json.Unmarshal(data, &rpcResp)
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error: %s", rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}
