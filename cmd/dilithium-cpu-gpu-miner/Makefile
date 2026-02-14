# Dilithium GPU Miner - Build Configuration
# ==========================================
#
# This directory is self-contained — copy it anywhere and build:
#   make gpu SM=86
#
# Or build CPU-only:
#   make

GO       ?= go
NVCC     ?= nvcc
BINARY   = dilithium-gpu-miner
CUDA_LIB = cuda/libgpuminer.a

# Go build flags
GOFLAGS  = -ldflags="-s -w"

# CUDA flags - adjust SM for your GPU:
#   sm_75  = RTX 20xx (Turing)
#   sm_80  = RTX 30xx (Ampere) / A100
#   sm_86  = RTX 30xx (Ampere laptop)
#   sm_89  = RTX 40xx (Ada Lovelace)
#   sm_90  = H100 (Hopper)
SM ?= 75
CUDA_ARCH = sm_$(SM)
NVCCFLAGS = -O3 -arch=$(CUDA_ARCH) --use_fast_math -Xcompiler -fPIC

.PHONY: all cpu gpu clean test benchmark

# Default: build CPU-only miner
all: cpu

# CPU-only miner (works everywhere, no CUDA needed)
cpu:
	$(GO) build $(GOFLAGS) -o $(BINARY) .
	@echo ""
	@echo "Built: $(BINARY) (CPU-only)"
	@echo "Usage: ./$(BINARY) --address YOUR_ADDRESS"
	@echo "       ./$(BINARY) --address YOUR_ADDRESS --node http://localhost:8080"
	@echo "       ./$(BINARY) --pool pool.example.com:3333"

# GPU-enabled miner (requires NVIDIA GPU + CUDA toolkit)
gpu: $(CUDA_LIB)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -tags cuda -o $(BINARY) .
	@echo ""
	@echo "Built: $(BINARY) (with CUDA SM=$(SM))"
	@echo "Usage: ./$(BINARY) --gpu --address YOUR_ADDRESS"
	@echo "       ./$(BINARY) --gpu --device 0 --batch-size 67108864"
	@echo "       ./$(BINARY) --gpu --pool pool.example.com:3333"

# Compile CUDA bridge to static library
$(CUDA_LIB): cuda/bridge.cu
	@mkdir -p cuda
	$(NVCC) $(NVCCFLAGS) -c cuda/bridge.cu -o cuda/bridge.o
	ar rcs $(CUDA_LIB) cuda/bridge.o
	@echo "Built CUDA library: $(CUDA_LIB)"

# Build both CPU and GPU versions
both: cpu gpu

# Run tests
test:
	$(GO) test -v -count=1 ./...

# Run benchmarks
benchmark:
	$(GO) test -bench=. -benchmem -count=1 ./...

# Run the built-in hashrate benchmark
bench: cpu
	./$(BINARY) --benchmark

clean:
	rm -f $(BINARY) $(CUDA_LIB) cuda/*.o cuda/*.a

# Cross-compilation targets
linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY)-linux-arm64 .

windows-amd64:
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY)-windows-amd64.exe .
