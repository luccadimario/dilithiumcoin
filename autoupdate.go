package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
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

// AutoUpdater propagates update announcements via gossip and optionally
// downloads + verifies + restarts when --auto-update is enabled.
// All nodes forward valid announcements; only auto-update nodes act on them.
type AutoUpdater struct {
	repoOwner  string
	repoName   string
	localMajor int
	localMinor int
	localPatch int

	trustedKeys       map[string]bool // trusted seed node pubkey hexes
	seen              map[string]bool // dedup keyed on "nodeID:version"
	isSeedNode        bool            // true if our NodeID is in trustedKeys
	autoUpdateEnabled bool            // true if --auto-update passed (controls download, not forwarding)
	node              *Node           // for broadcasting + signing

	httpClient *http.Client
	stopCh     chan struct{}
	shutdownFn func()

	mu            sync.RWMutex
	lastCheck     time.Time
	lastError     string
	latestRemote  string
	updatePending bool
}

// NewAutoUpdater creates a new gossip-based auto-updater.
// trustedKeys: hex-encoded Ed25519 public keys of seed nodes authorized to sign updates.
// autoUpdateEnabled: if true, this node will download and apply updates (not just forward).
func NewAutoUpdater(trustedKeys []string, autoUpdateEnabled bool, shutdownFn func()) *AutoUpdater {
	trusted := make(map[string]bool, len(trustedKeys))
	for _, k := range trustedKeys {
		if k != "" {
			trusted[k] = true
		}
	}

	return &AutoUpdater{
		repoOwner:         "luccadimario",
		repoName:          "dilithiumcoin",
		localMajor:        VersionMajor,
		localMinor:        VersionMinor,
		localPatch:        VersionPatch,
		trustedKeys:       trusted,
		seen:              make(map[string]bool),
		autoUpdateEnabled: autoUpdateEnabled,
		httpClient:         &http.Client{Timeout: 15 * time.Second},
		stopCh:            make(chan struct{}),
		shutdownFn:        shutdownFn,
	}
}

// SetNode sets the node reference (called after node is fully wired).
func (au *AutoUpdater) SetNode(node *Node) {
	au.node = node
	// Determine if we are a seed node
	if node.CensusManager != nil {
		au.isSeedNode = au.trustedKeys[node.CensusManager.NodeID()]
	}
}

// Start launches the gossip-based auto-updater.
// If this node is a trusted seed, it periodically emits update announcements.
func (au *AutoUpdater) Start() {
	mode := "gossip-relay"
	if au.isSeedNode {
		mode = "gossip-seed"
		go au.seedAnnouncementLoop()
	}
	if au.autoUpdateEnabled {
		mode += "+download"
	}
	fmt.Printf("[auto-update] Started (mode: %s, trusted keys: %d)\n", mode, len(au.trustedKeys))
}

// Stop signals background loops to exit.
func (au *AutoUpdater) Stop() {
	close(au.stopCh)
	fmt.Println("[auto-update] Auto-updater stopped")
}

// seedAnnouncementLoop emits update announcements every 30 minutes (seed nodes only).
func (au *AutoUpdater) seedAnnouncementLoop() {
	// Initial delay: let peers connect
	select {
	case <-time.After(30 * time.Second):
	case <-au.stopCh:
		return
	}

	au.emitUpdateAnnouncement()

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			au.emitUpdateAnnouncement()
		case <-au.stopCh:
			return
		}
	}
}

// emitUpdateAnnouncement builds, signs, and broadcasts an UpdateAnnounceMsg.
func (au *AutoUpdater) emitUpdateAnnouncement() {
	if au.node == nil || au.node.CensusManager == nil || au.node.PeerManager == nil {
		return
	}

	cm := au.node.CensusManager
	now := time.Now().Unix()
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	hash := selfBinaryHash()

	msg := &UpdateAnnounceMsg{
		NodeID:     cm.NodeID(),
		Version:    Version,
		BinaryHash: hash,
		Platform:   platform,
		Timestamp:  now,
		TTL:        8,
	}

	// Sign: "update:<nodeID>:<version>:<hash>:<timestamp>"
	sigData := fmt.Sprintf("update:%s:%s:%s:%d", msg.NodeID, msg.Version, msg.BinaryHash, msg.Timestamp)
	sig := cm.Sign([]byte(sigData))
	msg.Signature = hex.EncodeToString(sig)

	// Mark as seen so we don't process our own announcement
	dedupeKey := fmt.Sprintf("%s:%s", msg.NodeID, msg.Version)
	au.mu.Lock()
	au.seen[dedupeKey] = true
	au.mu.Unlock()

	au.node.PeerManager.Broadcast(MsgTypeUpdateAnnounce, msg)
	fmt.Printf("[auto-update] Emitted update announcement v%s (platform: %s)\n", Version, platform)
}

// EmitUpdateAnnouncement is the exported version for the admin API endpoint.
func (au *AutoUpdater) EmitUpdateAnnouncement() {
	if !au.isSeedNode {
		fmt.Println("[auto-update] Not a seed node, cannot emit update announcement")
		return
	}
	au.emitUpdateAnnouncement()
}

