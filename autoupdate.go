package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// UpdateCheckResponse is returned by the /update/check endpoint.
// Seed nodes serve this so other nodes know when a new version is available.
type UpdateCheckResponse struct {
	Version    string `json:"version"`
	Platform   string `json:"platform"`
	BinaryHash string `json:"binary_hash"`
}

// AutoUpdater checks seed nodes for new versions, downloads from GitHub,
// verifies the binary hash against the seed node's reported hash, and
// restarts the node if --auto-update is enabled.
type AutoUpdater struct {
	repoOwner  string
	repoName   string
	localMajor int
	localMinor int
	localPatch int

	seedHosts  []string // hostnames extracted from seed node addresses
	apiPort    string   // API port to check on seed nodes

	httpClient *http.Client
	stopCh     chan struct{}
	shutdownFn func()

	mu            sync.RWMutex
	lastCheck     time.Time
	lastError     string
	latestRemote  string
	updatePending bool
}

// NewAutoUpdater creates a new auto-updater that checks seed nodes for updates.
func NewAutoUpdater(seedNodes []string, shutdownFn func()) *AutoUpdater {
	// Extract unique hostnames from seed node addresses (strip P2P port)
	hosts := make([]string, 0, len(seedNodes))
	seen := make(map[string]bool)
	for _, addr := range seedNodes {
		host := addr
		if idx := strings.LastIndex(addr, ":"); idx != -1 {
			host = addr[:idx]
		}
		if !seen[host] && host != "" {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}

	return &AutoUpdater{
		repoOwner:  "luccadimario",
		repoName:   "dilithiumcoin",
		localMajor: VersionMajor,
		localMinor: VersionMinor,
		localPatch: VersionPatch,
		seedHosts:  hosts,
		apiPort:    "8001",
		httpClient: &http.Client{Timeout: 15 * time.Second},
		stopCh:     make(chan struct{}),
		shutdownFn: shutdownFn,
	}
}

// Start launches the background update check loop.
func (au *AutoUpdater) Start() {
	fmt.Println("[auto-update] Auto-updater started, checking seed nodes every 5 minutes")
	go au.loop()
}

// Stop signals the background loop to exit.
func (au *AutoUpdater) Stop() {
	close(au.stopCh)
	fmt.Println("[auto-update] Auto-updater stopped")
}

func (au *AutoUpdater) loop() {
	// Initial delay: let the node bootstrap and connect to peers
	select {
	case <-time.After(30 * time.Second):
	case <-au.stopCh:
		return
	}

	au.checkAndUpdate()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			au.checkAndUpdate()
		case <-au.stopCh:
			return
		}
	}
}

func (au *AutoUpdater) checkAndUpdate() {
	au.mu.Lock()
	au.lastCheck = time.Now()
	au.mu.Unlock()

	// Try each seed node until one responds
	for _, host := range au.seedHosts {
		resp, err := au.checkSeedNode(host)
		if err != nil {
			continue // try next seed
		}

		rMaj, rMin, rPatch, ok := parseVersion(resp.Version)
		if !ok {
			continue
		}

		au.mu.Lock()
		au.latestRemote = resp.Version
		au.mu.Unlock()

		if !isNewer(rMaj, rMin, rPatch, au.localMajor, au.localMinor, au.localPatch) {
			au.mu.Lock()
			au.lastError = ""
			au.updatePending = false
			au.mu.Unlock()
			fmt.Printf("[auto-update] Up to date (v%s)\n", Version)
			return
		}

		fmt.Printf("[auto-update] New version available: v%s (current: v%s)\n", resp.Version, Version)

		au.mu.Lock()
		au.updatePending = true
		au.mu.Unlock()

		binaryName := au.binaryName()
		downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s",
			au.repoOwner, au.repoName, resp.Version, binaryName)
		checksumsURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/checksums-sha256.txt",
			au.repoOwner, au.repoName, resp.Version)

		err = au.performUpdate(downloadURL, checksumsURL, binaryName, resp)
		if err != nil {
			au.mu.Lock()
			au.lastError = err.Error()
			au.mu.Unlock()
			fmt.Printf("[auto-update] Update failed: %v\n", err)
		}
		return
	}

	// No seed node responded
	au.mu.Lock()
	au.lastError = "no seed nodes reachable"
	au.mu.Unlock()
}

func (au *AutoUpdater) checkSeedNode(host string) (*UpdateCheckResponse, error) {
	url := fmt.Sprintf("http://%s:%s/update/check", host, au.apiPort)
	resp, err := au.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("seed node returned status %d", resp.StatusCode)
	}

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	// Re-marshal the Data field to decode into UpdateCheckResponse
	dataBytes, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, err
	}

	var updateResp UpdateCheckResponse
	if err := json.Unmarshal(dataBytes, &updateResp); err != nil {
		return nil, err
	}

	return &updateResp, nil
}

