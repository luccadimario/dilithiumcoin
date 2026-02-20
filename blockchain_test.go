package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

func TestGenesisBlock(t *testing.T) {
	t.Parallel()
	calc := GenesisBlock.CalculateHash()
	if calc != GenesisBlock.Hash {
		t.Fatalf("genesis hash mismatch: calculated %s, stored %s", calc, GenesisBlock.Hash)
	}
	if GenesisBlock.Index != 0 {
		t.Fatalf("genesis index = %d, want 0", GenesisBlock.Index)
	}
	if GenesisBlock.PreviousHash != "0" {
		t.Fatalf("genesis previous hash = %q, want %q", GenesisBlock.PreviousHash, "0")
	}
}

func TestNewBlockchain(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	if len(bc.Blocks) != 1 {
		t.Fatalf("new blockchain should have 1 block, got %d", len(bc.Blocks))
	}
	if !bc.IsValid() {
		t.Fatal("new blockchain should be valid")
	}
}

func TestBlockCalculateHash(t *testing.T) {
	t.Parallel()
	b := &Block{
		Index:        1,
		Timestamp:    1000,
		Transactions: []*Transaction{},
		PreviousHash: "abc",
		Nonce:        42,
		Difficulty:   2,
	}
	h1 := b.CalculateHash()
	h2 := b.CalculateHash()
	if h1 != h2 {
		t.Fatalf("CalculateHash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64", len(h1))
	}
}

func TestVerifyTransactionSignature(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100, 0)
	tx.Sign(w)
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("VerifyTransactionSignature failed: %v", err)
	}
}

func TestVerifyTransactionSignatureTampered(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	tx := NewTransaction(w.Address, "recipient", 100, 0)
	tx.Sign(w)

	// Tamper with amount
	tx.Amount = 999999
	if err := VerifyTransactionSignature(tx); err == nil {
		t.Fatal("expected error for tampered transaction, got nil")
	}
}

func TestVerifyTransactionSignatureMissingKey(t *testing.T) {
	t.Parallel()
	tx := &Transaction{
		From:      "someone",
		To:        "other",
		Amount:    100,
		Timestamp: 1000,
		Signature: "aabbccdd",
		PublicKey: "",
	}
	if err := VerifyTransactionSignature(tx); err == nil {
		t.Fatal("expected error for missing public key")
	}
}

func TestVerifyAddressMatchesPublicKey(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	pubKeyBytes, _ := w.PublicKey.MarshalBinary()
	pubKeyHex := hex.EncodeToString(pubKeyBytes)
	if err := VerifyAddressMatchesPublicKey(w.Address, pubKeyHex); err != nil {
		t.Fatalf("VerifyAddressMatchesPublicKey failed: %v", err)
	}
}

func TestVerifyAddressMatchesPublicKeyWrong(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()
	pubKeyBytes, _ := w.PublicKey.MarshalBinary()
	pubKeyHex := hex.EncodeToString(pubKeyBytes)
	if err := VerifyAddressMatchesPublicKey("0000000000000000000000000000000000000000", pubKeyHex); err == nil {
		t.Fatal("expected error for mismatched address")
	}
}

func TestGetBlockReward(t *testing.T) {
	t.Parallel()
	tests := []struct {
		height int64
		want   int64
	}{
		{0, 50 * DLTUnit},
		{250_000, 25 * DLTUnit},
		{500_000, int64(12.5 * float64(DLTUnit))},
		{64 * 250_000, 0},
	}
	for _, tt := range tests {
		got := GetBlockReward(tt.height)
		if got != tt.want {
			t.Errorf("GetBlockReward(%d) = %d, want %d", tt.height, got, tt.want)
		}
	}
}

