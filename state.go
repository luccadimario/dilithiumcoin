package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type ContractAccount struct {
	Address string            `json:"address"`
	Code    []byte            `json:"code"`
	Storage map[string][]byte `json:"storage"`
	Balance int64             `json:"balance"`
	Nonce   uint64            `json:"nonce"`
}

func DeriveContractAddress(deployerAddress string, deployerNonce uint64) string {
	data := deployerAddress + strconv.FormatUint(deployerNonce, 10)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:20])
}

func NewContractAccount(address string, code []byte) *ContractAccount {
	return &ContractAccount{
		Address: address,
		Code:    code,
		Storage: make(map[string][]byte),
		Balance: 0,
		Nonce:   0,
	}
}

func (ca *ContractAccount) Clone() *ContractAccount {
	cloned := &ContractAccount{
		Address: ca.Address,
		Code:    make([]byte, len(ca.Code)),
		Storage: make(map[string][]byte, len(ca.Storage)),
		Balance: ca.Balance,
		Nonce:   ca.Nonce,
	}
	copy(cloned.Code, ca.Code)
	for key, value := range ca.Storage {
		valueCopy := make([]byte, len(value))
		copy(valueCopy, value)
		cloned.Storage[key] = valueCopy
	}
	return cloned
}

type stateSnapshot struct {
	contracts map[string]*ContractAccount
}

type StateDB struct {
	contracts  map[string]*ContractAccount
	snapshots  []stateSnapshot
	dirty      map[string]bool
	blockchain *Blockchain
	store      *StateStore
	mutex      sync.RWMutex

	// Track deployer nonces for address derivation within a block
	deployerNonces map[string]uint64
}

func NewStateDB(blockchain *Blockchain, store *StateStore) *StateDB {
	state := &StateDB{
		contracts:      make(map[string]*ContractAccount),
		dirty:          make(map[string]bool),
		blockchain:     blockchain,
		store:          store,
		deployerNonces: make(map[string]uint64),
	}

	if store != nil {
		contracts, err := store.LoadAll()
		if err != nil {
			fmt.Printf("Warning: failed to load contracts from disk: %v\n", err)
		} else {
			for _, contract := range contracts {
				state.contracts[contract.Address] = contract
			}
			if len(contracts) > 0 {
				fmt.Printf("Loaded %d contracts from disk\n", len(contracts))
			}
		}
	}

	return state
}

func (s *StateDB) GetContract(address string) *ContractAccount {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.contracts[address]
}

func (s *StateDB) SetContract(account *ContractAccount) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.contracts[account.Address] = account
	s.dirty[account.Address] = true
}

func (s *StateDB) GetStorage(address string, key string) []byte {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	contract := s.contracts[address]
	if contract == nil {
		return nil
	}
	return contract.Storage[key]
}

func (s *StateDB) SetStorage(address string, key string, value []byte) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	contract := s.contracts[address]
	if contract == nil {
		return
	}
	contract.Storage[key] = value
	s.dirty[address] = true
}

func (s *StateDB) GetContractBalance(address string) int64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	contract := s.contracts[address]
	if contract == nil {
		return 0
	}
	return contract.Balance
}

func (s *StateDB) SetContractBalance(address string, balance int64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	contract := s.contracts[address]
	if contract == nil {
		return
	}
	contract.Balance = balance
	s.dirty[address] = true
}

func (s *StateDB) HasContract(address string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	_, exists := s.contracts[address]
	return exists
}

func (s *StateDB) GetCode(address string) []byte {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	contract := s.contracts[address]
	if contract == nil {
		return nil
	}
	return contract.Code
}

func (s *StateDB) GetDeployerNonce(address string) uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.deployerNonces[address]
}

func (s *StateDB) IncrementDeployerNonce(address string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.deployerNonces[address]++
}

func (s *StateDB) Snapshot() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	snapshot := stateSnapshot{
		contracts: make(map[string]*ContractAccount, len(s.contracts)),
	}
	for address, contract := range s.contracts {
		snapshot.contracts[address] = contract.Clone()
	}
	s.snapshots = append(s.snapshots, snapshot)
	return len(s.snapshots) - 1
}

func (s *StateDB) RevertToSnapshot(snapshotID int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if snapshotID < 0 || snapshotID >= len(s.snapshots) {
		return
	}

	snapshot := s.snapshots[snapshotID]
	s.contracts = snapshot.contracts
	s.snapshots = s.snapshots[:snapshotID]
}

func (s *StateDB) ClearSnapshots() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.snapshots = nil
}

func (s *StateDB) Commit() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.store == nil {
		s.dirty = make(map[string]bool)
		s.snapshots = nil
		return nil
	}

	for address := range s.dirty {
		contract := s.contracts[address]
		if contract == nil {
			if err := s.store.DeleteContract(address); err != nil {
				return fmt.Errorf("failed to delete contract %s: %w", address, err)
			}
		} else {
			if err := s.store.SaveContract(contract); err != nil {
				return fmt.Errorf("failed to save contract %s: %w", address, err)
			}
		}
	}

	s.dirty = make(map[string]bool)
	s.snapshots = nil
	return nil
}

func (s *StateDB) DeleteContract(address string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.contracts, address)
	s.dirty[address] = true
}

func (s *StateDB) ContractCount() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.contracts)
}

type StateStore struct {
	dir string
}

func NewStateStore(dataDir string) (*StateStore, error) {
	dir := filepath.Join(dataDir, "contracts")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create contracts directory: %w", err)
	}
	return &StateStore{dir: dir}, nil
}

func (s *StateStore) contractPath(address string) string {
	return filepath.Join(s.dir, address+".json")
}

func (s *StateStore) SaveContract(account *ContractAccount) error {
	data, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("failed to marshal contract %s: %w", account.Address, err)
	}

	path := s.contractPath(account.Address)
	tmpPath := path + ".tmp"

	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file for contract %s: %w", account.Address, err)
	}

	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write contract %s: %w", account.Address, err)
	}

	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync contract %s: %w", account.Address, err)
	}
	file.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename contract %s: %w", account.Address, err)
	}

	return nil
}

func (s *StateStore) LoadContract(address string) (*ContractAccount, error) {
	data, err := os.ReadFile(s.contractPath(address))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read contract %s: %w", address, err)
	}

	var account ContractAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, fmt.Errorf("failed to parse contract %s: %w", address, err)
	}

	if account.Storage == nil {
		account.Storage = make(map[string][]byte)
	}

	return &account, nil
}

func (s *StateStore) LoadAll() ([]*ContractAccount, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read contracts directory: %w", err)
	}

	var contracts []*ContractAccount
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		address := name[:len(name)-5]

		contract, err := s.LoadContract(address)
		if err != nil {
			fmt.Printf("Warning: skipping corrupt contract file %s: %v\n", name, err)
			continue
		}
		if contract != nil {
			contracts = append(contracts, contract)
		}
	}

	return contracts, nil
}

func (s *StateStore) DeleteContract(address string) error {
	path := s.contractPath(address)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete contract %s: %w", address, err)
	}
	return nil
}
