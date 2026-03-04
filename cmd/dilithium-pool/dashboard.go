package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewDashboard creates a new dashboard server
func NewDashboard(pool *Pool, db *Database, port int) *Dashboard {
	return &Dashboard{
		pool: pool,
		db:   db,
		port: port,
	}
}

// Start begins the dashboard HTTP server (blocking)
func (d *Dashboard) Start() error {
	mux := http.NewServeMux()

	// Web UI
	mux.HandleFunc("/", d.handleHome)

	// JSON API
	mux.HandleFunc("/api/stats", d.handleAPIStats)
	mux.HandleFunc("/api/workers", d.handleAPIWorkers)
	mux.HandleFunc("/api/blocks", d.handleAPIBlocks)
	mux.HandleFunc("/api/payouts", d.handleAPIPayouts)
	mux.HandleFunc("/api/worker/", d.handleAPIWorkerDetail)

	d.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", d.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	fmt.Printf("Dashboard: http://localhost:%d\n", d.port)
	return d.server.ListenAndServe()
}

// Stop shuts down the dashboard
func (d *Dashboard) Stop() {
	if d.server != nil {
		d.server.Close()
	}
}

// ============================================================================
// API Handlers
// ============================================================================

func (d *Dashboard) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	stats, totalWorkers, activeWorkers := d.db.GetStats()
	connectedWorkers := d.pool.getConnectedWorkerCount()
	sharesInWindow := d.db.GetSharesInWindow()

	// Estimate hashrate from recent shares
	d.db.mu.RLock()
	recentShares := 0
	cutoff := time.Now().Unix() - 300
	for _, s := range d.db.data.Shares {
		if s.Timestamp >= cutoff {
			recentShares++
		}
	}
	d.db.mu.RUnlock()

	result := map[string]interface{}{
		"pool_fee":           d.pool.feePercent,
		"connected_workers":  connectedWorkers,
		"active_workers":     activeWorkers,
		"total_workers":      totalWorkers,
		"shares_in_window":   sharesInWindow,
		"recent_shares_5min": recentShares,
		"blocks_found":       stats.TotalBlocksFound,
		"total_shares":       stats.TotalShares,
		"total_paid":         formatDLT(stats.TotalPaidOut),
		"total_paid_raw":     stats.TotalPaidOut,
		"uptime_seconds":     time.Now().Unix() - stats.PoolStartTime,
	}

	writeJSON(w, result)
}

func (d *Dashboard) handleAPIWorkers(w http.ResponseWriter, r *http.Request) {
	workers := d.db.GetWorkers()

	result := make([]map[string]interface{}, 0, len(workers))
	for _, worker := range workers {
		result = append(result, map[string]interface{}{
			"address":         worker.Address,
			"balance":         formatDLT(worker.Balance),
			"balance_raw":     worker.Balance,
			"total_earned":    formatDLT(worker.TotalEarned),
			"total_paid":      formatDLT(worker.TotalPaid),
			"shares_accepted": worker.SharesAccepted,
			"shares_rejected": worker.SharesRejected,
			"last_seen":       worker.LastSeen,
			"joined_at":       worker.JoinedAt,
		})
	}

	writeJSON(w, map[string]interface{}{"workers": result})
}

func (d *Dashboard) handleAPIBlocks(w http.ResponseWriter, r *http.Request) {
	blocks := d.db.GetRecentBlocks(50)

	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		hashDisplay := b.Hash
		if len(hashDisplay) > 16 {
			hashDisplay = hashDisplay[:16] + "..."
		}
		finderDisplay := b.FinderAddress
		if len(finderDisplay) > 12 {
			finderDisplay = finderDisplay[:12] + "..."
		}

		result = append(result, map[string]interface{}{
			"height":    b.Height,
			"hash":      hashDisplay,
			"hash_full": b.Hash,
			"finder":    finderDisplay,
			"reward":    formatDLT(b.Reward),
			"timestamp": b.Timestamp,
			"status":    b.Status,
		})
	}

	writeJSON(w, map[string]interface{}{"blocks": result})
}