func TestAddTransactionValidation(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Missing from
	err := bc.AddTransaction(&Transaction{From: "", To: "b", Amount: 1, Signature: "sig"})
	if err == nil {
		t.Error("expected error for missing from")
	}

	// Missing to
	err = bc.AddTransaction(&Transaction{From: "a", To: "", Amount: 1, Signature: "sig"})
	if err == nil {
		t.Error("expected error for missing to")
	}

	// Zero amount
	err = bc.AddTransaction(&Transaction{From: "a", To: "b", Amount: 0, Signature: "sig"})
	if err == nil {
		t.Error("expected error for zero amount")
	}

	// Missing signature
	err = bc.AddTransaction(&Transaction{From: "a", To: "b", Amount: 1, Signature: ""})
	if err == nil {
		t.Error("expected error for missing signature")
	}

	// Bad signature (non-SYSTEM tx with invalid sig)
	err = bc.AddTransaction(&Transaction{
		From: "a", To: "b", Amount: 1,
		Signature: "deadbeef",
		PublicKey: "deadbeef",
	})
	if err == nil {
		t.Error("expected error for bad signature")
	}
}

func TestAddTransactionDuplicate(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Use a SYSTEM tx to bypass signature verification
	tx := &Transaction{
		From:      "SYSTEM",
		To:        "miner",
		Amount:    100,
		Timestamp: 1000,
		Signature: fmt.Sprintf("test-sig-%d", 1),
	}

	added1, err := bc.AddTransactionIfNew(tx)
	if err != nil {
		t.Fatalf("first add error: %v", err)
	}
	if !added1 {
		t.Fatal("first add should return true")
	}

	added2, err := bc.AddTransactionIfNew(tx)
	if err != nil {
		t.Fatalf("second add error: %v", err)
	}
	if added2 {
		t.Fatal("second add should return false (duplicate)")
	}
}

func TestMiningIntegration(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)

	// Create wallets
	miner, _ := NewWallet()
	recipient, _ := NewWallet()

	// Mine first block to give miner some coins
	bc.MinePendingTransactions(miner.Address)

	minerBalance := bc.GetBalance(miner.Address)
	if minerBalance != 50*DLTUnit {
		t.Fatalf("miner balance = %d, want %d", minerBalance, 50*DLTUnit)
	}

	// Create and sign a transaction (with minimum fee)
	sendAmount := int64(10 * DLTUnit)
	fee := MinTransactionFee
	tx := NewTransaction(miner.Address, recipient.Address, sendAmount, fee)
	if err := tx.Sign(miner); err != nil {
		t.Fatalf("Sign error: %v", err)
	}

	if err := bc.AddTransaction(tx); err != nil {
		t.Fatalf("AddTransaction error: %v", err)
	}

	// Mine the block containing the transaction
	bc.MinePendingTransactions(miner.Address)

	// Check balances: miner got another block reward + fee minus send minus fee, recipient got send
	recipientBalance := bc.GetBalance(recipient.Address)
	if recipientBalance != sendAmount {
		t.Fatalf("recipient balance = %d, want %d", recipientBalance, sendAmount)
	}

	// Miner: 2 block rewards + fee (collected in coinbase) - sendAmount - fee (paid as sender)
	minerExpected := 2*50*DLTUnit + fee - sendAmount - fee // fee cancels out since miner pays and collects
	minerActual := bc.GetBalance(miner.Address)
	if minerActual != minerExpected {
		t.Fatalf("miner balance = %d, want %d", minerActual, minerExpected)
	}
}

func TestComputeMerkleRoot(t *testing.T) {
	t.Parallel()

	// Empty transactions => SHA-256("")
	emptyRoot := ComputeMerkleRoot([]*Transaction{})
	expectedEmpty := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if emptyRoot != expectedEmpty {
		t.Fatalf("empty merkle root = %s, want %s", emptyRoot, expectedEmpty)
	}

	// Single transaction => SHA-256(JSON(tx))
	tx1 := &Transaction{
		From:      "SYSTEM",
		To:        "miner",
		Amount:    100,
		Timestamp: 1000,
		Signature: "sig1",
	}
	root1 := ComputeMerkleRoot([]*Transaction{tx1})
	if len(root1) != 64 {
		t.Fatalf("merkle root length = %d, want 64", len(root1))
	}

	// Two transactions => deterministic
	tx2 := &Transaction{
		From:      "alice",
		To:        "bob",
		Amount:    50,
		Timestamp: 2000,
		Signature: "sig2",
	}
	root2a := ComputeMerkleRoot([]*Transaction{tx1, tx2})
	root2b := ComputeMerkleRoot([]*Transaction{tx1, tx2})
	if root2a != root2b {
		t.Fatalf("merkle root not deterministic: %s vs %s", root2a, root2b)
	}

	// Different order => different root
	root2c := ComputeMerkleRoot([]*Transaction{tx2, tx1})
	if root2a == root2c {
		t.Fatal("merkle root should differ for different transaction order")
	}

	// Single tx != two tx
	if root1 == root2a {
		t.Fatal("single-tx root should differ from two-tx root")
	}
}

