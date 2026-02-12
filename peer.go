package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	crand "crypto/rand"
	"encoding/binary"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// PEER STATE
// ============================================================================

// PeerState represents the connection state of a peer
type PeerState int

const (
	PeerStateDisconnected PeerState = iota
	PeerStateConnecting
	PeerStateConnected
	PeerStateHandshaking
	PeerStateActive
	PeerStateBanned
)

func (s PeerState) String() string {
	switch s {
	case PeerStateDisconnected:
		return "disconnected"
	case PeerStateConnecting:
		return "connecting"
	case PeerStateConnected:
		return "connected"
	case PeerStateHandshaking:
		return "handshaking"
	case PeerStateActive:
		return "active"
	case PeerStateBanned:
		return "banned"
	default:
		return "unknown"
	}
}

// ============================================================================
// PEER CONFIGURATION
// ============================================================================

// PeerConfig holds peer manager configuration
type PeerConfig struct {
	MaxInbound      int           // Maximum inbound connections
	MaxOutbound     int           // Maximum outbound connections
	HandshakeTimeout time.Duration // Timeout for handshake completion
	PingInterval    time.Duration // Interval between pings
	PingTimeout     time.Duration // Timeout waiting for pong
	ConnectTimeout  time.Duration // Timeout for TCP connection
	BanDuration     time.Duration // How long to ban misbehaving peers
	MinPeers        int           // Minimum peers to maintain
	MaxAddrBook     int           // Maximum addresses to store
}

// DefaultPeerConfig returns the default peer configuration
func DefaultPeerConfig() *PeerConfig {
	return &PeerConfig{
		MaxInbound:       32,
		MaxOutbound:      8,
		HandshakeTimeout: 30 * time.Second,
		PingInterval:     2 * time.Minute,
		PingTimeout:      30 * time.Second,
		ConnectTimeout:   10 * time.Second,
		BanDuration:      24 * time.Hour,
		MinPeers:         3,
		MaxAddrBook:      1000,
	}
}

// ============================================================================
// PEER
// ============================================================================

// Peer represents a connected peer
type Peer struct {
	// Connection info
	Addr       string
	Conn       net.Conn
	State      PeerState
	Inbound    bool   // True if peer connected to us
	ListenPort uint16 // Peer's advertised listening port (from version msg)

	// Handshake data
	Version     uint32
	Services    ServiceFlag
	UserAgent   string
	StartHeight int64
	Nonce       uint64

	// Timestamps
	ConnectedAt   time.Time
	LastSeen      time.Time
	LastPing      time.Time
	LastPong      time.Time

	// Ping tracking
	PingNonce     uint64
	PingPending   bool

	// Statistics
	BytesSent     uint64
	BytesRecv     uint64
	MsgsSent      uint64
	MsgsRecv      uint64

	// State management
	mutex         sync.RWMutex
	sendCh        chan *P2PMessage
	stopCh        chan struct{}
	manager       *PeerManager
}

// cryptoRandUint64 generates a cryptographically secure random uint64 (shannon #14)
func cryptoRandUint64() uint64 {
	var buf [8]byte
	if _, err := crand.Read(buf[:]); err != nil {
		// Fallback to math/rand if crypto/rand fails (shouldn't happen)
		return rand.Uint64()
	}
	return binary.LittleEndian.Uint64(buf[:])
}

// NewPeer creates a new peer instance
func NewPeer(addr string, conn net.Conn, inbound bool, manager *PeerManager) *Peer {
	return &Peer{
		Addr:        addr,
		Conn:        conn,
		State:       PeerStateConnected,
		Inbound:     inbound,
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
		sendCh:      make(chan *P2PMessage, 100),
		stopCh:      make(chan struct{}),
		manager:     manager,
	}
}

// Start begins the peer's read/write loops
func (p *Peer) Start() {
	go p.readLoop()
	go p.writeLoop()
	go p.pingLoop()
}

// Stop gracefully disconnects the peer
func (p *Peer) Stop() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.State == PeerStateDisconnected {
		return
	}

	p.State = PeerStateDisconnected
	close(p.stopCh)

	if p.Conn != nil {
		p.Conn.Close()
	}
}

// Send queues a message to be sent to the peer
func (p *Peer) Send(msg *P2PMessage) error {
	p.mutex.RLock()
	if p.State == PeerStateDisconnected {
		p.mutex.RUnlock()
		return fmt.Errorf("peer disconnected")
	}
	p.mutex.RUnlock()

	select {
	case p.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("send buffer full")
	}
}

// SendMessage creates and sends a typed message
func (p *Peer) SendMessage(msgType MsgType, payload interface{}) error {
	msg, err := NewP2PMessage(msgType, payload)
	if err != nil {
		return err
	}
	return p.Send(msg)
}

// readLoop reads messages from the peer
func (p *Peer) readLoop() {
	defer p.manager.handleDisconnect(p)

	scanner := bufio.NewScanner(p.Conn)
	// Increase buffer size for large messages (blocks with many transactions)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max

	for scanner.Scan() {
		select {
		case <-p.stopCh:
			return
		default:
		}

		data := scanner.Bytes()

		// Try to decode as P2PMessage first
		msg, err := DecodeMessage(data)
		if err != nil {
			// Try legacy format
			var legacy LegacyMessage
			if jsonErr := json.Unmarshal(data, &legacy); jsonErr == nil {
				msg, _ = legacy.ToP2PMessage()
			} else {
				fmt.Printf("Error decoding message from %s: %v\n", p.Addr, err)
				continue
			}
		}

		p.mutex.Lock()
		p.LastSeen = time.Now()
		p.BytesRecv += uint64(len(data))
		p.MsgsRecv++
		p.mutex.Unlock()

		p.manager.handleMessage(p, msg)
	}
}

// writeLoop writes messages to the peer
func (p *Peer) writeLoop() {
	for {
		select {
		case <-p.stopCh:
			return
		case msg := <-p.sendCh:
			data, err := EncodeMessage(msg)
			if err != nil {
				fmt.Printf("Error encoding message: %v\n", err)
				continue
			}

			_, err = fmt.Fprintf(p.Conn, "%s\n", data)
			if err != nil {
				fmt.Printf("Error sending to %s: %v\n", p.Addr, err)
				p.Stop()
				return
			}

			p.mutex.Lock()
			p.BytesSent += uint64(len(data))
			p.MsgsSent++
			p.mutex.Unlock()
		}
	}
}