func (d *Dashboard) handleAPIPayouts(w http.ResponseWriter, r *http.Request) {
	payouts := d.db.GetRecentPayouts(100)

	result := make([]map[string]interface{}, 0, len(payouts))
	for _, p := range payouts {
		workerDisplay := p.WorkerAddress
		if len(workerDisplay) > 12 {
			workerDisplay = workerDisplay[:12] + "..."
		}
		txDisplay := p.TxSignature
		if len(txDisplay) > 16 {
			txDisplay = txDisplay[:16] + "..."
		}

		result = append(result, map[string]interface{}{
			"worker":    workerDisplay,
			"amount":    formatDLT(p.Amount),
			"fee":       formatDLT(p.Fee),
			"timestamp": p.Timestamp,
			"status":    p.Status,
			"txid":      txDisplay,
		})
	}

	writeJSON(w, map[string]interface{}{"payouts": result})
}

func (d *Dashboard) handleAPIWorkerDetail(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimPrefix(r.URL.Path, "/api/worker/")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	worker, exists := d.db.GetWorkerByAddress(address)
	if !exists {
		http.Error(w, "worker not found", http.StatusNotFound)
		return
	}

	sharesInWindow := d.db.GetWorkerSharesInWindow(address)
	payouts := d.db.GetWorkerPayouts(address)

	payoutList := make([]map[string]interface{}, 0, len(payouts))
	for _, p := range payouts {
		payoutList = append(payoutList, map[string]interface{}{
			"amount":    formatDLT(p.Amount),
			"timestamp": p.Timestamp,
			"status":    p.Status,
		})
	}

	result := map[string]interface{}{
		"address":          worker.Address,
		"balance":          formatDLT(worker.Balance),
		"balance_raw":      worker.Balance,
		"total_earned":     formatDLT(worker.TotalEarned),
		"total_paid":       formatDLT(worker.TotalPaid),
		"shares_accepted":  worker.SharesAccepted,
		"shares_rejected":  worker.SharesRejected,
		"shares_in_window": sharesInWindow,
		"last_seen":        worker.LastSeen,
		"joined_at":        worker.JoinedAt,
		"payouts":          payoutList,
	}

	writeJSON(w, result)
}

// ============================================================================
// HTML Dashboard
// ============================================================================