func (au *AutoUpdater) binaryName() string {
	name := fmt.Sprintf("dilithium-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func (au *AutoUpdater) performUpdate(binaryURL, checksumsURL, binaryName string, seedResp *UpdateCheckResponse) error {
	// Step 1: Determine expected hash.
	// If same platform as seed node, use the seed node's hash directly.
	// Otherwise, fall back to GitHub checksums file.
	myPlatform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	var expectedHash string

	if seedResp.Platform == myPlatform && seedResp.BinaryHash != "" {
		expectedHash = seedResp.BinaryHash
		fmt.Println("[auto-update] Using hash from seed node (same platform)")
	} else {
		fmt.Println("[auto-update] Different platform from seed node, fetching GitHub checksums")
		var err error
		expectedHash, err = au.fetchExpectedChecksum(checksumsURL, binaryName)
		if err != nil {
			return fmt.Errorf("failed to get checksums: %w", err)
		}
	}

	// Step 2: Find current binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	// Step 3: Download new binary to temp file in same directory (same-filesystem rename)
	dir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(dir, "dilithium-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on failure
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	fmt.Printf("[auto-update] Downloading %s...\n", binaryURL)
	if err := au.downloadFile(tmpFile, binaryURL); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	// Step 4: Verify checksum
	actualHash, err := fileSHA256(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s — refusing update", expectedHash[:16], actualHash[:16])
	}
	fmt.Println("[auto-update] Checksum verified")

	// Step 5: Make executable (non-Windows)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0755); err != nil {
			return fmt.Errorf("failed to chmod: %w", err)
		}
	}

	// Step 6: Replace binary (current -> .old, new -> current)
	oldPath := execPath + ".old"
	os.Remove(oldPath) // Remove any previous .old file

	if err := os.Rename(execPath, oldPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		// Rollback
		os.Rename(oldPath, execPath)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	success = true
	fmt.Println("[auto-update] Binary replaced successfully, restarting...")

	// Step 7: Graceful shutdown
	au.shutdownFn()

	// Step 8: Re-exec with same arguments
	if runtime.GOOS == "windows" {
		proc, err := os.StartProcess(execPath, os.Args, &os.ProcAttr{
			Env:   os.Environ(),
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		})
		if err != nil {
			fmt.Printf("[auto-update] Failed to restart: %v\n", err)
			os.Exit(1)
		}
		proc.Release()
		os.Exit(0)
	} else {
		if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
			fmt.Printf("[auto-update] Failed to exec: %v\n", err)
			os.Exit(1)
		}
	}

	return nil // unreachable
}

func (au *AutoUpdater) fetchExpectedChecksum(checksumsURL, binaryName string) (string, error) {
	resp, err := au.httpClient.Get(checksumsURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums file returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB limit
	if err != nil {
		return "", err
	}

	// Parse checksums file: each line is "hash  filename"
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == binaryName {
			return parts[0], nil
		}
	}

	return "", fmt.Errorf("no checksum found for %s", binaryName)
}

func (au *AutoUpdater) downloadFile(dst *os.File, url string) error {
	// Use a longer timeout for binary downloads
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// 200MB limit
	_, err = io.Copy(dst, io.LimitReader(resp.Body, 200*1024*1024))
	return err
}

// UpdateStatus returns the current auto-updater status for API consumption.
func (au *AutoUpdater) UpdateStatus() map[string]interface{} {
	au.mu.RLock()
	defer au.mu.RUnlock()

	return map[string]interface{}{
		"enabled":        true,
		"last_check":     au.lastCheck.Unix(),
		"last_error":     au.lastError,
		"latest_remote":  au.latestRemote,
		"update_pending": au.updatePending,
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// fileSHA256 computes the SHA-256 hash of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// selfBinaryHash returns the cached SHA-256 hash of the currently running binary.
var (
	cachedBinaryHash string
	binaryHashOnce   sync.Once
)

func selfBinaryHash() string {
	binaryHashOnce.Do(func() {
		execPath, err := os.Executable()
		if err != nil {
			return
		}
		execPath, err = filepath.EvalSymlinks(execPath)
		if err != nil {
			return
		}
		hash, err := fileSHA256(execPath)
		if err != nil {
			return
		}
		cachedBinaryHash = hash
	})
	return cachedBinaryHash
}

// parseVersion parses "vX.Y.Z" or "X.Y.Z" into major, minor, patch.
func parseVersion(tag string) (int, int, int, bool) {
	tag = strings.TrimPrefix(tag, "v")
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, false
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, false
	}

	return major, minor, patch, true
}

// isNewer returns true if the remote version is newer than the local version.
func isNewer(rMaj, rMin, rPatch, lMaj, lMin, lPatch int) bool {
	if rMaj != lMaj {
		return rMaj > lMaj
	}
	if rMin != lMin {
		return rMin > lMin
	}
	return rPatch > lPatch
}
