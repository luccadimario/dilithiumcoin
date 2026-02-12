<script>
  import { walletAddress, showToast } from '../lib/stores.js'

  let copied = false

  async function copyAddress() {
    try {
      await navigator.clipboard.writeText($walletAddress)
      copied = true
      showToast('Address copied to clipboard', 'success')
      setTimeout(() => copied = false, 2000)
    } catch {
      showToast('Failed to copy', 'error')
    }
  }
</script>

<div class="receive-view">
  <h2>Receive DLT</h2>

  <div class="card">
    <p class="instructions">Share your address to receive DLT tokens.</p>

    <div class="address-box">
      <div class="address-label">Your Wallet Address</div>
      <div class="address-value">{$walletAddress}</div>
    </div>

    <button class="btn btn-primary" on:click={copyAddress}>
      {copied ? 'Copied!' : 'Copy Address'}
    </button>

    <div class="info-section">
      <div class="info-item">
        <span class="info-icon">&#9432;</span>
        <span>Only send DLT (Dilithium) to this address</span>
      </div>
      <div class="info-item">
        <span class="info-icon">&#9432;</span>
        <span>CRYSTALS-Dilithium Mode3 quantum-safe address</span>
      </div>
    </div>
  </div>
</div>

<style>
  .receive-view {
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

  .instructions {
    color: #9ca3af;
    font-size: 14px;
    margin-bottom: 24px;
  }

  .address-box {
    background: #161822;
    border: 1px solid #2d2f44;
    border-radius: 8px;
    padding: 16px;
    margin-bottom: 20px;
  }

  .address-label {
    font-size: 10px;
    color: #6b7280;
    text-transform: uppercase;
    letter-spacing: 1px;
    margin-bottom: 8px;
  }

  .address-value {
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 14px;
    color: #8b5cf6;
    word-break: break-all;
    line-height: 1.6;
    user-select: text;
    -webkit-user-select: text;
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

  .btn-primary {
    background: linear-gradient(135deg, #6366f1, #8b5cf6);
    color: white;
  }

  .btn-primary:hover {
    box-shadow: 0 4px 16px rgba(99, 102, 241, 0.4);
  }

  .info-section {
    margin-top: 24px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .info-item {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: #6b7280;
  }

  .info-icon {
    font-size: 16px;
    color: #4b5563;
  }
</style>
