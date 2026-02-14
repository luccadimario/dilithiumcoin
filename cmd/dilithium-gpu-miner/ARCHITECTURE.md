# Dilithium GPU Miner - Architecture

High-performance GPU-accelerated miner for Dilithium cryptocurrency, built with Rust. Supports NVIDIA CUDA and Apple Metal backends.

## Technical Architecture

### Mining Algorithm (Dilithium PoW)
```
Block Hash = SHA256(Index + Timestamp + Transactions(JSON) + PreviousHash + Nonce + Difficulty)
Difficulty: Bit-precise (e.g., 38 leading zero bits)
```

### Backend Abstraction

The miner uses a `GpuBackend` trait that abstracts over GPU APIs:

```rust
pub trait GpuBackend: Send + Sync {
    fn name(&self) -> &str;
    fn compute_unit_count(&self) -> i32;
    fn mine_batch(...) -> Result<Option<(u64, [u8; 32])>>;
}
```

At build time, exactly one backend is compiled via Cargo feature flags:
- `--features metal` compiles the Metal backend (macOS)
- `--features cuda` compiles the CUDA backend (Linux/Windows)

### Optimization Strategy

**Midstate Precomputation (both backends):**
- Fixed prefix: `Index + Timestamp + Transactions + PreviousHash`
- Compute SHA-256 midstate on CPU (one-time per block template)
- Variable suffix: `Nonce + Difficulty`
- GPU only hashes: `SHA256_resume(midstate, tail + nonce + suffix)`

**Metal-Specific Optimizations:**
- CPU precomputes first 11 SHA-256 rounds (`partial_state`)
- Message schedule rounds 16-24 partially precomputed on CPU
- Direct word construction: pack nonce+suffix+padding without per-byte operations
- Threadgroup shared memory caches MiningParams (1 load per threadgroup)
- Incremental nonce: CPU precomputes base_nonce_str, GPU adds thread ID with carry
- `start_offset` guarantees uniform nonce digit counts (no overflow branches)
- Fully unrolled all 64 SHA-256 rounds (macro-based)
- `fast_div10` multiply-shift trick replaces expensive uint64 division

**CUDA-Specific Optimizations:**
- Warp-level primitives (`__ballot_sync`, `__funnelshift_r`)
- Instruction-level parallelism (ILP) via loop unrolling
- Early exit via atomic result flag
- Async kernel launches

**Batch size:** 67,108,864 (2^26) nonces per kernel launch (configurable)

### Memory Architecture

**Metal (Apple Silicon):**
- `StorageModeShared` buffers — zero-copy on unified memory
- Threadgroup memory: MiningParams cache (~256 bytes, shared across threadgroup)
- Registers: W[16] + state[8] + temps (~28 per thread, spills go to fast L1)
- Register spills on Apple Silicon go to L1 cache (~4-5 cycles), not main memory

**CUDA (NVIDIA):**
- Constant memory: Midstate, prefix tail, suffix, difficulty bits
- Global memory: Result buffer (nonce + hash)
- Register usage: <64 per thread (target for high occupancy)

## Project Structure

```
dilithium-gpu-miner/
├── Cargo.toml                  # Rust dependencies and feature flags
├── build.rs                    # CUDA compilation (feature-gated)
├── install.sh                  # Auto-detecting install script
├── src/
│   ├── main.rs                 # Entry point, CLI args, backend detection
│   ├── miner.rs                # Mining coordinator, stale work detection
│   ├── network.rs              # Node API client (async, Tokio)
│   ├── worker.rs               # GPU worker: batch dispatch + nonce management
│   ├── sha256.rs               # Host-side SHA-256 (midstate computation)
│   ├── backend.rs              # GpuBackend trait + factory
│   ├── metal_backend.rs        # Apple Metal backend (macOS)
│   ├── cuda.rs                 # CUDA FFI bindings (Linux/Windows)
│   └── webui.rs                # Web dashboard server (Axum)
├── metal/
│   └── sha256_mining.metal     # Metal compute shader (macOS)
├── cuda/
│   ├── sha256.cuh              # CUDA SHA-256 device functions
│   ├── bridge.cu               # C API bridge for Rust FFI
│   └── bridge.h                # Header file
└── static/
    └── dashboard.html          # Web monitoring interface
```

## Performance Characteristics

### Reference Hashrates

