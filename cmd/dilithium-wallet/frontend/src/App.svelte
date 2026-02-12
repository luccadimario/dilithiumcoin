<script>
  import { onMount } from 'svelte'
  import { currentView, walletAddress, walletUnlocked, walletEncrypted, toast } from './lib/stores.js'
  import Sidebar from './components/Sidebar.svelte'
  import Welcome from './views/Welcome.svelte'
  import Dashboard from './views/Dashboard.svelte'
  import Send from './views/Send.svelte'
  import Receive from './views/Receive.svelte'
  import History from './views/History.svelte'
  import Settings from './views/Settings.svelte'

  onMount(async () => {
    try {
      const hasWallet = await window.go.main.App.HasWallet()
      if (hasWallet) {
        const encrypted = await window.go.main.App.IsEncrypted()
        if (encrypted) {
          // Wallet exists but is encrypted — show welcome to unlock
          $currentView = 'welcome'
        } else {
          // Unencrypted wallet — auto-load
          const info = await window.go.main.App.LoadWallet('')
          if (!info.address.startsWith('ERROR')) {
            $walletAddress = info.address
            $walletUnlocked = true
            $walletEncrypted = false
            $currentView = 'dashboard'
          }
        }
      }
    } catch (e) {
      console.error('Startup error:', e)
    }
  })
</script>

<main>
  {#if $walletUnlocked}
    <div class="app-layout">
      <Sidebar />
      <div class="content">
        {#if $currentView === 'dashboard'}
          <Dashboard />
        {:else if $currentView === 'send'}
          <Send />
        {:else if $currentView === 'receive'}
          <Receive />
        {:else if $currentView === 'history'}
          <History />
        {:else if $currentView === 'settings'}
          <Settings />
        {:else}
          <Dashboard />
        {/if}
      </div>
    </div>
  {:else}
    <Welcome />
  {/if}

  {#if $toast.visible}
    <div class="toast toast-{$toast.type}">
      {$toast.message}
    </div>
  {/if}
</main>

<style>
  main {
    width: 100vw;
    height: 100vh;
    display: flex;
    flex-direction: column;
  }

  .app-layout {
    display: flex;
    height: 100vh;
    width: 100%;
  }

  .content {
    flex: 1;
    overflow-y: auto;
    padding: 32px;
    background: #0f1117;
  }

  .toast {
    position: fixed;
    bottom: 24px;
    right: 24px;
    padding: 12px 20px;
    border-radius: 8px;
    font-size: 14px;
    font-weight: 500;
    z-index: 1000;
    animation: slideIn 0.3s ease;
    max-width: 400px;
  }

  .toast-info {
    background: #1e3a5f;
    border: 1px solid #2d5a8e;
    color: #8ec5fc;
  }

  .toast-success {
    background: #1a3a2a;
    border: 1px solid #2d6b4a;
    color: #6bcf8e;
  }

  .toast-error {
    background: #3a1a1a;
    border: 1px solid #6b2d2d;
    color: #cf6b6b;
  }

  @keyframes slideIn {
    from { transform: translateY(20px); opacity: 0; }
    to { transform: translateY(0); opacity: 1; }
  }
</style>