// pingLoop sends periodic pings to check connection health
func (p *Peer) pingLoop() {
	ticker := time.NewTicker(p.manager.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.mutex.Lock()
			if p.State != PeerStateActive {
				p.mutex.Unlock()
				continue
			}

			// Check if previous ping timed out
			if p.PingPending && time.Since(p.LastPing) > p.manager.config.PingTimeout {
				p.mutex.Unlock()
				fmt.Printf("Peer %s ping timeout\n", p.Addr)
				p.Stop()
				return
			}

			// Send new ping (use crypto/rand for nonces - shannon #14)
			p.PingNonce = cryptoRandUint64()
			p.PingPending = true
			p.LastPing = time.Now()
			p.mutex.Unlock()

			ping := NewPingMsg(p.PingNonce)
			p.SendMessage(MsgTypePing, ping)
		}
	}
}

// handlePong processes a pong response
func (p *Peer) handlePong(nonce uint64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.PingPending && p.PingNonce == nonce {
		p.PingPending = false
		p.LastPong = time.Now()
	}
}

// Info returns peer information for display
func (p *Peer) Info() map[string]interface{} {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return map[string]interface{}{
		"address":      p.Addr,
		"state":        p.State.String(),
		"inbound":      p.Inbound,
		"version":      p.Version,
		"user_agent":   p.UserAgent,
		"start_height": p.StartHeight,
		"connected_at": p.ConnectedAt.Unix(),
		"last_seen":    p.LastSeen.Unix(),
		"bytes_sent":   p.BytesSent,
		"bytes_recv":   p.BytesRecv,
		"msgs_sent":    p.MsgsSent,
		"msgs_recv":    p.MsgsRecv,
	}
}

// ============================================================================
// ADDRESS BOOK ENTRY
// ============================================================================

// AddrBookEntry stores information about a known peer address
type AddrBookEntry struct {
	Addr        *NetAddr
	LastAttempt time.Time
	LastSuccess time.Time
	Attempts    int
	Source      string // Address of peer that told us about this
}

// ============================================================================
// PEER MANAGER
// ============================================================================

// PeerManager manages all peer connections
type PeerManager struct {
	// Configuration
	config    *PeerConfig
	services  ServiceFlag
	localAddr *NetAddr
	nonce     uint64 // Our unique nonce to detect self-connections

	// Peer storage
	peers     map[string]*Peer
	peerMutex sync.RWMutex

	// Address book
	addrBook      map[string]*AddrBookEntry
	addrBookMutex sync.RWMutex

	// Ban list
	banned      map[string]time.Time
	bannedMutex sync.RWMutex

	// Node reference
	node *Node

	// State
	started bool
	stopCh  chan struct{}
}

// NewPeerManager creates a new peer manager
func NewPeerManager(node *Node, config *PeerConfig) *PeerManager {
	if config == nil {
		config = DefaultPeerConfig()
	}

	return &PeerManager{
		config:   config,
		services: SFNodeNetwork,
		nonce:    cryptoRandUint64(),
		peers:    make(map[string]*Peer),
		addrBook: make(map[string]*AddrBookEntry),
		banned:   make(map[string]time.Time),
		node:     node,
		stopCh:   make(chan struct{}),
	}
}

// Start starts the peer manager
func (pm *PeerManager) Start(localAddr *NetAddr) {
	pm.localAddr = localAddr
	pm.started = true

	go pm.maintainPeers()
	go pm.cleanupBanned()

	fmt.Println("Peer manager started")
}

// Stop stops the peer manager and disconnects all peers
func (pm *PeerManager) Stop() {
	close(pm.stopCh)

	pm.peerMutex.Lock()
	for _, peer := range pm.peers {
		peer.Stop()
	}
	pm.peerMutex.Unlock()

	fmt.Println("Peer manager stopped")
}

// ============================================================================
// CONNECTION MANAGEMENT
// ============================================================================

// HandleInbound handles a new inbound connection
func (pm *PeerManager) HandleInbound(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()

	// Check if banned
	if pm.IsBanned(remoteAddr) {
		fmt.Printf("Rejected banned peer: %s\n", remoteAddr)
		conn.Close()
		return
	}

	// Check inbound limit
	if pm.InboundCount() >= pm.config.MaxInbound {
		fmt.Printf("Rejecting connection from %s: inbound limit reached\n", remoteAddr)
		conn.Close()
		return
	}

	// Create peer
	peer := NewPeer(remoteAddr, conn, true, pm)

	pm.peerMutex.Lock()
	pm.peers[remoteAddr] = peer
	pm.peerMutex.Unlock()

	fmt.Printf("New inbound peer: %s\n", remoteAddr)

	// Start peer loops
	peer.Start()

	// Wait for version message (they should send first as they connected to us)
	// The handshake will be handled in handleMessage
}

