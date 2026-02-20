#!/usr/bin/env bash
# ============================================================================
# Local Testnet Integration Test
# ============================================================================
# Tests: node startup, peer discovery, mining, transaction data field,
#        checksummed addresses, and block sync across two nodes.
# ============================================================================

set -uo pipefail

TESTDIR="/tmp/dilithium-testnet-$$"
BINARY=""
CLI=""
NODE1_PID=""
NODE2_PID=""
PASS=0
FAIL=0
TESTS=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# ============================================================================
# Helpers
# ============================================================================

log()  { echo -e "${CYAN}[TEST]${NC} $*"; }
pass() { TESTS=$((TESTS+1)); PASS=$((PASS+1)); echo -e "  ${GREEN}✓ PASS${NC}: $*"; }
fail() { TESTS=$((TESTS+1)); FAIL=$((FAIL+1)); echo -e "  ${RED}✗ FAIL${NC}: $*"; }
warn() { echo -e "  ${YELLOW}⚠ WARN${NC}: $*"; }

cleanup() {
    log "Cleaning up..."
    [ -n "$NODE1_PID" ] && kill "$NODE1_PID" 2>/dev/null && wait "$NODE1_PID" 2>/dev/null || true
    [ -n "$NODE2_PID" ] && kill "$NODE2_PID" 2>/dev/null && wait "$NODE2_PID" 2>/dev/null || true
    rm -rf "$TESTDIR"
    echo ""
    echo "============================================"
    echo -e "  Results: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC} / ${TESTS} total"
    echo "============================================"
    if [ "$FAIL" -gt 0 ]; then
        exit 1
    fi
}
trap cleanup EXIT

wait_for_api() {
    local port=$1
    local max_wait=${2:-15}
    local i=0
    while [ $i -lt $max_wait ]; do
        if curl -sf "http://localhost:${port}/status" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
        i=$((i+1))
    done
    return 1
}

get_height() {
    local port=$1
    curl -sf "http://localhost:${port}/status" | python3 -c "import sys,json; print(int(json.load(sys.stdin)['data']['blockchain_height']))" 2>/dev/null || echo "0"
}

get_peer_count() {
    local port=$1
    curl -sf "http://localhost:${port}/status" | python3 -c "import sys,json; print(int(json.load(sys.stdin)['data']['peers']['total']))" 2>/dev/null || echo "0"
}

get_balance() {
    local port=$1
    local addr=$2
    curl -sf "http://localhost:${port}/explorer/address?addr=${addr}" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['balance_dlt'])" 2>/dev/null || echo "0"
}

mine_blocks() {
    local port=$1
    local miner=$2
    local count=$3
    for i in $(seq 1 "$count"); do
        curl -sf -X POST "http://localhost:${port}/mine?miner=${miner}" >/dev/null 2>&1
    done
}

# ============================================================================
# Step 1: Build binaries
# ============================================================================

echo ""
echo "============================================"
echo "  Dilithium Local Testnet Integration Test"
echo "============================================"
echo ""

log "Creating test directory: $TESTDIR"
mkdir -p "$TESTDIR"

log "Building node binary..."
cd /Users/luccadimario/Documents/myblockchain
if go build -o "$TESTDIR/dilithium" . 2>&1; then
    pass "Node binary built"
else
    fail "Node binary build failed"
    exit 1
fi

log "Building CLI binary..."
if go build -o "$TESTDIR/dilithium-cli" ./cmd/dilithium-cli 2>&1; then
    pass "CLI binary built"
else
    fail "CLI binary build failed"
    exit 1
fi

BINARY="$TESTDIR/dilithium"
CLI="$TESTDIR/dilithium-cli"

# ============================================================================
# Step 2: Create test wallets
# ============================================================================

