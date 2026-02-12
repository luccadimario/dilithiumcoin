<script>
  import { onMount } from 'svelte'
  import { nodeURL, walletAddress, walletUnlocked, walletEncrypted, currentView, showToast } from '../lib/stores.js'

  let nodeInput = ''
  let testing = false
  let testResult = null
  let showExportKey = false
  let exportedKey = ''
  let appVersion = ''

  onMount(async () => {
    nodeInput = await window.go.main.App.GetNodeURL()
    $nodeURL = nodeInput
    appVersion = await window.go.main.App.GetVersion()
  })

  async function testNode() {
    testing = true
    testResult = null
    try {
      await window.go.main.App.SetNodeURL(nodeInput)
      $nodeURL = nodeInput
      const status = await window.go.main.App.TestConnection()
      testResult = status
      if (status.connected) {
        showToast('Connected to node successfully', 'success')
      } else {
        showToast('Could not connect: ' + (status.error || 'unknown error'), 'error')
      }
    } catch (e) {
      showToast('Connection failed: ' + e, 'error')
    }
    testing = false
  }

  async function saveNodeURL() {
    await window.go.main.App.SetNodeURL(nodeInput)
    $nodeURL = nodeInput
    showToast('Node URL updated', 'success')
  }

  async function exportKey() {
    const key = await window.go.main.App.ExportPrivateKey()
    if (key.startsWith('ERROR')) {
      showToast(key, 'error')
      return
    }
    exportedKey = key
    showExportKey = true
  }

  async function lockWallet() {
    await window.go.main.App.LockWallet()
    $walletUnlocked = false
    $walletAddress = ''
    $currentView = 'welcome'
    showToast('Wallet locked', 'info')
  }
</script>