// Connect initiates an outbound connection to a peer
func (pm *PeerManager) Connect(addr string) error {
	// Check if trying to connect to ourselves
	if pm.isOwnAddress(addr) {
		return fmt.Errorf("cannot connect to self")
	}

	// Check if already connected (exact address match)
	if pm.IsConnected(addr) {
		return fmt.Errorf("already connected to %s", addr)
	}

	// Check if already connected by listen port (catches localhost variants)
	host, portStr, err := net.SplitHostPort(addr)
	if err == nil {
		var port uint16
		fmt.Sscanf(portStr, "%d", &port)
		if pm.isConnectedByListenPort(host, port) {
			return fmt.Errorf("already connected to peer at port %d", port)
		}
	}

	// Check if banned
	if pm.IsBanned(addr) {
		return fmt.Errorf("peer %s is banned", addr)
	}

	// Check outbound limit
	if pm.OutboundCount() >= pm.config.MaxOutbound {
		return fmt.Errorf("outbound connection limit reached")
	}

	// Connect with timeout
	conn, err := net.DialTimeout("tcp", addr, pm.config.ConnectTimeout)
	if err != nil {
		// Record failed attempt
		pm.recordAttempt(addr, false)
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Create peer
	peer := NewPeer(addr, conn, false, pm)

	pm.peerMutex.Lock()
	pm.peers[addr] = peer
	pm.peerMutex.Unlock()

	fmt.Printf("Connected to peer: %s\n", addr)

	// Start peer loops
	peer.Start()

	// Initiate handshake (we connected, so we send version first)
	pm.sendVersion(peer)

	return nil
}

// Disconnect disconnects a peer by address
func (pm *PeerManager) Disconnect(addr string) {
	pm.peerMutex.Lock()
	peer, exists := pm.peers[addr]
	pm.peerMutex.Unlock()

	if exists {
		peer.Stop()
	}
}

// handleDisconnect cleans up after a peer disconnects
func (pm *PeerManager) handleDisconnect(peer *Peer) {
	pm.peerMutex.Lock()
	delete(pm.peers, peer.Addr)
	pm.peerMutex.Unlock()

	fmt.Printf("Peer disconnected: %s\n", peer.Addr)
}

// ============================================================================
// HANDSHAKE
// ============================================================================

// sendVersion sends our version message to a peer
func (pm *PeerManager) sendVersion(peer *Peer) {
	recvAddr := NewNetAddr("", 0, 0) // We don't know their services yet

	version := NewVersionMsg(
		pm.services,
		recvAddr,
		pm.localAddr,
		pm.node.Blockchain.GetBlockCount(),
		pm.nonce,
	)

	peer.mutex.Lock()
	peer.State = PeerStateHandshaking
	peer.mutex.Unlock()

	peer.SendMessage(MsgTypeVersion, version)
}

// handleVersion processes an incoming version message
func (pm *PeerManager) handleVersion(peer *Peer, msg *VersionMsg) {
	// Check for self-connection
	if msg.Nonce == pm.nonce {
		fmt.Printf("Detected self-connection, disconnecting %s\n", peer.Addr)
		peer.Stop()
		return
	}

	// Store peer info including their advertised listen port
	peer.mutex.Lock()
	peer.Version = msg.Version
	peer.Services = msg.Services
	peer.UserAgent = msg.UserAgent
	peer.StartHeight = msg.StartHeight
	peer.Nonce = msg.Nonce
	if msg.AddrFrom != nil {
		peer.ListenPort = msg.AddrFrom.Port
	}
	wasHandshaking := peer.State == PeerStateHandshaking
	peer.mutex.Unlock()

	fmt.Printf("Received version from %s: %s height=%d\n", peer.Addr, msg.UserAgent, msg.StartHeight)

	// Learn the peer's advertised address for future reconnection/discovery
	if msg.AddrFrom != nil && msg.AddrFrom.Port > 0 {
		host, _, err := net.SplitHostPort(peer.Addr)
		if err == nil {
			// Normalize IPv6 (remove brackets)
			host = strings.TrimPrefix(host, "[")
			host = strings.TrimSuffix(host, "]")

			// Save peer's listening address (for reconnection or peer discovery)
			learnedAddr := NewNetAddr(host, msg.AddrFrom.Port, msg.Services)
			pm.addAddressDirect(learnedAddr, "version")
		}
	}

	// If we're inbound (they connected to us), send our version back
	if peer.Inbound {
		pm.sendVersion(peer)
	}

	// Send version acknowledgment
	peer.SendMessage(MsgTypeVersionAck, NewVersionAckMsg())

	// If we were waiting for their version (outbound), mark handshake as complete
	// The actual activation happens when we receive their verack
	if !wasHandshaking {
		peer.mutex.Lock()
		peer.State = PeerStateHandshaking
		peer.mutex.Unlock()
	}
}

// handleVersionAck processes a version acknowledgment
func (pm *PeerManager) handleVersionAck(peer *Peer) {
	peer.mutex.Lock()
	if peer.State != PeerStateHandshaking {
		peer.mutex.Unlock()
		return
	}
	peer.State = PeerStateActive
	startHeight := peer.StartHeight
	peer.mutex.Unlock()

	fmt.Printf("Handshake complete with %s\n", peer.Addr)

	// Record successful connection
	pm.recordAttempt(peer.Addr, true)

	// Request addresses from this peer
	peer.SendMessage(MsgTypeGetAddr, NewGetAddrMsg())

	// Send them our known addresses
	addrs := pm.GetAddresses(MaxAddrPerMessage)
	if len(addrs) > 0 {
		peer.SendMessage(MsgTypeAddr, NewAddrMsg(addrs))
	}

	// Sync blockchain if they have more blocks
	ourHeight := pm.node.Blockchain.GetBlockCount()
	if startHeight > ourHeight {
		fmt.Printf("Peer %s has longer chain (%d vs %d), requesting sync...\n",
			peer.Addr, startHeight, ourHeight)
		// Request blocks we don't have
		pm.requestChainSync(peer)
	}

	// Also sync mempool
	peer.SendMessage(MsgTypeMempool, NewMempoolMsg())

	// Send our chain to peer if we have more blocks
	if ourHeight > startHeight {
		fmt.Printf("Sending our chain to %s (we have %d, they have %d)\n",
			peer.Addr, ourHeight, startHeight)
		pm.sendChain(peer)
	}
}

// ============================================================================
// MESSAGE HANDLING
// ============================================================================

// handleMessage processes an incoming message from a peer
func (pm *PeerManager) handleMessage(peer *Peer, msg *P2PMessage) {
	// Version and VerAck can be processed without completed handshake
	switch msg.Type {
	case MsgTypeVersion:
		var version VersionMsg
		if err := msg.Decode(&version); err != nil {
			fmt.Printf("Error decoding version: %v\n", err)
			return
		}
		pm.handleVersion(peer, &version)
		return

	case MsgTypeVersionAck:
		pm.handleVersionAck(peer)
		return
	}

	// All other messages require completed handshake
	peer.mutex.RLock()
	state := peer.State
	peer.mutex.RUnlock()

	if state != PeerStateActive {
		fmt.Printf("Ignoring %s from %s: handshake not complete\n", msg.Type, peer.Addr)
		return
	}

	// Dispatch to appropriate handler
	switch msg.Type {
	case MsgTypePing:
		var ping PingMsg
		if err := msg.Decode(&ping); err == nil {
			peer.SendMessage(MsgTypePong, NewPongMsg(ping.Nonce))
		}

	case MsgTypePong:
		var pong PongMsg
		if err := msg.Decode(&pong); err == nil {
			peer.handlePong(pong.Nonce)
		}

	case MsgTypeAddr:
		var addr AddrMsg
		if err := msg.Decode(&addr); err == nil {
			pm.handleAddr(peer, &addr)
		}

	case MsgTypeGetAddr:
		addrs := pm.GetAddresses(MaxAddrPerMessage)
		peer.SendMessage(MsgTypeAddr, NewAddrMsg(addrs))

	case MsgTypeInv:
		var inv InvMsg
		if err := msg.Decode(&inv); err == nil {
			pm.handleInv(peer, &inv)
		}

	case MsgTypeGetData:
		var getData GetDataMsg
		if err := msg.Decode(&getData); err == nil {
			pm.handleGetData(peer, &getData)
		}

	case MsgTypeTx:
		var tx TxMsg
		if err := msg.Decode(&tx); err == nil {
			pm.handleTx(peer, &tx)
		}

	case MsgTypeBlock:
		var block BlockMsg
		if err := msg.Decode(&block); err == nil {
			pm.handleBlock(peer, &block)
		}

	case MsgTypeMempool:
		pm.handleMempool(peer)

	case MsgTypeReject:
		var reject RejectMsg
		if err := msg.Decode(&reject); err == nil {
			fmt.Printf("Peer %s rejected %s: %s (%s)\n", peer.Addr, reject.Message, reject.Reason, reject.Code)
		}

	// Legacy message support
	case MsgTypeTransaction:
		pm.handleLegacyTransaction(peer, msg)

	case MsgTypeChain:
		pm.handleLegacyChain(peer, msg)

	default:
		fmt.Printf("Unknown message type %s from %s\n", msg.Type, peer.Addr)
	}
}

// handleAddr processes received addresses
func (pm *PeerManager) handleAddr(peer *Peer, msg *AddrMsg) {
	count := 0
	for _, addr := range msg.Addresses {
		if pm.AddAddress(addr, peer.Addr) {
			count++
		}
	}
	if count > 0 {
		fmt.Printf("Added %d addresses from %s\n", count, peer.Addr)
	}
}

// handleInv processes inventory announcements
func (pm *PeerManager) handleInv(peer *Peer, msg *InvMsg) {
	// Build list of items we need
	var needed []*InvVector

	for _, inv := range msg.Inventory {
		switch inv.Type {
		case InvTypeTx:
			// Check if we already have this transaction
			if !pm.node.Blockchain.HasTransaction(inv.Hash) {
				needed = append(needed, inv)
			}
		case InvTypeBlock:
			// Check if we already have this block
			if !pm.node.Blockchain.HasBlock(inv.Hash) {
				needed = append(needed, inv)
			}
		}
	}

	// Request needed items
	if len(needed) > 0 {
		peer.SendMessage(MsgTypeGetData, NewGetDataMsg(needed))
	}
}

// handleGetData processes requests for specific data
func (pm *PeerManager) handleGetData(peer *Peer, msg *GetDataMsg) {
	for _, inv := range msg.Inventory {
		switch inv.Type {
		case InvTypeTx:
			tx := pm.node.Blockchain.GetTransaction(inv.Hash)
			if tx != nil {
				peer.SendMessage(MsgTypeTx, NewTxMsg(tx))
			}
		case InvTypeBlock:
			if inv.Hash == "sync" {
				// Special sync request — send full chain
				pm.sendChain(peer)
			} else {
				block := pm.node.Blockchain.GetBlock(inv.Hash)
				if block != nil {
					peer.SendMessage(MsgTypeBlock, NewBlockMsg(block))
				}
			}
		}
	}
}

// handleTx processes a received transaction
func (pm *PeerManager) handleTx(peer *Peer, msg *TxMsg) {
	if msg.Transaction == nil {
		return
	}

	added, err := pm.node.Blockchain.AddTransactionIfNew(msg.Transaction)
	if err != nil {
		// Send reject message
		reject := NewRejectMsg(MsgTypeTx, RejectInvalid, err.Error(), msg.Transaction.Signature)
		peer.SendMessage(MsgTypeReject, reject)
		return
	}

	if added {
		fmt.Printf("Received transaction from %s\n", peer.Addr)
		// Announce to other peers
		pm.BroadcastInv([]*InvVector{NewInvVector(InvTypeTx, msg.Transaction.Signature)}, peer.Addr)
	}
}

// handleBlock processes a received block
func (pm *PeerManager) handleBlock(peer *Peer, msg *BlockMsg) {
	if msg.Block == nil {
		return
	}

	// Use existing block handling logic from node
	// This creates a legacy message format for compatibility
	legacyMsg := &Message{
		Type:      "block",
		Data:      msg.Block,
		Timestamp: time.Now().Unix(),
	}

	pm.node.handleMessage(*legacyMsg, peer.Addr)

	// Announce to other peers if accepted
	pm.BroadcastInv([]*InvVector{NewInvVector(InvTypeBlock, msg.Block.Hash)}, peer.Addr)
}

// handleMempool responds with our mempool contents
func (pm *PeerManager) handleMempool(peer *Peer) {
	txs := pm.node.Blockchain.GetPendingTransactions()

	if len(txs) == 0 {
		return
	}

	// Send inventory of all mempool transactions
	var inv []*InvVector
	for _, tx := range txs {
		inv = append(inv, NewInvVector(InvTypeTx, tx.Signature))
	}

	peer.SendMessage(MsgTypeInv, NewInvMsg(inv))
}

// handleLegacyTransaction processes a legacy transaction message
func (pm *PeerManager) handleLegacyTransaction(peer *Peer, msg *P2PMessage) {
	legacyMsg := FromP2PMessage(msg)
	pm.node.handleMessage(Message{
		Type:      legacyMsg.Type,
		Data:      legacyMsg.Data,
		Timestamp: legacyMsg.Timestamp,
	}, peer.Addr)
}

// handleLegacyChain processes a legacy chain message
func (pm *PeerManager) handleLegacyChain(peer *Peer, msg *P2PMessage) {
	// Decode the chain directly
	var blocks []*Block
	if err := msg.Decode(&blocks); err != nil {
		fmt.Printf("Error decoding chain from %s: %v\n", peer.Addr, err)
		return
	}

	fmt.Printf("Received chain with %d blocks from %s\n", len(blocks), peer.Addr)

	pm.node.Blockchain.mutex.Lock()
	defer pm.node.Blockchain.mutex.Unlock()

	// Silently ignore chains that aren't longer
	if len(blocks) <= len(pm.node.Blockchain.Blocks) {
		return
	}

	// Use cumulative work for chain selection (shannon #7)
	if cumulativeWork(blocks) > cumulativeWork(pm.node.Blockchain.Blocks) && pm.node.isValidChain(blocks) {
		fmt.Printf("Replacing our chain (length %d) with peer's chain (length %d)\n",
			len(pm.node.Blockchain.Blocks), len(blocks))

		// Cancel any in-progress mining
		pm.node.cancelMining()

		pm.node.Blockchain.Blocks = blocks
		pm.node.Blockchain.recalcDifficultyFromChain()
		pm.node.Blockchain.clearMempool()
	} else if len(blocks) > len(pm.node.Blockchain.Blocks) {
		fmt.Printf("Received invalid chain from %s, ignoring\n", peer.Addr)
	}
}

// ============================================================================
// ADDRESS BOOK
// ============================================================================

// AddAddress adds an address to the address book (filters localhost for gossip)
func (pm *PeerManager) AddAddress(addr *NetAddr, source string) bool {
	// Filter localhost addresses - they're not useful from gossip
	if isLocalAddress(addr.IP) {
		return false
	}
	return pm.addAddressDirect(addr, source)
}

// addAddressDirect adds an address without localhost filtering
// Used for addresses learned directly from peer connections
func (pm *PeerManager) addAddressDirect(addr *NetAddr, source string) bool {
	key := addr.Address()

	// Don't add our own address (check multiple formats)
	if pm.isOwnAddress(key) {
		return false
	}

	// Don't add if already connected
	if pm.IsConnected(key) {
		return false
	}

	pm.addrBookMutex.Lock()
	defer pm.addrBookMutex.Unlock()

	// Check limit
	if len(pm.addrBook) >= pm.config.MaxAddrBook {
		// Could implement smarter eviction here
		return false
	}

	// Add or update
	if existing, exists := pm.addrBook[key]; exists {
		// Update timestamp if newer
		if addr.Timestamp > existing.Addr.Timestamp {
			existing.Addr = addr
		}
		return false
	}

	pm.addrBook[key] = &AddrBookEntry{
		Addr:   addr,
		Source: source,
	}

	return true
}

// GetAddresses returns known addresses for sharing
func (pm *PeerManager) GetAddresses(max int) []*NetAddr {
	pm.addrBookMutex.RLock()
	defer pm.addrBookMutex.RUnlock()

	var addrs []*NetAddr

	// Include connected peers (but not localhost addresses)
	pm.peerMutex.RLock()
	for addr, peer := range pm.peers {
		if peer.State == PeerStateActive {
			host, port, _ := net.SplitHostPort(addr)

			// Skip localhost addresses - they're not useful to share
			if isLocalAddress(host) {
				continue
			}

			var portNum uint16
			fmt.Sscanf(port, "%d", &portNum)
			addrs = append(addrs, NewNetAddr(host, portNum, peer.Services))
		}
		if len(addrs) >= max {
			pm.peerMutex.RUnlock()
			return addrs
		}
	}
	pm.peerMutex.RUnlock()

	// Add from address book (skip localhost)
	for _, entry := range pm.addrBook {
		if !isLocalAddress(entry.Addr.IP) {
			addrs = append(addrs, entry.Addr)
			if len(addrs) >= max {
				break
			}
		}
	}

	return addrs
}

// isLocalAddress checks if an address is a localhost/local address
func isLocalAddress(host string) bool {
	localHosts := []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"0.0.0.0",
		"",
	}

	for _, lh := range localHosts {
		if host == lh {
			return true
		}
	}

	return false
}

