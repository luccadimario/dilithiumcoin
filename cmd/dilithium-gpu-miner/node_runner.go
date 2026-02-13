package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// NodeRunner manages an embedded dilithium node subprocess.
type NodeRunner struct {
	cmd     *exec.Cmd
	apiPort int
}

// StartEmbeddedNode finds the dilithium binary, picks free ports,
// launches the node as a subprocess, and waits for the API to be ready.
// connectPeer is an optional peer address (host:port) to seed the node with.
func StartEmbeddedNode(connectPeer string) (*NodeRunner, error) {
	binaryPath, err := findDilithiumBinary()
	if err != nil {
		return nil, fmt.Errorf("cannot find dilithium binary: %w", err)
	}

	apiPort, err := findFreePort()
	if err != nil {
		return nil, fmt.Errorf("cannot find free API port: %w", err)
	}

	p2pPort, err := findFreePort()
	if err != nil {
		return nil, fmt.Errorf("cannot find free P2P port: %w", err)
	}

	args := []string{
		"--port", strconv.Itoa(p2pPort),
		"--api-port", strconv.Itoa(apiPort),
	}

	if connectPeer != "" {
		args = append(args, "--connect", connectPeer)
	}

	fmt.Printf("[*] Starting embedded node (binary: %s)\n", binaryPath)
	fmt.Printf("[*] Embedded node P2P port: %d, API port: %d\n", p2pPort, apiPort)
	if connectPeer != "" {
		fmt.Printf("[*] Connecting to seed peer: %s\n", connectPeer)
	}

	cmd := exec.Command(binaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start node: %w", err)
	}

	runner := &NodeRunner{
		cmd:     cmd,
		apiPort: apiPort,
	}

	if err := runner.waitForReady(30 * time.Second); err != nil {
		runner.Stop()
		return nil, fmt.Errorf("node failed to become ready: %w", err)
	}

	fmt.Println("[+] Embedded node is ready!")
	return runner, nil
}

// Stop gracefully shuts down the embedded node process.
func (nr *NodeRunner) Stop() {
	if nr.cmd == nil || nr.cmd.Process == nil {
		return
	}

	fmt.Println("[*] Stopping embedded node...")
	nr.cmd.Process.Signal(os.Interrupt)

	done := make(chan error, 1)
	go func() {
		done <- nr.cmd.Wait()
	}()

	select {
	case <-done:
		fmt.Println("[+] Embedded node stopped.")
	case <-time.After(10 * time.Second):
		fmt.Println("[!] Force killing embedded node...")
		nr.cmd.Process.Kill()
	}
}

// APIURL returns the URL for the embedded node's API.
func (nr *NodeRunner) APIURL() string {
	return fmt.Sprintf("http://localhost:%d", nr.apiPort)
}

// waitForReady polls the node's /status endpoint until it responds or timeout.
func (nr *NodeRunner) waitForReady(timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://localhost:%d/status", nr.apiPort)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}

		if nr.cmd.ProcessState != nil && nr.cmd.ProcessState.Exited() {
			return fmt.Errorf("node process exited prematurely")
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timed out after %s waiting for node API", timeout)
}

// findDilithiumBinary looks for the dilithium binary in the same directory
// as the miner executable, then the current working directory, then PATH.
func findDilithiumBinary() (string, error) {
	binaryName := "dilithium"
	if runtime.GOOS == "windows" {
		binaryName = "dilithium.exe"
	}

	// Check same directory as the miner executable
	execPath, err := os.Executable()
	if err == nil {
		sameDir := filepath.Join(filepath.Dir(execPath), binaryName)
		if _, err := os.Stat(sameDir); err == nil {
			return sameDir, nil
		}
	}

	// Check current working directory
	if cwd, err := os.Getwd(); err == nil {
		cwdBin := filepath.Join(cwd, binaryName)
		if _, err := os.Stat(cwdBin); err == nil {
			return cwdBin, nil
		}
	}

	// Fall back to PATH
	pathBinary, err := exec.LookPath(binaryName)
	if err == nil {
		return pathBinary, nil
	}

	return "", fmt.Errorf("%s not found next to miner, in current directory, or in PATH", binaryName)
}

// findFreePort asks the OS for an available TCP port.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