func TestCalculateHashForkTransition(t *testing.T) {
	t.Parallel()

	txs := []*Transaction{
		{From: "SYSTEM", To: "miner", Amount: 100, Timestamp: 1000, Signature: "sig1"},
	}
	merkleRoot := ComputeMerkleRoot(txs)

	// Block below fork height — uses legacy JSON serialization
	preForkBlock := &Block{
		Index:        MerkleRootForkHeight - 1,
		Timestamp:    1000,
		Transactions: txs,
		MerkleRoot:   merkleRoot,
		PreviousHash: "abc",
		Nonce:        0,
		Difficulty:   2,
	}

	// Block at fork height — uses MerkleRoot
	postForkBlock := &Block{
		Index:        MerkleRootForkHeight,
		Timestamp:    1000,
		Transactions: txs,
		MerkleRoot:   merkleRoot,
		PreviousHash: "abc",
		Nonce:        0,
		Difficulty:   2,
	}

	preForkHash := preForkBlock.CalculateHash()
	postForkHash := postForkBlock.CalculateHash()

	// The hashes must differ because the txData in the hash input changes
	if preForkHash == postForkHash {
		t.Fatalf("pre-fork and post-fork hashes should differ, both = %s", preForkHash)
	}

	// Both should be deterministic
	if preForkBlock.CalculateHash() != preForkHash {
		t.Fatal("pre-fork hash not deterministic")
	}
	if postForkBlock.CalculateHash() != postForkHash {
		t.Fatal("post-fork hash not deterministic")
	}
}

func TestMerkleRootWithMultipleTransactions(t *testing.T) {
	t.Parallel()

	// Build a block with coinbase + 3 user transactions
	coinbase := &Transaction{
		From: "SYSTEM", To: "miner_addr", Amount: 5000000000,
		Timestamp: 1000, Signature: "coinbase-test",
	}
	tx1 := &Transaction{
		From: "alice", To: "bob", Amount: 100, Fee: 10000,
		Timestamp: 1001, Signature: "sig1", PublicKey: "pk1",
	}
	tx2 := &Transaction{
		From: "bob", To: "charlie", Amount: 50, Fee: 10000,
		Timestamp: 1002, Signature: "sig2", PublicKey: "pk2",
	}
	tx3 := &Transaction{
		From: "charlie", To: "dave", Amount: 25, Fee: 10000,
		Timestamp: 1003, Signature: "sig3", PublicKey: "pk3",
	}
	txs := []*Transaction{coinbase, tx1, tx2, tx3}

	// Compute Merkle root
	root := ComputeMerkleRoot(txs)
	if len(root) != 64 {
		t.Fatalf("merkle root length = %d, want 64", len(root))
	}

	// Deterministic
	if ComputeMerkleRoot(txs) != root {
		t.Fatal("merkle root not deterministic with 4 txs")
	}

	// Build post-fork block and verify hash uses MerkleRoot, not JSON
	block := &Block{
		Index:        MerkleRootForkHeight,
		Timestamp:    1000,
		Transactions: txs,
		MerkleRoot:   root,
		PreviousHash: "prev",
		Nonce:        42,
		Difficulty:   2,
	}
	postForkHash := block.CalculateHash()

	// Manually compute what the hash should be (using MerkleRoot)
	expectedData := fmt.Sprintf("%d%d%s%s%d%d",
		block.Index, block.Timestamp, root, block.PreviousHash, block.Nonce, block.Difficulty)
	expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(expectedData)))
	if postForkHash != expectedHash {
		t.Fatalf("post-fork hash mismatch:\n  got:  %s\n  want: %s", postForkHash, expectedHash)
	}

	// Same block as pre-fork should produce a DIFFERENT hash (using JSON)
	block.Index = MerkleRootForkHeight - 1
	preForkHash := block.CalculateHash()
	if preForkHash == postForkHash {
		t.Fatal("pre-fork and post-fork hashes should differ with multi-tx block")
	}

	// Verify pre-fork used JSON
	txJSON, _ := json.Marshal(txs)
	expectedPreData := fmt.Sprintf("%d%d%s%s%d%d",
		block.Index, block.Timestamp, string(txJSON), block.PreviousHash, block.Nonce, block.Difficulty)
	expectedPreHash := fmt.Sprintf("%x", sha256.Sum256([]byte(expectedPreData)))
	if preForkHash != expectedPreHash {
		t.Fatalf("pre-fork hash mismatch:\n  got:  %s\n  want: %s", preForkHash, expectedPreHash)
	}
}