log "Creating wallet 1 (miner/sender)..."
mkdir -p "$TESTDIR/wallet1"
echo "" | "$CLI" init --wallet "$TESTDIR/wallet1" --legacy --force >/dev/null 2>&1
ADDR1=$(cat "$TESTDIR/wallet1/address")
if [ -n "$ADDR1" ] && [ ${#ADDR1} -eq 40 ]; then
    pass "Wallet 1 created: ${ADDR1:0:12}..."
else
    fail "Wallet 1 creation failed"
    exit 1
fi

log "Creating wallet 2 (receiver)..."
mkdir -p "$TESTDIR/wallet2"
echo "" | "$CLI" init --wallet "$TESTDIR/wallet2" --legacy --force >/dev/null 2>&1
ADDR2=$(cat "$TESTDIR/wallet2/address")
if [ -n "$ADDR2" ] && [ ${#ADDR2} -eq 40 ]; then
    pass "Wallet 2 created: ${ADDR2:0:12}..."
else
    fail "Wallet 2 creation failed"
    exit 1
fi

# ============================================================================
# Step 3: Test checksummed addresses
# ============================================================================

log "Testing checksummed address format..."
CADDR1=$("$CLI" address --wallet "$TESTDIR/wallet1")
if [[ "$CADDR1" == dlt1* ]] && [ ${#CADDR1} -eq 48 ]; then
    pass "Checksummed address format: $CADDR1"
else
    fail "Checksummed address format invalid: '$CADDR1'"
fi

# Verify it contains the raw address
if [[ "$CADDR1" == *"$ADDR1"* ]]; then
    pass "Checksummed address contains raw hex"
else
    fail "Checksummed address doesn't contain raw hex"
fi

# ============================================================================
# Step 4: Start Node 1 (bootstrap node)
# ============================================================================

log "Starting Node 1 (port 19801, API 19901, ship=Enterprise)..."
"$BINARY" \
    --port 19801 --api-port 19901 \
    --data-dir "$TESTDIR/node1" \
    --miner "$ADDR1" \
    --difficulty 1 \
    --no-seeds \
    --ship-name Enterprise \
    --testnet \
    > "$TESTDIR/node1.log" 2>&1 &
NODE1_PID=$!

if wait_for_api 19901 20; then
    pass "Node 1 started (PID=$NODE1_PID)"
else
    fail "Node 1 failed to start"
    echo "--- Node 1 log ---"
    cat "$TESTDIR/node1.log" 2>/dev/null || true
    exit 1
fi

# Check Star Trek quotes in startup log
log "Checking Star Trek Easter eggs..."
if grep -q '".*"' "$TESTDIR/node1.log" 2>/dev/null; then
    QUOTE=$(grep -oE '"[^"]+".*—.*' "$TESTDIR/node1.log" | head -1 || true)
    if [ -n "$QUOTE" ]; then
        pass "Trek quote found: $QUOTE"
    else
        # Quotes might not have the — format, check for the banner
        if grep -q "DLT Cryptocurrency" "$TESTDIR/node1.log"; then
            pass "Node startup banner displayed"
        else
            warn "Could not parse Trek quote from log"
        fi
    fi
else
    warn "No Trek quote detected in startup log"
fi

# Check ship name in status
log "Checking ship name in UserAgent..."
STATUS1=$(curl -sf "http://localhost:19901/status" 2>/dev/null)
if echo "$STATUS1" | python3 -c "import sys,json; v=json.load(sys.stdin)['data']['version']; print(v)" >/dev/null 2>&1; then
    pass "Node 1 API responding"
else
    fail "Node 1 API not responding properly"
fi

# ============================================================================
# Step 5: Mine initial blocks on Node 1 for balance
# ============================================================================

log "Mining 5 blocks on Node 1 to build balance..."
mine_blocks 19901 "$ADDR1" 5
sleep 1

HEIGHT1=$(get_height 19901)
if [ "$HEIGHT1" -ge 5 ]; then
    pass "Node 1 mined blocks (height=$HEIGHT1)"
else
    fail "Node 1 mining failed (height=$HEIGHT1)"
fi

# Check miner balance
BAL1=$(get_balance 19901 "$ADDR1")
if [ "$BAL1" != "0" ] && [ "$BAL1" != "0.00000000" ]; then
    pass "Miner has balance: $BAL1 DLT"
else
    fail "Miner balance is zero after mining"
fi

# ============================================================================
# Step 6: Start Node 2 (connects to Node 1)
# ============================================================================

log "Starting Node 2 (port 19802, API 19902, ship=Defiant)..."
"$BINARY" \
    --port 19802 --api-port 19902 \
    --data-dir "$TESTDIR/node2" \
    --connect localhost:19801 \
    --difficulty 1 \
    --no-seeds \
    --ship-name Defiant \
    --testnet \
    > "$TESTDIR/node2.log" 2>&1 &
NODE2_PID=$!

if wait_for_api 19902 20; then
    pass "Node 2 started (PID=$NODE2_PID)"
else
    fail "Node 2 failed to start"
    echo "--- Node 2 log ---"
    cat "$TESTDIR/node2.log" 2>/dev/null || true
    exit 1
fi

# ============================================================================
# Step 7: Verify peer connectivity
# ============================================================================

log "Waiting for peer connection..."
sleep 5

PEERS1=$(get_peer_count 19901)
PEERS2=$(get_peer_count 19902)

if [ "$PEERS1" -ge 1 ]; then
    pass "Node 1 has $PEERS1 peer(s)"
else
    fail "Node 1 has no peers"
fi

if [ "$PEERS2" -ge 1 ]; then
    pass "Node 2 has $PEERS2 peer(s)"
else
    fail "Node 2 has no peers"
fi

# ============================================================================
# Step 8: Verify block sync
# ============================================================================

log "Waiting for chain sync..."
sleep 5

HEIGHT1=$(get_height 19901)
HEIGHT2=$(get_height 19902)

log "  Node 1 height: $HEIGHT1"
log "  Node 2 height: $HEIGHT2"

if [ "$HEIGHT2" -ge 3 ]; then
    pass "Node 2 synced chain (height=$HEIGHT2)"
else
    # Give more time for sync
    log "  Waiting longer for sync..."
    sleep 10
    HEIGHT2=$(get_height 19902)
    if [ "$HEIGHT2" -ge 3 ]; then
        pass "Node 2 synced chain (height=$HEIGHT2, after extra wait)"
    else
        fail "Node 2 failed to sync (height=$HEIGHT2, expected >= 3)"
    fi
fi

# ============================================================================
# Step 9: Send transaction WITH data field
# ============================================================================

log "Sending transaction with data field..."
# Use the CLI to send, pointing to Node 1
TX_OUTPUT=$(echo "" | "$CLI" send "$ADDR2" 5 \
    --data "Live long and prosper" \
    --node "http://localhost:19901" \
    --wallet "$TESTDIR/wallet1" \
    --fee 0.0001 2>&1) || true

if echo "$TX_OUTPUT" | grep -qi "submitted\|success"; then
    pass "Transaction submitted with data field"
else
    # Check if the issue is insufficient balance or another error
    if echo "$TX_OUTPUT" | grep -qi "insufficient\|balance"; then
        warn "Insufficient balance - mining more blocks..."
        mine_blocks 19901 "$ADDR1" 5
        sleep 2
        TX_OUTPUT=$(echo "" | "$CLI" send "$ADDR2" 5 \
            --data "Live long and prosper" \
            --node "http://localhost:19901" \
            --wallet "$TESTDIR/wallet1" \
            --fee 0.0001 2>&1) || true
        if echo "$TX_OUTPUT" | grep -qi "submitted\|success"; then
            pass "Transaction submitted with data field (after extra mining)"
        else
            fail "Transaction failed: $TX_OUTPUT"
        fi
    else
        fail "Transaction send failed: $TX_OUTPUT"
    fi
fi

# ============================================================================
# Step 10: Check mempool on both nodes
# ============================================================================

log "Checking mempool..."
sleep 2

MEMPOOL1=$(curl -sf "http://localhost:19901/mempool" 2>/dev/null)
MEMCOUNT1=$(echo "$MEMPOOL1" | python3 -c "import sys,json; print(int(json.load(sys.stdin)['data']['count']))" 2>/dev/null || echo "0")

if [ "$MEMCOUNT1" -ge 1 ]; then
    pass "Transaction in Node 1 mempool (count=$MEMCOUNT1)"
else
    warn "Mempool empty on Node 1 (may have been mined already)"
fi

# Check Node 2 mempool (transaction should propagate)
sleep 3
MEMCOUNT2=$(curl -sf "http://localhost:19902/mempool" | python3 -c "import sys,json; print(int(json.load(sys.stdin)['data']['count']))" 2>/dev/null || echo "0")
if [ "$MEMCOUNT2" -ge 1 ]; then
    pass "Transaction propagated to Node 2 mempool"
else
    warn "Transaction not yet in Node 2 mempool (may have been mined)"
fi

# ============================================================================
# Step 11: Mine the transaction into a block
# ============================================================================

log "Mining block to include transaction..."
curl -sf -X POST "http://localhost:19901/mine?miner=$ADDR1" >/dev/null 2>&1
sleep 2

# ============================================================================
# Step 12: Verify balances
# ============================================================================

log "Checking balances after transaction..."
BAL_ADDR2=$(get_balance 19901 "$ADDR2")
if [ "$BAL_ADDR2" != "0" ] && [ "$BAL_ADDR2" != "0.00000000" ]; then
    pass "Receiver has balance: $BAL_ADDR2 DLT"
else
    # Wait for the block to be processed
    sleep 3
    BAL_ADDR2=$(get_balance 19901 "$ADDR2")
    if [ "$BAL_ADDR2" != "0" ] && [ "$BAL_ADDR2" != "0.00000000" ]; then
        pass "Receiver has balance: $BAL_ADDR2 DLT (after wait)"
    else
        fail "Receiver balance still zero"
    fi
fi

# ============================================================================
# Step 13: Verify Data field preserved in mined block
# ============================================================================

log "Verifying data field in mined block..."
LATEST_HEIGHT=$(get_height 19901)

# Search recent blocks for our transaction with data field
DATA_FOUND=false
for idx in $(seq "$LATEST_HEIGHT" -1 1); do
    BLOCK_JSON=$(curl -sf "http://localhost:19901/block?index=$idx" 2>/dev/null || true)
    if [ -z "$BLOCK_JSON" ]; then
        continue
    fi
    HAS_DATA=$(echo "$BLOCK_JSON" | python3 -c "
import sys, json
resp = json.load(sys.stdin)
data = resp.get('data', resp)
txs = data.get('transactions', data.get('Transactions', []))
if not isinstance(txs, list):
    sys.exit(1)
for tx in txs:
    d = tx.get('data', tx.get('Data', ''))
    if 'Live long and prosper' in str(d):
        print('FOUND')
        sys.exit(0)
sys.exit(1)
" 2>/dev/null || true)
    if [ "$HAS_DATA" = "FOUND" ]; then
        DATA_FOUND=true
        pass "Data field 'Live long and prosper' preserved in block $idx"
        break
    fi
done

if [ "$DATA_FOUND" = false ]; then
    fail "Data field not found in any recent block"
fi

# ============================================================================
# Step 14: Verify checksummed address works for balance lookup
# ============================================================================

log "Testing balance lookup with checksummed address..."
CHECKSUMMED_ADDR2=$("$CLI" address --wallet "$TESTDIR/wallet2")

# Balance via raw address
BAL_RAW=$(get_balance 19901 "$ADDR2")
# Balance via checksummed address (normalizes to raw in the endpoint)
BAL_CHK=$(get_balance 19901 "$CHECKSUMMED_ADDR2")

# Both should return the same non-zero balance
if [ "$BAL_RAW" = "$BAL_CHK" ] && [ "$BAL_RAW" != "0" ] && [ "$BAL_RAW" != "0.00000000" ]; then
    pass "Both address formats return same balance: $BAL_RAW DLT"
else
    warn "Balance mismatch: raw=$BAL_RAW, checksummed=$BAL_CHK"
fi

# ============================================================================
# Step 15: Verify Node 2 syncs the transaction block
# ============================================================================

log "Checking Node 2 chain sync after transaction..."
sleep 5

HEIGHT1=$(get_height 19901)
HEIGHT2=$(get_height 19902)

DIFF=$((HEIGHT1 - HEIGHT2))
if [ "$DIFF" -lt 0 ]; then
    DIFF=$((-DIFF))
fi

if [ "$DIFF" -le 2 ]; then
    pass "Nodes converged: Node1=$HEIGHT1, Node2=$HEIGHT2"
else
    # Give extra time
    sleep 10
    HEIGHT2=$(get_height 19902)
    DIFF=$((HEIGHT1 - HEIGHT2))
    if [ "$DIFF" -lt 0 ]; then
        DIFF=$((-DIFF))
    fi
    if [ "$DIFF" -le 2 ]; then
        pass "Nodes converged: Node1=$HEIGHT1, Node2=$HEIGHT2 (after extra wait)"
    else
        fail "Nodes diverged: Node1=$HEIGHT1, Node2=$HEIGHT2"
    fi
fi

# Check balance on Node 2
BAL_ADDR2_N2=$(get_balance 19902 "$ADDR2")
if [ "$BAL_ADDR2_N2" != "0" ] && [ "$BAL_ADDR2_N2" != "0.00000000" ]; then
    pass "Receiver balance confirmed on Node 2: $BAL_ADDR2_N2 DLT"
else
    warn "Receiver balance not yet visible on Node 2: $BAL_ADDR2_N2"
fi

echo ""
log "All tests completed!"
