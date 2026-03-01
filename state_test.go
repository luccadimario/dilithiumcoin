package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDBBasic(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	// No contract initially
	if stateDB.GetContract("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent contract")
	}

	// Set a contract
	contract := &ContractAccount{
		Address: "contract_abc",
		Code:    []byte{0x01, 0x02, 0x03},
		Storage: make(map[string][]byte),
		Balance: 1000,
		Nonce:   0,
	}
	stateDB.SetContract(contract)

	// Retrieve it
	retrieved := stateDB.GetContract("contract_abc")
	if retrieved == nil {
		t.Fatal("expected non-nil contract")
	}
	if retrieved.Balance != 1000 {
		t.Fatalf("balance = %d, want 1000", retrieved.Balance)
	}
	if len(retrieved.Code) != 3 {
		t.Fatalf("code length = %d, want 3", len(retrieved.Code))
	}
}

func TestStateDBStorage(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)
	stateDB.SetContract(&ContractAccount{
		Address: "store_test",
		Code:    []byte{},
		Storage: make(map[string][]byte),
	})

	// Set storage
	key := "0000000000000000000000000000000000000000000000000000000000000001"
	value := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	stateDB.SetStorage("store_test", key, value)

	// Get storage
	got := stateDB.GetStorage("store_test", key)
	if len(got) != 4 || got[0] != 0xDE || got[3] != 0xEF {
		t.Fatalf("storage value = %x, want deadbeef", got)
	}

	// Get nonexistent key
	empty := stateDB.GetStorage("store_test", "0000000000000000000000000000000000000000000000000000000000000099")
	if empty != nil {
		t.Fatalf("expected nil for nonexistent key, got %x", empty)
	}
}

func TestStateDBSnapshotRevert(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)
	stateDB.SetContract(&ContractAccount{
		Address: "snap_test",
		Code:    []byte{0x00},
		Storage: make(map[string][]byte),
		Balance: 500,
	})

	key := "0000000000000000000000000000000000000000000000000000000000000001"

	// Take snapshot
	snapID := stateDB.Snapshot()

	// Make changes
	stateDB.SetStorage("snap_test", key, []byte{0xFF})
	contract := stateDB.GetContract("snap_test")
	contract.Balance = 999
	stateDB.SetContract(contract)

	// Verify changes exist
	if stateDB.GetStorage("snap_test", key) == nil {
		t.Fatal("storage should have value after set")
	}
	if stateDB.GetContract("snap_test").Balance != 999 {
		t.Fatal("balance should be 999 after set")
	}

	// Revert
	stateDB.RevertToSnapshot(snapID)

	// Changes should be undone
	if got := stateDB.GetStorage("snap_test", key); got != nil {
		t.Fatalf("storage should be nil after revert, got %x", got)
	}
	if stateDB.GetContract("snap_test").Balance != 500 {
		t.Fatalf("balance should be 500 after revert, got %d", stateDB.GetContract("snap_test").Balance)
	}
}

func TestStateDBDeleteContract(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)
	stateDB.SetContract(&ContractAccount{
		Address: "delete_me",
		Code:    []byte{0x00},
		Storage: make(map[string][]byte),
	})

	if stateDB.GetContract("delete_me") == nil {
		t.Fatal("contract should exist before delete")
	}

	stateDB.DeleteContract("delete_me")

	if stateDB.GetContract("delete_me") != nil {
		t.Fatal("contract should not exist after delete")
	}
}

func TestDeriveContractAddress(t *testing.T) {
	t.Parallel()

	addr1 := DeriveContractAddress("deployer_abc", 0)
	addr2 := DeriveContractAddress("deployer_abc", 1)
	addr3 := DeriveContractAddress("deployer_abc", 0)

	if len(addr1) != 40 {
		t.Fatalf("address length = %d, want 40", len(addr1))
	}
	if addr1 == addr2 {
		t.Fatal("different nonces should produce different addresses")
	}
	if addr1 != addr3 {
		t.Fatal("same deployer+nonce should produce same address")
	}
}

func TestStateDBDeployerNonce(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)

	nonce := stateDB.GetDeployerNonce("some_deployer")
	if nonce != 0 {
		t.Fatalf("initial nonce = %d, want 0", nonce)
	}

	stateDB.IncrementDeployerNonce("some_deployer")
	nonce = stateDB.GetDeployerNonce("some_deployer")
	if nonce != 1 {
		t.Fatalf("nonce after increment = %d, want 1", nonce)
	}

	stateDB.IncrementDeployerNonce("some_deployer")
	nonce = stateDB.GetDeployerNonce("some_deployer")
	if nonce != 2 {
		t.Fatalf("nonce after 2 increments = %d, want 2", nonce)
	}
}

func TestStateStorePersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	contractDir := filepath.Join(dir, "contracts")

	store, err := NewStateStore(dir)
	if err != nil {
		t.Fatalf("NewStateStore error: %v", err)
	}

	contract := &ContractAccount{
		Address: "persist_test",
		Code:    []byte{0xAA, 0xBB},
		Storage: map[string][]byte{
			"key1": {0x01, 0x02},
		},
		Balance: 42,
		Nonce:   1,
	}

	if err := store.SaveContract(contract); err != nil {
		t.Fatalf("SaveContract error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(contractDir, "persist_test.json")); err != nil {
		t.Fatalf("contract file should exist: %v", err)
	}

	// Load it back
	loaded, err := store.LoadContract("persist_test")
	if err != nil {
		t.Fatalf("LoadContract error: %v", err)
	}
	if loaded.Address != "persist_test" {
		t.Fatalf("address = %q, want %q", loaded.Address, "persist_test")
	}
	if loaded.Balance != 42 {
		t.Fatalf("balance = %d, want 42", loaded.Balance)
	}
	if len(loaded.Code) != 2 || loaded.Code[0] != 0xAA {
		t.Fatalf("code mismatch: %x", loaded.Code)
	}

	// Delete
	if err := store.DeleteContract("persist_test"); err != nil {
		t.Fatalf("DeleteContract error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(contractDir, "persist_test.json")); !os.IsNotExist(err) {
		t.Fatal("contract file should not exist after delete")
	}
}

func TestStateStoreLoadAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	store, err := NewStateStore(dir)
	if err != nil {
		t.Fatalf("NewStateStore error: %v", err)
	}

	// Save 3 contracts
	for i := 0; i < 3; i++ {
		addr := DeriveContractAddress("deployer", uint64(i))
		store.SaveContract(&ContractAccount{
			Address: addr,
			Code:    []byte{byte(i)},
			Storage: make(map[string][]byte),
		})
	}

	// Load all
	contracts, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(contracts) != 3 {
		t.Fatalf("loaded %d contracts, want 3", len(contracts))
	}
}

func TestStateDBMultipleSnapshots(t *testing.T) {
	t.Parallel()
	stateDB := NewStateDB(nil, nil)
	stateDB.SetContract(&ContractAccount{
		Address: "multi_snap",
		Code:    []byte{0x00},
		Storage: make(map[string][]byte),
		Balance: 100,
	})

	key := "0000000000000000000000000000000000000000000000000000000000000001"

	// Snapshot 0: balance=100, no storage
	snap0 := stateDB.Snapshot()

	// Change 1
	stateDB.SetStorage("multi_snap", key, []byte{0x01})
	c := stateDB.GetContract("multi_snap")
	c.Balance = 200
	stateDB.SetContract(c)

	// Snapshot 1: balance=200, storage[key]=0x01
	snap1 := stateDB.Snapshot()

	// Change 2
	stateDB.SetStorage("multi_snap", key, []byte{0x02})
	c = stateDB.GetContract("multi_snap")
	c.Balance = 300
	stateDB.SetContract(c)

	// Revert to snap1: should have balance=200, storage[key]=0x01
	stateDB.RevertToSnapshot(snap1)
	if stateDB.GetContract("multi_snap").Balance != 200 {
		t.Fatalf("balance after revert to snap1 = %d, want 200", stateDB.GetContract("multi_snap").Balance)
	}
	if v := stateDB.GetStorage("multi_snap", key); v == nil || v[0] != 0x01 {
		t.Fatalf("storage after revert to snap1 = %x, want 01", v)
	}

	// Revert to snap0: should have balance=100, no storage
	stateDB.RevertToSnapshot(snap0)
	if stateDB.GetContract("multi_snap").Balance != 100 {
		t.Fatalf("balance after revert to snap0 = %d, want 100", stateDB.GetContract("multi_snap").Balance)
	}
	if v := stateDB.GetStorage("multi_snap", key); v != nil {
		t.Fatalf("storage after revert to snap0 should be nil, got %x", v)
	}
}
