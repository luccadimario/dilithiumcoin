# Dilithium GPU Miner (Rust)

A high-performance GPU-accelerated miner for the Dilithium cryptocurrency network, built in Rust with support for **NVIDIA CUDA** and **Apple Metal** backends.

## Features

- **Dual GPU Backend**: CUDA (Linux/Windows) and Metal (macOS Apple Silicon)
- **High Performance**: ~800 MH/s on Apple M4, 2000+ MH/s on RTX 3060 Ti
- **Optimized SHA-256 Kernel**: Midstate precomputation, unrolled rounds, CPU-side precomputation
- **Real-time Monitoring**: Built-in web dashboard for stats and monitoring
- **Dynamic Difficulty**: Automatic adaptation to network difficulty changes
- **Safe & Reliable**: Memory-safe Rust implementation with proper error handling

## Requirements

### macOS (Metal backend)

- Apple Silicon Mac (M1/M2/M3/M4 or later)
- macOS 13.0 (Ventura) or later
- Rust toolchain (1.70+)
- Xcode Command Line Tools (`xcode-select --install`)

### Linux / Windows (CUDA backend)

- NVIDIA GPU with CUDA support (Compute Capability 3.5+)
- CUDA Toolkit 11.0 or later
- Rust toolchain (1.70+)
- C++ compiler (GCC on Linux, MSVC on Windows)

## Quick Start

### macOS (Apple Silicon)

```bash
git clone https://github.com/luccadimario/dilithiumcoin.git
cd dilithiumcoin/cmd/dilithium-gpu-miner
cargo build --release --features metal
./target/release/dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -n http://NODE_URL:8080
```

### Linux (NVIDIA CUDA)

```bash
# Install CUDA Toolkit (Ubuntu/Debian)
sudo apt-get install nvidia-cuda-toolkit

# Install Rust
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh

git clone https://github.com/luccadimario/dilithiumcoin.git
cd dilithiumcoin/cmd/dilithium-gpu-miner
cargo build --release --features cuda
./target/release/dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -n http://NODE_URL:8080
```

### Windows (NVIDIA CUDA)

1. Install [CUDA Toolkit](https://developer.nvidia.com/cuda-downloads)
2. Install [Rust](https://rustup.rs/)
3. Install [Visual Studio Build Tools](https://visualstudio.microsoft.com/visual-cpp-build-tools/) with C++ support

```powershell
git clone https://github.com/luccadimario/dilithiumcoin.git
cd dilithiumcoin\cmd\dilithium-gpu-miner
cargo build --release --features cuda
.\target\release\dilithium-gpu-miner.exe -a YOUR_WALLET_ADDRESS -n http://NODE_URL:8080
```

### One-Line Install (Auto-detects backend)

```bash
cd dilithiumcoin/cmd/dilithium-gpu-miner
./install.sh
```

The install script detects your platform, selects the correct backend (Metal on macOS, CUDA on Linux), builds, and installs to `/usr/local/bin`.

## Usage

```bash
dilithium-gpu-miner --address YOUR_WALLET_ADDRESS --node http://localhost:8080
```

### Command-Line Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--address` | `-a` | *(required)* | Your wallet address |
| `--node` | `-n` | `http://localhost:8080` | Node URL |
| `--device` | `-d` | `0` | GPU device ID |
| `--batch-size` | `-b` | `67108864` | Nonces per kernel launch |
| `--webui-port` | `-w` | `8080` | Web dashboard port |

### Examples

```bash
# Mine to local node
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS

# Mine to remote node
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -n http://node.example.com:8080

# Use specific GPU and custom batch size
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -d 1 -b 134217728

# Custom web dashboard port
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -w 9090
```

## Web Dashboard

Access the web dashboard at `http://127.0.0.1:8080` (or your custom port) to view:
- Real-time hashrate
- Blocks mined
- Total earnings
- GPU statistics
- Session uptime

## Performance Tuning

### Batch Size

The `--batch-size` parameter controls how many nonces are processed per GPU kernel launch:
- **Higher values** (134217728): Better throughput for high difficulty
- **Lower values** (33554432): Faster stale work detection
- **Default** (67108864): Good balance for most scenarios

### Reference Hashrates

| GPU | Backend | Hashrate |
|-----|---------|----------|
| Apple M4 (10-core GPU) | Metal | ~800 MH/s |
| Apple M4 Pro (16-core GPU) | Metal | ~1,280 MH/s (est.) |
| Apple M4 Max (40-core GPU) | Metal | ~3,200 MH/s (est.) |
| NVIDIA RTX 3060 Ti | CUDA | ~2,000 MH/s |
| NVIDIA RTX 3080 | CUDA | ~3,200 MH/s |
| NVIDIA RTX 4090 | CUDA | ~5,000+ MH/s |

*Actual performance varies by difficulty, batch size, thermal conditions, and system configuration.*

### Multiple GPUs

Run multiple instances with different `--device` and `--webui-port` values:
```bash
dilithium-gpu-miner -a YOUR_WALLET -d 0 -w 8080 &
dilithium-gpu-miner -a YOUR_WALLET -d 1 -w 8081 &
```

## Building from Source

### Feature Flags

| Feature | Platform | Description |
|---------|----------|-------------|
| `metal` | macOS | Apple Metal GPU backend |
| `cuda` | Linux/Windows | NVIDIA CUDA GPU backend |

You must specify exactly one feature:
```bash
# macOS
cargo build --release --features metal

# Linux/Windows
cargo build --release --features cuda
```

### Build from Root

The top-level `build.sh` can also build the GPU miner:
```bash
# From repository root
./build.sh --gpu-miner
```
This auto-detects your platform and selects the correct backend.

### CUDA SM Architecture

If you need a specific SM architecture (default is `sm_86`), set the `CUDA_SM` environment variable:
```bash
CUDA_SM=sm_89 cargo build --release --features cuda
```

## Troubleshooting

### macOS / Metal

**"No Metal device found"**
- Ensure you're on Apple Silicon (M1 or later). Intel Macs are not supported.

**Low hashrate**
- Check Activity Monitor > GPU to ensure no other GPU-intensive apps are running
- Thermal throttling reduces sustained hashrate by ~5-10%

### Linux/Windows / CUDA

**"CUDA error" on startup**
- Verify CUDA is installed: `nvcc --version`
- Check GPU is detected: `nvidia-smi`
- Ensure your GPU supports CUDA Compute Capability 3.5+

**"Failed to run nvcc"**
- CUDA Toolkit must be installed and `nvcc` must be in your PATH
- On Linux, ensure `gcc` is available (not just `g++`)

### General

**"Failed to connect to node"**
- Verify node URL is correct and the node is running
- Check firewall isn't blocking the connection

**Low hashrate**
- Check GPU isn't throttling due to temperature
- Try adjusting batch size
- Ensure no other GPU-intensive applications are running

## Architecture

- **Rust**: Mining coordinator, networking, web dashboard
- **Metal Shader** (macOS): Highly optimized SHA-256 compute kernel with CPU precomputation
- **CUDA Kernel** (Linux/Windows): GPU-accelerated SHA-256 hashing
- **Tokio**: Async runtime for efficient I/O
- **Axum**: Web server for monitoring dashboard

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed technical documentation.

## Development

```bash
# Debug build (Metal)
cargo build --features metal

# Debug build (CUDA)
cargo build --features cuda

# Run with verbose logging
RUST_LOG=debug cargo run --features metal -- -a YOUR_WALLET

# Run tests
cargo test
```

## License

MIT License - See LICENSE file for details.
