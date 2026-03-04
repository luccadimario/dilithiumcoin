package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// NodeCandidate represents a discovered node with quality metrics.
type NodeCandidate struct {
	URL       string  `json:"url"`
	Height    float64 `json:"height"`
	Latency   int64   `json:"latency_ms"`
	Reachable bool    `json:"reachable"`
	LastSeen  int64   `json:"last_seen"`
}

// nodeCacheFile is the path to the cached known-good nodes file.
var nodeCacheFile string

func init() {
	home, _ := os.UserHomeDir()
	nodeCacheFile = filepath.Join(home, ".dilithium", "nodes.dat")
}

// hardcodedSeeds is the expanded list of seed node API endpoints.
var hardcodedSeeds = []string{
	"http://seed.dilithiumcoin.com:8001",
	"http://seed1.dilithiumcoin.com:8001",
	"http://seed2.dilithiumcoin.com:8001",
	"http://seed3.dilithiumcoin.com:8001",
	"http://seed4.dilithiumcoin.com:8001",
	"http://seed5.dilithiumcoin.com:8001",
	"http://seed6.dilithiumcoin.com:8001",
	"http://seed7.dilithiumcoin.com:8001",
	"http://seed8.dilithiumcoin.com:8001",
	"http://seed9.dilithiumcoin.com:8001",
}

// DiscoverNodes finds reachable nodes using a resolution chain:
//  1. Cached known-good nodes (nodes.dat)
//  2. localhost:8001 (local node)
//  3. DNS TXT seeds (stub)
//  4. Hardcoded seed list
//
// Returns the top 3 candidates ranked by: reachable > highest height > lowest latency.
func DiscoverNodes() []NodeCandidate {
	var candidates []NodeCandidate
	seen := make(map[string]bool)

	// 1. Load cached nodes
	cached := LoadNodeCache()
	for _, c := range cached {
		if !seen[c.URL] {
			candidates = append(candidates, c)
			seen[c.URL] = true
		}
	}

	// 2. Try localhost
	localURL := "http://localhost:8001"
	if !seen[localURL] {
		candidates = append(candidates, NodeCandidate{URL: localURL})
		seen[localURL] = true
	}

	// 3. DNS TXT lookup
	dnsSeeds := resolveDNSTXTSeeds("_dilt._tcp.seeds.dilithiumcoin.com")
	for _, seed := range dnsSeeds {
		if !seen[seed] {
			candidates = append(candidates, NodeCandidate{URL: seed})
			seen[seed] = true
		}
	}

	// 4. Hardcoded seeds
	for _, seed := range hardcodedSeeds {
		if !seen[seed] {
			candidates = append(candidates, NodeCandidate{URL: seed})
			seen[seed] = true
		}
	}

	// Probe all candidates in parallel
	probed := probeNodes(candidates)

	// Rank: reachable first, then by height desc, then latency asc
	sort.Slice(probed, func(i, j int) bool {
		if probed[i].Reachable != probed[j].Reachable {
			return probed[i].Reachable
		}
		if probed[i].Height != probed[j].Height {
			return probed[i].Height > probed[j].Height
		}
		return probed[i].Latency < probed[j].Latency
	})

	// Return top 3
	if len(probed) > 3 {
		probed = probed[:3]
	}

	// Save reachable nodes to cache
	var reachable []NodeCandidate
	for _, c := range probed {
		if c.Reachable {
			reachable = append(reachable, c)
		}
	}
	if len(reachable) > 0 {
		SaveNodeCache(reachable)
	}

	return probed
}

// probeNodes concurrently checks each candidate's /status endpoint.
func probeNodes(candidates []NodeCandidate) []NodeCandidate {
	client := &http.Client{Timeout: 3 * time.Second}
	result := make([]NodeCandidate, len(candidates))
	var wg sync.WaitGroup

	for i, c := range candidates {
		wg.Add(1)
		go func(idx int, candidate NodeCandidate) {
			defer wg.Done()

			start := time.Now()
			resp, err := client.Get(candidate.URL + "/status")
			latency := time.Since(start).Milliseconds()

			result[idx] = candidate
			result[idx].Latency = latency

			if err != nil {
				result[idx].Reachable = false
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				result[idx].Reachable = false
				return
			}

			var apiResp APIResponse
			if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
				result[idx].Reachable = false
				return
			}

			result[idx].Reachable = true
			result[idx].LastSeen = time.Now().Unix()

			if apiResp.Data != nil {
				if h, ok := apiResp.Data["blockchain_height"].(float64); ok {
					result[idx].Height = h
				}
			}
		}(i, c)
	}

	wg.Wait()
	return result
}

// SaveNodeCache writes known-good nodes to ~/.dilithium/nodes.dat.
func SaveNodeCache(nodes []NodeCandidate) {
	dir := filepath.Dir(nodeCacheFile)
	os.MkdirAll(dir, 0700)

	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(nodeCacheFile, data, 0600)
}

// LoadNodeCache reads cached nodes, skipping entries older than 1 hour.
func LoadNodeCache() []NodeCandidate {
	data, err := os.ReadFile(nodeCacheFile)
	if err != nil {
		return nil
	}

	var nodes []NodeCandidate
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil
	}

	// Filter out stale entries (older than 1 hour)
	cutoff := time.Now().Unix() - 3600
	var fresh []NodeCandidate
	for _, n := range nodes {
		if n.LastSeen >= cutoff {
			fresh = append(fresh, n)
		}
	}

	return fresh
}

// DiscoverBestNode returns the URL of the best available node.
// If nodeURL is explicitly provided and reachable, it is used directly.
func DiscoverBestNode(explicitNode string) string {
	// If user explicitly set a node, try it first
	if explicitNode != "" && explicitNode != "http://localhost:8001" {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(explicitNode + "/status")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return explicitNode
			}
		}
	}

	nodes := DiscoverNodes()
	for _, n := range nodes {
		if n.Reachable {
			if n.URL != explicitNode {
				fmt.Printf("Discovered node: %s (height %.0f, %dms)\n", n.URL, n.Height, n.Latency)
			}
			return n.URL
		}
	}

	// Nothing reachable — return the explicit one and let the command fail with a useful error
	if explicitNode != "" {
		return explicitNode
	}
	return "http://localhost:8001"
}

// DiscoverAllReachable returns all reachable nodes for multi-submit.
func DiscoverAllReachable(explicitNode string) []string {
	var urls []string
	seen := make(map[string]bool)

	// Always include explicit node if set
	if explicitNode != "" {
		urls = append(urls, explicitNode)
		seen[explicitNode] = true
	}

	nodes := DiscoverNodes()
	for _, n := range nodes {
		if n.Reachable && !seen[n.URL] {
			urls = append(urls, n.URL)
			seen[n.URL] = true
		}
	}

	if len(urls) == 0 {
		urls = append(urls, "http://localhost:8001")
	}
	return urls
}

// resolveDNSTXTSeeds resolves DNS TXT records for seed discovery.
// TXT records should contain "host:port" entries for node API endpoints.
func resolveDNSTXTSeeds(hostname string) []string {
	records, err := net.LookupTXT(hostname)
	if err != nil {
		return nil
	}

	var seeds []string
	for _, record := range records {
		// Each TXT record may contain one or more host:port entries separated by spaces
		for _, entry := range strings.Fields(record) {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			// Ensure it has a scheme for HTTP usage
			if !strings.HasPrefix(entry, "http://") && !strings.HasPrefix(entry, "https://") {
				entry = "http://" + entry
			}
			seeds = append(seeds, entry)
		}
	}
	return seeds
}
