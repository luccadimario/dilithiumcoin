# Dilithium Smart Contracts — Reference Document

## 1. Overview

Dilithium smart contracts bring programmability to the Dilithium blockchain. Contracts are programs stored on-chain as bytecode, executed by a stack-based virtual machine (VM) embedded in every node. When a user submits a contract transaction, every node executes the same bytecode deterministically and arrives at the same resulting state.

**Key differences from Ethereum:**

- **Not EVM-compatible.** Dilithium uses its own opcode set (EVM-inspired but simplified).
- **No Merkle Patricia Trie.** Contract storage is a flat key-value store per contract, persisted as JSON.
- **SHA-256 everywhere.** Dilithium uses SHA-256 (not Keccak) for hashing, including ABI function selectors.
- **Post-quantum signatures.** Transactions are signed with Dilithium (CRYSTALS-Dilithium mode3), not ECDSA.
- **Account-based model.** Dilithium already uses accounts (not UTXO). Contract accounts are a natural extension.
- **No native ERC-20 standard.** Token contracts can be built, but there is no protocol-level token standard yet.

## 2. Transaction Types

Transactions carry a `Type` field (uint8) that determines how they are processed:

| Type | Name | Purpose |
|------|------|---------|
| 0 | Transfer | Standard value transfer between accounts (backward compatible) |
| 1 | Deploy | Deploy new contract bytecode to the chain |
| 2 | Call | Invoke a function on an existing contract |

### Transaction Fields

```json
{
  "type":       0,
  "from":       "sender_address_hex",
  "to":         "recipient_or_contract_address",
  "amount":     1000000,
  "fee":        10000,
  "gas_limit":  500000,
  "gas_price":  100,
  "data":       "hex_encoded_bytecode_or_calldata",
  "timestamp":  1700000000,
  "signature":  "hex_dilithium_signature",
  "public_key": "hex_dilithium_pubkey"
}
```

**Type 0 (Transfer):**
- `to`: recipient wallet address
- `data`: optional memo (max 256 bytes)
- `gas_limit`, `gas_price`: ignored (zero)

**Type 1 (Deploy):**
- `to`: empty string (contract address is derived)
- `data`: hex-encoded deployment bytecode (max 24 KB)
- `gas_limit`: must be > 0, max 10,000,000
- `gas_price`: must be >= 100 (minimum gas price)
- `amount`: initial balance to send to the contract

**Type 2 (Call):**
- `to`: contract address
- `data`: hex-encoded calldata (function selector + arguments, max 8 KB)
- `gas_limit`: must be > 0, max 10,000,000
- `gas_price`: must be >= 100
- `amount`: value to send to the contract (can be 0)

### Signing Format

The signing data includes all fields for replay protection:

```
{NetworkName}:{type}{from}{to}{amount}{fee}{gasLimit}{gasPrice}{timestamp}:{data}
```

For Type 0 transactions without gas fields, the format falls back to the existing format for backward compatibility.

## 3. Contract Accounts

Contract accounts differ from wallet accounts:

- **Address derivation:** `SHA256(deployerAddress + deployerNonce)[:40]` (hex, 20 bytes)
- **Immutable code:** Once deployed, a contract's bytecode cannot be changed.
- **Persistent storage:** Each contract has its own key-value store.
- **Balance:** Contracts can hold DLT and transfer it via the CALL opcode.
- **No private key:** Contracts cannot initiate transactions; they only execute in response to calls.

The deployer's nonce is the number of contracts they have previously deployed. This ensures unique addresses.

## 4. VM Architecture

The Dilithium VM is a stack-based bytecode interpreter with 256-bit words.

**Stack:** Fixed-size array of 1024 uint256 values. Operations push/pop from the top.

**Memory:** Byte-addressable, dynamically expanding. Accessed via MLOAD, MSTORE, MSTORE8. Memory is zeroed at the start of each execution context and is not persisted.