func (d *Dashboard) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Dilithium Mining Pool</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: 'Courier New', monospace; background: #0a0e17; color: #c0c8d8; min-height: 100vh; }
  .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
  h1 { color: #00bfff; font-size: 1.8em; margin-bottom: 5px; }
  .subtitle { color: #607080; margin-bottom: 30px; }
  h2 { color: #00bfff; font-size: 1.2em; margin: 30px 0 15px 0; border-bottom: 1px solid #1a2030; padding-bottom: 8px; }
  .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin-bottom: 20px; }
  .stat-card { background: #111827; border: 1px solid #1e293b; border-radius: 8px; padding: 15px; }
  .stat-label { color: #607080; font-size: 0.8em; text-transform: uppercase; letter-spacing: 1px; }
  .stat-value { color: #e0e8f0; font-size: 1.5em; font-weight: bold; margin-top: 5px; }
  .stat-value.highlight { color: #00bfff; }
  table { width: 100%; border-collapse: collapse; background: #111827; border-radius: 8px; overflow: hidden; }
  th { background: #1e293b; color: #00bfff; text-align: left; padding: 10px 12px; font-size: 0.85em; text-transform: uppercase; letter-spacing: 1px; }
  td { padding: 10px 12px; border-bottom: 1px solid #1a2030; font-size: 0.9em; }
  tr:hover { background: #151f30; }
  .status-confirmed { color: #22c55e; }
  .status-sent { color: #22c55e; }
  .status-failed { color: #ef4444; }
  .status-orphaned { color: #f59e0b; }
  .refresh-note { color: #404858; font-size: 0.75em; margin-top: 20px; text-align: center; }
  .empty { color: #404858; padding: 20px; text-align: center; }
</style>
</head>
<body>
<div class="container">
  <h1>Dilithium Mining Pool</h1>
  <p class="subtitle">PPLNS | Stratum V1</p>

  <div class="stats-grid" id="stats">
    <div class="stat-card"><div class="stat-label">Loading...</div><div class="stat-value">--</div></div>
  </div>

  <h2>Workers</h2>
  <div id="workers"><div class="empty">Loading...</div></div>

  <h2>Recent Blocks</h2>
  <div id="blocks"><div class="empty">Loading...</div></div>

  <h2>Recent Payouts</h2>
  <div id="payouts"><div class="empty">Loading...</div></div>

  <p class="refresh-note">Auto-refreshes every 10 seconds</p>
</div>
<script>
function timeAgo(ts) {
  const s = Math.floor(Date.now()/1000 - ts);
  if (s < 60) return s + 's ago';
  if (s < 3600) return Math.floor(s/60) + 'm ago';
  if (s < 86400) return Math.floor(s/3600) + 'h ago';
  return Math.floor(s/86400) + 'd ago';
}

function formatUptime(s) {
  const d = Math.floor(s/86400);
  const h = Math.floor((s%86400)/3600);
  const m = Math.floor((s%3600)/60);
  if (d > 0) return d + 'd ' + h + 'h';
  if (h > 0) return h + 'h ' + m + 'm';
  return m + 'm';
}

async function update() {
  try {
    const [statsR, workersR, blocksR, payoutsR] = await Promise.all([
      fetch('/api/stats').then(r=>r.json()),
      fetch('/api/workers').then(r=>r.json()),
      fetch('/api/blocks').then(r=>r.json()),
      fetch('/api/payouts').then(r=>r.json())
    ]);

    // Stats
    document.getElementById('stats').innerHTML =
      '<div class="stat-card"><div class="stat-label">Pool Fee</div><div class="stat-value">' + statsR.pool_fee + '%</div></div>' +
      '<div class="stat-card"><div class="stat-label">Connected Workers</div><div class="stat-value highlight">' + statsR.connected_workers + '</div></div>' +
      '<div class="stat-card"><div class="stat-label">Blocks Found</div><div class="stat-value highlight">' + statsR.blocks_found + '</div></div>' +
      '<div class="stat-card"><div class="stat-label">Total Shares</div><div class="stat-value">' + statsR.total_shares + '</div></div>' +
      '<div class="stat-card"><div class="stat-label">PPLNS Window</div><div class="stat-value">' + statsR.shares_in_window + '</div></div>' +
      '<div class="stat-card"><div class="stat-label">Total Paid</div><div class="stat-value">' + statsR.total_paid + '</div></div>' +
      '<div class="stat-card"><div class="stat-label">Uptime</div><div class="stat-value">' + formatUptime(statsR.uptime_seconds) + '</div></div>';

    // Workers
    if (workersR.workers && workersR.workers.length > 0) {
      let html = '<table><tr><th>Address</th><th>Balance</th><th>Total Earned</th><th>Shares</th><th>Last Seen</th></tr>';
      for (const w of workersR.workers) {
        const addr = w.address.length > 12 ? w.address.substr(0,12)+'...' : w.address;
        html += '<tr><td>' + addr + '</td><td>' + w.balance + '</td><td>' + w.total_earned + '</td><td>' + w.shares_accepted + '</td><td>' + timeAgo(w.last_seen) + '</td></tr>';
      }
      html += '</table>';
      document.getElementById('workers').innerHTML = html;
    } else {
      document.getElementById('workers').innerHTML = '<div class="empty">No workers yet</div>';
    }

    // Blocks
    if (blocksR.blocks && blocksR.blocks.length > 0) {
      let html = '<table><tr><th>Height</th><th>Hash</th><th>Finder</th><th>Reward</th><th>Time</th><th>Status</th></tr>';
      for (const b of [...blocksR.blocks].reverse()) {
        html += '<tr><td>' + b.height + '</td><td>' + b.hash + '</td><td>' + b.finder + '</td><td>' + b.reward + '</td><td>' + timeAgo(b.timestamp) + '</td><td class="status-' + b.status + '">' + b.status + '</td></tr>';
      }
      html += '</table>';
      document.getElementById('blocks').innerHTML = html;
    } else {
      document.getElementById('blocks').innerHTML = '<div class="empty">No blocks found yet</div>';
    }

    // Payouts
    if (payoutsR.payouts && payoutsR.payouts.length > 0) {
      let html = '<table><tr><th>Worker</th><th>Amount</th><th>Fee</th><th>Time</th><th>Status</th></tr>';
      for (const p of [...payoutsR.payouts].reverse()) {
        html += '<tr><td>' + p.worker + '</td><td>' + p.amount + '</td><td>' + p.fee + '</td><td>' + timeAgo(p.timestamp) + '</td><td class="status-' + p.status + '">' + p.status + '</td></tr>';
      }
      html += '</table>';
      document.getElementById('payouts').innerHTML = html;
    } else {
      document.getElementById('payouts').innerHTML = '<div class="empty">No payouts yet</div>';
    }
  } catch(e) {
    console.error('Update failed:', e);
  }
}

update();
setInterval(update, 10000);
</script>
</body>
</html>`

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(data)
}
