#!/bin/bash

# Dilithium Build Script
# Builds binaries for multiple platforms

VERSION="3.4.0"
PROJECT_NAME="dilithium"

# Parse flags
BUILD_GPU_MINER=false
for arg in "$@"; do
    case $arg in
        --gpu-miner)
            BUILD_GPU_MINER=true
            shift
            ;;
        --help)
            echo "Usage: ./build.sh [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --gpu-miner    Build Rust+CUDA GPU miner (requires Rust and CUDA Toolkit)"
            echo "  --help         Show this help message"
            echo ""
            exit 0
            ;;
    esac
done

# Create dist directory
mkdir -p dist

echo "Building Dilithium v${VERSION}..."
echo ""

# Define platforms
platforms=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

# ============================================================================
# BUILD NODE
# ============================================================================
echo "Building Node..."
for platform in "${platforms[@]}"
do
    IFS='/' read -r GOOS GOARCH <<< "$platform"
    
    output_name="dilithium-${GOOS}-${GOARCH}"
    
    if [ "$GOOS" = "windows" ]; then
        output_name="${output_name}.exe"
    fi
    
    echo "  Building for $GOOS/$GOARCH..."
    
    env GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "-X main.AppVersion=${VERSION}" \
        -o dist/$output_name \
        .
    
    if [ $? -ne 0 ]; then
        echo "Error building for $platform"
        exit 1
    fi
done

echo "Node build complete!"
echo ""

# ============================================================================
# BUILD CLI
# ============================================================================
echo "Building CLI Tool..."
for platform in "${platforms[@]}"
do
    IFS='/' read -r GOOS GOARCH <<< "$platform"
    
    output_name="dilithium-cli-${GOOS}-${GOARCH}"
    
    if [ "$GOOS" = "windows" ]; then
        output_name="${output_name}.exe"
    fi
    
    echo "  Building for $GOOS/$GOARCH..."
    
    env GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "-X main.AppVersion=${VERSION}" \
        -o dist/$output_name \
        ./cmd/dilithium-cli
    
    if [ $? -ne 0 ]; then
        echo "Error building CLI for $platform"
        exit 1
    fi
done

echo "CLI build complete!"
echo ""

# ============================================================================
# BUILD MINER
# ============================================================================
echo "Building Miner..."
for platform in "${platforms[@]}"
do
    IFS='/' read -r GOOS GOARCH <<< "$platform"

    output_name="dilithium-miner-${GOOS}-${GOARCH}"

    if [ "$GOOS" = "windows" ]; then
        output_name="${output_name}.exe"
    fi

    echo "  Building for $GOOS/$GOARCH..."

    env GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "-X main.AppVersion=${VERSION}" \
        -o dist/$output_name \
        ./cmd/dilithium-miner

    if [ $? -ne 0 ]; then
        echo "Error building Miner for $platform"
        exit 1
    fi
done

echo "Miner build complete!"
echo ""

# ============================================================================
# BUILD CPU/GPU MINER (CPU-only cross-compile — GPU users build locally with make gpu)
# ============================================================================
echo "Building CPU/GPU Miner..."
for platform in "${platforms[@]}"
do
    IFS='/' read -r GOOS GOARCH <<< "$platform"

    output_name="dilithium-cpu-gpu-miner-${GOOS}-${GOARCH}"

    if [ "$GOOS" = "windows" ]; then
        output_name="${output_name}.exe"
    fi

    echo "  Building for $GOOS/$GOARCH..."

    (cd cmd/dilithium-cpu-gpu-miner && env CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "-X main.AppVersion=${VERSION}" \
        -o ../../dist/$output_name \
        .)

    if [ $? -ne 0 ]; then
        echo "Error building CPU/GPU Miner for $platform"
        exit 1
    fi
done

echo "CPU/GPU Miner build complete!"
echo ""

# ============================================================================
# BUILD RUST+CUDA GPU MINER (Optional - requires --gpu-miner flag)
# ============================================================================
if [ "$BUILD_GPU_MINER" = true ]; then
    echo "Building Rust GPU Miner..."

    if [ -d "cmd/dilithium-gpu-miner" ]; then
        # Check for Rust
        if command -v cargo &> /dev/null; then
            # Detect GPU backend based on platform
            GPU_FEATURES=""
            GPU_BACKEND_NAME=""

            if [[ "$OSTYPE" == "darwin"* ]]; then
                GPU_FEATURES="--features metal"
                GPU_BACKEND_NAME="metal"
                echo "  macOS detected, using Metal backend"
            elif command -v nvcc &> /dev/null; then
                GPU_FEATURES="--features cuda"
                GPU_BACKEND_NAME="cuda"
                echo "  Found Rust and CUDA, building..."
            else
                echo "  ✗ No GPU backend available"
                echo "    macOS: Metal is used automatically"
                echo "    Linux/Windows: Install CUDA Toolkit"
                exit 1
            fi

            (cd cmd/dilithium-gpu-miner && cargo build --release $GPU_FEATURES 2>&1 | grep -E "(Compiling|Finished|error)" || true)

            if [ $? -eq 0 ] && [ -f "cmd/dilithium-gpu-miner/target/release/dilithium-gpu-miner" ]; then
                # Copy binary to dist
                mkdir -p dist

                # Determine platform-specific name
                if [[ "$OSTYPE" == "linux-gnu"* ]]; then
                    cp cmd/dilithium-gpu-miner/target/release/dilithium-gpu-miner dist/dilithium-gpu-miner-${GPU_BACKEND_NAME}-linux-amd64
                elif [[ "$OSTYPE" == "darwin"* ]]; then
                    cp cmd/dilithium-gpu-miner/target/release/dilithium-gpu-miner dist/dilithium-gpu-miner-${GPU_BACKEND_NAME}-darwin-arm64
                elif [[ "$OSTYPE" == "msys" || "$OSTYPE" == "win32" ]]; then
                    cp cmd/dilithium-gpu-miner/target/release/dilithium-gpu-miner.exe dist/dilithium-gpu-miner-${GPU_BACKEND_NAME}-windows-amd64.exe
                fi

                echo "  ✓ Rust+${GPU_BACKEND_NAME} GPU Miner build complete!"
            else
                echo "  ✗ Rust GPU Miner build failed"
                exit 1
            fi
        else
            echo "  ✗ Rust not found - install from https://rustup.rs"
            exit 1
        fi
    else
        echo "  ✗ cmd/dilithium-gpu-miner directory not found"
        exit 1
    fi

    echo ""
fi

# ============================================================================
# SUMMARY
# ============================================================================
echo "Build complete! Binaries in dist/ directory:"
echo ""
ls -lh dist/ | tail -n +2 | awk '{print "  " $9 " (" $5 ")"}'
echo ""
echo "Usage:"
echo "  Node:      ./dist/dilithium-darwin-amd64 --port 5001 --api-port 8001"
echo "  CLI:       ./dist/dilithium-cli-darwin-amd64 wallet create"
echo "  Miner:     ./dist/dilithium-miner-darwin-amd64 --node http://localhost:8001 --miner <address>"
echo ""
echo "To build Rust GPU miner:"
echo "  ./build.sh --gpu-miner"
echo "  (requires Rust toolchain; CUDA on Linux, Metal on macOS)"
