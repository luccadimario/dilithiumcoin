package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ============================================================================
// CENSUS MANAGER
// ============================================================================

// CensusManager tracks network-wide node announcements for census data.
type CensusManager struct {
	announcements map[string]*NodeAnnounceMsg // nodeID -> latest announcement
	seen          map[string]bool             // dedup: "nodeID:timestamp"
	nodeKey       ed25519.PrivateKey
	nodeID        string
	startTime     time.Time

	mu   sync.RWMutex
	node *Node

	stopCh chan struct{}
}

// CensusData is the aggregated census snapshot returned via API.
type CensusData struct {
	EstimatedNodes      int                `json:"estimated_nodes"`
	VersionDistribution map[string]int     `json:"version_distribution"`
	AverageHeight       float64            `json:"average_height"`
	AveragePeerCount    float64            `json:"average_peer_count"`
	Timestamp           int64              `json:"timestamp"`
}

// NewCensusManager loads or generates a node key and returns a new CensusManager.
func NewCensusManager(dataDir string, node *Node) *CensusManager {
	keyPath := filepath.Join(dataDir, "node_key")
	privKey := loadOrGenerateNodeKey(keyPath)
	pubHex := hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

	return &CensusManager{
		announcements: make(map[string]*NodeAnnounceMsg),
		seen:          make(map[string]bool),
		nodeKey:       privKey,
		nodeID:        pubHex,
		startTime:     time.Now(),
		node:          node,
		stopCh:        make(chan struct{}),
	}
}

// NodeID returns this node's public identity.
func (cm *CensusManager) NodeID() string {
	return cm.nodeID
}

// Start launches the announcement and cleanup goroutines.
func (cm *CensusManager) Start() {
	go cm.announcementLoop()
	go cm.cleanupLoop()
	fmt.Println("Census manager started")
}

// Stop shuts down census goroutines.
func (cm *CensusManager) Stop() {
	close(cm.stopCh)
}

// ============================================================================
// ANNOUNCEMENT CREATION
// ============================================================================

// CreateAnnouncement builds and signs our own NodeAnnounceMsg.
func (cm *CensusManager) CreateAnnouncement() *NodeAnnounceMsg {
	var height int64
	var peerCount int
	var services uint64

	if cm.node != nil {
		height = cm.node.Blockchain.GetBlockCount()
		if cm.node.PeerManager != nil {
			peerCount = cm.node.PeerManager.PeerCount()
		}
		services = uint64(SFNodeNetwork)
	}

	now := time.Now()
	msg := &NodeAnnounceMsg{
		NodeID:    cm.nodeID,
		Version:   Version,
		Height:    height,
		Services:  services,
		PeerCount: peerCount,
		Uptime:    int64(now.Sub(cm.startTime).Seconds()),
		Timestamp: now.Unix(),
		TTL:       3,
	}

	// Sign: data = nodeID + version + height + timestamp
	sigData := fmt.Sprintf("%s:%s:%d:%d", msg.NodeID, msg.Version, msg.Height, msg.Timestamp)
	sig := ed25519.Sign(cm.nodeKey, []byte(sigData))
	msg.Signature = hex.EncodeToString(sig)

	return msg
}

// ============================================================================
// ANNOUNCEMENT PROCESSING
// ============================================================================

// ProcessAnnouncement validates, deduplicates, and stores an incoming announcement.
// Returns true if the announcement should be forwarded to peers.
func (cm *CensusManager) ProcessAnnouncement(msg *NodeAnnounceMsg) bool {
	if msg == nil || msg.NodeID == "" || msg.Signature == "" {
		return false
	}

	// Ignore our own announcements
	if msg.NodeID == cm.nodeID {
		return false
	}

	// TTL exhausted
	if msg.TTL <= 0 {
		return false
	}

	// Reject announcements older than 30 minutes
	if time.Now().Unix()-msg.Timestamp > 30*60 {
		return false
	}

	// Dedup check
	dedupeKey := fmt.Sprintf("%s:%d", msg.NodeID, msg.Timestamp)
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.seen[dedupeKey] {
		return false
	}

	// Verify Ed25519 signature
	pubBytes, err := hex.DecodeString(msg.NodeID)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false
	}
	sigData := fmt.Sprintf("%s:%s:%d:%d", msg.NodeID, msg.Version, msg.Height, msg.Timestamp)
	sigBytes, err := hex.DecodeString(msg.Signature)
	if err != nil {
		return false
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte(sigData), sigBytes) {
		return false
	}

	// Store (replace older announcement from same node)
	existing, exists := cm.announcements[msg.NodeID]
	if exists && existing.Timestamp >= msg.Timestamp {
		return false
	}

	cm.announcements[msg.NodeID] = msg
	cm.seen[dedupeKey] = true

	return true
}