<div class="settings-view">
  <h2>Settings</h2>

  <div class="section">
    <h3>Node Connection</h3>
    <div class="card">
      <div class="form-group">
        <label for="node-url">Node API URL</label>
        <div class="input-row">
          <input
            id="node-url"
            type="text"
            bind:value={nodeInput}
            placeholder="https://api.dilithiumcoin.com"
          />
          <button class="btn btn-sm" on:click={testNode} disabled={testing}>
            {testing ? 'Testing...' : 'Test'}
          </button>
          <button class="btn btn-sm" on:click={saveNodeURL}>Save</button>
        </div>
      </div>

      {#if testResult}
        <div class="test-result" class:connected={testResult.connected}>
          {#if testResult.connected}
            <div class="test-status">Connected</div>
            <div class="test-detail">Version: {testResult.version}</div>
            <div class="test-detail">Block Height: {testResult.block_height}</div>
            <div class="test-detail">Peers: {testResult.peer_count}</div>
            <div class="test-detail">Pending TXs: {testResult.pending_txs}</div>
          {:else}
            <div class="test-status">Disconnected</div>
            <div class="test-detail">{testResult.error}</div>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  <div class="section">
    <h3>Wallet</h3>
    <div class="card">
      <div class="wallet-info">
        <div class="info-row">
          <span class="info-label">Address</span>
          <span class="info-value mono">{$walletAddress}</span>
        </div>
        <div class="info-row">
          <span class="info-label">Encrypted</span>
          <span class="info-value">{$walletEncrypted ? 'Yes' : 'No'}</span>
        </div>
        <div class="info-row">
          <span class="info-label">Algorithm</span>
          <span class="info-value">CRYSTALS-Dilithium Mode3</span>
        </div>
        <div class="info-row">
          <span class="info-label">Location</span>
          <span class="info-value mono">~/.dilithium/wallet/</span>
        </div>
      </div>

      <div class="button-group">
        {#if !showExportKey}
          <button class="btn btn-danger-outline" on:click={exportKey}>
            Export Private Key
          </button>
        {:else}
          <div class="export-warning">
            <div class="warning-text">
              WARNING: Anyone with this key can steal your funds. Do not share it.
            </div>
            <pre class="export-key">{exportedKey}</pre>
            <button class="btn btn-sm" on:click={() => { showExportKey = false; exportedKey = '' }}>
              Hide Key
            </button>
          </div>
        {/if}

        <button class="btn btn-danger" on:click={lockWallet}>
          Lock Wallet
        </button>
      </div>
    </div>
  </div>

  <div class="section">
    <h3>About</h3>
    <div class="card">
      <div class="about-info">
        <div>Dilithium Wallet v{appVersion}</div>
        <div class="about-detail">Quantum-safe desktop wallet for the Dilithium blockchain</div>
      </div>
    </div>
  </div>
</div>

<style>
  .settings-view {
    max-width: 620px;
  }

  h2 {
    font-size: 22px;
    font-weight: 700;
    color: #e1e4ea;
    margin-bottom: 24px;
  }

  .section {
    margin-bottom: 28px;
  }

  h3 {
    font-size: 14px;
    font-weight: 600;
    color: #9ca3af;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 12px;
  }

  .card {
    background: #1a1c2e;
    border: 1px solid #232536;
    border-radius: 12px;
    padding: 20px;
  }

  .form-group {
    margin-bottom: 12px;
  }

  .form-group label {
    display: block;
    font-size: 12px;
    color: #6b7280;
    margin-bottom: 6px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .input-row {
    display: flex;
    gap: 8px;
  }

  .input-row input {
    flex: 1;
    padding: 10px 12px;
    border-radius: 6px;
    border: 1px solid #2d2f44;
    background: #161822;
    color: #e1e4ea;
    font-size: 13px;
    outline: none;
  }

  .input-row input:focus {
    border-color: #6366f1;
  }

  .btn {
    padding: 10px 16px;
    border-radius: 6px;
    border: none;
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.15s;
    white-space: nowrap;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-sm {
    background: #252840;
    color: #9ca3af;
    border: 1px solid #2d2f44;
  }

  .btn-sm:hover:not(:disabled) {
    background: #2d3050;
    color: #e1e4ea;
  }

  .btn-danger-outline {
    background: transparent;
    color: #ef4444;
    border: 1px solid #6b2d2d;
    width: 100%;
  }

  .btn-danger-outline:hover {
    background: #1a0f0f;
  }

  .btn-danger {
    background: #7f1d1d;
    color: #fca5a5;
    width: 100%;
  }

  .btn-danger:hover {
    background: #991b1b;
  }

  .test-result {
    margin-top: 12px;
    padding: 12px;
    border-radius: 8px;
    background: #161822;
    border: 1px solid #2d2f44;
  }

  .test-result.connected {
    border-color: #2d6b4a;
  }

  .test-status {
    font-weight: 600;
    font-size: 14px;
    margin-bottom: 6px;
  }

  .test-result.connected .test-status { color: #22c55e; }
  .test-result:not(.connected) .test-status { color: #ef4444; }

  .test-detail {
    font-size: 12px;
    color: #6b7280;
  }

  .wallet-info {
    margin-bottom: 16px;
  }

  .info-row {
    display: flex;
    justify-content: space-between;
    padding: 8px 0;
    border-bottom: 1px solid #232536;
  }

  .info-row:last-child {
    border-bottom: none;
  }

  .info-label {
    font-size: 13px;
    color: #6b7280;
  }

  .info-value {
    font-size: 13px;
    color: #e1e4ea;
    text-align: right;
    max-width: 360px;
    word-break: break-all;
  }

  .mono {
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 11px;
  }

  .button-group {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .export-warning {
    padding: 14px;
    background: #1a0f0f;
    border: 1px solid #6b2d2d;
    border-radius: 8px;
  }

  .warning-text {
    color: #ef4444;
    font-size: 12px;
    font-weight: 600;
    margin-bottom: 10px;
  }

  .export-key {
    background: #0f1117;
    padding: 12px;
    border-radius: 6px;
    font-size: 10px;
    color: #9ca3af;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-all;
    margin-bottom: 10px;
    user-select: text;
    -webkit-user-select: text;
  }

  .about-info {
    font-size: 14px;
    color: #e1e4ea;
  }

  .about-detail {
    font-size: 12px;
    color: #6b7280;
    margin-top: 4px;
  }
</style>
