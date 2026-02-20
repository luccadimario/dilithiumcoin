package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ChainStore persists blockchain blocks to disk.
// Each block is stored as a separate JSON file: blocks/000000.json, blocks/000001.json, etc.
type ChainStore struct {
	dir string // blocks directory path
}

// NewChainStore creates a ChainStore that writes to dataDir/blocks/.
func NewChainStore(dataDir string) (*ChainStore, error) {
	dir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create blocks directory: %w", err)
	}
	return &ChainStore{dir: dir}, nil
}

// blockFileName returns the filename for a block at the given index.
func blockFileName(index int64) string {
	return fmt.Sprintf("%09d.json", index)
}

// SaveBlock writes a single block to disk with fsync for durability.
func (s *ChainStore) SaveBlock(block *Block) error {
	data, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to marshal block %d: %w", block.Index, err)
	}

	path := filepath.Join(s.dir, blockFileName(block.Index))

	// Write to temp file then rename for atomicity
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file for block %d: %w", block.Index, err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write block %d: %w", block.Index, err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync block %d: %w", block.Index, err)
	}
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename block %d: %w", block.Index, err)
	}

	// Fsync the directory to ensure the rename is durable across crashes
	if dirFd, err := os.Open(s.dir); err == nil {
		dirFd.Sync()
		dirFd.Close()
	}

	return nil
}

// SaveChain writes all blocks from startIndex onward, and removes any
// block files beyond the end of the chain (for chain replacement).
func (s *ChainStore) SaveChain(blocks []*Block, startIndex int) error {
	// Write new/changed blocks
	for i := startIndex; i < len(blocks); i++ {
		if err := s.SaveBlock(blocks[i]); err != nil {
			return err
		}
	}

	// Remove orphaned block files beyond the chain length
	lastValidIndex := int64(0)
	if len(blocks) > 0 {
		lastValidIndex = blocks[len(blocks)-1].Index
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil // not fatal
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".json")
		idx, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			continue
		}

		if idx > lastValidIndex {
			os.Remove(filepath.Join(s.dir, entry.Name()))
		}
	}

	return nil
}

// LoadChain reads all block files from disk and returns them in order.
// Returns nil (not an error) if no blocks exist on disk.
func (s *ChainStore) LoadChain() ([]*Block, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read blocks directory: %w", err)
	}

	// Collect JSON files
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}

	if len(files) == 0 {
		return nil, nil
	}

	// Sort by filename (ensures block order)
	sort.Strings(files)

	blocks := make([]*Block, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(s.dir, file))
		if err != nil {
			return nil, fmt.Errorf("failed to read block file %s: %w", file, err)
		}

		var block Block
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, fmt.Errorf("failed to parse block file %s: %w", file, err)
		}

		// Validate chain continuity: each block must connect to the previous
		if len(blocks) > 0 {
			prev := blocks[len(blocks)-1]
			if block.PreviousHash != prev.Hash {
				fmt.Printf("[!] Chain gap detected at block %d (file %s): previous_hash doesn't match block %d hash. Loaded %d valid blocks.\n",
					block.Index, file, prev.Index, len(blocks))
				break // Stop loading — sync will fill in the rest
			}
		}

		blocks = append(blocks, &block)
	}

	// Verify block contiguity: each block's Index must match its position
	totalLoaded := len(blocks)
	for i, block := range blocks {
		if block.Index != int64(i) {
			discarded := totalLoaded - i
			fmt.Printf("WARNING: Loaded %d blocks from disk (truncated at gap: block %d missing, %d blocks after gap discarded)\n",
				i, i, discarded)
			blocks = blocks[:i]
			break
		}
	}

	return blocks, nil
}

// BlockCount returns the number of block files on disk.
func (s *ChainStore) BlockCount() int {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}