// recordAttempt records a connection attempt
func (pm *PeerManager) recordAttempt(addr string, success bool) {
	pm.addrBookMutex.Lock()
	defer pm.addrBookMutex.Unlock()

	entry, exists := pm.addrBook[addr]
	if !exists {
		return
	}

	entry.LastAttempt = time.Now()
	entry.Attempts++

	if success {
		entry.LastSuccess = time.Now()
	}
}

// ============================================================================
// BAN LIST
// ============================================================================

// Ban adds a peer to the ban list
func (pm *PeerManager) Ban(addr string, reason string) {
	pm.bannedMutex.Lock()
	pm.banned[addr] = time.Now().Add(pm.config.BanDuration)
	pm.bannedMutex.Unlock()

	fmt.Printf("Banned peer %s: %s\n", addr, reason)

	// Disconnect if connected
	pm.Disconnect(addr)
}

// Unban removes a peer from the ban list
func (pm *PeerManager) Unban(addr string) {
	pm.bannedMutex.Lock()
	delete(pm.banned, addr)
	pm.bannedMutex.Unlock()
}

// IsBanned checks if a peer is banned
func (pm *PeerManager) IsBanned(addr string) bool {
	pm.bannedMutex.RLock()
	expiry, exists := pm.banned[addr]
	pm.bannedMutex.RUnlock()

	if !exists {
		return false
	}

	return time.Now().Before(expiry)
}

