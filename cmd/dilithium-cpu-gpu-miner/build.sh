#!/bin/bash
# Build script for Dilithium GPU Miner
#
# Usage (from cmd/dilithium-gpu-miner/):
#   ./build.sh              # CPU-only build
#   ./build.sh --gpu        # GPU build (SM 75)
#   ./build.sh --gpu --sm 89  # GPU build for RTX 40xx

set -e

BINARY="dilithium-gpu-miner"
CUDA_LIB="cuda/libgpuminer.a"

# Detect if CUDA is available
CUDA_AVAILABLE=0
if command -v nvcc &> /dev/null; then
    CUDA_AVAILABLE=1
    echo "CUDA toolkit detected"
    nvcc --version | head -1
fi

# Parse arguments
BUILD_MODE="cpu"
SM="75"

while [[ $# -gt 0 ]]; do
    case $1 in
        --gpu)
            BUILD_MODE="gpu"
            shift
            ;;
        --sm)
            SM="$2"
            shift 2
            ;;
        --clean)
            echo "Cleaning build artifacts..."
            rm -f "$BINARY" "$CUDA_LIB" cuda/*.o cuda/*.a
            echo "Clean complete"
            exit 0
            ;;
        --help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --gpu          Build with GPU support (requires CUDA)"
            echo "  --sm <arch>    GPU architecture (default: 75)"
            echo "                   75  = RTX 20xx"
            echo "                   80  = RTX 30xx desktop / A100"
            echo "                   86  = RTX 30xx laptop"
            echo "                   89  = RTX 40xx"
            echo "                   90  = H100"
            echo "  --clean        Clean build artifacts"
            echo "  --help         Show this help"
            echo ""
            echo "Examples:"
            echo "  $0                    # CPU-only build"
            echo "  $0 --gpu              # GPU build (SM 75)"
            echo "  $0 --gpu --sm 89      # GPU build for RTX 40xx"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Run '$0 --help' for usage"
            exit 1
            ;;
    esac
done

if [ "$BUILD_MODE" = "gpu" ]; then
    if [ "$CUDA_AVAILABLE" -eq 0 ]; then
        echo "ERROR: GPU build requested but CUDA toolkit not found"
        echo ""
        echo "Please install NVIDIA CUDA Toolkit:"
        echo "  Ubuntu/Debian: sudo apt install nvidia-cuda-toolkit"
        echo "  Arch: sudo pacman -S cuda"
        echo "  macOS: CUDA is not supported on Apple Silicon"
        echo ""
        exit 1
    fi

    echo "Building CUDA library (SM $SM)..."
    mkdir -p cuda
    nvcc -O3 -arch=sm_$SM --use_fast_math -Xcompiler -fPIC -c cuda/bridge.cu -o cuda/bridge.o
    ar rcs "$CUDA_LIB" cuda/bridge.o
    echo "CUDA library built"

    echo "Building GPU-enabled miner..."
    CGO_ENABLED=1 go build -tags cuda -ldflags="-s -w" -o "$BINARY" .
    echo ""
    echo "Built: $BINARY (with CUDA SM $SM)"
    echo ""
    echo "Usage:"
    echo "  ./$BINARY --gpu --address YOUR_ADDRESS"
    echo "  ./$BINARY --gpu --pool pool.example.com:3333"
else
    echo "Building CPU-only miner..."
    go build -ldflags="-s -w" -o "$BINARY" .
    echo ""
    echo "Built: $BINARY (CPU-only)"
    echo ""
    echo "Usage:"
    echo "  ./$BINARY --address YOUR_ADDRESS"
    echo "  ./$BINARY --pool pool.example.com:3333"
    echo ""
    echo "To build with GPU support:"
    echo "  $0 --gpu"
fi