**Storage:** 256-bit key to 256-bit value mapping, persisted per-contract between transactions. Accessed via SLOAD/SSTORE.

**Program counter:** Points to the current opcode in the bytecode. Advances by 1 after most opcodes, or jumps to a JUMPDEST target.

**Gas:** Each opcode costs a fixed amount of gas. The VM tracks remaining gas and halts with an out-of-gas error if it reaches zero.

**Execution context:** Contains caller, contract address, origin, value, gas price, block info, and calldata.

**Word size:** 256 bits (uint256), stored as four uint64 values in little-endian order.

## 5. Opcode Reference

### Arithmetic

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x01 | ADD | 3 | 2→1 | a + b (mod 2^256) |
| 0x02 | SUB | 3 | 2→1 | a - b (mod 2^256) |
| 0x03 | MUL | 5 | 2→1 | a * b (mod 2^256) |
| 0x04 | DIV | 5 | 2→1 | a / b (0 if b==0) |
| 0x05 | MOD | 5 | 2→1 | a % b (0 if b==0) |
| 0x06 | ADDMOD | 8 | 3→1 | (a + b) % N |
| 0x07 | MULMOD | 8 | 3→1 | (a * b) % N |
| 0x08 | EXP | 10+10/byte | 2→1 | a ** b |

### Comparison

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x10 | LT | 3 | 2→1 | 1 if a < b, else 0 |
| 0x11 | GT | 3 | 2→1 | 1 if a > b, else 0 |
| 0x12 | EQ | 3 | 2→1 | 1 if a == b, else 0 |
| 0x13 | ISZERO | 3 | 1→1 | 1 if a == 0, else 0 |

### Bitwise

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x16 | AND | 3 | 2→1 | a & b |
| 0x17 | OR | 3 | 2→1 | a \| b |
| 0x18 | XOR | 3 | 2→1 | a ^ b |
| 0x19 | NOT | 3 | 1→1 | ~a (bitwise complement) |
| 0x1A | BYTE | 3 | 2→1 | ith byte of b (0 = most significant) |
| 0x1B | SHL | 3 | 2→1 | b << a |
| 0x1C | SHR | 3 | 2→1 | b >> a (logical) |

### Crypto

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x20 | SHA256 | 30+6/word | 2→1 | SHA-256 of memory[offset:offset+length] |

### Context

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x30 | ADDRESS | 2 | 0→1 | Current contract address (as uint256) |
| 0x31 | BALANCE | 400 | 1→1 | Balance of given address |
| 0x32 | ORIGIN | 2 | 0→1 | tx.origin (original transaction signer) |
| 0x33 | CALLER | 2 | 0→1 | msg.sender (immediate caller) |
| 0x34 | CALLVALUE | 2 | 0→1 | DLT sent with this call |
| 0x35 | CALLDATALOAD | 3 | 1→1 | Load 32 bytes from calldata at offset |
| 0x36 | CALLDATASIZE | 2 | 0→1 | Length of calldata in bytes |
| 0x37 | CALLDATACOPY | 3+3/word | 3→0 | Copy calldata to memory |
| 0x38 | CODESIZE | 2 | 0→1 | Length of executing code |
| 0x39 | CODECOPY | 3+3/word | 3→0 | Copy code to memory |

### Block Info

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x42 | TIMESTAMP | 2 | 0→1 | Current block timestamp |
| 0x43 | NUMBER | 2 | 0→1 | Current block height |
| 0x44 | DIFFICULTY | 2 | 0→1 | Current block difficulty bits |
| 0x45 | GASLIMIT | 2 | 0→1 | Transaction gas limit |

