package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ============================================================================
// VERSION INFO
// ============================================================================

const (
	// Version is the current Dilithium version
	Version = "3.2.6"

	// VersionMajor is the major version number
	VersionMajor = 3

	// VersionMinor is the minor version number
	VersionMinor = 2

	// VersionPatch is the patch version number
	VersionPatch = 6

	// VersionPreRelease is the pre-release identifier (empty for release)
	VersionPreRelease = ""

	// NetworkName identifies this blockchain network
	NetworkName = "dilithium-mainnet"
)

// ============================================================================
// NETWORK CONFIGURATION
// ============================================================================

// NetworkConfig holds all network-related configuration
type NetworkConfig struct {
	// Seed nodes - hardcoded bootstrap nodes
	SeedNodes []string

	// Connection limits
	MinOutbound int // Minimum outbound connections to maintain
	MaxOutbound int // Maximum outbound connections
	MaxInbound  int // Maximum inbound connections

	// Timeouts
	ConnectTimeout   time.Duration // TCP connection timeout
	HandshakeTimeout time.Duration // Version handshake timeout
	PingInterval     time.Duration // Ping frequency
	PingTimeout      time.Duration // Pong wait timeout
	SeedTimeout      time.Duration // How long to try peers.dat before seeds

	// Retry configuration
	MaxRetries       int           // Max connection retries
	BaseRetryDelay   time.Duration // Initial retry delay
	MaxRetryDelay    time.Duration // Maximum retry delay (cap)
	RetryMultiplier  float64       // Backoff multiplier

	// Peer database
	PeersFile        string        // Path to peers.dat
	PeerSaveInterval time.Duration // How often to save peer database
	MaxStoredPeers   int           // Maximum peers to store
	PruneInterval    time.Duration // How often to prune dead peers
	PruneAge         time.Duration // Age after which to prune unresponsive peers

	// Banning
	BanDuration      time.Duration // Default ban duration
	BanThreshold     int           // Misbehavior score threshold for ban
}

// DefaultNetworkConfig returns the default network configuration
func DefaultNetworkConfig() *NetworkConfig {
	return &NetworkConfig{
		// Seed nodes for mainnet bootstrap
		// These are the initial entry points to the network
		SeedNodes: []string{
			"seed.dilithiumcoin.com:1701",
			"seed1.dilithiumcoin.com:1701",
			"seed2.dilithiumcoin.com:1701",
		},

		// Connection limits
		MinOutbound: 8,
		MaxOutbound: 16,
		MaxInbound:  64,

		// Timeouts
		ConnectTimeout:   10 * time.Second,
		HandshakeTimeout: 30 * time.Second,
		PingInterval:     2 * time.Minute,
		PingTimeout:      30 * time.Second,
		SeedTimeout:      11 * time.Second,

		// Retry configuration
		MaxRetries:      5,
		BaseRetryDelay:  1 * time.Second,
		MaxRetryDelay:   5 * time.Minute,
		RetryMultiplier: 2.0,

		// Peer database
		PeersFile:        "peers.dat",
		PeerSaveInterval: 5 * time.Minute,
		MaxStoredPeers:   2000,
		PruneInterval:    1 * time.Hour,
		PruneAge:         7 * 24 * time.Hour, // 7 days

		// Banning
		BanDuration:  24 * time.Hour,
		BanThreshold: 100,
	}
}

// ============================================================================
// BLOCKCHAIN CONFIGURATION
// ============================================================================

// ChainConfig holds blockchain-specific configuration
type ChainConfig struct {
	// Mining
	DefaultDifficulty int   // Default mining difficulty
	MiningReward      int64 // Block reward (in base units)

	// Mempool
	MaxMempoolSize     int           // Maximum mempool transactions
	MaxTxSize          int           // Maximum transaction size in bytes
	MinTxFee           int64         // Minimum transaction fee (in base units)
	MempoolExpiry      time.Duration // Time after which to expire mempool txs

	// Block
	MaxBlockSize       int           // Maximum block size in bytes
	MaxTxPerBlock      int           // Maximum transactions per block
	BlockInterval      time.Duration // Target block time
}

// DefaultChainConfig returns the default chain configuration
func DefaultChainConfig() *ChainConfig {
	return &ChainConfig{
		// Mining
		DefaultDifficulty: 6,
		MiningReward:      50 * 100_000_000, // 50 DLT = InitialBlockReward

		// Mempool
		MaxMempoolSize:  10000,
		MaxTxSize:       100 * 1024, // 100KB
		MinTxFee:        10000,      // 0.0001 DLT in base units
		MempoolExpiry:   72 * time.Hour,

		// Block
		MaxBlockSize:  1 * 1024 * 1024, // 1MB
		MaxTxPerBlock: 5000,
		BlockInterval: 1 * time.Minute, // Match TargetBlockTime
	}
}