func TestMerkleRootOddTransactionCount(t *testing.T) {
	t.Parallel()

	// 3 transactions (odd) — tests the duplicate-last-leaf logic
	txs := []*Transaction{
		{From: "SYSTEM", To: "miner", Amount: 100, Timestamp: 1, Signature: "s1"},
		{From: "a", To: "b", Amount: 50, Timestamp: 2, Signature: "s2"},
		{From: "c", To: "d", Amount: 25, Timestamp: 3, Signature: "s3"},
	}

	root3 := ComputeMerkleRoot(txs)
	if len(root3) != 64 {
		t.Fatalf("odd-count merkle root length = %d, want 64", len(root3))
	}

	// Adding a 4th tx should change the root
	txs4 := append(txs, &Transaction{From: "e", To: "f", Amount: 10, Timestamp: 4, Signature: "s4"})
	root4 := ComputeMerkleRoot(txs4)
	if root3 == root4 {
		t.Fatal("3-tx and 4-tx merkle roots should differ")
	}
}

// TestMerkleRootForkWithRealTransactions creates wallets, signs real transactions,
// and verifies that blocks containing them validate correctly both before and after
// the fork height. This exercises the full signing → merkle root → hash → validate path.
func TestMerkleRootForkWithRealTransactions(t *testing.T) {
	// Override fork height to a low value so the test doesn't need to mine 6000 blocks.
	// Not parallel because we mutate the global MerkleRootForkHeight.
	origForkHeight := MerkleRootForkHeight
	MerkleRootForkHeight = 5
	defer func() { MerkleRootForkHeight = origForkHeight }()

	// Create wallets
	minerWallet, _ := NewWallet()
	alice, _ := NewWallet()
	bob, _ := NewWallet()

	bc := NewBlockchain(1) // difficulty 1 for fast mining

	// Mine blocks up to fork height - 2 so the miner has funds
	for i := int64(1); i < MerkleRootForkHeight-1; i++ {
		block := bc.MinePendingTransactions(minerWallet.Address)
		if block == nil {
			t.Fatalf("failed to mine block %d", i)
		}
	}

	currentHeight := bc.GetBlockCount()
	t.Logf("Pre-funded chain height: %d (fork at %d)", currentHeight, MerkleRootForkHeight)

	// Verify miner has funds
	minerBalance := bc.GetBalance(minerWallet.Address)
	t.Logf("Miner balance: %s DLT", FormatDLT(minerBalance))
	if minerBalance <= 0 {
		t.Fatalf("miner should have balance, got %d", minerBalance)
	}

	// ---- PRE-FORK BLOCK WITH REAL TRANSACTION ----
	// This should be the last pre-fork block (height = MerkleRootForkHeight - 1)

	sendAmount := int64(10 * DLTUnit)
	fee := MinTransactionFee

	tx1 := NewTransaction(minerWallet.Address, alice.Address, sendAmount, fee)
	if err := tx1.Sign(minerWallet); err != nil {
		t.Fatalf("Sign tx1 error: %v", err)
	}
	if err := bc.AddTransaction(tx1); err != nil {
		t.Fatalf("AddTransaction tx1 error: %v", err)
	}

	preForkBlock := bc.MinePendingTransactions(minerWallet.Address)
	if preForkBlock == nil {
		t.Fatal("failed to mine pre-fork block with transaction")
	}

	t.Logf("Pre-fork block %d: %d txs, MerkleRoot=%s, Hash=%s",
		preForkBlock.Index, len(preForkBlock.Transactions),
		preForkBlock.MerkleRoot[:16], preForkBlock.Hash[:16])

	// Verify this block IS pre-fork
	if preForkBlock.Index >= MerkleRootForkHeight {
		t.Fatalf("expected pre-fork block, got index %d (fork at %d)",
			preForkBlock.Index, MerkleRootForkHeight)
	}

	// Verify hash was computed using JSON(Transactions), not MerkleRoot
	txJSON, _ := json.Marshal(preForkBlock.Transactions)
	preForkData := fmt.Sprintf("%d%d%s%s%d%d",
		preForkBlock.Index, preForkBlock.Timestamp,
		string(txJSON), preForkBlock.PreviousHash,
		preForkBlock.Nonce, preForkBlock.Difficulty)
	expectedPreHash := fmt.Sprintf("%x", sha256.Sum256([]byte(preForkData)))
	if preForkBlock.Hash != expectedPreHash {
		t.Fatalf("pre-fork block hash does not match JSON-based computation")
	}

	// ---- POST-FORK BLOCK WITH REAL TRANSACTION ----
	// This is the first post-fork block (height = MerkleRootForkHeight)

	tx2 := NewTransaction(minerWallet.Address, bob.Address, sendAmount, fee)
	if err := tx2.Sign(minerWallet); err != nil {
		t.Fatalf("Sign tx2 error: %v", err)
	}
	if err := bc.AddTransaction(tx2); err != nil {
		t.Fatalf("AddTransaction tx2 error: %v", err)
	}

	postForkBlock := bc.MinePendingTransactions(minerWallet.Address)
	if postForkBlock == nil {
		t.Fatal("failed to mine post-fork block with transaction")
	}

	t.Logf("Post-fork block %d: %d txs, MerkleRoot=%s, Hash=%s",
		postForkBlock.Index, len(postForkBlock.Transactions),
		postForkBlock.MerkleRoot[:16], postForkBlock.Hash[:16])

	// Verify this block IS post-fork
	if postForkBlock.Index < MerkleRootForkHeight {
		t.Fatalf("expected post-fork block, got index %d (fork at %d)",
			postForkBlock.Index, MerkleRootForkHeight)
	}

	// Verify hash was computed using MerkleRoot, not JSON
	postForkData := fmt.Sprintf("%d%d%s%s%d%d",
		postForkBlock.Index, postForkBlock.Timestamp,
		postForkBlock.MerkleRoot, postForkBlock.PreviousHash,
		postForkBlock.Nonce, postForkBlock.Difficulty)
	expectedPostHash := fmt.Sprintf("%x", sha256.Sum256([]byte(postForkData)))
	if postForkBlock.Hash != expectedPostHash {
		t.Fatalf("post-fork block hash does not match MerkleRoot-based computation")
	}

	// Verify MerkleRoot was populated correctly
	expectedMerkle := ComputeMerkleRoot(postForkBlock.Transactions)
	if postForkBlock.MerkleRoot != expectedMerkle {
		t.Fatalf("post-fork MerkleRoot mismatch:\n  block:    %s\n  computed: %s",
			postForkBlock.MerkleRoot, expectedMerkle)
	}

	// ---- MINE A FEW MORE POST-FORK BLOCKS WITH TRANSACTIONS ----
	for i := 0; i < 3; i++ {
		tx := NewTransaction(minerWallet.Address, alice.Address, int64(DLTUnit), fee)
		if err := tx.Sign(minerWallet); err != nil {
			t.Fatalf("Sign tx error (round %d): %v", i, err)
		}
		if err := bc.AddTransaction(tx); err != nil {
			t.Fatalf("AddTransaction error (round %d): %v", i, err)
		}
		block := bc.MinePendingTransactions(minerWallet.Address)
		if block == nil {
			t.Fatalf("failed to mine post-fork block %d", i)
		}
		t.Logf("Post-fork block %d: %d txs, MerkleRoot=%s",
			block.Index, len(block.Transactions), block.MerkleRoot[:16])
	}

	// ---- VALIDATE ENTIRE CHAIN ----
	if !bc.IsValid() {
		t.Fatal("chain should be valid after mining across fork with transactions")
	}

	// Check balances are correct
	aliceBalance := bc.GetBalance(alice.Address)
	bobBalance := bc.GetBalance(bob.Address)
	t.Logf("Final balances: Alice=%s, Bob=%s, Miner=%s",
		FormatDLT(aliceBalance), FormatDLT(bobBalance), FormatDLT(bc.GetBalance(minerWallet.Address)))

	expectedAlice := sendAmount + 3*DLTUnit // 10 DLT + 3*1 DLT
	if aliceBalance != expectedAlice {
		t.Fatalf("alice balance = %d, want %d", aliceBalance, expectedAlice)
	}
	if bobBalance != sendAmount {
		t.Fatalf("bob balance = %d, want %d", bobBalance, sendAmount)
	}
}