// cleanupBanned periodically removes expired bans
func (pm *PeerManager) cleanupBanned() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-pm.stopCh:
			return
		case <-ticker.C:
			pm.bannedMutex.Lock()
			now := time.Now()
			for addr, expiry := range pm.banned {
				if now.After(expiry) {
					delete(pm.banned, addr)
				}
			}
			pm.bannedMutex.Unlock()
		}
	}
}

// ============================================================================
// PEER QUERIES
// ============================================================================

// IsConnected checks if we're connected to an address
func (pm *PeerManager) IsConnected(addr string) bool {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()
	_, exists := pm.peers[addr]
	return exists
}

// isConnectedByListenPort checks if we're already connected to a peer
// that advertises the given host and listen port. This catches duplicate
// connections where the address strings differ (e.g., localhost vs [::1]).
func (pm *PeerManager) isConnectedByListenPort(host string, port uint16) bool {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()

	// Normalize host
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	for _, peer := range pm.peers {
		if peer.ListenPort == port && peer.ListenPort > 0 {
			// Same listen port - check if same host (for localhost testing)
			peerHost, _, err := net.SplitHostPort(peer.Addr)
			if err == nil {
				peerHost = strings.TrimPrefix(peerHost, "[")
				peerHost = strings.TrimSuffix(peerHost, "]")

				// If both are localhost variants, consider them the same peer
				if isLocalAddress(host) && isLocalAddress(peerHost) {
					return true
				}
				// If IPs match exactly
				if host == peerHost {
					return true
				}
			}
		}
	}
	return false
}

