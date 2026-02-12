<script>
  import { currentView, walletAddress, walletUnlocked, walletEncrypted, showToast } from '../lib/stores.js'

  let mode = 'choose' // 'choose', 'create', 'unlock'
  let passphrase = ''
  let confirmPassphrase = ''
  let loading = false
  let hasExisting = false

  import { onMount } from 'svelte'

  onMount(async () => {
    try {
      hasExisting = await window.go.main.App.HasWallet()
      if (hasExisting) mode = 'unlock'
    } catch {}
  })

  async function createWallet() {
    if (passphrase && passphrase !== confirmPassphrase) {
      showToast('Passphrases do not match', 'error')
      return
    }

    loading = true
    try {
      const info = await window.go.main.App.CreateWallet(passphrase)
      if (info.address.startsWith('ERROR')) {
        showToast(info.address, 'error')
        return
      }
      $walletAddress = info.address
      $walletEncrypted = info.encrypted
      $walletUnlocked = true
      $currentView = 'dashboard'
      showToast('Wallet created successfully!', 'success')
    } catch (e) {
      showToast('Failed to create wallet: ' + e, 'error')
    } finally {
      loading = false
    }
  }

  async function unlockWallet() {
    loading = true
    try {
      const info = await window.go.main.App.LoadWallet(passphrase)
      if (info.address.startsWith('ERROR')) {
        showToast(info.address.replace('ERROR: ', ''), 'error')
        return
      }
      $walletAddress = info.address
      $walletEncrypted = info.encrypted
      $walletUnlocked = true
      $currentView = 'dashboard'
      showToast('Wallet unlocked', 'success')
    } catch (e) {
      showToast('Failed to unlock: ' + e, 'error')
    } finally {
      loading = false
    }
  }
</script>

<div class="welcome">
  <div class="welcome-card">
    <div class="logo-big">D</div>
    <h1>Dilithium Wallet</h1>
    <p class="subtitle">Quantum-safe cryptocurrency wallet</p>

    {#if mode === 'choose'}
      <div class="button-group">
        <button class="btn btn-primary" on:click={() => mode = 'create'}>
          Create New Wallet
        </button>
        {#if hasExisting}
          <button class="btn btn-secondary" on:click={() => mode = 'unlock'}>
            Unlock Existing Wallet
          </button>
        {/if}
      </div>
    {:else if mode === 'create'}
      <form on:submit|preventDefault={createWallet} class="form">
        <div class="form-group">
          <label for="pass">Passphrase (optional)</label>
          <input
            id="pass"
            type="password"
            bind:value={passphrase}
            placeholder="Leave empty for no encryption"
          />
        </div>
        {#if passphrase}
          <div class="form-group">
            <label for="confirm">Confirm Passphrase</label>
            <input
              id="confirm"
              type="password"
              bind:value={confirmPassphrase}
              placeholder="Confirm passphrase"
            />
          </div>
        {/if}
        <div class="button-group">
          <button type="submit" class="btn btn-primary" disabled={loading}>
            {loading ? 'Creating...' : 'Create Wallet'}
          </button>
          <button type="button" class="btn btn-secondary" on:click={() => mode = 'choose'}>
            Back
          </button>
        </div>
        <p class="hint">Your wallet will be stored in ~/.dilithium/wallet/</p>
      </form>
    {:else if mode === 'unlock'}
      <form on:submit|preventDefault={unlockWallet} class="form">
        <div class="form-group">
          <label for="unlock-pass">Passphrase</label>
          <input
            id="unlock-pass"
            type="password"
            bind:value={passphrase}
            placeholder="Enter your passphrase"
            autofocus
          />
        </div>
        <div class="button-group">
          <button type="submit" class="btn btn-primary" disabled={loading}>
            {loading ? 'Unlocking...' : 'Unlock'}
          </button>
          {#if !hasExisting}
            <button type="button" class="btn btn-secondary" on:click={() => mode = 'choose'}>
              Back
            </button>
          {/if}
        </div>
      </form>
    {/if}
  </div>
</div>

<style>
  .welcome {
    width: 100vw;
    height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, #0f1117 0%, #1a1530 50%, #0f1117 100%);
  }

  .welcome-card {
    text-align: center;
    max-width: 400px;
    width: 100%;
    padding: 48px 40px;
  }

  .logo-big {
    width: 72px;
    height: 72px;
    border-radius: 18px;
    background: linear-gradient(135deg, #6366f1, #8b5cf6);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 36px;
    font-weight: 700;
    color: white;
    margin: 0 auto 24px;
    box-shadow: 0 8px 32px rgba(99, 102, 241, 0.3);
  }

  h1 {
    font-size: 28px;
    font-weight: 700;
    color: #e1e4ea;
    margin-bottom: 8px;
  }

  .subtitle {
    color: #6b7280;
    font-size: 14px;
    margin-bottom: 36px;
  }

  .form {
    text-align: left;
  }

  .form-group {
    margin-bottom: 16px;
  }

  .form-group label {
    display: block;
    font-size: 12px;
    color: #9ca3af;
    margin-bottom: 6px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
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

  .button-group {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 24px;
  }

  .btn {
    padding: 12px 24px;
    border-radius: 8px;
    border: none;
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
    transition: all 0.2s;
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

  .btn-secondary:hover:not(:disabled) {
    background: #252840;
    color: #e1e4ea;
  }

  .hint {
    text-align: center;
    font-size: 12px;
    color: #4b5563;
    margin-top: 16px;
  }
</style>