| GPU | Backend | Hashrate |
|-----|---------|----------|
| Apple M4 (10-core GPU) | Metal | ~800 MH/s |
| Apple M4 Pro (16-core) | Metal | ~1,280 MH/s (est.) |
| NVIDIA RTX 3060 Ti | CUDA | ~2,000 MH/s |
| NVIDIA RTX 3080 | CUDA | ~3,200 MH/s |
| NVIDIA RTX 4090 | CUDA | ~5,000+ MH/s |

*Metal hashrate scales linearly with GPU core count across the M-series lineup.*

### Performance vs Go Miner

**Rust+GPU Advantages:**
1. **Zero-Cost Abstractions** - Direct GPU integration, no CGO overhead
2. **Memory Safety** - No garbage collection pauses, deterministic resource management
3. **Superior Concurrency** - Tokio async runtime for efficient I/O
4. **Optimization** - LLVM backend with LTO and profile-guided optimization

**Typical Performance Gain:** 30-50% faster than equivalent Go+CGO implementation

## Design Decisions

### Why Rust?
- Memory safety without garbage collection
- Zero-cost abstractions for GPU FFI
- Excellent async/await support (Tokio)
- Strong type system prevents common bugs

### Why Dual Backends?
- CUDA only works on NVIDIA GPUs (Linux/Windows)
- Metal provides native GPU access on macOS Apple Silicon
- Backend trait abstracts the difference — mining logic is shared
- Feature flags keep binaries lean (only one backend compiled per build)

### Why Separate from Go Codebase?
- Different toolchain (cargo vs go build)
- GPU shaders require local compilation (no cross-compile)
- Optional component (not all users have supported GPUs)
- Independent release cycle

### Difficulty Adjustment Support
- Compatible with legacy difficulty adjustment (pre-fork)
- Compatible with DAA (Difficulty Adjustment Algorithm) fork
- Compatible with DAAv2 (difficulty-adjusted LWMA)
- Fetches difficulty dynamically from node /status endpoint

## Implementation Notes

### MiningParams Struct (256 bytes)

The `MiningParams` struct is shared between Rust (`#[repr(C)]`) and the GPU shader. It must have identical layout on both sides:

```
midstate[8]           32 bytes   SHA-256 midstate after full blocks
tail_w[16]            64 bytes   Remaining prefix bytes as big-endian uint32 words
tail_len               4 bytes   Length of tail in bytes
base_nonce_len         4 bytes   Length of base nonce string
total_prefix_len       8 bytes   Total prefix length for bit-length calculation
suffix[8]              8 bytes   Difficulty suffix bytes
suffix_len             4 bytes   Length of suffix
nonce_start            8 bytes   Starting nonce for this batch
difficulty_bits        4 bytes   Number of leading zero bits required
num_precomputed_rounds 4 bytes   How many SHA-256 rounds are precomputed
batch_size             8 bytes   Number of nonces in this batch
base_nonce_str[20]    20 bytes   ASCII decimal nonce string (base)
partial_state[8]      32 bytes   SHA-256 state after precomputed rounds
precomputed_w[12]     48 bytes   Precomputed message schedule words
Total: 256 bytes (with padding)
```

### Hash Counting
- `total_hashes`: Updated when mining completes (solution found or stale work)
- `current_hashrate`: Updated every 3 seconds during mining for real-time dashboard

### Stale Work Detection
- Checks every 3 seconds if blockchain height advanced
- Immediately aborts current work if chain tip changed
- Prevents wasted GPU cycles on outdated blocks

### Timestamp Rotation
- When nonce space is exhausted without finding a solution, the block timestamp is rotated
- This generates a new prefix/midstate, providing a fresh nonce space
- No network round-trip needed — purely local operation

### Web Dashboard
- Real-time hashrate (updated every 3 seconds)
- 5-minute rolling average for stable metrics
- Blocks mined and earnings tracking
- Session uptime and GPU statistics

## Build Process

### Metal (macOS)
- Metal shader (`sha256_mining.metal`) is included at compile time via `include_str!`
- Compiled to a Metal library at runtime on first launch
- No external build tools needed beyond Xcode Command Line Tools

### CUDA (Linux/Windows)
The `build.rs` script:
1. Detects CUDA installation path
2. Compiles bridge.cu with nvcc
3. Creates static library (`libgpuminer.a`)
4. Links with Rust code

On Linux, nvcc is invoked directly with `gcc` as the host compiler to avoid C++ header compatibility issues.
