<script>
  import { showToast, balance } from '../lib/stores.js'
  import { formatDLT } from '../lib/formatters.js'

  let recipient = ''
  let amount = ''
  let fee = '0.0001'
  let step = 'form' // 'form', 'confirm', 'result'
  let sending = false
  let result = null

  const MIN_FEE = 0.0001
  $: feeValue = parseFloat(fee) || 0
  $: feeInvalid = fee !== '' && feeValue < MIN_FEE

  function review() {
    if (!recipient || recipient.length < 10) {
      showToast('Please enter a valid address', 'error')
      return
    }
    if (!amount || parseFloat(amount) <= 0) {
      showToast('Please enter a valid amount', 'error')
      return
    }
    if (feeInvalid || fee === '') {
      showToast('Minimum transaction fee is 0.0001 DLT', 'error')
      return
    }
    step = 'confirm'
  }

  async function send() {
    sending = true
    try {
      result = await window.go.main.App.SendTransaction(recipient, amount, fee)
      step = 'result'
      if (result.success) {
        showToast('Transaction sent!', 'success')
        // Refresh balance
        const bal = await window.go.main.App.GetBalance()
        if (!bal.error) $balance = bal
      }
    } catch (e) {
      showToast('Failed to send: ' + e, 'error')
    }
    sending = false
  }

  function reset() {
    recipient = ''
    amount = ''
    fee = '0.0001'
    step = 'form'
    result = null
  }
</script>

<div class="send-view">
  <h2>Send DLT</h2>

  {#if step === 'form'}
    <div class="card">
      <div class="form-group">
        <label for="recipient">Recipient Address</label>
        <input
          id="recipient"
          type="text"
          bind:value={recipient}
          placeholder="Enter recipient address"
        />
      </div>

      <div class="form-group">
        <label for="amount">
          Amount (DLT)
          <span class="available">Available: {formatDLT($balance.balance_dlt)} DLT</span>
        </label>
        <input
          id="amount"
          type="text"
          bind:value={amount}
          placeholder="0.00"
        />
      </div>

      <div class="form-group">
        <label for="fee">
          Transaction Fee (DLT)
          <span class="fee-min">Min: 0.0001 DLT</span>
        </label>
        <input
          id="fee"
          type="text"
          bind:value={fee}
          placeholder="0.0001"
          class:invalid={feeInvalid}
        />
        {#if feeInvalid}
          <div class="field-error">Minimum fee is 0.0001 DLT</div>
        {/if}
      </div>

      <button class="btn btn-primary" on:click={review} disabled={feeInvalid}>
        Review Transaction
      </button>
    </div>

  {:else if step === 'confirm'}
    <div class="card">
      <div class="confirm-header">Confirm Transaction</div>

      <div class="confirm-detail">
        <span class="detail-label">To</span>
        <span class="detail-value mono">{recipient}</span>
      </div>

      <div class="confirm-detail">
        <span class="detail-label">Amount</span>
        <span class="detail-value">{amount} DLT</span>
      </div>

      <div class="confirm-detail">
        <span class="detail-label">Fee</span>
        <span class="detail-value">{fee} DLT</span>
      </div>

      <div class="confirm-detail total">
        <span class="detail-label">Total</span>
        <span class="detail-value">{(parseFloat(amount || '0') + parseFloat(fee || '0')).toFixed(8)} DLT</span>
      </div>

      <div class="button-row">
        <button class="btn btn-secondary" on:click={() => step = 'form'} disabled={sending}>
          Back
        </button>
        <button class="btn btn-primary" on:click={send} disabled={sending}>
          {sending ? 'Sending...' : 'Send Now'}
        </button>
      </div>
    </div>

  {:else if step === 'result'}
    <div class="card">
      <div class="result" class:success={result?.success} class:failure={!result?.success}>
        <div class="result-icon">{result?.success ? '✓' : '✗'}</div>
        <div class="result-title">{result?.success ? 'Transaction Sent' : 'Transaction Failed'}</div>
        <div class="result-message">{result?.message || 'Transaction submitted to mempool'}</div>
      </div>

      <button class="btn btn-primary" on:click={reset}>
        Send Another
      </button>
    </div>
  {/if}
</div>

<style>
  .send-view {
    max-width: 520px;
  }

  h2 {
    font-size: 22px;
    font-weight: 700;
    color: #e1e4ea;
    margin-bottom: 24px;
  }

  .card {
    background: #1a1c2e;
    border: 1px solid #232536;
    border-radius: 12px;
    padding: 28px;
  }

  .form-group {
    margin-bottom: 20px;
  }

  .form-group label {
    display: flex;
    justify-content: space-between;
    font-size: 12px;
    color: #9ca3af;
    margin-bottom: 8px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .available {
    text-transform: none;
    letter-spacing: 0;
    color: #6b7280;
    font-family: 'SF Mono', 'Fira Code', monospace;
  }

  .form-group input {
    width: 100%;
    padding: 12px 14px;
    border-radius: 8px;
    border: 1px solid #2d2f44;
    background: #161822;
    color: #e1e4ea;
    font-size: 14px;
    outline: none;
    transition: border-color 0.2s;
  }

  .form-group input:focus {
    border-color: #6366f1;
  }

  .form-group input.invalid {
    border-color: #ef4444;
  }

  .form-group input.invalid:focus {
    border-color: #ef4444;
  }

  .field-error {
    color: #ef4444;
    font-size: 11px;
    margin-top: 6px;
  }

  .fee-min {
    text-transform: none;
    letter-spacing: 0;
    color: #6b7280;
    font-family: 'SF Mono', 'Fira Code', monospace;
  }

  .btn {
    padding: 12px 24px;
    border-radius: 8px;
    border: none;
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
    transition: all 0.2s;
    width: 100%;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-primary {
    background: linear-gradient(135deg, #6366f1, #8b5cf6);
    color: white;
  }

  .btn-primary:hover:not(:disabled) {
    box-shadow: 0 4px 16px rgba(99, 102, 241, 0.4);
  }

  .btn-secondary {
    background: #1e2030;
    color: #9ca3af;
    border: 1px solid #2d2f44;
  }

  .confirm-header {
    font-size: 18px;
    font-weight: 600;
    color: #e1e4ea;
    margin-bottom: 24px;
    text-align: center;
  }

  .confirm-detail {
    display: flex;
    justify-content: space-between;
    padding: 12px 0;
    border-bottom: 1px solid #232536;
  }

  .detail-label {
    color: #6b7280;
    font-size: 14px;
  }

  .detail-value {
    color: #e1e4ea;
    font-size: 14px;
    text-align: right;
    max-width: 300px;
    word-break: break-all;
  }

  .mono {
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
  }

  .confirm-detail.total {
    border-bottom: none;
    font-weight: 600;
  }

  .confirm-detail.total .detail-label,
  .confirm-detail.total .detail-value {
    color: #e1e4ea;
  }

  .button-row {
    display: flex;
    gap: 12px;
    margin-top: 24px;
  }

  .button-row .btn {
    flex: 1;
  }

  .result {
    text-align: center;
    padding: 24px 0;
    margin-bottom: 20px;
  }

  .result-icon {
    font-size: 48px;
    margin-bottom: 12px;
  }

  .result.success .result-icon { color: #22c55e; }
  .result.failure .result-icon { color: #ef4444; }

  .result-title {
    font-size: 20px;
    font-weight: 600;
    color: #e1e4ea;
    margin-bottom: 8px;
  }

  .result-message {
    font-size: 14px;
    color: #6b7280;
  }
</style>
