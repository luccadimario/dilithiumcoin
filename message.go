package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// PROTOCOL CONSTANTS
// ============================================================================

const (
	// ProtocolVersion is the current P2P protocol version
	ProtocolVersion uint32 = 1

	// MagicBytes identifies Dilithium network messages
	MagicBytes uint32 = 0x44494C54 // "DILT" in hex

	// MaxAddrPerMessage is the maximum addresses per Addr message
	MaxAddrPerMessage = 35

	// MaxInvPerMessage is the maximum inventory items per Inv message
	MaxInvPerMessage = 50000

	// UserAgent identifies this node implementation
	UserAgent = "/Dilithium:" + Version + "/"

	// PingInterval is how often to send ping messages
	PingInterval = 2 * time.Minute

	// PingTimeout is how long to wait for a pong response
	PingTimeout = 30 * time.Second
)

// ============================================================================
// MESSAGE TYPES
// ============================================================================

// MsgType represents the type of P2P message
type MsgType string

const (
	MsgTypeVersion    MsgType = "version"
	MsgTypeVersionAck MsgType = "verack"
	MsgTypeAddr       MsgType = "addr"
	MsgTypeGetAddr    MsgType = "getaddr"
	MsgTypeInv        MsgType = "inv"
	MsgTypeGetData    MsgType = "getdata"
	MsgTypeTx         MsgType = "tx"
	MsgTypeBlock      MsgType = "block"
	MsgTypeMempool    MsgType = "mempool"
	MsgTypePing       MsgType = "ping"
	MsgTypePong       MsgType = "pong"
	MsgTypeReject     MsgType = "reject"

	// Legacy message types for backwards compatibility
	MsgTypeChain       MsgType = "chain"
	MsgTypeTransaction MsgType = "transaction"
)

// ============================================================================
// SERVICE FLAGS
// ============================================================================

// ServiceFlag represents services a node supports
type ServiceFlag uint64

const (
	// SFNodeNetwork indicates a full node that can serve blocks
	SFNodeNetwork ServiceFlag = 1 << 0

	// SFNodeMiner indicates a node that participates in mining
	SFNodeMiner ServiceFlag = 1 << 1

	// SFNodeAPI indicates a node running the HTTP API
	SFNodeAPI ServiceFlag = 1 << 2
)

// ============================================================================
// INVENTORY TYPES
// ============================================================================

// InvType represents the type of inventory item
type InvType uint32

const (
	InvTypeError InvType = 0
	InvTypeTx    InvType = 1
	InvTypeBlock InvType = 2
)

func (t InvType) String() string {
	switch t {
	case InvTypeTx:
		return "transaction"
	case InvTypeBlock:
		return "block"
	default:
		return "unknown"
	}
}

// ============================================================================
// P2P MESSAGE ENVELOPE
// ============================================================================

// P2PMessage is the envelope for all P2P messages
type P2PMessage struct {
	Magic     uint32          `json:"magic"`     // Network identifier
	Type      MsgType         `json:"type"`      // Message type
	Length    uint32          `json:"length"`    // Payload length
	Checksum  string          `json:"checksum"`  // First 8 chars of SHA256(payload)
	Payload   json.RawMessage `json:"payload"`   // Message-specific data
	Timestamp int64           `json:"timestamp"` // Unix timestamp
}

// NewP2PMessage creates a new P2P message with the given type and payload
func NewP2PMessage(msgType MsgType, payload interface{}) (*P2PMessage, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	hash := sha256.Sum256(payloadBytes)
	checksum := hex.EncodeToString(hash[:4])

	return &P2PMessage{
		Magic:     MagicBytes,
		Type:      msgType,
		Length:    uint32(len(payloadBytes)),
		Checksum:  checksum,
		Payload:   payloadBytes,
		Timestamp: time.Now().Unix(),
	}, nil
}

// VerifyChecksum validates the message checksum
func (m *P2PMessage) VerifyChecksum() bool {
	hash := sha256.Sum256(m.Payload)
	expected := hex.EncodeToString(hash[:4])
	return m.Checksum == expected
}