// PeerCount returns the total number of connected peers
func (pm *PeerManager) PeerCount() int {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()
	return len(pm.peers)
}

// InboundCount returns the number of inbound peers
func (pm *PeerManager) InboundCount() int {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()

	count := 0
	for _, peer := range pm.peers {
		if peer.Inbound {
			count++
		}
	}
	return count
}

// OutboundCount returns the number of outbound peers
func (pm *PeerManager) OutboundCount() int {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()

	count := 0
	for _, peer := range pm.peers {
		if !peer.Inbound {
			count++
		}
	}
	return count
}

// GetPeers returns info about all connected peers
func (pm *PeerManager) GetPeers() []map[string]interface{} {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()

	var peers []map[string]interface{}
	for _, peer := range pm.peers {
		peers = append(peers, peer.Info())
	}
	return peers
}

// GetActivePeers returns all active peers
func (pm *PeerManager) GetActivePeers() []*Peer {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()

	var peers []*Peer
	for _, peer := range pm.peers {
		peer.mutex.RLock()
		active := peer.State == PeerStateActive
		peer.mutex.RUnlock()

		if active {
			peers = append(peers, peer)
		}
	}
	return peers
}

// ============================================================================
// BROADCASTING
// ============================================================================

// Broadcast sends a message to all active peers
func (pm *PeerManager) Broadcast(msgType MsgType, payload interface{}) {
	msg, err := NewP2PMessage(msgType, payload)
	if err != nil {
		fmt.Printf("Error creating broadcast message: %v\n", err)
		return
	}

	for _, peer := range pm.GetActivePeers() {
		peer.Send(msg)
	}
}

// BroadcastExcept sends a message to all active peers except one
func (pm *PeerManager) BroadcastExcept(msgType MsgType, payload interface{}, except string) {
	msg, err := NewP2PMessage(msgType, payload)
	if err != nil {
		fmt.Printf("Error creating broadcast message: %v\n", err)
		return
	}

	for _, peer := range pm.GetActivePeers() {
		if peer.Addr != except {
			peer.Send(msg)
		}
	}
}

// BroadcastInv announces inventory to all peers except the source
func (pm *PeerManager) BroadcastInv(inv []*InvVector, except string) {
	if len(inv) == 0 {
		return
	}
	pm.BroadcastExcept(MsgTypeInv, NewInvMsg(inv), except)
}

// BroadcastTransaction broadcasts a new transaction
func (pm *PeerManager) BroadcastTransaction(tx *Transaction) {
	pm.Broadcast(MsgTypeTx, NewTxMsg(tx))
}

// BroadcastBlock broadcasts a new block
func (pm *PeerManager) BroadcastBlock(block *Block) {
	pm.Broadcast(MsgTypeBlock, NewBlockMsg(block))
}

// sendChain sends our full blockchain to a peer
func (pm *PeerManager) sendChain(peer *Peer) {
	blocks := pm.node.Blockchain.GetBlocks()

	// Send as legacy chain message for compatibility
	msg, err := NewP2PMessage(MsgTypeChain, blocks)
	if err != nil {
		fmt.Printf("Error creating chain message: %v\n", err)
		return
	}
	peer.Send(msg)
}

// requestChainSync requests the blockchain from a peer
func (pm *PeerManager) requestChainSync(peer *Peer) {
	// Request the peer's chain by sending a getdata for a block inventory
	// The peer will respond with their full chain
	ourHeight := pm.node.Blockchain.GetBlockCount()
	fmt.Printf("Requesting chain sync from %s (our height: %d)\n", peer.Addr, ourHeight)

	// Send a legacy chain request that triggers sendChain on the peer side
	inv := []*InvVector{NewInvVector(InvTypeBlock, "sync")}
	peer.SendMessage(MsgTypeGetData, NewGetDataMsg(inv))
}

// GetPeerByAddr returns a connected peer by address, or nil if not found
func (pm *PeerManager) GetPeerByAddr(addr string) *Peer {
	pm.peerMutex.RLock()
	defer pm.peerMutex.RUnlock()
	for _, peer := range pm.peers {
		if peer.Addr == addr {
			return peer
		}
	}
	return nil
}

// ============================================================================
// PEER MAINTENANCE
// ============================================================================

// maintainPeers ensures we have enough peer connections
func (pm *PeerManager) maintainPeers() {
	// Initial quick check after 5 seconds
	time.Sleep(5 * time.Second)
	pm.checkAndConnectPeers()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pm.stopCh:
			return
		case <-ticker.C:
			pm.checkAndConnectPeers()
		}
	}
}

// checkAndConnectPeers checks if we need more peers and tries to connect
func (pm *PeerManager) checkAndConnectPeers() {
	outbound := pm.OutboundCount()
	needed := pm.config.MinPeers - outbound

	if needed > 0 {
		fmt.Printf("Need %d more outbound peers (have %d)\n", needed, outbound)
		pm.connectToNewPeers(needed)
	}
}

// connectToNewPeers attempts to connect to new peers from address book
func (pm *PeerManager) connectToNewPeers(count int) {
	pm.addrBookMutex.RLock()
	var candidates []*AddrBookEntry

	for _, entry := range pm.addrBook {
		addr := entry.Addr.Address()

		// Skip our own address
		if pm.isOwnAddress(addr) {
			continue
		}

		// Skip if connected or recently attempted
		if pm.IsConnected(addr) {
			continue
		}
		if time.Since(entry.LastAttempt) < 5*time.Minute {
			continue
		}
		candidates = append(candidates, entry)
	}
	pm.addrBookMutex.RUnlock()

	// Shuffle and try to connect
	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	connected := 0
	for _, entry := range candidates {
		if connected >= count {
			break
		}

		addr := entry.Addr.Address()
		if err := pm.Connect(addr); err == nil {
			connected++
		}
	}
}

