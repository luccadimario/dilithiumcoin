# Dilithium

A quantum-safe, peer-to-peer cryptocurrency blockchain implementation in Go with competitive proof-of-work mining.

**Token Symbol:** DLT
**Tagline:** Quantum-Safe. Decentralized. Revolutionary.

## Quantum Safety

Dilithium is one of the first blockchains with **native post-quantum cryptographic signatures**. All transactions are signed using **CRYSTALS-Dilithium Mode3** (the NIST post-quantum standard), providing **192-bit quantum-safe security**. This means Dilithium wallets and transactions are resistant to attacks from both classical and quantum computers.

| Property | Value |
|----------|-------|
| Algorithm | CRYSTALS-Dilithium Mode3 |
| Security Level | NIST Level 3 (192-bit quantum-safe) |
| Public Key Size | 1,952 bytes |
| Private Key Size | 4,000 bytes |
| Signature Size | 3,293 bytes |
| Library | [Cloudflare CIRCL](https://github.com/cloudflare/circl) |

## Features

- **Post-Quantum Security** - CRYSTALS-Dilithium Mode3 signatures (NIST PQC standard)
- **Proof-of-Work Mining** - SHA-256 based mining with configurable difficulty
- **Competitive Mining** - Multiple nodes compete to mine blocks; first valid block wins
- **P2P Networking** - Nodes automatically sync and broadcast transactions/blocks
- **Automatic Mining** - Nodes can auto-mine when transactions are pending
- **Wallet Management** - Quantum-safe Dilithium key pairs for signing transactions
- **REST API** - HTTP endpoints for interacting with the node
- **UPnP Support** - Automatic port forwarding for easier connectivity
- **Cross-Platform** - Builds for Linux, macOS, and Windows (amd64/arm64)

## Quick Start

### Download

Download the appropriate binary for your platform from the [Releases](../../releases) page.

| Platform | Node | CLI | Miner |
|----------|------|-----|-------|
| macOS (Apple Silicon) | `dilithium-darwin-arm64` | `dilithium-cli-darwin-arm64` | `dilithium-miner-darwin-arm64` |
| macOS (Intel) | `dilithium-darwin-amd64` | `dilithium-cli-darwin-amd64` | `dilithium-miner-darwin-amd64` |
| Linux (x64) | `dilithium-linux-amd64` | `dilithium-cli-linux-amd64` | `dilithium-miner-linux-amd64` |
| Linux (ARM64) | `dilithium-linux-arm64` | `dilithium-cli-linux-arm64` | `dilithium-miner-linux-arm64` |
| Windows (x64) | `dilithium-windows-amd64.exe` | `dilithium-cli-windows-amd64.exe` | `dilithium-miner-windows-amd64.exe` |

### Build from Source

```bash
git clone https://github.com/luccadimario/dilithiumcoin.git
cd dilithiumcoin
./build.sh
```

Requires Go 1.25 or later.

### Run a Node

```bash
# Start a node with auto-mining
./dilithium --port 5001 --api-port 8001 --auto-mine --miner YOUR_WALLET_ADDRESS

# Connect to an existing node
./dilithium --port 5002 --api-port 8002 --connect PEER_IP:5001 --auto-mine --miner YOUR_WALLET_ADDRESS
```

### Create a Wallet

```bash
./dilithium-cli wallet create
```

### Start Mining

```bash
# Simplest — auto-starts an embedded node
./dilithium-miner --miner YOUR_WALLET_ADDRESS

# Or connect to your own node
./dilithium-miner --node http://localhost:8001 --miner YOUR_WALLET_ADDRESS
```

### Send a Transaction

```bash
./dilithium-cli send --to RECIPIENT_ADDRESS --amount 10
```

## Node Flags

```
dilithium [flags]

Flags:
  --port string       P2P port (default "5001")
  --api-port string   HTTP API port (default "8001")
  --difficulty int    Mining difficulty (default 6)
  --connect string    Peer address to connect to (e.g., "192.168.1.10:5001")
  --miner string      Your wallet address for mining rewards
  --auto-mine         Enable automatic mining
  --data-dir string   Data directory path
  --testnet           Run on testnet
  --no-seeds          Don't connect to seed nodes (for local testing)
  --version           Show version and exit
```

## CLI Commands

```
dilithium-cli wallet create                Create a new wallet
dilithium-cli wallet info                  Display wallet information
dilithium-cli address                      Show wallet address
dilithium-cli balance                      Check wallet balance
dilithium-cli send --to <addr> --amount N  Send DLT to an address
dilithium-cli tx sign [flags]              Sign a transaction
```

## Miner Flags

```
dilithium-miner [flags]

Flags:
  --miner string      Miner wallet address
  --node string       Node API URL (if not set, an embedded node is started)
  --no-node           Disable embedded node (requires --node)
  --threads int       Number of mining threads (default 1)
  --wallet string     Wallet directory (auto-detect address)
  --version           Show version
```

The miner automatically starts an embedded `dilithium` node if no `--node` URL is provided. Both binaries must be in the same directory (or the node binary must be in your PATH).

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
# Terminal 1 - First node
./dilithium --port 5001 --api-port 8001 --auto-mine --miner wallet1

# Terminal 2 - Second node (connects and competes)
./dilithium --port 5002 --api-port 8002 --connect localhost:5001 --auto-mine --miner wallet2

# Terminal 3 - Submit transactions
curl -X POST http://localhost:8001/transaction \
  -H "Content-Type: application/json" \
  -d '{"from":"alice","to":"bob","amount":10,"timestamp":1234567890,"signature":"sig1"}'
```

### Multiple Machines

```bash
# Machine A (first node - share your IP with others)
./dilithium --port 5001 --api-port 8001 --auto-mine --miner your_address

# Machine B (connect to Machine A)
./dilithium --port 5001 --api-port 8001 --connect MACHINE_A_IP:5001 --auto-mine --miner your_address
```

## How Mining Works

1. Transactions are submitted to any node and broadcast to all peers
2. Transactions collect in each node's mempool
3. When auto-mining is enabled, nodes continuously attempt to mine blocks
4. Mining involves finding a hash with N leading zeros (proof-of-work)
5. The first node to find a valid hash broadcasts the block
6. Other nodes verify and accept the block, abandoning their mining attempt
7. The winning miner receives a reward (10 DLT by default)
8. All nodes sync to the longest valid chain

## Project Structure

```
dilithiumcoin/
├── main.go           # Node entry point and CLI flags
├── blockchain.go     # Block and blockchain implementation
├── transaction.go    # Transaction structure and validation
├── network.go        # P2P networking and message handling
├── wallet.go         # Wallet and cryptographic signing
├── api.go            # HTTP REST API server
├── config.go         # Node configuration
├── mempool.go        # Transaction mempool
├── message.go        # P2P message types
├── peer.go           # Peer management
├── upnp.go           # UPnP port forwarding
├── build.sh          # Multi-platform build script
├── cmd/
│   ├── dilithium-cli/    # CLI tool
│   └── dilithium-miner/  # Standalone miner
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
