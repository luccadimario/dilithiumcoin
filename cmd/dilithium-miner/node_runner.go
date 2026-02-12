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

// NodeRunner manages an embedded dilithium node subprocess
type NodeRunner struct {
	cmd     *exec.Cmd
	apiPort int
}

// StartEmbeddedNode finds the dilithium binary, picks a free API port,
// launches the node as a subprocess, and waits for the API to be ready.
func StartEmbeddedNode() (*NodeRunner, error) {
	binaryPath, err := findDilithiumBinary()
	if err != nil {
		return nil, fmt.Errorf("cannot find dilithium binary: %w", err)
	}

	apiPort, err := findFreePort()
	if err != nil {
		return nil, fmt.Errorf("cannot find free port: %w", err)
	}

	fmt.Printf("Starting embedded node (binary: %s, API port: %d)...\n", binaryPath, apiPort)

	cmd := exec.Command(binaryPath,
		"--port", "1701",
		"--api-port", strconv.Itoa(apiPort),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start node: %w", err)
	}

	runner := &NodeRunner{
		cmd:     cmd,
		apiPort: apiPort,
	}

	// Wait for the API to become ready
	if err := runner.waitForReady(30 * time.Second); err != nil {
		runner.Stop()
		return nil, fmt.Errorf("node failed to become ready: %w", err)
	}

	fmt.Println("Embedded node is ready!")
	return runner, nil
}

// Stop gracefully shuts down the embedded node process.
func (nr *NodeRunner) Stop() {
	if nr.cmd == nil || nr.cmd.Process == nil {
		return
	}

	fmt.Println("Stopping embedded node...")
	nr.cmd.Process.Signal(os.Interrupt)

	// Wait up to 10 seconds for clean exit
	done := make(chan error, 1)
	go func() {
		done <- nr.cmd.Wait()
	}()

	select {
	case <-done:
		fmt.Println("Embedded node stopped.")
	case <-time.After(10 * time.Second):
		fmt.Println("Force killing embedded node...")
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

		// Check if process has exited
		if nr.cmd.ProcessState != nil && nr.cmd.ProcessState.Exited() {
			return fmt.Errorf("node process exited prematurely")
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timed out after %s waiting for node API", timeout)
}

// findDilithiumBinary looks for the dilithium binary in the same directory
// as the miner executable, then falls back to PATH.
func findDilithiumBinary() (string, error) {
	// Names to search for, in priority order
	names := []string{"dilithium"}
	switch runtime.GOOS {
	case "windows":
		names = []string{"dilithium.exe", "dilithium-windows-amd64.exe"}
	case "darwin":
		names = append(names, "dilithium-darwin-amd64", "dilithium-darwin-arm64")
	default: // linux
		names = append(names, "dilithium-linux-amd64", "dilithium-linux-arm64")
	}

	// Check same directory as the miner executable
	execPath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(execPath)
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}

	// Fall back to PATH
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("dilithium not found in same directory as miner or in PATH")
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