// Decode unmarshals the payload into the provided struct
func (m *P2PMessage) Decode(v interface{}) error {
	return json.Unmarshal(m.Payload, v)
}

// ============================================================================
// NET ADDRESS
// ============================================================================

// NetAddr represents a network address with metadata
type NetAddr struct {
	Timestamp int64       `json:"timestamp"` // Last seen time
	Services  ServiceFlag `json:"services"`  // Services this node offers
	IP        string      `json:"ip"`        // IP address
	Port      uint16      `json:"port"`      // Port number
}

// NewNetAddr creates a new network address
func NewNetAddr(ip string, port uint16, services ServiceFlag) *NetAddr {
	return &NetAddr{
		Timestamp: time.Now().Unix(),
		Services:  services,
		IP:        ip,
		Port:      port,
	}
}

// Address returns the address as "ip:port" string
// Handles IPv6 addresses correctly with brackets
func (a *NetAddr) Address() string {
	// Check if IPv6 (contains colon)
	if strings.Contains(a.IP, ":") {
		return fmt.Sprintf("[%s]:%d", a.IP, a.Port)
	}
	return fmt.Sprintf("%s:%d", a.IP, a.Port)
}

// ============================================================================
// INVENTORY VECTOR
// ============================================================================

// InvVector represents an inventory item (transaction or block hash)
type InvVector struct {
	Type InvType `json:"type"` // Type of inventory
	Hash string  `json:"hash"` // Hash of the item
}

// NewInvVector creates a new inventory vector
func NewInvVector(invType InvType, hash string) *InvVector {
	return &InvVector{
		Type: invType,
		Hash: hash,
	}
}

// ============================================================================
// VERSION MESSAGE
// ============================================================================

// VersionMsg is sent when connecting to establish protocol parameters
type VersionMsg struct {
	Version     uint32      `json:"version"`      // Protocol version
	Services    ServiceFlag `json:"services"`     // Services bitmap
	Timestamp   int64       `json:"timestamp"`    // Current time
	AddrRecv    *NetAddr    `json:"addr_recv"`    // Address of receiving node
	AddrFrom    *NetAddr    `json:"addr_from"`    // Address of sending node
	Nonce       uint64      `json:"nonce"`        // Random nonce for connection identification
	UserAgent   string      `json:"user_agent"`   // Node software identifier
	StartHeight int64       `json:"start_height"` // Last block received by sending node
	Relay       bool        `json:"relay"`        // Whether to relay transactions
}

// NewVersionMsg creates a new version message
func NewVersionMsg(services ServiceFlag, addrRecv, addrFrom *NetAddr, startHeight int64, nonce uint64) *VersionMsg {
	return &VersionMsg{
		Version:     ProtocolVersion,
		Services:    services,
		Timestamp:   time.Now().Unix(),
		AddrRecv:    addrRecv,
		AddrFrom:    addrFrom,
		Nonce:       nonce,
		UserAgent:   UserAgent,
		StartHeight: startHeight,
		Relay:       true,
	}
}

// ============================================================================
// VERSION ACK MESSAGE
// ============================================================================

// VersionAckMsg acknowledges the version message, completing the handshake
type VersionAckMsg struct {
	// Empty payload - the message type itself is the acknowledgment
}

// NewVersionAckMsg creates a new version acknowledgment message
func NewVersionAckMsg() *VersionAckMsg {
	return &VersionAckMsg{}
}

// ============================================================================
// ADDR MESSAGE
// ============================================================================

// AddrMsg advertises known peer addresses
type AddrMsg struct {
	Addresses []*NetAddr `json:"addresses"` // List of known addresses (max 35)
}

// NewAddrMsg creates a new address message
func NewAddrMsg(addresses []*NetAddr) *AddrMsg {
	// Limit to max addresses per message
	if len(addresses) > MaxAddrPerMessage {
		addresses = addresses[:MaxAddrPerMessage]
	}
	return &AddrMsg{
		Addresses: addresses,
	}
}

// ============================================================================
// GETADDR MESSAGE
// ============================================================================

