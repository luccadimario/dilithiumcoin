package main

import (
	"flag"
	"os"
)

// Config holds all exchange configuration.
type Config struct {
	DatabasePath     string
	HTTPPort         int
	ETHRPCURL        string  // Alchemy/Infura/public ETH RPC
	BTCAPIEndpoint   string  // BlockCypher base URL
	DLTNodeURL       string  // Dilithium node API
	ETHSeedPath      string  // Path to ETH/BTC HD seed file (shared BIP39 seed)
	DLTWalletDir     string  // Exchange hot DLT wallet directory
	JWTSecret        string  // Secret for JWT signing
	ETHConfirmations int     // Blocks before crediting ETH deposit (default 12)
	BTCConfirmations int     // Blocks before crediting BTC deposit (default 3)
	DLTConfirmations int     // Blocks before crediting DLT deposit (default 1)
	MakerFee         float64 // Maker fee rate e.g. 0.001 = 0.1%
	TakerFee         float64 // Taker fee rate e.g. 0.002 = 0.2%
	WithdrawFeeETH   int64   // Wei taken as withdrawal fee
	WithdrawFeeBTC   int64   // Satoshis taken as withdrawal fee
	WithdrawFeeDLT   int64   // Base units taken as withdrawal fee
}

func parseConfig() *Config {
	c := &Config{}

	flag.StringVar(&c.DatabasePath, "db", "exchange.db", "SQLite database path")
	flag.IntVar(&c.HTTPPort, "port", 8082, "HTTP API port")
	flag.StringVar(&c.ETHRPCURL, "eth-rpc", "https://eth.llamarpc.com", "Ethereum RPC URL (Alchemy/Infura/public)")
	flag.StringVar(&c.BTCAPIEndpoint, "btc-api", "https://api.blockcypher.com/v1/btc/main", "BlockCypher BTC API base URL")
	flag.StringVar(&c.DLTNodeURL, "node", "http://localhost:8001", "Dilithium node API URL")
	flag.StringVar(&c.ETHSeedPath, "seed", "exchange_seed.enc", "Path to encrypted HD seed file (ETH+BTC wallets)")
	flag.StringVar(&c.DLTWalletDir, "dlt-wallet", defaultDLTWalletDir(), "Exchange DLT wallet directory")
	flag.StringVar(&c.JWTSecret, "jwt-secret", "", "JWT signing secret (required)")
	flag.IntVar(&c.ETHConfirmations, "eth-confirms", 12, "ETH confirmations before crediting deposit")
	flag.IntVar(&c.BTCConfirmations, "btc-confirms", 3, "BTC confirmations before crediting deposit")
	flag.IntVar(&c.DLTConfirmations, "dlt-confirms", 1, "DLT confirmations before crediting deposit")
	flag.Float64Var(&c.MakerFee, "maker-fee", 0.001, "Maker fee rate (0.001 = 0.1%)")
	flag.Float64Var(&c.TakerFee, "taker-fee", 0.002, "Taker fee rate (0.002 = 0.2%)")

	// Withdrawal fees
	c.WithdrawFeeETH = 0        // 0 wei (charged as gas)
	c.WithdrawFeeBTC = 10000    // 0.0001 BTC (10000 satoshis)
	c.WithdrawFeeDLT = 10000    // 0.0001 DLT (10000 base units)

	flag.Parse()

	// Allow JWT secret from environment
	if c.JWTSecret == "" {
		c.JWTSecret = os.Getenv("EXCHANGE_JWT_SECRET")
	}
	if c.JWTSecret == "" {
		c.JWTSecret = "dilithium-exchange-dev-secret-change-in-production"
	}

	return c
}

func defaultDLTWalletDir() string {
	home, _ := os.UserHomeDir()
	return home + "/.dilithium/exchange-wallet"
}