// GetAllAnnouncements returns a copy of all cached announcements.
func (cm *CensusManager) GetAllAnnouncements() []*NodeAnnounceMsg {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]*NodeAnnounceMsg, 0, len(cm.announcements))
	for _, ann := range cm.announcements {
		result = append(result, ann)
	}
	return result
}

// ============================================================================
// CENSUS AGGREGATION
// ============================================================================

// GetCensus aggregates the current census data.
func (cm *CensusManager) GetCensus() *CensusData {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	data := &CensusData{
		EstimatedNodes:      len(cm.announcements) + 1, // +1 for ourselves
		VersionDistribution: make(map[string]int),
		Timestamp:           time.Now().Unix(),
	}

	// Include ourselves
	data.VersionDistribution[Version] = 1
	var totalHeight float64
	var totalPeers float64

	if cm.node != nil {
		totalHeight = float64(cm.node.Blockchain.GetBlockCount())
		if cm.node.PeerManager != nil {
			totalPeers = float64(cm.node.PeerManager.PeerCount())
		}
	}

	for _, ann := range cm.announcements {
		data.VersionDistribution[ann.Version]++
		totalHeight += float64(ann.Height)
		totalPeers += float64(ann.PeerCount)
	}

	if data.EstimatedNodes > 0 {
		data.AverageHeight = totalHeight / float64(data.EstimatedNodes)
		data.AveragePeerCount = totalPeers / float64(data.EstimatedNodes)
	}

	return data
}

// ============================================================================
// BACKGROUND GOROUTINES
// ============================================================================

// announcementLoop emits our announcement every 5 minutes.
func (cm *CensusManager) announcementLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Emit an initial announcement after a short delay
	time.Sleep(10 * time.Second)
	cm.emitAnnouncement()

	for {
		select {
		case <-cm.stopCh:
			return
		case <-ticker.C:
			cm.emitAnnouncement()
		}
	}
}

// emitAnnouncement creates and broadcasts our announcement.
func (cm *CensusManager) emitAnnouncement() {
	if cm.node == nil || cm.node.PeerManager == nil {
		return
	}

	ann := cm.CreateAnnouncement()
	cm.node.PeerManager.Broadcast(MsgTypeNodeAnnounce, ann)
}

// cleanupLoop prunes stale announcements every minute.
func (cm *CensusManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-cm.stopCh:
			return
		case <-ticker.C:
			cm.pruneStale()
		}
	}
}

// pruneStale removes announcements older than 30 minutes.
func (cm *CensusManager) pruneStale() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cutoff := time.Now().Unix() - 30*60

	for id, ann := range cm.announcements {
		if ann.Timestamp < cutoff {
			delete(cm.announcements, id)
		}
	}

	// Also prune seen-set entries (keep last hour of keys)
	seenCutoff := time.Now().Unix() - 3600
	for key := range cm.seen {
		// Parse timestamp from key "nodeID:timestamp"
		var ts int64
		for i := len(key) - 1; i >= 0; i-- {
			if key[i] == ':' {
				fmt.Sscanf(key[i+1:], "%d", &ts)
				break
			}
		}
		if ts > 0 && ts < seenCutoff {
			delete(cm.seen, key)
		}
	}
}

// ============================================================================
// NODE KEY MANAGEMENT
// ============================================================================

// loadOrGenerateNodeKey loads an Ed25519 key from disk, or generates a new one.
func loadOrGenerateNodeKey(path string) ed25519.PrivateKey {
	data, err := os.ReadFile(path)
	if err == nil && len(data) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(data)
	}

	// Generate new key
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Printf("Failed to generate node key: %v\n", err)
		os.Exit(1)
	}

	// Save to disk
	if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
		os.WriteFile(path, []byte(priv), 0600)
	}

	fmt.Println("Generated new node identity key")
	return priv
}
