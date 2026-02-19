# Dilithium: A Post-Quantum Proof-of-Work Cryptocurrency

**Version 1.0 — February 2026**

## Abstract

We present Dilithium, a proof-of-work cryptocurrency that replaces the elliptic curve cryptography used by existing blockchains with CRYSTALS-Dilithium, a lattice-based digital signature scheme standardized by NIST for post-quantum security. Transactions are signed with Dilithium Mode3 at the 192-bit security level, providing resistance to both classical and quantum adversaries. Blocks are mined using SHA-256 with a bit-granularity difficulty target and a linearly weighted moving average (LWMA) difficulty adjustment algorithm. The network operates on a custom binary protocol with Bitcoin-style peer scoring, headers-first synchronization, and support for both solo and pool mining across CPU and GPU hardware. Total supply is capped at 25,000,000 DLT through a halving schedule that reduces block rewards every 250,000 blocks.

## 1. Introduction

The security of Bitcoin and most existing cryptocurrencies depends on the computational hardness of the elliptic curve discrete logarithm problem (ECDLP). Shor's algorithm, running on a sufficiently large quantum computer, solves ECDLP in polynomial time, rendering ECDSA signatures forgeable. While large-scale fault-tolerant quantum computers do not yet exist, the timeline for their arrival is uncertain, and blockchain addresses holding funds today must remain secure for decades.

NIST completed its Post-Quantum Cryptography standardization process in 2024, selecting CRYSTALS-Dilithium (FIPS 204, ML-DSA) as the primary signature standard. Dilithium is a lattice-based scheme whose security relies on the hardness of the Module Learning With Errors (MLWE) problem, which is believed to resist both classical and quantum attacks.

This paper describes a blockchain built from the ground up with CRYSTALS-Dilithium as the sole signature algorithm. Every transaction, from the genesis block forward, is signed with a post-quantum key pair. No migration or hybrid scheme is necessary because no legacy cryptography is present.

## 2. Cryptographic Primitives

### 2.1 Digital Signatures

All transaction signatures use CRYSTALS-Dilithium Mode3, which targets NIST Security Level 3 (approximately 192-bit classical security). The key parameters are:

| Parameter | Value |
|-----------|-------|
| Public key size | 1,952 bytes |
| Private key size | 4,000 bytes |
| Signature size | 3,293 bytes |
| Security level | NIST Level 3 (~192-bit) |
| Underlying problem | Module-LWE |

The implementation uses Cloudflare's `circl` library, which provides a constant-time, side-channel-resistant Dilithium implementation in Go.

### 2.2 Address Derivation

An address is derived from a public key by computing:

```
address = hex(SHA-256(public_key_bytes))[0:40]
```

This produces a 40-character hexadecimal string (20 bytes), providing 160 bits of collision resistance against address impersonation.

### 2.3 Key Generation and Recovery

Key pairs may be generated from cryptographic randomness or derived deterministically from a BIP39 mnemonic phrase. The deterministic derivation path is:

1. Generate 256 bits of entropy and encode as a 24-word BIP39 mnemonic.
2. Derive a 64-byte seed using PBKDF2-HMAC-SHA512 with 2,048 iterations and the passphrase `"mnemonic"` (per BIP39).
3. Expand the seed into a deterministic byte stream using HKDF-SHA256 with the salt `"dilithium-v1-keypair"`.
4. Pass the HKDF reader to the Dilithium Mode3 key generation function.

The same mnemonic always produces the same key pair. The mnemonic is displayed once at wallet creation and is never stored on disk.

### 2.4 Private Key Encryption

Private keys stored on disk may be encrypted with a user-chosen passphrase using AES-256-GCM. The encryption key is derived by computing SHA-256(salt || passphrase) and iterating SHA-256 on the result 100,000 times. A random 16-byte salt and 12-byte nonce are prepended to the ciphertext.

## 3. Transactions

