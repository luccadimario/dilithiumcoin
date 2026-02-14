#!/bin/bash

# Dilithium GPU Miner Installation Script
# Installs the Rust+GPU miner with all dependencies
# Supports CUDA (Linux/Windows) and Metal (macOS)

set -e

VERSION="1.0.0"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
SYSTEMD_DIR="/etc/systemd/system"

echo "======================================================="
echo "   DILITHIUM GPU MINER INSTALLER"
echo "   Version ${VERSION}"
echo "======================================================="
echo ""

# Detect OS
OS="unknown"
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    OS="linux"
elif [[ "$OSTYPE" == "darwin"* ]]; then
    OS="macos"
elif [[ "$OSTYPE" == "msys" || "$OSTYPE" == "win32" ]]; then
    OS="windows"
fi

echo "[*] Detected OS: $OS"
echo ""

# Determine GPU backend
GPU_BACKEND=""
CARGO_FEATURES=""

if [[ "$OS" == "macos" ]]; then
    echo "[*] macOS detected - using Metal backend"
    GPU_BACKEND="metal"
    CARGO_FEATURES="--features metal"
else
    # Check for CUDA
    echo "[*] Checking for CUDA..."
    if command -v nvcc &> /dev/null; then
        CUDA_VERSION=$(nvcc --version | grep "release" | sed -n 's/.*release \([0-9.]*\).*/\1/p')
        echo "    ✓ CUDA ${CUDA_VERSION} found"
        GPU_BACKEND="cuda"
        CARGO_FEATURES="--features cuda"
    else
        echo "    ✗ CUDA not found!"
        echo ""
        echo "Please install CUDA Toolkit from:"
        echo "  https://developer.nvidia.com/cuda-downloads"
        echo ""
        read -p "Continue anyway? (y/N) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 1
        fi
    fi
fi

echo "[*] GPU backend: ${GPU_BACKEND:-none}"
echo ""

# Check for Rust
echo "[*] Checking for Rust..."
if command -v cargo &> /dev/null; then
    RUST_VERSION=$(cargo --version | awk '{print $2}')
    echo "    ✓ Rust ${RUST_VERSION} found"
else
    echo "    ✗ Rust not found!"
    echo ""
    echo "Installing Rust..."
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
    source "$HOME/.cargo/env"
    echo "    ✓ Rust installed"
fi
echo ""

# Check for C++ compiler (only needed for CUDA)
if [[ "$GPU_BACKEND" == "cuda" ]]; then
    echo "[*] Checking for C++ compiler..."
    if command -v g++ &> /dev/null || command -v clang++ &> /dev/null; then
        echo "    ✓ C++ compiler found"
    else
        echo "    ✗ C++ compiler not found!"
        if [[ "$OS" == "linux" ]]; then
            echo "Installing build-essential..."
            sudo apt-get update
            sudo apt-get install -y build-essential
        else
            echo "Please install a C++ compiler (GCC or Clang)"
            exit 1
        fi
    fi
    echo ""
fi

# Build the miner
echo "[*] Building GPU miner (${GPU_BACKEND:-no GPU} backend)..."
cargo build --release $CARGO_FEATURES

if [ $? -ne 0 ]; then
    echo "    ✗ Build failed!"
    exit 1
fi
echo "    ✓ Build successful"
echo ""

# Install binary
echo "[*] Installing binary to ${INSTALL_DIR}..."
if [[ "$OS" == "windows" ]]; then
    BINARY_NAME="dilithium-gpu-miner.exe"
else
    BINARY_NAME="dilithium-gpu-miner"
fi

if [ -w "$INSTALL_DIR" ]; then
    cp "target/release/$BINARY_NAME" "$INSTALL_DIR/"
else
    sudo cp "target/release/$BINARY_NAME" "$INSTALL_DIR/"
fi
echo "    ✓ Binary installed"
echo ""

# Install systemd service (Linux only)
if [[ "$OS" == "linux" ]]; then
    read -p "Install systemd service? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        read -p "Enter wallet address: " WALLET_ADDRESS
        read -p "Enter node URL (default: http://localhost:8080): " NODE_URL
        NODE_URL=${NODE_URL:-http://localhost:8080}

        cat > /tmp/dilithium-gpu-miner.service <<EOF
[Unit]
Description=Dilithium GPU Miner
After=network.target

[Service]
Type=simple
User=$USER
ExecStart=$INSTALL_DIR/dilithium-gpu-miner --address $WALLET_ADDRESS --node $NODE_URL
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

        sudo mv /tmp/dilithium-gpu-miner.service "$SYSTEMD_DIR/"
        sudo systemctl daemon-reload
        sudo systemctl enable dilithium-gpu-miner
        echo "    ✓ Systemd service installed"
        echo ""
        echo "To start the miner:"
        echo "  sudo systemctl start dilithium-gpu-miner"
        echo ""
        echo "To check status:"
        echo "  sudo systemctl status dilithium-gpu-miner"
        echo ""
        echo "To view logs:"
        echo "  journalctl -u dilithium-gpu-miner -f"
    fi
fi

echo ""
echo "======================================================="
echo "   INSTALLATION COMPLETE!"
echo "   Backend: ${GPU_BACKEND:-none}"
echo "======================================================="
echo ""
echo "Usage:"
echo "  dilithium-gpu-miner --address YOUR_WALLET --node http://localhost:8080"
echo ""
echo "Web dashboard will be available at:"
echo "  http://127.0.0.1:8080"
echo ""
echo "For more options:"
echo "  dilithium-gpu-miner --help"
echo ""
