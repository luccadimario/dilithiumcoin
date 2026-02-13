# Dilithium GPU Miner - Architecture

High-performance CUDA-accelerated miner for Dilithium cryptocurrency, built with Rust and CUDA.

## Technical Architecture

### Mining Algorithm (Dilithium PoW)
```
Block Hash = SHA256(Index + Timestamp + Transactions(JSON) + PreviousHash + Nonce + Difficulty)
Difficulty: Bit-precise (e.g., 38 leading zero bits)
```

### Optimization Strategy

**Midstate Precomputation:**
- Fixed prefix: `Index + Timestamp + Transactions + PreviousHash`
- Compute SHA-256 midstate on CPU (one-time per block template)
- Variable suffix: `Nonce + Difficulty`
- GPU only hashes: `SHA256_resume(midstate, tail + nonce + suffix)`

**CUDA Kernel Design:**
```
Threads per block: 256 (optimized for modern GPUs)
Batch size: 67,108,864 (2^26) nonces per kernel launch (configurable)
```

**Memory Architecture:**
- Constant memory: Midstate (32 bytes), prefix tail, suffix, difficulty bits
- Global memory: Result buffer (nonce + hash)
- Shared memory: None (SHA-256 is register-bound)
- Register usage: <64 per thread (target for high occupancy)

**Advanced Optimizations:**
- Warp-level primitives (__ballot_sync, __funnelshift_r)
- Instruction-level parallelism (ILP) via loop unrolling
- Early exit via atomic result flag
- Async kernel launches

## Project Structure

```
gpu-miner/
├── Cargo.toml                  # Rust dependencies
├── build.rs                    # CUDA compilation script
├── src/
│   ├── main.rs                 # Entry point, CLI args
│   ├── miner.rs                # Mining coordinator
│   ├── network.rs              # Node API client (async)
│   ├── worker.rs               # GPU worker threads
│   ├── sha256.rs               # Host-side SHA-256 (midstate)
│   ├── cuda.rs                 # CUDA FFI bindings
│   └── webui.rs                # Web dashboard server
├── cuda/
│   ├── sha256.cuh              # CUDA SHA-256 device functions
│   ├── kernel.cu               # Mining kernel implementation
│   └── bridge.cu               # C API for Rust FFI
└── static/
    └── dashboard.html          # Web monitoring interface
```

## Performance Characteristics

### Expected Hashrates (reference values)
- NVIDIA RTX 3060 Ti: ~2000 MH/s
- NVIDIA RTX 3070: ~2400 MH/s
- NVIDIA RTX 3080: ~3200 MH/s
- NVIDIA RTX 4090: ~5000+ MH/s

*Actual performance varies based on difficulty, batch size, and system configuration.*

### Performance vs Go Miner

**Rust+CUDA Advantages:**
1. **Zero-Cost Abstractions** - Direct CUDA integration, no CGO overhead
2. **Memory Safety** - No garbage collection pauses, deterministic resource management
3. **Superior Concurrency** - Tokio async runtime for efficient I/O
4. **Optimization** - LLVM backend with LTO and profile-guided optimization

**Typical Performance Gain:** 30-50% faster than equivalent Go+CGO implementation

## Design Decisions

### Why Rust?
- Memory safety without garbage collection
- Zero-cost abstractions for CUDA FFI
- Excellent async/await support (Tokio)
- Strong type system prevents common bugs

### Why Separate from Go Codebase?
- Different toolchain (cargo vs go build)
- CUDA requires local compilation (no cross-compile)
- Optional component (not all users have NVIDIA GPUs)
- Independent release cycle

### Difficulty Adjustment Support
- Compatible with legacy difficulty adjustment (pre-fork)
- Compatible with DAA (Difficulty Adjustment Algorithm) fork
- Compatible with DAAv2 (difficulty-adjusted LWMA)
- Fetches difficulty dynamically from node /status endpoint

## Implementation Notes

### Hash Counting
- `total_hashes`: Updated when mining completes (solution found or stale work)
- `current_hashrate`: Updated every 3 seconds during mining for real-time dashboard

### Stale Work Detection
- Checks every 3 seconds if blockchain height advanced
- Immediately aborts current work if chain tip changed
- Prevents wasted GPU cycles on outdated blocks

### Web Dashboard
- Real-time hashrate (updated every 3 seconds)
- 5-minute rolling average for stable metrics
- Blocks mined and earnings tracking
- Session uptime and GPU statistics

## Build Process

The miner uses a custom `build.rs` script that:
1. Detects CUDA installation
2. Compiles CUDA kernels with nvcc
3. Creates static library
4. Links with Rust code

See `build.rs` for full compilation details.