### 3.1 Structure

A transaction consists of the following fields:

| Field | Type | Description |
|-------|------|-------------|
| From | string | Sender address (40 hex characters), or `"SYSTEM"` for coinbase |
| To | string | Recipient address (40 hex characters) |
| Amount | int64 | Transfer amount in base units |
| Fee | int64 | Transaction fee in base units |
| Timestamp | int64 | Unix timestamp (seconds) |
| Signature | string | Hex-encoded Dilithium Mode3 signature |
| PublicKey | string | Hex-encoded sender public key |

The base monetary unit is defined such that 1 DLT = 100,000,000 base units, providing eight decimal places of precision.

### 3.2 Signing and Verification

The data signed for a transaction is the concatenation:

```
"dilithium-mainnet:" + From + To + Amount + Fee + Timestamp
```

The chain identifier `"dilithium-mainnet"` serves as domain separation to prevent cross-chain replay attacks. The signature is produced using the sender's Dilithium Mode3 private key and verified using the public key included in the transaction. The verifier also confirms that `SHA-256(PublicKey)[0:40]` matches the `From` address.

### 3.3 Coinbase Transactions

Each block must contain exactly one coinbase transaction, which creates new currency. A coinbase transaction has `From = "SYSTEM"`, no signature verification, and `Amount = block_reward + total_fees`, where `total_fees` is the sum of fees from all other transactions in the block.

### 3.4 Fees

All non-coinbase transactions must include a fee of at least 10,000 base units (0.0001 DLT). Miners collect the sum of all transaction fees in the block they mine, in addition to the block reward. The minimum fee prevents spam while remaining negligible for legitimate transfers.

### 3.5 Validation Rules

A transaction is valid if and only if:

1. `Amount > 0`
2. `Fee >= 10,000` (non-coinbase only)
3. The Dilithium signature verifies against the included public key
4. The address derived from the public key matches `From`
5. The sender's confirmed balance is at least `Amount + Fee`
6. The serialized transaction is at most 100 KB

## 4. Blocks

### 4.1 Structure

A block consists of:

| Field | Type | Description |
|-------|------|-------------|
| Index | int64 | Block height (genesis = 0) |
| Timestamp | int64 | Unix timestamp (seconds) |
| Transactions | []*Transaction | Ordered list of transactions |
| MerkleRoot | string | Merkle root of transactions (block 6,000+) |
| PreviousHash | string | SHA-256 hash of the previous block |
| Hash | string | SHA-256 hash of this block |
| Nonce | int64 | Proof-of-work nonce |
| Difficulty | int | Legacy difficulty (hex digits) |
| DifficultyBits | int | Bit-precise difficulty target |

### 4.2 Block Hash

Beginning at block 6,000 (Merkle root hard fork), the block hash uses a Merkle root instead of the full JSON-serialized transaction list:

```
Pre-fork  (block < 6,000):  txData = JSON(Transactions)
Post-fork (block >= 6,000): txData = MerkleRoot

data = str(Index) + str(Timestamp) + txData + PreviousHash + str(Nonce) + str(Difficulty)
Hash = hex(SHA-256(data))
```

