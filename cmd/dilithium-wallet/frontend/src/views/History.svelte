<script>
  import { onMount } from 'svelte'
  import TransactionRow from '../components/TransactionRow.svelte'

  let transactions = []
  let filter = 'all' // 'all', 'sent', 'received'
  let loading = true

  onMount(async () => {
    await refresh()
  })

  async function refresh() {
    loading = true
    try {
      const txs = await window.go.main.App.GetTransactionHistory()
      transactions = txs || []
    } catch (e) {
      console.error('Failed to load history', e)
    }
    loading = false
  }

  $: filtered = filter === 'all'
    ? transactions
    : transactions.filter(tx => tx.direction === filter)
</script>

<div class="history-view">
  <div class="header">
    <h2>Transaction History</h2>
    <button class="refresh-btn" on:click={refresh} disabled={loading}>
      {loading ? 'Loading...' : 'Refresh'}
    </button>
  </div>

  <div class="filter-bar">
    <button class="filter-btn" class:active={filter === 'all'} on:click={() => filter = 'all'}>
      All ({transactions.length})
    </button>
    <button class="filter-btn" class:active={filter === 'sent'} on:click={() => filter = 'sent'}>
      Sent ({transactions.filter(t => t.direction === 'sent').length})
    </button>
    <button class="filter-btn" class:active={filter === 'received'} on:click={() => filter = 'received'}>
      Received ({transactions.filter(t => t.direction === 'received').length})
    </button>
  </div>

  {#if loading}
    <div class="empty">Loading transactions...</div>
  {:else if filtered.length === 0}
    <div class="empty">No transactions found</div>
  {:else}
    <div class="tx-list">
      {#each filtered as tx}
        <TransactionRow {tx} />
      {/each}
    </div>
  {/if}
</div>

<style>
  .history-view {
    max-width: 720px;
  }

  .header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 20px;
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

  .filter-bar {
    display: flex;
    gap: 8px;
    margin-bottom: 16px;
  }

  .filter-btn {
    padding: 8px 16px;
    border-radius: 6px;
    border: 1px solid #2d2f44;
    background: #1a1c2e;
    color: #6b7280;
    font-size: 13px;
    cursor: pointer;
    transition: all 0.15s;
  }

  .filter-btn:hover {
    color: #9ca3af;
  }

  .filter-btn.active {
    background: #252840;
    color: #8b5cf6;
    border-color: #3d3f5c;
  }

  .tx-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .empty {
    padding: 48px;
    text-align: center;
    color: #4b5563;
    background: #1a1c2e;
    border-radius: 8px;
    border: 1px solid #232536;
    font-size: 14px;
  }
</style>
