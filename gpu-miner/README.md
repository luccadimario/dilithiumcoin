# Dilithium GPU Miner

A high-performance CUDA-accelerated miner for the Dilithium cryptocurrency network, built with Rust and CUDA.

## Features

- **High Performance**: 2000+ MH/s on NVIDIA RTX 3060 Ti
- **CUDA Optimization**: Custom optimized SHA-256 kernel with midstate precomputation
- **Real-time Monitoring**: Built-in web dashboard for stats and monitoring
- **Dynamic Difficulty**: Automatic adaptation to network difficulty changes
- **Safe & Reliable**: Memory-safe Rust implementation with proper error handling

## Requirements

### Hardware
- NVIDIA GPU with CUDA support (Compute Capability 3.5+)
- Recommended: RTX 3060 Ti or better

### Software
- CUDA Toolkit 11.0 or later
- Rust toolchain (1.70+)
- C++ compiler (MSVC on Windows, GCC on Linux)

## Installation

### Windows

1. Install [CUDA Toolkit](https://developer.nvidia.com/cuda-downloads)
2. Install [Rust](https://rustup.rs/)
3. Install Visual Studio Build Tools with C++ support

```bash
git clone https://github.com/yourusername/dilithium-gpu-miner.git
cd dilithium-gpu-miner
cargo build --release
```

### Linux

1. Install CUDA Toolkit:
```bash
# Ubuntu/Debian
sudo apt-get install nvidia-cuda-toolkit

# Or download from NVIDIA website for latest version
```

2. Install Rust:
```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
```

3. Build:
```bash
git clone https://github.com/yourusername/dilithium-gpu-miner.git
cd dilithium-gpu-miner
cargo build --release
```

## Usage

```bash
dilithium-gpu-miner --address YOUR_WALLET_ADDRESS --node http://localhost:8080
```

### Command-Line Options

- `--address, -a`: Your wallet address (required)
- `--node, -n`: Node URL (default: http://localhost:8080)
- `--device, -d`: GPU device ID (default: 0)
- `--batch-size, -b`: Nonces per kernel launch (default: 67108864)
- `--webui-port, -w`: Web dashboard port (default: 8080)

### Examples

**Mine to local node:**
```bash
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS
```

**Mine to remote node:**
```bash
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -n http://node.example.com:8080
```

**Use specific GPU and custom batch size:**
```bash
dilithium-gpu-miner -a YOUR_WALLET_ADDRESS -d 1 -b 134217728
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
- **Higher values** (134217728): Better for high difficulty, but slower to detect stale work
- **Lower values** (33554432): Better for low difficulty, faster stale detection
- **Default** (67108864): Good balance for most scenarios

### Multiple GPUs

To mine with multiple GPUs, run multiple instances with different `--device` values:
```bash
dilithium-gpu-miner -a YOUR_WALLET -d 0 -w 8080
dilithium-gpu-miner -a YOUR_WALLET -d 1 -w 8081
```

## Troubleshooting

### "CUDA error" on startup
- Verify CUDA is installed: `nvcc --version`
- Check GPU is detected: `nvidia-smi`
- Ensure your GPU supports CUDA Compute Capability 3.5+

### Low hashrate
- Check GPU isn't throttling due to temperature
- Try adjusting batch size
- Ensure no other GPU-intensive applications are running

### "Failed to connect to node"
- Verify node URL is correct
- Check node is running and accessible
- Ensure firewall isn't blocking the connection

## Architecture

- **Rust**: High-level mining logic, networking, and coordination
- **CUDA**: GPU-accelerated SHA-256 hashing kernel
- **Tokio**: Async runtime for efficient I/O
- **Axum**: Web server for monitoring dashboard

## Development

### Build in debug mode:
```bash
cargo build
```

### Run with logging:
```bash
RUST_LOG=debug cargo run -- -a YOUR_WALLET
```

### Run tests:
```bash
cargo test
```

## License

MIT License - See LICENSE file for details

## Contributing

Contributions are welcome! Please submit pull requests or open issues on GitHub.

## Acknowledgments

- Built for the [Dilithium](https://github.com/luccadimario/dilithiumcoin) cryptocurrency
- Optimized CUDA kernels inspired by Bitcoin mining best practices