// AddSeedNode adds a seed node address
func (pm *PeerManager) AddSeedNode(addr string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}

	var portNum uint16
	fmt.Sscanf(port, "%d", &portNum)

	netAddr := NewNetAddr(host, portNum, SFNodeNetwork)
	pm.AddAddress(netAddr, "seed")
}

// isOwnAddress checks if an address is our own node (various formats)
func (pm *PeerManager) isOwnAddress(addr string) bool {
	if pm.localAddr == nil {
		return false
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	localPort := fmt.Sprintf("%d", pm.localAddr.Port)
	if port != localPort {
		return false
	}

	// Normalize IPv6 (remove brackets if present)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	// Check various localhost formats
	localHosts := []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"0.0.0.0",
		"",
	}

	for _, lh := range localHosts {
		if host == lh {
			return true
		}
	}

	// Check if it matches our configured local address
	if pm.localAddr.IP != "" && host == pm.localAddr.IP {
		return true
	}

	return false
}

// ============================================================================
// PEER DATABASE PERSISTENCE
// ============================================================================

// PeerDatabase is the serializable format for peers.dat
type PeerDatabase struct {
	Version   int                      `json:"version"`
	Timestamp int64                    `json:"timestamp"`
	Peers     []*PeerDatabaseEntry     `json:"peers"`
	Banned    map[string]int64         `json:"banned"` // addr -> ban expiry
}

// PeerDatabaseEntry is a single entry in the peer database
type PeerDatabaseEntry struct {
	IP          string  `json:"ip"`
	Port        uint16  `json:"port"`
	Services    uint64  `json:"services"`
	LastSeen    int64   `json:"last_seen"`
	LastAttempt int64   `json:"last_attempt"`
	LastSuccess int64   `json:"last_success"`
	Attempts    int     `json:"attempts"`
	Source      string  `json:"source"`
}

// SavePeerDatabase saves the peer database to disk
func (pm *PeerManager) SavePeerDatabase(path string) error {
	pm.addrBookMutex.RLock()
	pm.bannedMutex.RLock()
	defer pm.addrBookMutex.RUnlock()
	defer pm.bannedMutex.RUnlock()

	db := &PeerDatabase{
		Version:   1,
		Timestamp: time.Now().Unix(),
		Peers:     make([]*PeerDatabaseEntry, 0, len(pm.addrBook)),
		Banned:    make(map[string]int64),
	}

	// Save address book
	for _, entry := range pm.addrBook {
		db.Peers = append(db.Peers, &PeerDatabaseEntry{
			IP:          entry.Addr.IP,
			Port:        entry.Addr.Port,
			Services:    uint64(entry.Addr.Services),
			LastSeen:    entry.Addr.Timestamp,
			LastAttempt: entry.LastAttempt.Unix(),
			LastSuccess: entry.LastSuccess.Unix(),
			Attempts:    entry.Attempts,
			Source:      entry.Source,
		})
	}

	// Save ban list
	for addr, expiry := range pm.banned {
		db.Banned[addr] = expiry.Unix()
	}

	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal peer database: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil { // (shannon #20)
		return fmt.Errorf("failed to write peer database: %w", err)
	}

	fmt.Printf("Saved %d peers to database\n", len(db.Peers))
	return nil
}

// LoadPeerDatabase loads the peer database from disk
func (pm *PeerManager) LoadPeerDatabase(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No database yet, that's fine
		}
		return fmt.Errorf("failed to read peer database: %w", err)
	}

	var db PeerDatabase
	if err := json.Unmarshal(data, &db); err != nil {
		return fmt.Errorf("failed to parse peer database: %w", err)
	}

	pm.addrBookMutex.Lock()
	pm.bannedMutex.Lock()
	defer pm.addrBookMutex.Unlock()
	defer pm.bannedMutex.Unlock()

	// Load peers
	loaded := 0
	for _, entry := range db.Peers {
		addr := &NetAddr{
			IP:        entry.IP,
			Port:      entry.Port,
			Services:  ServiceFlag(entry.Services),
			Timestamp: entry.LastSeen,
		}

		key := addr.Address()
		pm.addrBook[key] = &AddrBookEntry{
			Addr:        addr,
			LastAttempt: time.Unix(entry.LastAttempt, 0),
			LastSuccess: time.Unix(entry.LastSuccess, 0),
			Attempts:    entry.Attempts,
			Source:      entry.Source,
		}
		loaded++
	}

	// Load ban list
	now := time.Now()
	for addr, expiry := range db.Banned {
		expiryTime := time.Unix(expiry, 0)
		if expiryTime.After(now) {
			pm.banned[addr] = expiryTime
		}
	}

	fmt.Printf("Loaded %d peers from database\n", loaded)
	return nil
}

// PruneDeadPeers removes peers that haven't been seen in a long time
func (pm *PeerManager) PruneDeadPeers(maxAge time.Duration) int {
	pm.addrBookMutex.Lock()
	defer pm.addrBookMutex.Unlock()

	cutoff := time.Now().Add(-maxAge)
	pruned := 0

	for key, entry := range pm.addrBook {
		// Keep peers we've successfully connected to recently
		if !entry.LastSuccess.IsZero() && entry.LastSuccess.After(cutoff) {
			continue
		}

		// Keep peers we haven't tried yet or only tried recently
		if entry.Attempts == 0 || entry.LastAttempt.After(cutoff) {
			continue
		}

		// Keep seed nodes
		if entry.Source == "seed" {
			continue
		}

		// Prune this peer
		delete(pm.addrBook, key)
		pruned++
	}

	if pruned > 0 {
		fmt.Printf("Pruned %d dead peers\n", pruned)
	}
	return pruned
}

// startPeerDatabaseSaver periodically saves the peer database
func (pm *PeerManager) startPeerDatabaseSaver(path string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.stopCh:
			// Final save on shutdown
			pm.SavePeerDatabase(path)
			return
		case <-ticker.C:
			pm.SavePeerDatabase(path)
		}
	}
}

// ============================================================================
// EXPONENTIAL BACKOFF & RETRY LOGIC
// ============================================================================

// RetryState tracks retry attempts for a peer
type RetryState struct {
	Attempts    int
	LastAttempt time.Time
	NextRetry   time.Time
	Backoff     time.Duration
}

