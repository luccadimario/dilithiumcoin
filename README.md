# Rebellion

A peer-to-peer cryptocurrency blockchain implementation in Go with competitive proof-of-work mining.

**Token Symbol:** RBL
**Tagline:** Decentralized. Distributed. Revolutionary.

## Features

- **Proof-of-Work Mining** - SHA-256 based mining with configurable difficulty
- **Competitive Mining** - Multiple nodes compete to mine blocks; first valid block wins
- **P2P Networking** - Nodes automatically sync and broadcast transactions/blocks
- **Automatic Mining** - Nodes can auto-mine when transactions are pending
- **Wallet Management** - RSA-2048 key pairs for signing transactions
- **REST API** - HTTP endpoints for interacting with the node
- **UPnP Support** - Automatic port forwarding for easier connectivity
- **Cross-Platform** - Builds for Linux, macOS, and Windows (amd64/arm64)

## Quick Start

### Download

Download the appropriate binary for your platform from the [Releases](../../releases) page.

| Platform | Node | CLI |
|----------|------|-----|
| macOS (Apple Silicon) | `rebellion-darwin-arm64` | `rebellion-cli-darwin-arm64` |
| macOS (Intel) | `rebellion-darwin-amd64` | `rebellion-cli-darwin-amd64` |
| Linux (x64) | `rebellion-linux-amd64` | `rebellion-cli-linux-amd64` |
| Linux (ARM64) | `rebellion-linux-arm64` | `rebellion-cli-linux-arm64` |
| Windows (x64) | `rebellion-windows-amd64.exe` | `rebellion-cli-windows-amd64.exe` |

### Run a Node

```bash
# Start a node with auto-mining
./rebellion-darwin-arm64 --port 5001 --api-port 8001 --auto-mine --miner YOUR_WALLET_ADDRESS

# Connect to an existing node
./rebellion-darwin-arm64 --port 5002 --api-port 8002 --connect PEER_IP:5001 --auto-mine --miner YOUR_WALLET_ADDRESS
```

### Create a Wallet

```bash
# Create a new wallet
./rebellion-cli-darwin-arm64 wallet create --save ~/.rebellion

# View wallet info
./rebellion-cli-darwin-arm64 wallet info --key ~/.rebellion/wallet_private.pem
```

### Send a Transaction

```bash
# Sign a transaction
./rebellion-cli-darwin-arm64 transaction sign \
  --from YOUR_ADDRESS \
  --to RECIPIENT_ADDRESS \
  --amount 50 \
  --key ~/.rebellion/wallet_private.pem

# The CLI outputs a curl command to submit the transaction
```

## Node Commands

```
rebellion [flags]

Flags:
  --port string       P2P port (default "5001")
  --api-port string   HTTP API port (default "8001")
  --difficulty int    Mining difficulty - number of leading zeros (default 3)
  --connect string    Peer address to connect to (e.g., "192.168.1.10:5001")
  --miner string      Your wallet address for mining rewards
  --auto-mine         Enable automatic mining when transactions are pending
  --version           Show version
```

## CLI Commands

```
rebellion-cli wallet create --save <directory>    Create a new wallet
rebellion-cli wallet info --key <file>            Display wallet information
rebellion-cli transaction sign [flags]            Sign a transaction

Transaction Flags:
  --from string     Sender address (your wallet)
  --to string       Recipient address
  --amount float    Amount to send
  --key string      Path to your private key file
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/chain` | Get the full blockchain |
| GET | `/status` | Node status and statistics |
| GET | `/peers` | List connected peers |
| GET | `/mempool` | View pending transactions |
| POST | `/transaction` | Submit a signed transaction |
| POST | `/mine?miner=ADDRESS` | Manually mine pending transactions |
| POST | `/add-peer?address=IP:PORT` | Connect to a new peer |

### Example API Usage

```bash
# Check blockchain
curl http://localhost:8001/chain

# View node status
curl http://localhost:8001/status

# Submit a transaction
curl -X POST http://localhost:8001/transaction \
  -H "Content-Type: application/json" \
  -d '{
    "from": "abc123...",
    "to": "def456...",
    "amount": 10.0,
    "timestamp": 1234567890,
    "signature": "..."
  }'

# Connect to a peer
curl -X POST "http://localhost:8001/add-peer?address=192.168.1.10:5001"
```

## Running a Network

### Single Machine (Testing)

```bash
# Terminal 1 - First node (creates the initial blockchain)
./rebellion --port 5001 --api-port 8001 --auto-mine --miner wallet1

# Terminal 2 - Second node (connects and competes for mining)
./rebellion --port 5002 --api-port 8002 --connect localhost:5001 --auto-mine --miner wallet2

# Terminal 3 - Submit transactions
curl -X POST http://localhost:8001/transaction \
  -H "Content-Type: application/json" \
  -d '{"from":"alice","to":"bob","amount":10,"timestamp":1234567890,"signature":"sig1"}'
```

### Multiple Machines

```bash
# Machine A (first node - share your IP with others)
./rebellion --port 5001 --api-port 8001 --auto-mine --miner your_address

# Machine B (connect to Machine A)
./rebellion --port 5001 --api-port 8001 --connect MACHINE_A_IP:5001 --auto-mine --miner your_address
```

## How Mining Works

1. Transactions are submitted to any node and broadcast to all peers
2. Transactions collect in each node's mempool
3. When auto-mining is enabled, nodes continuously attempt to mine blocks
4. Mining involves finding a hash with N leading zeros (proof-of-work)
5. The first node to find a valid hash broadcasts the block
6. Other nodes verify and accept the block, abandoning their mining attempt
7. The winning miner receives a reward (10 RBL by default)
8. All nodes sync to the longest valid chain

## Building from Source

### Prerequisites

- Go 1.21 or later

### Build

```bash
# Clone the repository
git clone https://github.com/yourusername/rebellion.git
cd rebellion

# Build for your platform
go build -o rebellion .
go build -o rebellion-cli ./cmd/rebellion-cli

# Or build for all platforms
chmod +x build.sh
./build.sh
```

## Project Structure

```
rebellion/
├── main.go           # Node entry point and CLI flags
├── blockchain.go     # Block and blockchain implementation
├── transaction.go    # Transaction structure and validation
├── network.go        # P2P networking and message handling
├── wallet.go         # Wallet and cryptographic signing
├── api.go            # HTTP REST API server
├── upnp.go           # UPnP port forwarding
├── build.sh          # Multi-platform build script
├── cmd/
│   └── rebellion-cli/
│       ├── main.go        # CLI entry point
│       ├── wallet.go      # Wallet commands
│       └── transaction.go # Transaction signing
└── dist/             # Built binaries (see Releases)
```

## Configuration

### Mining Difficulty

The `--difficulty` flag controls how hard it is to mine a block. Each increment roughly doubles the mining time:

| Difficulty | Leading Zeros | Approximate Time |
|------------|---------------|------------------|
| 3 | `000...` | ~1 second |
| 4 | `0000...` | ~10 seconds |
| 5 | `00000...` | ~1-2 minutes |
| 6 | `000000...` | ~10-20 minutes |

### Port Forwarding

For nodes to connect over the internet:

1. **UPnP** - If your router supports UPnP, port forwarding is automatic
2. **Manual** - Forward the P2P port (default 5001) to your machine

## License

MIT License

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.