func TestChainValidation(t *testing.T) {
	t.Parallel()
	bc := NewBlockchain(2)
	miner, _ := NewWallet()

	// Mine a few blocks
	bc.MinePendingTransactions(miner.Address)
	bc.MinePendingTransactions(miner.Address)
	bc.MinePendingTransactions(miner.Address)

	if !bc.IsValid() {
		t.Fatal("chain should be valid after mining")
	}

	// Corrupt a block hash
	bc.Blocks[2].Hash = "corrupted_hash"
	if bc.IsValid() {
		t.Fatal("chain should be invalid after corruption")
	}
}

func TestDataForkHeight(t *testing.T) {
	t.Parallel()
	// Save and restore original fork height
	origFork := DataForkHeight
	defer func() { DataForkHeight = origFork }()
	DataForkHeight = 5

	w, _ := NewWallet()

	// Transaction with data before fork height should be rejected
	tx := NewTransaction(w.Address, "recipient", 100, 10000)
	tx.Data = "test memo"
	tx.Sign(w)

	if err := validateTransactionForBlock(tx, 4); err == nil {
		t.Fatal("expected error for data transaction before fork height")
	}

	// Transaction with data at fork height should be accepted
	if err := validateTransactionForBlock(tx, 5); err != nil {
		t.Fatalf("expected success for data transaction at fork height: %v", err)
	}

	// Transaction with data after fork height should be accepted
	if err := validateTransactionForBlock(tx, 10); err != nil {
		t.Fatalf("expected success for data transaction after fork height: %v", err)
	}

	// Transaction without data should be accepted at any height
	txNoData := NewTransaction(w.Address, "recipient", 100, 10000)
	txNoData.Sign(w)

	if err := validateTransactionForBlock(txNoData, 1); err != nil {
		t.Fatalf("expected success for no-data transaction before fork: %v", err)
	}
	if err := validateTransactionForBlock(txNoData, 10); err != nil {
		t.Fatalf("expected success for no-data transaction after fork: %v", err)
	}
}

func TestVerifySignatureWithData(t *testing.T) {
	t.Parallel()
	w, _ := NewWallet()

	// Sign with data, verify succeeds
	tx := NewTransaction(w.Address, "recipient", 500, 10000)
	tx.Data = "payment for coffee"
	tx.Sign(w)
	if err := VerifyTransactionSignature(tx); err != nil {
		t.Fatalf("verification should pass for tx with data: %v", err)
	}

	// Tamper with data, verify fails
	tx.Data = "tampered"
	if err := VerifyTransactionSignature(tx); err == nil {
		t.Fatal("verification should fail for tampered data")
	}

	// Sign without data, verify succeeds (backward compat)
	txNoData := NewTransaction(w.Address, "recipient", 500, 10000)
	txNoData.Sign(w)
	if err := VerifyTransactionSignature(txNoData); err != nil {
		t.Fatalf("verification should pass for tx without data: %v", err)
	}
}
