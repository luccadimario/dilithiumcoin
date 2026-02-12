import { writable } from 'svelte/store'

// Current view: 'welcome', 'dashboard', 'send', 'receive', 'history', 'settings'
export const currentView = writable('welcome')

// Wallet state
export const walletAddress = writable('')
export const walletUnlocked = writable(false)
export const walletEncrypted = writable(false)

// Balance
export const balance = writable({
  balance_dlt: '0.00000000',
  total_received_dlt: '0.00000000',
  total_sent_dlt: '0.00000000',
  tx_count: 0,
})

// Node connection
export const nodeConnected = writable(false)
export const nodeURL = writable('https://api.dilithiumcoin.com')

// UI state
export const loading = writable(false)
export const toast = writable({ message: '', type: 'info', visible: false })

// Show a toast notification
export function showToast(message, type = 'info', duration = 4000) {
  toast.set({ message, type, visible: true })
  setTimeout(() => {
    toast.update(t => ({ ...t, visible: false }))
  }, duration)
}
