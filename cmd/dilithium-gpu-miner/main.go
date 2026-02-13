package main

import (
	"crypto/sha256"
	"encoding"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var AppVersion = "dev"

const banner = `
    ____  _ ___ __  __    _
   / __ \(_) (_) /_/ /_  (_)_  ______ ___
  / / / / / / / __/ __ \/ / / / / __ '__ \
 / /_/ / / / / /_/ / / / / /_/ / / / / / /
/_____/_/_/_/\__/_/ /_/_/\__,_/_/ /_/ /_/

  GPU MINER v%s — Crush the competition
  SHA-256 Midstate | Multi-Core | Zero-Alloc Hot Loop
`

func main() {
	nodeURL := flag.String("node", "", "Dilithium node URL (if not set, starts an embedded node)")
	address := flag.String("address", "", "Mining reward address (auto-detected from wallet if not set)")
	walletDir := flag.String("wallet", "", "Wallet directory to load address from")
	threads := flag.Int("threads", runtime.NumCPU(), "Number of CPU mining threads")
	peer := flag.String("peer", "", "Seed peer address (host:port) for embedded node to connect to")
	noNode := flag.Bool("no-node", false, "Disable embedded node (requires --node)")
	showVersion := flag.Bool("version", false, "Show version and exit")
	benchmark := flag.Bool("benchmark", false, "Run hashrate benchmark and exit")

	// GPU flags
	useGPU := flag.Bool("gpu", false, "Enable GPU mining (requires CUDA build)")
	gpuDevice := flag.Int("device", 0, "GPU device ID to use")
	batchSize := flag.Uint64("batch-size", 1<<26, "GPU batch size (number of nonces per kernel launch)")

	// Pool mining flags
	poolAddr := flag.String("pool", "", "Pool address (host:port) for pool mining mode")

	flag.Parse()

	if *showVersion {
		fmt.Printf("dilithium-gpu-miner v%s\n", AppVersion)
		os.Exit(0)
	}

	fmt.Printf(banner, AppVersion)
	fmt.Println()

	if *benchmark {
		runBenchmark(*threads)
		return
	}

	// Check GPU availability
	if *useGPU && !GPUMiningAvailable {
		fmt.Println("[!] Error: GPU mining not available")
		fmt.Println()
		fmt.Println("    This binary was not compiled with CUDA support.")
		fmt.Println("    To enable GPU mining:")
		fmt.Println("      1. Install NVIDIA CUDA Toolkit")
		fmt.Println("      2. Run: cd cmd/dilithium-gpu-miner && make gpu")
		fmt.Println("      3. Or build with: go build -tags cuda")
		fmt.Println()
		os.Exit(1)
	}

	// --- Pool mining mode ---
	if *poolAddr != "" {
		minerAddress := resolveAddress(*address, *walletDir)
		if minerAddress == "" {
			fmt.Println("[!] Error: Pool worker needs a payout address.")
			fmt.Println()
			fmt.Println("    Provide an address with --address or --wallet:")
			fmt.Println("      dilithium-gpu-miner --pool <host:port> --address <DLT_ADDRESS>")
			fmt.Println("      dilithium-gpu-miner --pool <host:port> --wallet ~/.dilithium/wallet")
			os.Exit(1)
		}
		fmt.Printf("[*] Pool mining mode: %s\n", *poolAddr)
		fmt.Printf("[*] Address: %s\n", minerAddress)
		if *useGPU {
			fmt.Printf("[*] GPU device: %d | Batch size: %d\n", *gpuDevice, *batchSize)
		} else {
			fmt.Printf("[*] CPU threads: %d\n", *threads)
		}
		fmt.Println()

		poolWorker := NewPoolWorker(*poolAddr, minerAddress, *threads, *useGPU, *gpuDevice, *batchSize)
		if err := poolWorker.Start(); err != nil {
			fmt.Printf("[!] Failed to start pool worker: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("[*] Pool mining started. Press Ctrl+C to stop.")
		fmt.Println()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		fmt.Println()
		fmt.Println("[*] Shutting down...")
		poolWorker.Stop()

		fmt.Println()
		fmt.Printf("[*] Session complete\n")
		fmt.Printf("    Shares submitted: %d\n", poolWorker.sharesSubmitted.Load())
		fmt.Printf("    Blocks found:     %d\n", poolWorker.blocksFound.Load())
		fmt.Printf("    Total hashes:     %d\n", poolWorker.totalHashes.Load())
		return
	}

	// --- Solo mining mode ---

	// --- Resolve miner address ---
	minerAddress := resolveAddress(*address, *walletDir)
	if minerAddress == "" {
		fmt.Println("[!] Error: No miner address found.")
		fmt.Println()
		fmt.Println("    Provide an address with --address or --wallet:")
		fmt.Println("      dilithium-gpu-miner --address <DLT_ADDRESS>")
		fmt.Println("      dilithium-gpu-miner --wallet ~/.dilithium/wallet")
		fmt.Println()
		fmt.Println("    Or create a wallet first with the dilithium CLI.")
		os.Exit(1)
	}

	// --- Resolve node URL ---
	var nodeRunner *NodeRunner
	activeNodeURL := strings.TrimRight(*nodeURL, "/")

	if activeNodeURL == "" && !*noNode {
		var err error
		nodeRunner, err = StartEmbeddedNode(*peer)
		if err != nil {
			fmt.Printf("[!] Error starting embedded node: %v\n", err)
			fmt.Println()
			fmt.Println("    You can run a node separately and connect with --node:")
			fmt.Println("      dilithium-gpu-miner --node http://localhost:8080")
			fmt.Println()
			fmt.Println("    Or ensure the 'dilithium' binary is in the same directory or PATH.")
			os.Exit(1)
		}
		activeNodeURL = nodeRunner.APIURL()
	} else if activeNodeURL == "" {
		fmt.Println("[!] Error: --no-node requires --node <url>")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("[*] Node:    %s\n", activeNodeURL)
	fmt.Printf("[*] Address: %s\n", minerAddress)
	if *useGPU {
		fmt.Printf("[*] Mode:    GPU\n")
		fmt.Printf("[*] Device:  %d\n", *gpuDevice)
		fmt.Printf("[*] Batch:   %d nonces\n", *batchSize)
	} else {
		fmt.Printf("[*] Mode:    CPU\n")
		fmt.Printf("[*] Threads: %d\n", *threads)
	}
	fmt.Printf("[*] Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	miner := NewMiner(activeNodeURL, minerAddress, *threads)
	miner.useGPU = *useGPU
	miner.gpuDevice = *gpuDevice
	miner.batchSize = *batchSize

	if err := miner.Start(); err != nil {
		fmt.Printf("[!] Failed to start miner: %v\n", err)
		if nodeRunner != nil {
			nodeRunner.Stop()
		}
		os.Exit(1)
	}

	fmt.Println("[*] Mining started. Press Ctrl+C to stop.")
	fmt.Println()

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println()
	fmt.Println("[*] Shutting down...")
	miner.Stop()

	if nodeRunner != nil {
		nodeRunner.Stop()
	}

	// Print final stats
	fmt.Println()
	fmt.Printf("[*] Session complete\n")
	fmt.Printf("    Blocks mined:  %d\n", miner.blocksMined.Load())
	fmt.Printf("    Total hashes:  %d\n", miner.totalHashes.Load())
	fmt.Printf("    Earnings:      %s DLT\n", FormatDLT(miner.earnings.Load()))
}

// resolveAddress determines the miner address from flags or auto-detection.
func resolveAddress(flagAddr, flagWallet string) string {
	// 1. Explicit --address flag
	if flagAddr != "" {
		return flagAddr
	}

	// 2. Explicit --wallet directory
	if flagWallet != "" {
		addr, err := loadAddressFromWallet(flagWallet)
		if err != nil {
			fmt.Printf("[!] Error loading wallet from %s: %v\n", flagWallet, err)
			return ""
		}
		fmt.Printf("[+] Loaded address from wallet: %s\n", flagWallet)
		return addr
	}

	// 3. Auto-detect from default wallet location ~/.dilithium/wallet/
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	defaultDir := filepath.Join(home, ".dilithium", "wallet")
	addr, err := loadAddressFromWallet(defaultDir)
	if err != nil {
		return ""
	}

	fmt.Printf("[+] Auto-detected address from %s\n", defaultDir)
	return addr
}

// loadAddressFromWallet reads the address file from a wallet directory.
func loadAddressFromWallet(walletDir string) (string, error) {
	addressPath := filepath.Join(walletDir, "address")
	data, err := os.ReadFile(addressPath)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", addressPath, err)
	}
	addr := strings.TrimSpace(string(data))
	if addr == "" {
		return "", fmt.Errorf("address file is empty")
	}
	return addr, nil
}

// runBenchmark measures raw hashrate without connecting to a node.
func runBenchmark(threads int) {
	fmt.Printf("[*] Running hashrate benchmark with %d threads...\n", threads)
	fmt.Println()

	// Simulate realistic mining data
	prefix := []byte(`1173849284[{"from":"SYSTEM","to":"DLT_benchmark_address_1234567890abcdef","amount":5000000000,"timestamp":1738368000,"signature":"coinbase-1-1738368000123456789"}]0000002835112676fbe3d7588fa08557751aa4045cc8575f16037247350815ae`)
	suffix := []byte("6")

	// Compute midstate using hardware-accelerated stdlib SHA-256
	h := sha256.New()
	fullBlocks := (len(prefix) / 64) * 64
	if fullBlocks > 0 {
		h.Write(prefix[:fullBlocks])
	}
	midstate, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	prefixTail := prefix[fullBlocks:]

	fmt.Printf("    Prefix length:  %d bytes\n", len(prefix))
	fmt.Printf("    Midstate blocks: %d (skipping %d bytes per hash)\n", fullBlocks/64, fullBlocks)
	fmt.Printf("    Tail + nonce:   ~%d bytes per hash\n", len(prefixTail)+10+len(suffix))
	fmt.Println()

	// Benchmark single thread first
	fmt.Print("    Single-thread: ")
	singleRate := benchmarkThread(midstate, prefixTail, suffix)
	fmt.Printf("%.2f MH/s\n", singleRate/1e6)

	// Multi-thread benchmark
	if threads > 1 {
		fmt.Printf("    %d-thread:     ", threads)
		multiRate := benchmarkMultiThread(midstate, prefixTail, suffix, threads)
		fmt.Printf("%.2f MH/s\n", multiRate/1e6)
		fmt.Printf("    Scaling:        %.1fx\n", multiRate/singleRate)
	}

	fmt.Println()
	fmt.Println("[*] Benchmark complete")
}

func benchmarkThread(midstate, tail, suffix []byte) float64 {
	h := sha256.New()
	var nonceBuf [20]byte
	var hashBuf [32]byte

	const iterations = 5_000_000
	start := time.Now()

	for i := int64(0); i < iterations; i++ {
		h.(encoding.BinaryUnmarshaler).UnmarshalBinary(midstate)
		h.Write(tail)
		n := writeInt64(nonceBuf[:], i+1000000)
		h.Write(nonceBuf[:n])
		h.Write(suffix)
		h.Sum(hashBuf[:0])
	}

	elapsed := time.Since(start).Seconds()
	return float64(iterations) / elapsed
}

func benchmarkMultiThread(midstate, tail, suffix []byte, threads int) float64 {
	const totalIterations = 20_000_000
	perThread := int64(totalIterations / threads)

	done := make(chan float64, threads)

	for t := 0; t < threads; t++ {
		go func(startNonce int64) {
			h := sha256.New()
			var nonceBuf [20]byte
			var hashBuf [32]byte

			start := time.Now()
			for i := int64(0); i < perThread; i++ {
				h.(encoding.BinaryUnmarshaler).UnmarshalBinary(midstate)
				h.Write(tail)
				n := writeInt64(nonceBuf[:], startNonce+i)
				h.Write(nonceBuf[:n])
				h.Write(suffix)
				h.Sum(hashBuf[:0])
			}
			elapsed := time.Since(start).Seconds()
			done <- float64(perThread) / elapsed
		}(int64(t) * perThread)
	}

	var totalRate float64
	for t := 0; t < threads; t++ {
		totalRate += <-done
	}
	return totalRate
}