// CalculateBackoff computes the next backoff duration
func CalculateBackoff(attempts int, baseDelay, maxDelay time.Duration, multiplier float64) time.Duration {
	if attempts <= 0 {
		return baseDelay
	}

	backoff := baseDelay
	for i := 0; i < attempts; i++ {
		backoff = time.Duration(float64(backoff) * multiplier)
		if backoff > maxDelay {
			return maxDelay
		}
	}
	return backoff
}

// ConnectWithRetry attempts to connect with exponential backoff
func (pm *PeerManager) ConnectWithRetry(addr string, maxRetries int) error {
	baseDelay := 1 * time.Second
	maxDelay := 5 * time.Minute
	multiplier := 2.0

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := pm.Connect(addr)
		if err == nil {
			return nil
		}

		if attempt < maxRetries-1 {
			backoff := CalculateBackoff(attempt, baseDelay, maxDelay, multiplier)
			fmt.Printf("Connection to %s failed, retrying in %v (attempt %d/%d)\n",
				addr, backoff, attempt+1, maxRetries)

			select {
			case <-pm.stopCh:
				return fmt.Errorf("shutdown requested")
			case <-time.After(backoff):
			}
		}
	}

	return fmt.Errorf("failed to connect after %d attempts", maxRetries)
}

// ============================================================================
// STARTUP SEQUENCE
// ============================================================================

// Bootstrap performs the initial network bootstrap
// 1. Try to connect to peers from peers.dat
// 2. If no connections after timeout, fall back to seed nodes
// 3. Use getaddr to discover more peers
func (pm *PeerManager) Bootstrap(config *NetworkConfig, peersFile string) error {
	fmt.Println("Starting network bootstrap...")

	// Load saved peers
	if err := pm.LoadPeerDatabase(peersFile); err != nil {
		fmt.Printf("Warning: Could not load peer database: %v\n", err)
	}

	// Start peer database saver
	go pm.startPeerDatabaseSaver(peersFile, config.PeerSaveInterval)

	// Try to connect to known peers first
	connected := pm.tryKnownPeers(config.SeedTimeout)

	// If we couldn't connect to any known peers, try seed nodes
	if connected == 0 {
		fmt.Println("No connections from peers.dat, trying seed nodes...")
		connected = pm.trySeedNodes(config.SeedNodes)
	}

	if connected == 0 {
		fmt.Println("Warning: Could not connect to any peers")
		// Continue anyway - we might get inbound connections
	} else {
		fmt.Printf("Bootstrap complete: connected to %d peers\n", connected)
	}

	return nil
}

// tryKnownPeers attempts to connect to peers from the address book
func (pm *PeerManager) tryKnownPeers(timeout time.Duration) int {
	pm.addrBookMutex.RLock()
	var candidates []string
	for _, entry := range pm.addrBook {
		candidates = append(candidates, entry.Addr.Address())
	}
	pm.addrBookMutex.RUnlock()

	if len(candidates) == 0 {
		return 0
	}

	// Shuffle for randomness
	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	fmt.Printf("Trying %d known peers (timeout: %v)...\n", len(candidates), timeout)

	deadline := time.Now().Add(timeout)
	connected := 0
	maxOutbound := pm.config.MaxOutbound

	for _, addr := range candidates {
		if time.Now().After(deadline) {
			break
		}

		if pm.OutboundCount() >= maxOutbound {
			break
		}

		// Try to connect (don't block too long on each)
		done := make(chan error, 1)
		go func(a string) {
			done <- pm.Connect(a)
		}(addr)

		select {
		case err := <-done:
			if err == nil {
				connected++
				fmt.Printf("Connected to known peer: %s\n", addr)
			}
		case <-time.After(pm.config.ConnectTimeout):
			// Timeout on this peer, try next
		}
	}

	return connected
}

// trySeedNodes attempts to connect to seed nodes until one succeeds
func (pm *PeerManager) trySeedNodes(seeds []string) int {
	if len(seeds) == 0 {
		return 0
	}

	fmt.Printf("Trying %d seed nodes...\n", len(seeds))

	connected := 0
	for _, seed := range seeds {
		// Add to address book
		pm.AddSeedNode(seed)

		// Try to connect
		if err := pm.Connect(seed); err == nil {
			connected++
			fmt.Printf("Connected to seed node: %s\n", seed)

			// One seed connection is enough to bootstrap
			// The getaddr exchange will give us more peers
			if connected >= 1 {
				return connected
			}
		} else {
			fmt.Printf("Failed to connect to seed %s: %v\n", seed, err)
		}
	}

	return connected
}

// ============================================================================
// NETWORK STATISTICS
// ============================================================================

// NetworkStats holds network statistics
type NetworkStats struct {
	PeerCount      int   `json:"peer_count"`
	InboundCount   int   `json:"inbound_count"`
	OutboundCount  int   `json:"outbound_count"`
	KnownAddresses int   `json:"known_addresses"`
	BannedCount    int   `json:"banned_count"`
	BytesSent      int64 `json:"bytes_sent"`
	BytesRecv      int64 `json:"bytes_recv"`
}

// GetNetworkStats returns current network statistics
func (pm *PeerManager) GetNetworkStats() *NetworkStats {
	pm.addrBookMutex.RLock()
	knownAddrs := len(pm.addrBook)
	pm.addrBookMutex.RUnlock()

	pm.bannedMutex.RLock()
	bannedCount := len(pm.banned)
	pm.bannedMutex.RUnlock()

	var bytesSent, bytesRecv int64
	for _, peer := range pm.GetActivePeers() {
		peer.mutex.RLock()
		bytesSent += int64(peer.BytesSent)
		bytesRecv += int64(peer.BytesRecv)
		peer.mutex.RUnlock()
	}

	return &NetworkStats{
		PeerCount:      pm.PeerCount(),
		InboundCount:   pm.InboundCount(),
		OutboundCount:  pm.OutboundCount(),
		KnownAddresses: knownAddrs,
		BannedCount:    bannedCount,
		BytesSent:      bytesSent,
		BytesRecv:      bytesRecv,
	}
}

// GetBannedPeers returns a list of banned peers
func (pm *PeerManager) GetBannedPeers() map[string]time.Time {
	pm.bannedMutex.RLock()
	defer pm.bannedMutex.RUnlock()

	result := make(map[string]time.Time)
	for addr, expiry := range pm.banned {
		result[addr] = expiry
	}
	return result
}
