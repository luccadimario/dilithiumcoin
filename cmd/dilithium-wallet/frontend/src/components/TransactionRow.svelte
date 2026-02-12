<script>
  import { truncateAddress, formatDLT, timeAgo } from '../lib/formatters.js'

  export let tx
</script>

<div class="tx-row">
  <div class="tx-direction" class:sent={tx.direction === 'sent'} class:received={tx.direction === 'received'}>
    {tx.direction === 'sent' ? '↑' : '↓'}
  </div>
  <div class="tx-details">
    <div class="tx-address">
      {tx.direction === 'sent' ? 'To: ' : 'From: '}
      <span class="mono">{truncateAddress(tx.direction === 'sent' ? tx.to : tx.from, 8)}</span>
    </div>
    <div class="tx-time">{timeAgo(tx.timestamp)}</div>
  </div>
  <div class="tx-amount" class:sent={tx.direction === 'sent'} class:received={tx.direction === 'received'}>
    {tx.direction === 'sent' ? '-' : '+'}{formatDLT(tx.amount_dlt)} DLT
  </div>
</div>

<style>
  .tx-row {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
    background: #1a1c2e;
    border-radius: 8px;
    border: 1px solid #232536;
  }

  .tx-direction {
    width: 36px;
    height: 36px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 16px;
    flex-shrink: 0;
  }

  .tx-direction.sent {
    background: #2a1a1a;
    color: #ef4444;
  }

  .tx-direction.received {
    background: #1a2a1a;
    color: #22c55e;
  }

  .tx-details {
    flex: 1;
    min-width: 0;
  }

  .tx-address {
    font-size: 14px;
    color: #e1e4ea;
  }

  .mono {
    font-family: 'SF Mono', 'Fira Code', monospace;
    color: #9ca3af;
  }

  .tx-time {
    font-size: 12px;
    color: #6b7280;
    margin-top: 2px;
  }

  .tx-amount {
    font-size: 15px;
    font-weight: 600;
    font-family: 'SF Mono', 'Fira Code', monospace;
    white-space: nowrap;
  }

  .tx-amount.sent {
    color: #ef4444;
  }

  .tx-amount.received {
    color: #22c55e;
  }
</style>