All numeric fields are rendered as decimal ASCII strings. For pre-fork blocks, the transaction array is serialized as JSON with fields omitted when zero-valued (using Go's `omitempty` tag). For post-fork blocks, `txData` is the 64-character hex Merkle root, making block headers a fixed size regardless of transaction count.

### 4.3 Merkle Tree

The Merkle root is computed using a standard binary Merkle tree (Bitcoin-style):

1. For each transaction, compute `SHA-256(JSON(tx))` to produce leaf hashes.
2. If there is an odd number of leaves, duplicate the last one.
3. Pair adjacent hashes and compute `SHA-256(left || right)` up the tree.
4. The root is the final 64-character hex string.
5. For an empty block (no transactions), the Merkle root is `SHA-256("")`.

This provides O(log n) proof-of-inclusion for any transaction in a block, enabling future support for lightweight SPV clients.

### 4.4 Validation Rules

A block is accepted if and only if:

1. `Hash == SHA-256(block_data)` (hash integrity)
2. `PreviousHash == previous_block.Hash` (chain continuity)
3. The hash satisfies the difficulty target (see Section 5)
4. `Timestamp <= now + 7200` (not more than 2 hours in the future)
5. `Timestamp >= previous_block.Timestamp` (monotonic)
6. The difficulty matches the value computed by the DAA (see Section 6)
7. All transactions pass validation (see Section 3.5)
8. Exactly one coinbase transaction is present
9. Serialized block size does not exceed 1,048,576 bytes (1 MB)
10. Transaction count does not exceed 5,000

### 4.5 Genesis Block

The genesis block was mined on February 1, 2025:

| Field | Value |
|-------|-------|
| Index | 0 |
| Timestamp | 1738368000 (2025-02-01 00:00:00 UTC) |
| Transactions | (empty) |
| PreviousHash | `"0"` |
| Difficulty | 6 |
| Nonce | 5,892,535 |
| Hash | `0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae` |

## 5. Proof-of-Work

### 5.1 Difficulty Target

Dilithium uses a bit-granularity difficulty system. A block hash satisfies difficulty `d` if its first `d` bits are zero. This is verified by:

1. Checking that the first `floor(d / 8)` bytes of the hash are `0x00`.
2. Checking that the next byte, masked with `0xFF << (8 - (d mod 8))`, is `0x00`.

This provides 4x finer difficulty granularity than a hex-digit-based system and allows smoother adjustments. The difficulty ranges from a minimum of 16 bits to a maximum of 80 bits.

### 5.2 Proof-of-Work Function

Mining consists of searching for a nonce such that `SHA-256(block_data)` satisfies the difficulty target. The expected number of hash evaluations to find a valid block is `2^d`, where `d` is the difficulty in bits.

### 5.3 Fork Selection

When the network encounters competing chains, the chain with the greatest cumulative proof-of-work is selected. Cumulative work is the sum of `2^d_i` for each block `i` in the chain. This ensures that difficulty reductions do not create incentives for chain splitting.

## 6. Difficulty Adjustment

### 6.1 Target Block Time

The target block time is 60 seconds (1 minute).

### 6.2 Legacy Algorithm (Blocks 0-599)

During the initial launch phase, difficulty adjusts every 50 blocks. The ratio of expected time (3,000 seconds) to actual time is computed and clamped to [0.25, 4.0]. The adjustment in bits is `round(log2(ratio))`, capped at ±2 bits per adjustment.

### 6.3 LWMA Algorithm (Block 600+)

Beginning at block 600, difficulty adjusts every block using a Linearly Weighted Moving Average (LWMA) over the previous 20 blocks. Recent blocks are weighted more heavily:

```
weight(i) = i + 1,  for i in [0, 19]
weighted_avg = sum(adjusted_time[i] * weight(i)) / sum(weight(i))
```

Solve times are normalized by the difficulty at which they were mined. If block `i` was mined at difficulty `b_i` and the current difficulty is `d`:

```
delta = d - b_i
adjusted_time[i] = solve_time[i] * 2^delta    (if delta > 0)
adjusted_time[i] = solve_time[i] / 2^|delta|  (if delta < 0)
```

This normalization prevents oscillation when difficulty changes rapidly.

The adjustment rule is:

| Condition | Adjustment |
|-----------|------------|
| weighted_avg < 42s (0.7 * target) | +1 bit |
| weighted_avg > 78s (1.3 * target) | -1 bit |

The maximum adjustment is ±1 bit per block.

## 7. Supply and Economics

### 7.1 Monetary Policy

The total supply of DLT is capped at 25,000,000. Supply is created exclusively through coinbase transactions in mined blocks. The block reward schedule is:

| Blocks | Reward (DLT) |
|--------|-------------|
| 0 - 249,999 | 50 |
| 250,000 - 499,999 | 25 |
| 500,000 - 749,999 | 12.5 |
| 750,000 - 999,999 | 6.25 |
| ... | halves every 250,000 blocks |

The reward at block height `h` is:

```
halvings = floor(h / 250,000)
reward = 50 * 100,000,000 >> halvings   (in base units)
```

After approximately 64 halvings, the reward reaches zero and miners are compensated entirely through transaction fees. The geometric series of rewards sums to:

```
250,000 * 50 * (1 + 1/2 + 1/4 + ...) = 250,000 * 50 * 2 = 25,000,000 DLT
```

### 7.2 Halving Interval

At 60-second block times, 250,000 blocks corresponds to approximately 174 days. The first halving is expected roughly 6 months after genesis.

## 8. Network Protocol

### 8.1 Transport

Nodes communicate over TCP using a custom binary protocol. Each message is prefixed with the magic bytes `0x44494C54` ("DILT") for stream identification.

### 8.2 Protocol Version

The current protocol version is 2. Nodes exchange `version` and `verack` messages during handshake, including their protocol version, block height, and service flags.

### 8.3 Service Flags

| Flag | Value | Meaning |
|------|-------|---------|
| SFNodeNetwork | 0x01 | Full node (serves blocks) |
| SFNodeMiner | 0x02 | Mining node |
| SFNodeAPI | 0x04 | HTTP API enabled |

### 8.4 Message Types

**Handshake:** `version`, `verack`

**Data exchange:** `inv` (inventory announcement), `getdata` (request), `block`, `tx`

**Synchronization:** `getheaders`, `headers`, `getblocks`, `blocks`

**Peer discovery:** `addr`, `getaddr`

**Mempool:** `mempool`

**Liveness:** `ping`, `pong` (with random nonces, every 2 minutes)

**Rejection:** `reject` (with reason code)

### 8.5 Synchronization

New nodes synchronize using a headers-first approach. The node requests up to 2,000 block headers per message, validates the proof-of-work chain, then downloads full blocks in batches of 500. This allows the node to verify the chain structure before downloading transaction data.

### 8.6 Peer Discovery

Nodes discover peers through:

1. **Seed nodes** — hardcoded bootstrap addresses at `*.dilithiumcoin.com:1701`
2. **Address gossip** — nodes exchange `addr` messages containing known peers, broadcast every 10 minutes
3. **Persistent peer database** — known addresses are saved to `peers.dat` and loaded on restart

Address records have a 4-hour TTL and are pruned after 7 days of inactivity. A random sample of known addresses is TCP-probed every 15 minutes to verify reachability. Subnet diversity (/16 IPv4) is preferred when selecting outbound connections to resist eclipse attacks.

### 8.7 Connection Limits

| Parameter | Value |
|-----------|-------|
| Maximum outbound connections | 16 |
| Maximum inbound connections | 64 |
| Minimum outbound target | 8 |
| Maximum address book entries | 2,000 |

### 8.8 Peer Scoring

Nodes track misbehavior using a point-based scoring system inspired by Bitcoin Core. Penalties are assigned for protocol violations:

| Violation | Points |
|-----------|--------|
| Invalid block (bad PoW, hash, transactions) | 100 |
| Invalid transaction relay | 10 |
| Message decode failure | 1 |
| Message spam (>1,000 msg/10s) | 100 |

A peer is banned for 24 hours when its cumulative score reaches 100 points. This allows transient errors (decode failures) while immediately banning peers that relay invalid blocks.

## 9. Mining

### 9.1 Solo Mining

Solo miners construct block templates from the mempool, create a coinbase transaction, and search for a valid nonce. Mining can be performed using the CPU miner (`dilithium-miner`) or the hybrid CPU/GPU miner (`dilithium-cpu-gpu-miner`).

### 9.2 GPU Mining

GPU mining uses NVIDIA CUDA to parallelize SHA-256 hash evaluation. The implementation employs a midstate optimization: the SHA-256 internal state is computed on the CPU for the fixed block prefix (all fields except the nonce), and only the variable suffix containing the nonce is hashed on the GPU. This reduces per-nonce GPU work by 50-80%.

The CUDA kernel uses constant memory for the midstate and prefix data (enabling cached broadcast to all threads), warp-level parallelism with 256 threads per block, and atomic compare-and-swap for early termination when a solution is found.

### 9.3 Pool Mining

Pool mining is supported through a JSON-over-TCP protocol. The pool server distributes work templates to connected workers with a share difficulty set 8 bits below the block difficulty (256x easier). Workers submit shares that meet the share target; if a share also meets the full block difficulty, the pool submits it to the network.

Rewards are distributed proportionally based on submitted shares. Share counts reset after each block is found.

### 9.4 Performance

| Hardware | Mode | Approximate Hashrate |
|----------|------|---------------------|
| Intel i7 (8 threads) | CPU | ~80 MH/s |
| Apple M4 (10 threads) | CPU | ~180 MH/s |
| NVIDIA RTX 3080 | GPU | ~1,400 MH/s |
| NVIDIA RTX 4090 | GPU | ~5,000 MH/s |

## 10. Wallet Software

Dilithium provides both a command-line interface (`dilithium-cli`) and a desktop GUI wallet (`dilithium-wallet`) built with the Wails framework (Go backend, Svelte frontend).

Both tools support:

- Wallet creation with 24-word BIP39 recovery phrase
- Wallet restoration from recovery phrase
- Optional passphrase encryption (AES-256-GCM)
- Balance queries and transaction history
- Transaction signing and submission with configurable fees
- Automatic node discovery across seed nodes

The desktop wallet automatically discovers the best available node by probing cached nodes, localhost, and seed nodes, selecting by reachability, block height, and latency.

## 11. REST API

Every node exposes an HTTP REST API (default port 8001) for querying the blockchain and submitting transactions. The API supports:

- Node status, block explorer statistics, and network census
- Block and transaction lookup
- Address balance and transaction history
- Transaction submission
- Peer management
- Mempool inspection

Rate limiting is applied at 600 requests/minute for reads, 120 for writes, and 60 for mining operations.

## 12. Conclusion

Dilithium demonstrates that a fully post-quantum cryptocurrency can be built today using standardized cryptography. By using CRYSTALS-Dilithium Mode3 exclusively — with no legacy signature algorithms — the system avoids the complexity of hybrid schemes and migration paths. The SHA-256 proof-of-work mechanism provides a well-understood consensus layer, while the LWMA difficulty adjustment and bit-granularity targets enable responsive, smooth mining difficulty. The 25,000,000 DLT supply cap, halving schedule, and transaction fee system provide a deflationary monetary policy. The network protocol, with headers-first sync, peer scoring, and subnet-diverse peer selection, ensures robust decentralized operation.

## References

1. Ducas, L., Kiltz, E., Lepoint, T., Lyubashevsky, V., Schwabe, P., Seiler, G., Stehlé, D. (2024). CRYSTALS-Dilithium: A Lattice-Based Digital Signature Scheme. NIST FIPS 204 (ML-DSA).

2. Nakamoto, S. (2008). Bitcoin: A Peer-to-Peer Electronic Cash System.

3. Zahnentferner, J. (2018). Linearly Weighted Moving Average (LWMA) Difficulty Adjustment Algorithm.

4. NIST (2024). Post-Quantum Cryptography Standardization. https://csrc.nist.gov/projects/post-quantum-cryptography

5. Bitcoin Core Developers (2024). Bitcoin P2P Network Protocol. https://developer.bitcoin.org/reference/p2p_networking.html
