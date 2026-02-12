<script>
  import { onMount, onDestroy } from 'svelte'
  import { balance, walletAddress, nodeConnected } from '../lib/stores.js'
  import { formatDLT, truncateAddress } from '../lib/formatters.js'
  import TransactionRow from '../components/TransactionRow.svelte'

  let recentTxs = []
  let loadingBalance = true
  let interval

  onMount(async () => {
    await refresh()
    interval = setInterval(refresh, 15000)
  })

  onDestroy(() => {
    if (interval) clearInterval(interval)
  })

  async function refresh() {
    loadingBalance = true
    try {
      const [bal, txs] = await Promise.all([
        window.go.main.App.GetBalance(),
        window.go.main.App.GetTransactionHistory(),
      ])
      if (!bal.error) {
        $balance = bal
      }
      recentTxs = (txs || []).slice(0, 5)
    } catch (e) {
      console.error('refresh failed', e)
    }
    loadingBalance = false
  }
</script>

<div class="dashboard">
  <div class="header">
    <h2>Dashboard</h2>
    <button class="refresh-btn" on:click={refresh} disabled={loadingBalance}>
      {loadingBalance ? 'Refreshing...' : 'Refresh'}
    </button>
  </div>

  <div class="balance-card">
    <div class="balance-label">Total Balance</div>
    <div class="balance-amount">{formatDLT($balance.balance_dlt)} <span class="currency">DLT</span></div>
    <div class="balance-address">{$walletAddress}</div>

    <div class="balance-stats">
      <div class="stat">
        <span class="stat-label">Received</span>
        <span class="stat-value received">{formatDLT($balance.total_received_dlt)} DLT</span>
      </div>
      <div class="stat">
        <span class="stat-label">Sent</span>
        <span class="stat-value sent">{formatDLT($balance.total_sent_dlt)} DLT</span>
      </div>
      <div class="stat">
        <span class="stat-label">Transactions</span>
        <span class="stat-value">{$balance.tx_count}</span>
      </div>
    </div>
  </div>

  <div class="section">
    <h3>Recent Transactions</h3>
    {#if recentTxs.length === 0}
      <div class="empty">No transactions yet</div>
    {:else}
      <div class="tx-list">
        {#each recentTxs as tx}
          <TransactionRow {tx} />
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .dashboard {
    max-width: 720px;
  }

  .header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 24px;
  }

  h2 {
    font-size: 22px;
    font-weight: 700;
    color: #e1e4ea;
  }

  .refresh-btn {
    padding: 8px 16px;
    border-radius: 6px;
    border: 1px solid #2d2f44;
    background: #1e2030;
    color: #9ca3af;
    font-size: 13px;
    cursor: pointer;
    transition: all 0.15s;
  }

  .refresh-btn:hover:not(:disabled) {
    background: #252840;
    color: #e1e4ea;
  }

  .refresh-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .balance-card {
    background: linear-gradient(135deg, #1e1a3a, #1a1c2e);
    border: 1px solid #2d2a4a;
    border-radius: 16px;
    padding: 32px;
    margin-bottom: 32px;
  }

  .balance-label {
    font-size: 13px;
    color: #6b7280;
    text-transform: uppercase;
    letter-spacing: 1px;
    margin-bottom: 8px;
  }

  .balance-amount {
    font-size: 36px;
    font-weight: 700;
    color: #e1e4ea;
    font-family: 'SF Mono', 'Fira Code', monospace;
    margin-bottom: 8px;
  }

  .currency {
    font-size: 18px;
    color: #8b5cf6;
    font-weight: 600;
  }

  .balance-address {
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
    color: #4b5563;
    margin-bottom: 24px;
    word-break: break-all;
  }

  .balance-stats {
    display: flex;
    gap: 24px;
    border-top: 1px solid #2d2a4a;
    padding-top: 16px;
  }

  .stat {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .stat-label {
    font-size: 11px;
    color: #6b7280;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .stat-value {
    font-size: 14px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    color: #9ca3af;
  }

  .stat-value.received { color: #22c55e; }
  .stat-value.sent { color: #ef4444; }

  .section {
    margin-bottom: 24px;
  }

  h3 {
    font-size: 16px;
    font-weight: 600;
    color: #e1e4ea;
    margin-bottom: 14px;
  }

  .tx-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .empty {
    padding: 32px;
    text-align: center;
    color: #4b5563;
    background: #1a1c2e;
    border-radius: 8px;
    border: 1px solid #232536;
    font-size: 14px;
  }
</style>
