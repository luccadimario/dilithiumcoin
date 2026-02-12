<script>
  import { currentView, walletAddress, nodeConnected } from '../lib/stores.js'
  import { truncateAddress } from '../lib/formatters.js'
  import { onMount, onDestroy } from 'svelte'

  let interval

  onMount(async () => {
    checkConnection()
    interval = setInterval(checkConnection, 30000)
  })

  onDestroy(() => {
    if (interval) clearInterval(interval)
  })

  async function checkConnection() {
    try {
      const status = await window.go.main.App.TestConnection()
      $nodeConnected = status.connected
    } catch {
      $nodeConnected = false
    }
  }

  const navItems = [
    { id: 'dashboard', label: 'Dashboard', icon: '◆' },
    { id: 'send', label: 'Send', icon: '↑' },
    { id: 'receive', label: 'Receive', icon: '↓' },
    { id: 'history', label: 'History', icon: '☰' },
    { id: 'settings', label: 'Settings', icon: '⚙' },
  ]
</script>

<aside class="sidebar">
  <div class="logo-section">
    <div class="logo">D</div>
    <div class="logo-text">
      <span class="logo-title">Dilithium</span>
      <span class="logo-subtitle">Wallet</span>
    </div>
  </div>

  <div class="address-section">
    <div class="address-label">Your Address</div>
    <div class="address-value">{truncateAddress($walletAddress, 6)}</div>
  </div>

  <nav>
    {#each navItems as item}
      <button
        class="nav-item"
        class:active={$currentView === item.id}
        on:click={() => $currentView = item.id}
      >
        <span class="nav-icon">{item.icon}</span>
        <span class="nav-label">{item.label}</span>
      </button>
    {/each}
  </nav>

  <div class="status-section">
    <div class="status-dot" class:connected={$nodeConnected}></div>
    <span class="status-text">{$nodeConnected ? 'Connected' : 'Disconnected'}</span>
  </div>
</aside>

<style>
  .sidebar {
    width: 220px;
    min-width: 220px;
    background: #161822;
    border-right: 1px solid #232536;
    display: flex;
    flex-direction: column;
    padding: 20px 0;
    height: 100vh;
  }

  .logo-section {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 20px;
    margin-bottom: 28px;
  }

  .logo {
    width: 40px;
    height: 40px;
    border-radius: 10px;
    background: linear-gradient(135deg, #6366f1, #8b5cf6);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 20px;
    font-weight: 700;
    color: white;
  }

  .logo-text {
    display: flex;
    flex-direction: column;
  }

  .logo-title {
    font-size: 16px;
    font-weight: 700;
    color: #e1e4ea;
  }

  .logo-subtitle {
    font-size: 11px;
    color: #6b7280;
    text-transform: uppercase;
    letter-spacing: 1px;
  }

  .address-section {
    padding: 12px 20px;
    margin: 0 12px 20px;
    background: #1e2030;
    border-radius: 8px;
  }

  .address-label {
    font-size: 10px;
    color: #6b7280;
    text-transform: uppercase;
    letter-spacing: 1px;
    margin-bottom: 4px;
  }

  .address-value {
    font-size: 13px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    color: #8b5cf6;
  }

  nav {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 0 8px;
  }

  .nav-item {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 16px;
    border: none;
    background: none;
    color: #9ca3af;
    font-size: 14px;
    cursor: pointer;
    border-radius: 8px;
    transition: all 0.15s;
    text-align: left;
    width: 100%;
  }

  .nav-item:hover {
    background: #1e2030;
    color: #e1e4ea;
  }

  .nav-item.active {
    background: #252840;
    color: #8b5cf6;
  }

  .nav-icon {
    font-size: 16px;
    width: 20px;
    text-align: center;
  }

  .nav-label {
    font-weight: 500;
  }

  .status-section {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 16px 20px;
    border-top: 1px solid #232536;
    margin-top: auto;
  }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #ef4444;
  }

  .status-dot.connected {
    background: #22c55e;
  }

  .status-text {
    font-size: 12px;
    color: #6b7280;
  }
</style>
