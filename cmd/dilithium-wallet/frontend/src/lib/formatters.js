// Format a DLT amount string for display (trim trailing zeros but keep 2 min)
export function formatDLT(amountStr) {
  if (!amountStr) return '0.00'
  const parts = amountStr.split('.')
  if (parts.length === 1) return amountStr + '.00'
  // Trim trailing zeros but keep at least 2 decimal places
  let decimals = parts[1].replace(/0+$/, '')
  if (decimals.length < 2) decimals = decimals.padEnd(2, '0')
  return parts[0] + '.' + decimals
}

// Truncate an address for display
export function truncateAddress(addr, chars = 8) {
  if (!addr || addr.length <= chars * 2) return addr
  return addr.slice(0, chars) + '...' + addr.slice(-chars)
}

// Format a Unix timestamp to a human-readable date
export function formatTimestamp(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  return d.toLocaleDateString() + ' ' + d.toLocaleTimeString()
}

// Format relative time
export function timeAgo(ts) {
  if (!ts) return ''
  const now = Math.floor(Date.now() / 1000)
  const diff = now - ts
  if (diff < 60) return 'just now'
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago'
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago'
  return Math.floor(diff / 86400) + 'd ago'
}