// ============================================================================
// API CONFIGURATION
// ============================================================================

// APIConfig holds HTTP API configuration
type APIConfig struct {
	Enabled      bool
	Host         string
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	EnableCORS   bool
	CORSOrigin   string
}

// DefaultAPIConfig returns the default API configuration
func DefaultAPIConfig() *APIConfig {
	return &APIConfig{
		Enabled:      true,
		Host:         "127.0.0.1",
		Port:         "8001",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		EnableCORS:   true,
		CORSOrigin:   "*",
	}
}

// ============================================================================
// FULL CONFIGURATION
// ============================================================================

// Config holds all configuration for a Dilithium node
type Config struct {
	// Node identity
	NodeName string `json:"node_name"`
	DataDir  string `json:"data_dir"`

	// Network
	Network *NetworkConfig `json:"network"`
	P2PPort string         `json:"p2p_port"`

	// Chain
	Chain *ChainConfig `json:"chain"`

	// API
	API *APIConfig `json:"api"`

	// Mining
	MinerAddress string `json:"miner_address"`
	AutoMine     bool   `json:"auto_mine"`
}

// DefaultConfig returns a fully populated default configuration
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".dilithium")

	return &Config{
		NodeName: "",
		DataDir:  dataDir,
		Network:  DefaultNetworkConfig(),
		P2PPort:  "5001",
		Chain:    DefaultChainConfig(),
		API:      DefaultAPIConfig(),
		AutoMine: false,
	}
}

// PeersFilePath returns the full path to peers.dat
func (c *Config) PeersFilePath() string {
	return filepath.Join(c.DataDir, c.Network.PeersFile)
}

// EnsureDataDir creates the data directory if it doesn't exist
func (c *Config) EnsureDataDir() error {
	return os.MkdirAll(c.DataDir, 0755)
}

// ============================================================================
// CONFIGURATION LOADING/SAVING
// ============================================================================

// LoadConfig loads configuration from a JSON file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	config := DefaultConfig()
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return config, nil
}

// SaveConfig saves configuration to a JSON file
func (c *Config) SaveConfig(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// ============================================================================
// SEED NODE HELPERS
// ============================================================================

// TestnetConfig returns configuration for testnet
func TestnetConfig() *Config {
	config := DefaultConfig()
	config.DataDir = filepath.Join(config.DataDir, "testnet")
	config.Network.SeedNodes = []string{
		"testnet-seed1.dilithiumcoin.com:1701",
		"testnet-seed2.dilithiumcoin.com:1701",
	}
	config.Chain.DefaultDifficulty = 4 // Easier mining for testnet
	config.Chain.MiningReward = 50 * 100_000_000 // Higher rewards for testing
	return config
}

// LocalConfig returns configuration for local development
func LocalConfig() *Config {
	config := DefaultConfig()
	config.DataDir = filepath.Join(config.DataDir, "local")
	config.Network.SeedNodes = []string{} // No seeds for local
	config.Network.MinOutbound = 0        // Don't require connections
	config.Chain.DefaultDifficulty = 2    // Very easy mining
	return config
}

// AddSeedNodes adds additional seed nodes to the configuration
func (c *Config) AddSeedNodes(nodes ...string) {
	c.Network.SeedNodes = append(c.Network.SeedNodes, nodes...)
}

// ============================================================================
// ENVIRONMENT OVERRIDE
// ============================================================================

// ApplyEnvOverrides applies environment variable overrides
func (c *Config) ApplyEnvOverrides() {
	if port := os.Getenv("DILITHIUM_P2P_PORT"); port != "" {
		c.P2PPort = port
	}
	if apiPort := os.Getenv("DILITHIUM_API_PORT"); apiPort != "" {
		c.API.Port = apiPort
	}
	if dataDir := os.Getenv("DILITHIUM_DATA_DIR"); dataDir != "" {
		c.DataDir = dataDir
	}
	if miner := os.Getenv("DILITHIUM_MINER"); miner != "" {
		c.MinerAddress = miner
	}
	if os.Getenv("DILITHIUM_AUTO_MINE") == "true" {
		c.AutoMine = true
	}
}

// ============================================================================
// VALIDATION
// ============================================================================

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	if c.P2PPort == "" {
		return fmt.Errorf("P2P port is required")
	}

	if c.Network.MinOutbound > c.Network.MaxOutbound {
		return fmt.Errorf("MinOutbound cannot exceed MaxOutbound")
	}

	if c.Chain.DefaultDifficulty < 1 {
		return fmt.Errorf("difficulty must be at least 1")
	}

	return nil
}