// GetAddrMsg requests known peer addresses
type GetAddrMsg struct {
	// Empty payload - requesting peer addresses
}

// NewGetAddrMsg creates a new get address message
func NewGetAddrMsg() *GetAddrMsg {
	return &GetAddrMsg{}
}

// ============================================================================
// INV MESSAGE
// ============================================================================

// InvMsg announces inventory (transactions or blocks) available
type InvMsg struct {
	Inventory []*InvVector `json:"inventory"` // List of available items
}

// NewInvMsg creates a new inventory message
func NewInvMsg(inventory []*InvVector) *InvMsg {
	// Limit to max inventory items per message
	if len(inventory) > MaxInvPerMessage {
		inventory = inventory[:MaxInvPerMessage]
	}
	return &InvMsg{
		Inventory: inventory,
	}
}

// NewTxInvMsg creates an inventory message for a single transaction
func NewTxInvMsg(txHash string) *InvMsg {
	return &InvMsg{
		Inventory: []*InvVector{
			{Type: InvTypeTx, Hash: txHash},
		},
	}
}

// NewBlockInvMsg creates an inventory message for a single block
func NewBlockInvMsg(blockHash string) *InvMsg {
	return &InvMsg{
		Inventory: []*InvVector{
			{Type: InvTypeBlock, Hash: blockHash},
		},
	}
}

// ============================================================================
// GETDATA MESSAGE
// ============================================================================

// GetDataMsg requests specific transactions or blocks by hash
type GetDataMsg struct {
	Inventory []*InvVector `json:"inventory"` // Items to request
}

// NewGetDataMsg creates a new get data message
func NewGetDataMsg(inventory []*InvVector) *GetDataMsg {
	return &GetDataMsg{
		Inventory: inventory,
	}
}

// ============================================================================
// TX MESSAGE
// ============================================================================

// TxMsg sends a full transaction
type TxMsg struct {
	Transaction *Transaction `json:"transaction"`
}

// NewTxMsg creates a new transaction message
func NewTxMsg(tx *Transaction) *TxMsg {
	return &TxMsg{
		Transaction: tx,
	}
}

// ============================================================================
// BLOCK MESSAGE
// ============================================================================

// BlockMsg sends a full block
type BlockMsg struct {
	Block *Block `json:"block"`
}

// NewBlockMsg creates a new block message
func NewBlockMsg(block *Block) *BlockMsg {
	return &BlockMsg{
		Block: block,
	}
}

// ============================================================================
// MEMPOOL MESSAGE
// ============================================================================

// MempoolMsg requests the contents of the peer's mempool
type MempoolMsg struct {
	// Empty payload - requesting mempool contents
}

// NewMempoolMsg creates a new mempool request message
func NewMempoolMsg() *MempoolMsg {
	return &MempoolMsg{}
}

// ============================================================================
// PING MESSAGE
// ============================================================================

// PingMsg is a keep-alive message
type PingMsg struct {
	Nonce uint64 `json:"nonce"` // Random value to match with pong
}

// NewPingMsg creates a new ping message
func NewPingMsg(nonce uint64) *PingMsg {
	return &PingMsg{
		Nonce: nonce,
	}
}

// ============================================================================
// PONG MESSAGE
// ============================================================================

// PongMsg responds to a ping message
type PongMsg struct {
	Nonce uint64 `json:"nonce"` // Must match the ping's nonce
}

// NewPongMsg creates a new pong message
func NewPongMsg(nonce uint64) *PongMsg {
	return &PongMsg{
		Nonce: nonce,
	}
}

// ============================================================================
// REJECT MESSAGE
// ============================================================================

// RejectCode represents why a message was rejected
type RejectCode uint8

const (
	RejectMalformed       RejectCode = 0x01
	RejectInvalid         RejectCode = 0x10
	RejectObsolete        RejectCode = 0x11
	RejectDuplicate       RejectCode = 0x12
	RejectNonstandard     RejectCode = 0x40
	RejectInsufficientFee RejectCode = 0x42
	RejectCheckpoint      RejectCode = 0x43
)