### Stack, Memory, Storage

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x50 | POP | 2 | 1→0 | Discard top stack item |
| 0x51 | MLOAD | 3 | 1→1 | Load 32-byte word from memory |
| 0x52 | MSTORE | 3 | 2→0 | Store 32-byte word to memory |
| 0x53 | MSTORE8 | 3 | 2→0 | Store single byte to memory |
| 0x54 | SLOAD | 800 | 1→1 | Load from persistent storage |
| 0x55 | SSTORE | 5000/20000 | 2→0 | Store to persistent storage (5000 update, 20000 new) |
| 0x59 | MSIZE | 2 | 0→1 | Current memory size in bytes |
| 0x5A | GAS | 2 | 0→1 | Remaining gas |

### Flow Control

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x56 | JUMP | 8 | 1→0 | Unconditional jump to destination |
| 0x57 | JUMPI | 10 | 2→0 | Jump if condition is non-zero |
| 0x5B | JUMPDEST | 1 | 0→0 | Valid jump target marker |

### Push (0x60 - 0x7F)

PUSH1 through PUSH32: push 1 to 32 immediate bytes onto the stack. Gas: 3.

### Dup (0x80 - 0x8F)

DUP1 through DUP16: duplicate the Nth stack item to the top. Gas: 3.

### Swap (0x90 - 0x9F)

SWAP1 through SWAP16: swap the top item with the (N+1)th item. Gas: 3.

### Logging

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0xA0 | LOG0 | 375+8/byte | 2→0 | Emit event with 0 topics |
| 0xA1 | LOG1 | 375+375+8/byte | 3→0 | Emit event with 1 topic |
| 0xA2 | LOG2 | 375+750+8/byte | 4→0 | Emit event with 2 topics |
| 0xA3 | LOG3 | 375+1125+8/byte | 5→0 | Emit event with 3 topics |
| 0xA4 | LOG4 | 375+1500+8/byte | 6→0 | Emit event with 4 topics |

### System

| Hex | Name | Gas | Stack (in→out) | Description |
|-----|------|-----|----------------|-------------|
| 0x00 | STOP | 0 | 0→0 | Halt execution successfully |
| 0xF1 | CALL | 700+value+gas | 7→1 | Call another contract |
| 0xF3 | RETURN | 0 | 2→0 | Return data from memory and halt |
| 0xFD | REVERT | 0 | 2→0 | Revert state changes and return data |
| 0xFF | SELFDESTRUCT | 5000 | 1→0 | Destroy contract, send balance to address |

## 6. Gas Model

Gas is the unit of computational cost. Every opcode consumes a fixed amount of gas.

**Gas pricing:**
- Each opcode has a fixed gas cost (see opcode table above).
- `gasPrice` is denominated in base units per gas unit (minimum: 100, i.e., 0.000001 DLT per gas unit).
- `gasLimit` is the maximum gas the sender is willing to consume (max: 10,000,000).

**Fee calculation:**
1. Sender pre-pays `gasLimit * gasPrice` (deducted from balance before execution).
2. VM executes, tracking gas consumed.
3. After execution: refund `(gasLimit - gasUsed) * gasPrice` to sender.
4. Miner receives `gasUsed * gasPrice` (included in coinbase reward).

**Out-of-gas:** If gas runs out during execution, all state changes revert, but all gas is consumed (no refund).

**REVERT:** State changes revert, but only gas consumed up to the REVERT point is charged (remaining gas is refunded).

**Deployment gas:** `BaseDeployGas (32,000) + GasPerDeployByte (200) * len(bytecode) + execution gas`.

**Call gas:** `BaseCallGas (21,000) + GasPerByte (4) * len(calldata) + execution gas`.

## 7. Contract Storage

Each contract has a flat key-value store:

- **Keys:** 32-byte (256-bit) values, hex-encoded.
- **Values:** 32-byte (256-bit) values.
- **Access:** SLOAD reads a value (800 gas), SSTORE writes a value (5,000 for update, 20,000 for new key).
- **Persistence:** Storage survives across transactions. State changes are committed atomically after successful block execution.
- **Revert:** On execution failure, all storage changes from that transaction are rolled back.

**Disk format:** Contracts are stored as JSON files in `~/.dilithium/contracts/{address}.json`:

