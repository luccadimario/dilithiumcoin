<script>
  import { onMount } from 'svelte'
  import { showToast, balance } from '../lib/stores.js'
  import { formatDLT } from '../lib/formatters.js'

  let sectorX = Math.floor(Math.random() * 10)
  let sectorY = Math.floor(Math.random() * 10)
  let log = ["USS Dilithium initialized.", "Tactical sensors online."]
  let processing = false
  let shipName = "USS Dilithium"

  // Game Grid (10x10 for visual clarity)
  const gridSize = 10
  const sectors = Array.from({ length: gridSize * gridSize }, (_, i) => ({
    x: i % gridSize,
    y: Math.floor(i / gridSize)
  }))

  onMount(async () => {
    try {
      const name = await window.go.main.App.GetShipName()
      shipName = name
      addLog(`Captain on the bridge of the ${shipName}.`)
    } catch (e) {
      console.error(e)
    }
  })

  async function updateShipName() {
    try {
      await window.go.main.App.SetShipName(shipName)
      addLog(`Ship registry updated: ${shipName}`)
      showToast('Ship name updated', 'success')
    } catch (e) {
      showToast('Failed to update name', 'error')
    }
  }

  async function execute(action, target) {
    if (processing) return
    processing = true
    
    addLog(`Initiating ${action} sequence: ${target}...`)
    
    try {
      const result = await window.go.main.App.ExecuteGameAction(action, target)
      if (result.success) {
        addLog(`Subspace command broadcast successful.`)
        
        if (action === 'WARP') {
          const coords = target.split(',')
          sectorX = parseInt(coords[0])
          sectorY = parseInt(coords[1])
          addLog(`Warp complete. Entering Sector ${sectorX},${sectorY}.`)
        } else if (action === 'SCAN') {
          addLog(`Scanning Sector ${sectorX},${sectorY}... No lifeforms detected.`)
        }
        
        setTimeout(async () => {
          const bal = await window.go.main.App.GetBalance()
          if (!bal.error) $balance = bal
        }, 2000)
      } else {
        addLog(`Comm Failure: ${result.message}`)
        showToast(result.message, 'error')
      }
    } catch (e) {
      addLog(`System failure: ${e}`)
    } finally {
      processing = false
    }
  }

  function addLog(msg) {
    const timestamp = new Date().toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
    log = [`[${timestamp}] ${msg}`, ...log].slice(0, 50)
  }

  function handleSectorClick(x, y) {
    if (x === sectorX && y === sectorY) {
      execute('SCAN', `${x},${y}`)
    } else {
      execute('WARP', `${x},${y}`)
    }
  }
</script>