func (c RejectCode) String() string {
	switch c {
	case RejectMalformed:
		return "malformed"
	case RejectInvalid:
		return "invalid"
	case RejectObsolete:
		return "obsolete"
	case RejectDuplicate:
		return "duplicate"
	case RejectNonstandard:
		return "nonstandard"
	case RejectInsufficientFee:
		return "insufficient fee"
	case RejectCheckpoint:
		return "checkpoint"
	default:
		return "unknown"
	}
}

// RejectMsg indicates a message was rejected
type RejectMsg struct {
	Message MsgType    `json:"message"` // Type of message rejected
	Code    RejectCode `json:"code"`    // Rejection reason code
	Reason  string     `json:"reason"`  // Human-readable reason
	Data    string     `json:"data"`    // Optional: hash of rejected item
}

// NewRejectMsg creates a new reject message
func NewRejectMsg(message MsgType, code RejectCode, reason string, data string) *RejectMsg {
	return &RejectMsg{
		Message: message,
		Code:    code,
		Reason:  reason,
		Data:    data,
	}
}

// ============================================================================
// MESSAGE SERIALIZATION HELPERS
// ============================================================================

// EncodeMessage serializes a P2PMessage to JSON bytes (line-delimited format)
func EncodeMessage(msg *P2PMessage) ([]byte, error) {
	return json.Marshal(msg)
}

// DecodeMessage deserializes JSON bytes to a P2PMessage
func DecodeMessage(data []byte) (*P2PMessage, error) {
	var msg P2PMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to decode message: %w", err)
	}

	// Verify magic bytes
	if msg.Magic != MagicBytes {
		return nil, fmt.Errorf("invalid magic bytes: expected %x, got %x", MagicBytes, msg.Magic)
	}

	// Verify checksum
	if !msg.VerifyChecksum() {
		return nil, fmt.Errorf("checksum verification failed")
	}

	return &msg, nil
}

// CreateMessage is a convenience function to create and encode a message
func CreateMessage(msgType MsgType, payload interface{}) ([]byte, error) {
	msg, err := NewP2PMessage(msgType, payload)
	if err != nil {
		return nil, err
	}
	return EncodeMessage(msg)
}

// ============================================================================
// MESSAGE TYPE HELPERS
// ============================================================================

// IsHandshakeMsg returns true if this is a handshake-related message
func (t MsgType) IsHandshakeMsg() bool {
	return t == MsgTypeVersion || t == MsgTypeVersionAck
}

// IsDataMsg returns true if this message contains actual data (tx/block)
func (t MsgType) IsDataMsg() bool {
	return t == MsgTypeTx || t == MsgTypeBlock
}

// IsRequestMsg returns true if this message requests data from peers
func (t MsgType) IsRequestMsg() bool {
	return t == MsgTypeGetAddr || t == MsgTypeGetData || t == MsgTypeMempool
}

// RequiresHandshake returns true if this message requires completed handshake
func (t MsgType) RequiresHandshake() bool {
	return !t.IsHandshakeMsg()
}

// ============================================================================
// LEGACY MESSAGE CONVERSION
// ============================================================================

// LegacyMessage represents the old message format for backwards compatibility
type LegacyMessage struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// ToP2PMessage converts a legacy message to the new format
func (m *LegacyMessage) ToP2PMessage() (*P2PMessage, error) {
	var msgType MsgType
	switch m.Type {
	case "block":
		msgType = MsgTypeBlock
	case "transaction":
		msgType = MsgTypeTx
	case "chain":
		msgType = MsgTypeChain
	default:
		msgType = MsgType(m.Type)
	}

	return NewP2PMessage(msgType, m.Data)
}

// FromP2PMessage converts a P2P message to legacy format
func FromP2PMessage(msg *P2PMessage) *LegacyMessage {
	var data interface{}
	json.Unmarshal(msg.Payload, &data)

	legacyType := string(msg.Type)
	// Convert new types to legacy equivalents
	if msg.Type == MsgTypeTx {
		legacyType = "transaction"
	}

	return &LegacyMessage{
		Type:      legacyType,
		Data:      data,
		Timestamp: msg.Timestamp,
	}
}