// ProcessUpdateAnnouncement validates, deduplicates, and optionally triggers a download.
// Returns true if the announcement should be forwarded to peers.
func (au *AutoUpdater) ProcessUpdateAnnouncement(msg *UpdateAnnounceMsg) bool {
	if msg == nil || msg.NodeID == "" || msg.Signature == "" || msg.Version == "" {
		return false
	}

	// TTL exhausted
	if msg.TTL <= 0 {
		return false
	}

	// Reject announcements older than 1 hour
	if time.Now().Unix()-msg.Timestamp > 3600 {
		return false
	}

	// Trust check: signer must be in trustedKeys
	if !au.trustedKeys[msg.NodeID] {
		return false
	}

	// Verify Ed25519 signature
	pubBytes, err := hex.DecodeString(msg.NodeID)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false
	}
	sigData := fmt.Sprintf("update:%s:%s:%s:%d", msg.NodeID, msg.Version, msg.BinaryHash, msg.Timestamp)
	sigBytes, err := hex.DecodeString(msg.Signature)
	if err != nil {
		return false
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte(sigData), sigBytes) {
		return false
	}

	// Dedup check
	dedupeKey := fmt.Sprintf("%s:%s", msg.NodeID, msg.Version)
	au.mu.Lock()
	if au.seen[dedupeKey] {
		au.mu.Unlock()
		return false
	}
	au.seen[dedupeKey] = true
	au.mu.Unlock()

	rMaj, rMin, rPatch, ok := parseVersion(msg.Version)
	if !ok {
		return true // still forward even if we can't parse
	}

	au.mu.Lock()
	au.latestRemote = msg.Version
	au.lastCheck = time.Now()
	au.mu.Unlock()

	if !isNewer(rMaj, rMin, rPatch, au.localMajor, au.localMinor, au.localPatch) {
		au.mu.Lock()
		au.lastError = ""
		au.updatePending = false
		au.mu.Unlock()
		fmt.Printf("[auto-update] Received announcement v%s (up to date)\n", msg.Version)
		return true // forward even though we don't need it
	}

	fmt.Printf("[auto-update] New version announced: v%s (current: v%s) from trusted seed %s...\n",
		msg.Version, Version, msg.NodeID[:16])

	au.mu.Lock()
	au.updatePending = true
	au.mu.Unlock()

	// Only download if --auto-update is enabled
	if au.autoUpdateEnabled {
		go au.triggerDownload(msg)
	}

	return true // forward to peers
}

// triggerDownload downloads, verifies, and applies an update from a gossip announcement.
func (au *AutoUpdater) triggerDownload(msg *UpdateAnnounceMsg) {
	binaryName := binaryName()
	downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s",
		au.repoOwner, au.repoName, msg.Version, binaryName)
	checksumsURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/checksums-sha256.txt",
		au.repoOwner, au.repoName, msg.Version)

	resp := &UpdateCheckResponse{
		Version:    msg.Version,
		Platform:   msg.Platform,
		BinaryHash: msg.BinaryHash,
	}

	err := au.performUpdate(downloadURL, checksumsURL, binaryName, resp)
	if err != nil {
		au.mu.Lock()
		au.lastError = err.Error()
		au.mu.Unlock()
		fmt.Printf("[auto-update] Update failed: %v\n", err)
	}
}

func binaryName() string {
	name := fmt.Sprintf("dilithium-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func (au *AutoUpdater) performUpdate(binaryURL, checksumsURL, binName string, seedResp *UpdateCheckResponse) error {
	// Step 1: Determine expected hash.
	// If same platform as seed node, use the seed node's hash directly.
	// Otherwise, fall back to GitHub checksums file.
	myPlatform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	var expectedHash string

	if seedResp.Platform == myPlatform && seedResp.BinaryHash != "" {
		expectedHash = seedResp.BinaryHash
		fmt.Println("[auto-update] Using hash from announcement (same platform)")
	} else {
		fmt.Println("[auto-update] Different platform from announcer, fetching GitHub checksums")
		var err error
		expectedHash, err = au.fetchExpectedChecksum(checksumsURL, binName)
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

func (au *AutoUpdater) fetchExpectedChecksum(checksumsURL, binName string) (string, error) {
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
		if len(parts) == 2 && parts[1] == binName {
			return parts[0], nil
		}
	}

	return "", fmt.Errorf("no checksum found for %s", binName)
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

	mode := "gossip"
	if au.isSeedNode {
		mode = "gossip-seed"
	}

	return map[string]interface{}{
		"enabled":            au.autoUpdateEnabled,
		"mode":               mode,
		"is_seed_node":       au.isSeedNode,
		"trusted_keys_count": len(au.trustedKeys),
		"last_check":         au.lastCheck.Unix(),
		"last_error":         au.lastError,
		"latest_remote":      au.latestRemote,
		"update_pending":     au.updatePending,
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
