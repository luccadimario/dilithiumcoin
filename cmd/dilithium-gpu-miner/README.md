# Dilithium GPU Miner

GPU-accelerated SHA-256 proof-of-work miner for the Dilithium blockchain. Uses CUDA for NVIDIA GPUs with a CPU fallback that works everywhere.

## Quick Start

### Option 1: Download pre-built binary (CPU mode)

Download `dilithium-gpu-miner` from the [releases page](https://github.com/luccadimario/dilithiumcoin/releases), then:

```bash
chmod +x dilithium-gpu-miner
./dilithium-gpu-miner --address YOUR_DLT_ADDRESS
```

The miner starts an embedded node automatically. Use `--peer` to connect to the network:

```bash
./dilithium-gpu-miner --address YOUR_ADDRESS --peer seed.dilithium.network:5000
```

### Option 2: Build with CUDA (GPU mode)

Requires NVIDIA GPU + [CUDA Toolkit](https://developer.nvidia.com/cuda-toolkit).

```bash
cd cmd/dilithium-gpu-miner
make gpu SM=86          # Set SM for your GPU (see table below)
./dilithium-gpu-miner --gpu --address YOUR_ADDRESS
```

## CUDA SM Architecture Table

| SM Value | GPU Generation | Example GPUs |
|----------|---------------|--------------|
| `75` | Turing | RTX 2060, 2070, 2080 |
| `80` | Ampere | A100, RTX 3090 (desktop) |
| `86` | Ampere | RTX 3060, 3070, 3080 (laptop) |
| `89` | Ada Lovelace | RTX 4060, 4070, 4080, 4090 |
| `90` | Hopper | H100, H200 |

Find your GPU's SM version: `nvidia-smi --query-gpu=compute_cap --format=csv`

## Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--address` | auto-detect | Mining reward address |
| `--wallet` | `~/.dilithium/wallet` | Wallet directory for auto-detection |
| `--node` | (embedded) | Node API URL (e.g., `http://localhost:8080`) |
| `--no-node` | false | Disable embedded node (requires `--node`) |
| `--peer` | | Seed peer for embedded node |
| `--threads` | all CPUs | CPU mining thread count |
| `--gpu` | false | Enable GPU mining |
| `--device` | 0 | GPU device ID |
| `--batch-size` | 67108864 | Nonces per GPU kernel launch |
| `--pool` | | Pool address for pool mining (host:port) |
| `--benchmark` | false | Run hashrate benchmark and exit |
| `--version` | false | Show version |

## Performance

Typical hashrates (SHA-256 with midstate optimization):

| Hardware | Mode | Hashrate |
|----------|------|----------|
| Apple M4 (1 thread) | CPU | ~20 MH/s |
| Apple M4 (10 threads) | CPU | ~180 MH/s |
| Intel i7 (8 threads) | CPU | ~80 MH/s |
| RTX 3080 | GPU | ~2,000 MH/s |
| RTX 4090 | GPU | ~5,000 MH/s |

Run `./dilithium-gpu-miner --benchmark` to measure your hardware.

## Pool Mining

```bash
# CPU pool mining
./dilithium-gpu-miner --pool pool.example.com:3333 --address YOUR_ADDRESS

# GPU pool mining
./dilithium-gpu-miner --pool pool.example.com:3333 --address YOUR_ADDRESS --gpu
```

## Building from Source

### CPU-only (from repo root)

```bash
go build ./cmd/dilithium-gpu-miner
```

### With CUDA support

```bash
cd cmd/dilithium-gpu-miner
make gpu SM=86    # adjust SM for your GPU
```

Or using the build script:

```bash
cd cmd/dilithium-gpu-miner
./build.sh --gpu --sm 86
```

## Troubleshooting

**"GPU mining not available"** -- Binary was built without CUDA. Rebuild with `make gpu`.

**"failed to initialize CUDA device"** -- Check that NVIDIA drivers are installed (`nvidia-smi`) and the CUDA toolkit matches your driver version.

**"CUDA error: no kernel image is available"** -- SM architecture mismatch. Rebuild with the correct `SM=` value for your GPU.

**Low GPU hashrate** -- Try increasing `--batch-size` (e.g., `--batch-size 134217728`). Larger batches improve GPU utilization.
