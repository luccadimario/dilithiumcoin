package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MinerGUIVersion = "4.1.0"

// MinerStatus holds current mining state for the frontend
type MinerStatus struct {
	Mining       bool    `json:"mining"`
	Address      string  `json:"address"`
	NodeURL      string  `json:"node_url"`
	Threads      int     `json:"threads"`
	MaxThreads   int     `json:"max_threads"`
	BlocksMined  int64   `json:"blocks_mined"`
	TotalHashes  int64   `json:"total_hashes"`
	HashRate     float64 `json:"hash_rate"`
	EarningsDLT  string  `json:"earnings_dlt"`
	Uptime       string  `json:"uptime"`
	NodeHeight   int     `json:"node_height"`
	Difficulty   int     `json:"difficulty"`
	DiffBits     int     `json:"diff_bits"`
	NodeOnline   bool    `json:"node_online"`
	Error        string  `json:"error,omitempty"`
}

// MinerLog holds a log entry
type MinerLog struct {
	Time    string `json:"time"`
	Message string `json:"message"`
	Type    string `json:"type"` // "info", "success", "error"
}

// App is the main application struct
type App struct {
	ctx       context.Context
	miner     *miner
	nodeURL   string
	address   string
	threads   int
	mu        sync.Mutex
	logs      []MinerLog
	logMu     sync.Mutex
}

func NewApp() *App {
	home, _ := os.UserHomeDir()
	defaultAddr := ""
	addrPath := filepath.Join(home, ".dilithium", "wallet", "address")
	if data, err := os.ReadFile(addrPath); err == nil {
		defaultAddr = strings.TrimSpace(string(data))
	}

	return &App{
		nodeURL: "http://localhost:8001",
		address: defaultAddr,
		threads: 1,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.miner != nil {
		a.miner.Stop()
	}
}

func (a *App) addLog(msg, typ string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	entry := MinerLog{
		Time:    time.Now().Format("15:04:05"),
		Message: msg,
		Type:    typ,
	}
	a.logs = append(a.logs, entry)
	if len(a.logs) > 200 {
		a.logs = a.logs[len(a.logs)-200:]
	}
}

// --- Config ---

func (a *App) GetVersion() string { return MinerGUIVersion }

func (a *App) GetMaxThreads() int { return runtime.NumCPU() }

func (a *App) GetNodeURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nodeURL
}

func (a *App) SetNodeURL(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nodeURL = strings.TrimRight(url, "/")
}

func (a *App) GetAddress() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.address
}

func (a *App) SetAddress(addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.address = addr
}

func (a *App) GetThreads() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.threads
}

func (a *App) SetThreads(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	max := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	if n > max {
		n = max
	}
	a.threads = n
}

// --- Mining ---

func (a *App) StartMining() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.miner != nil {
		return "Already mining"
	}
	if a.address == "" {
		return "No miner address set"
	}

	// Test node connection
	if err := checkNode(a.nodeURL); err != nil {
		return fmt.Sprintf("Cannot connect to node: %v", err)
	}

	a.miner = newMiner(a.nodeURL, a.address, a.threads, func(msg, typ string) {
		a.addLog(msg, typ)
	})
	a.miner.Start()
	a.addLog(fmt.Sprintf("Mining started (%d threads, node: %s)", a.threads, a.nodeURL), "success")
	return ""
}

func (a *App) StopMining() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.miner != nil {
		a.miner.Stop()
		a.addLog("Mining stopped", "info")
		a.miner = nil
	}
}

func (a *App) GetStatus() MinerStatus {
	a.mu.Lock()
	m := a.miner
	addr := a.address
	nodeURL := a.nodeURL
	threads := a.threads
	a.mu.Unlock()

	status := MinerStatus{
		Address:    addr,
		NodeURL:    nodeURL,
		Threads:    threads,
		MaxThreads: runtime.NumCPU(),
	}

	if m != nil {
		status.Mining = true
		blocks := atomic.LoadInt64(&m.blocksMined)
		hashes := atomic.LoadInt64(&m.totalHashes)
		earnings := atomic.LoadInt64(&m.earnings)
		elapsed := time.Since(m.startTime)

		status.BlocksMined = blocks
		status.TotalHashes = hashes
		status.EarningsDLT = formatDLT(earnings)

		if elapsed.Seconds() > 0 {
			status.HashRate = float64(hashes) / elapsed.Seconds()
		}

		hrs := int(elapsed.Hours())
		mins := int(elapsed.Minutes()) % 60
		secs := int(elapsed.Seconds()) % 60
		status.Uptime = fmt.Sprintf("%02d:%02d:%02d", hrs, mins, secs)
	} else {
		status.EarningsDLT = "0.00000000"
		status.Uptime = "00:00:00"
	}

	// Check node status
	template, err := getWork(nodeURL)
	if err == nil {
		status.NodeOnline = true
		status.NodeHeight = int(template.Height)
		status.Difficulty = template.Difficulty
		status.DiffBits = template.DifficultyBits
	}

	return status
}

func (a *App) GetLogs() []MinerLog {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	result := make([]MinerLog, len(a.logs))
	copy(result, a.logs)
	return result
}

func (a *App) TestConnection() string {
	a.mu.Lock()
	url := a.nodeURL
	a.mu.Unlock()

	if err := checkNode(url); err != nil {
		return "Error: " + err.Error()
	}
	return "Connected"
}