<div class="game-view">
  <div class="lcars-header">
    <div class="lcars-bar lcars-pink" style="width: 150px"></div>
    <div class="lcars-title">TACTICAL DISPLAY</div>
    <div class="lcars-bar lcars-blue"></div>
  </div>

  <div class="main-layout">
    <!-- Left Column: Settings & Status -->
    <div class="left-panel">
      <div class="lcars-bracket">
        <div class="form-group">
          <label>SHIP REGISTRY</label>
          <div class="input-row">
            <input type="text" bind:value={shipName} placeholder="Enter ship name" />
            <button class="small-btn" on:click={updateShipName}>SET</button>
          </div>
        </div>
      </div>

      <div class="status-panel">
        <div class="status-item">
          <span class="label">LOCATION</span>
          <span class="value">SEC {sectorX}-{sectorY}</span>
        </div>
        <div class="status-item">
          <span class="label">ENERGY</span>
          <span class="value">{formatDLT($balance.balance_dlt)}</span>
        </div>
        <div class="status-item">
          <span class="label">STATUS</span>
          <span class="value" class:blink={processing}>{processing ? 'ENGAGED' : 'STANDBY'}</span>
        </div>
      </div>

      <div class="lcars-controls">
        <button class="lcars-btn lcars-orange" on:click={() => execute('SCAN', `${sectorX},${sectorY}`)} disabled={processing}>
          LONG RANGE SCAN
        </button>
        <button class="lcars-btn lcars-red" on:click={() => addLog("Red Alert! Protocol active.")}>
          RED ALERT
        </button>
      </div>
    </div>

    <!-- Center: Tactical Map -->
    <div class="center-panel">
      <div class="tactical-grid">
        {#each sectors as sector}
          <button 
            class="sector-tile" 
            class:current={sector.x === sectorX && sector.y === sectorY}
            on:click={() => handleSectorClick(sector.x, sector.y)}
            disabled={processing}
          >
            {#if sector.x === sectorX && sector.y === sectorY}
              <div class="ship-icon">▲</div>
            {/if}
            <span class="coords">{sector.x},{sector.y}</span>
          </button>
        {/each}
      </div>
    </div>

    <!-- Right: Comms Log -->
    <div class="right-panel">
      <div class="log-container">
        <div class="log-header">COMMUNICATIONS</div>
        <div class="log-content">
          {#each log as entry}
            <div class="log-entry">{entry}</div>
          {/each}
        </div>
      </div>
    </div>
  </div>

  <div class="lcars-footer">
    <div class="lcars-bar lcars-orange"></div>
    <div class="lcars-tag">{shipName} // NCC-1701-D</div>
    <div class="lcars-bar lcars-pink" style="width: 100px"></div>
  </div>
</div>

<style>
  .game-view {
    height: 100%;
    display: flex;
    flex-direction: column;
    color: #ff9900;
    font-family: 'Arial Narrow', sans-serif;
    text-transform: uppercase;
    background: #000;
  }

  .lcars-header, .lcars-footer {
    display: flex;
    align-items: center;
    gap: 15px;
    margin-bottom: 20px;
  }

  .lcars-bar {
    height: 30px;
    border-radius: 15px;
  }

  .lcars-pink { background: #ff99cc; }
  .lcars-blue { background: #99ccff; flex: 1; }
  .lcars-orange { background: #ff9900; flex: 1; }
  .lcars-red { background: #cc0000; }

  .lcars-title {
    font-size: 28px;
    font-weight: bold;
    color: #ffcc00;
  }

  .main-layout {
    display: flex;
    gap: 20px;
    flex: 1;
    min-height: 0;
    padding: 0 10px;
  }

  /* Panels */
  .left-panel { width: 220px; display: flex; flex-direction: column; gap: 20px; }
  .center-panel { flex: 1; display: flex; justify-content: center; align-items: center; background: #080808; border-radius: 20px; border: 2px solid #333; }
  .right-panel { width: 250px; display: flex; flex-direction: column; }

  /* Settings */
  .lcars-bracket {
    border-left: 10px solid #99ccff;
    padding-left: 15px;
    border-radius: 10px 0 0 10px;
  }

  .form-group label { font-size: 11px; color: #99ccff; margin-bottom: 5px; display: block; }
  .input-row { display: flex; gap: 5px; }
  input { background: #000; border: 1px solid #ff9900; color: #ffcc00; padding: 5px; width: 100%; font-size: 12px; }
  .small-btn { background: #ff99cc; border: none; padding: 5px 10px; font-weight: bold; cursor: pointer; border-radius: 5px; font-size: 10px; }

  /* Status */
  .status-panel { display: flex; flex-direction: column; gap: 5px; }
  .status-item { display: flex; justify-content: space-between; background: #111; padding: 8px 12px; border-radius: 5px; border-right: 5px solid #ff9900; }
  .status-item .label { color: #99ccff; font-size: 11px; }
  .status-item .value { font-weight: bold; font-size: 14px; }

  /* Grid Map */
  .tactical-grid {
    display: grid;
    grid-template-columns: repeat(10, 1fr);
    grid-template-rows: repeat(10, 1fr);
    gap: 2px;
    width: 90%;
    height: 90%;
    max-width: 500px;
    max-height: 500px;
    background: #111;
    padding: 5px;
    border: 1px solid #444;
  }

  .sector-tile {
    background: #000;
    border: 1px solid #222;
    position: relative;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    transition: all 0.2s;
  }

  .sector-tile:hover:not(:disabled) { background: #1a1a1a; border-color: #ff9900; z-index: 10; }
  .sector-tile.current { background: #1e2a3a; border-color: #99ccff; }

  .coords { font-size: 8px; color: #333; position: absolute; bottom: 2px; right: 2px; }
  .sector-tile:hover .coords { color: #666; }

  .ship-icon {
    font-size: 20px;
    color: #99ccff;
    text-shadow: 0 0 10px #99ccff;
    animation: pulse 2s infinite;
  }

  /* Controls */
  .lcars-controls { display: flex; flex-direction: column; gap: 10px; }
  .lcars-btn {
    border: none; padding: 12px; color: #000; font-weight: bold; cursor: pointer;
    border-radius: 15px; font-size: 14px; text-align: right; padding-right: 20px;
  }

  .log-container { flex: 1; display: flex; flex-direction: column; background: #000; border: 1px solid #333; min-height: 0; }
  .log-header { padding: 8px; background: #333; color: #99ccff; font-size: 12px; font-weight: bold; }
  .log-content { padding: 10px; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; font-family: monospace; font-size: 10px; color: #99ccff; }

  @keyframes pulse {
    0% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.2); opacity: 0.7; }
    100% { transform: scale(1); opacity: 1; }
  }

  .blink { animation: blink 1s infinite; }
  @keyframes blink { 0% { opacity: 1; } 50% { opacity: 0.3; } 100% { opacity: 1; } }
</style>
