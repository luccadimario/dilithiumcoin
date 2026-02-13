#!/bin/bash

# Dilithium Build Script
# Builds binaries for multiple platforms

VERSION="3.3.0"
PROJECT_NAME="dilithium"

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
# BUILD GPU MINER (CPU-only cross-compile — GPU users build locally with make gpu)
# ============================================================================
echo "Building GPU Miner..."
for platform in "${platforms[@]}"
do
    IFS='/' read -r GOOS GOARCH <<< "$platform"

    output_name="dilithium-gpu-miner-${GOOS}-${GOARCH}"

    if [ "$GOOS" = "windows" ]; then
        output_name="${output_name}.exe"
    fi

    echo "  Building for $GOOS/$GOARCH..."

    (cd cmd/dilithium-gpu-miner && env CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "-X main.AppVersion=${VERSION}" \
        -o ../../dist/$output_name \
        .)

    if [ $? -ne 0 ]; then
        echo "Error building GPU Miner for $platform"
        exit 1
    fi
done

echo "GPU Miner build complete!"
echo ""

# ============================================================================
# SUMMARY
# ============================================================================
echo "Build complete! Binaries in dist/ directory:"
echo ""
ls -lh dist/ | tail -n +2 | awk '{print "  " $9 " (" $5 ")"}'
echo ""
echo "Usage:"
echo "  Node:   ./dist/dilithium-darwin-amd64 --port 5001 --api-port 8001"
echo "  CLI:    ./dist/dilithium-cli-darwin-amd64 wallet create"
echo "  Miner:  ./dist/dilithium-miner-darwin-amd64 --node http://localhost:8001 --miner <address>"