```json
{
  "address": "abc123...",
  "code": "hex_encoded_bytecode",
  "storage": {
    "0000...0001": "0000...0064",
    "0000...0002": "0000...00ff"
  },
  "balance": 1000000,
  "nonce": 0
}
```

## 8. ABI Encoding

Dilithium uses a simplified ABI encoding compatible with Ethereum's ABI layout but using SHA-256 for selectors.

**Function selector:** First 4 bytes of `SHA256(functionSignature)`.

Example: `transfer(address,uint256)` → `SHA256("transfer(address,uint256)")[:4]`

**Argument encoding:** Each argument is left-padded to 32 bytes and concatenated after the selector.

**Supported types:**
- `uint256` — 256-bit unsigned integer
- `int256` — 256-bit signed integer (two's complement)
- `address` — 20-byte address, left-padded to 32 bytes
- `bool` — 0 or 1, padded to 32 bytes
- `bytes32` — 32 raw bytes
- `bytes` — dynamic-length bytes (offset + length + data)

**Calldata layout:**

```
[4 bytes: function selector][32 bytes: arg0][32 bytes: arg1]...
```

## 9. Contract-to-Contract Calls

The CALL opcode enables one contract to invoke another:

**Stack inputs (7):** gas, to, value, argsOffset, argsLength, retOffset, retLength

**Behavior:**
1. Create a new execution context with the target contract's code.
2. msg.sender becomes the calling contract's address.
3. tx.origin remains the original transaction signer.
4. If value > 0, transfer DLT from caller to callee.
5. Forward up to the specified gas (capped at 63/64 of remaining gas).
6. On success: push 1, copy return data to caller's memory.
7. On failure: push 0, all callee state changes revert, but gas up to the failure point is consumed.

**Call depth:** Maximum 256 nested calls. Exceeding this depth causes the call to fail.

**Reentrancy:** Dilithium does not enforce reentrancy guards at the protocol level. Contract authors should use storage-based locks (checks-effects-interactions pattern).

## 10. Consensus Rules

**Fork activation:** Smart contracts activate at `SmartContractForkHeight` (configurable, var for testnet override).

- Before fork height: Type 1/2 transactions are rejected by all validation.
- After fork height: Type 1/2 transactions are processed through the VM.
- Type 0 transactions work identically before and after the fork.

**Block validation with contracts:**

```
ValidateBlockTransactions(block, previousBlocks):
  1. Build balance map from previous blocks (existing logic)
  2. For each transaction:
     a. Type 0: existing balance check logic
     b. Type 1 (deploy):
        - Deduct gasLimit * gasPrice from sender
        - Execute deployment bytecode → get runtime code
        - Success: create contract account, refund unused gas
        - Failure: revert, consume all gas
     c. Type 2 (call):
        - Deduct gasLimit * gasPrice from sender
        - Load contract, execute with calldata
        - Success: commit storage, transfer value, refund unused gas
        - Failure: revert storage, consume gas
  3. Coinbase = blockReward + sum(gasUsed * gasPrice for all txs)
```

**Determinism:** The VM has no floating point, no randomness, no external I/O. Block timestamp and height are fixed per block. All nodes produce identical state from identical blocks.

## 11. API Endpoints

| Method | Endpoint | Purpose |
|--------|----------|---------|
| POST | `/contract/deploy` | Submit a deploy transaction |
| POST | `/contract/call` | Submit a call transaction |
| POST | `/contract/query` | Read-only call (no state change, no gas cost) |
| GET | `/contract/code?address=X` | Get contract bytecode |
| GET | `/contract/storage?address=X&key=K` | Read a storage slot |
| POST | `/contract/estimate-gas` | Estimate gas for a call |

### POST /contract/query

Executes the VM in read-only mode against the current state. No transaction is created, no gas is consumed, and no state changes are persisted. Useful for reading token balances, checking contract state, etc.

Request body:

```json
{
  "to": "contract_address",
  "from": "caller_address",
  "data": "hex_encoded_calldata",
  "value": 0
}
```

Response includes `return_data` (hex-encoded) and `gas_used`.

### POST /contract/estimate-gas

Runs the VM and returns the gas that would be consumed, without persisting state.

## 12. CLI Commands

```
dilithium-cli contract deploy --code <hex> --gas-limit 1000000 --gas-price 100
dilithium-cli contract call --to <addr> --data <hex> --value 0 --gas-limit 500000
dilithium-cli contract query --to <addr> --data <hex>
dilithium-cli contract code --address <addr>
dilithium-cli contract storage --address <addr> --key <hex>
```

All commands accept `--node <url>` and `--wallet <path>` flags.

## 13. Error Handling

**Out-of-gas:** All state changes revert. All gas is consumed (sender pays full gasLimit * gasPrice). Execution halts immediately.

**REVERT opcode:** State changes revert. Gas consumed up to the REVERT is charged; remaining gas is refunded. Return data (if any) is available to the caller.

**STOP opcode:** Execution halts successfully. State changes are committed. Remaining gas is refunded.

**RETURN opcode:** Like STOP, but also returns data to the caller.

**Invalid opcode:** Treated as out-of-gas. All state changes revert, all gas consumed.

**Stack underflow/overflow:** Treated as out-of-gas.

**Invalid jump destination:** Treated as out-of-gas (jump target must be a JUMPDEST opcode).

**Nested call failure:** Only the inner call's state changes revert. The outer call continues with a 0 (failure) pushed onto its stack.

## 14. Security Considerations

**Reentrancy:** The CALL opcode transfers control to another contract, which may call back. Use checks-effects-interactions pattern: update state before making external calls.

**Gas limits:** MaxGasLimit (10,000,000) prevents denial-of-service via infinite loops. MaxCallDepth (256) prevents stack overflow attacks.

**Integer overflow:** All arithmetic is modular (mod 2^256). Contracts must check for overflow if needed.

**Stack depth attacks:** Call depth is limited to 256. Contracts should not rely on calls succeeding.

**Determinism:** No floating point, no randomness, no timestamps other than block timestamp. All nodes must compute identical results.

**Code size limit:** Maximum 24 KB prevents excessive storage and memory usage during execution.

**SELFDESTRUCT:** Removes a contract permanently and sends its balance to a specified address. Cannot be undone.

## 15. Examples

### Simple Counter Contract

A contract that stores a counter and provides increment/get functions.

**Function signatures:**
- `increment()` → selector: `SHA256("increment()")[:4]`
- `get()` → selector: `SHA256("get()")[:4]`

**Storage layout:**
- Slot 0: counter value

**Bytecode (pseudocode):**
```
// Read function selector from calldata
PUSH1 0x00
CALLDATALOAD
PUSH1 0xE0
SHR                    // Get first 4 bytes

// Check if increment()
DUP1
PUSH4 <increment_selector>
EQ
PUSH1 <increment_label>
JUMPI

// Check if get()
DUP1
PUSH4 <get_selector>
EQ
PUSH1 <get_label>
JUMPI

// Fallback: revert
PUSH1 0x00
PUSH1 0x00
REVERT

// increment:
JUMPDEST
PUSH1 0x00
SLOAD              // Load current value
PUSH1 0x01
ADD                // Increment
PUSH1 0x00
SSTORE             // Store back
STOP

// get:
JUMPDEST
PUSH1 0x00
SLOAD              // Load current value
PUSH1 0x00
MSTORE             // Store in memory
PUSH1 0x20
PUSH1 0x00
RETURN             // Return 32 bytes from memory
```

**Deploy:** Submit Type 1 transaction with the bytecode in `data`.

**Call increment:** Submit Type 2 transaction with `data = <increment_selector>`.

**Query get:** POST to `/contract/query` with `data = <get_selector>`. Returns the counter value.
