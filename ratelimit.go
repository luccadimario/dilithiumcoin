package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter tracks request rates per IP address
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // max requests per window
	window   time.Duration // time window
}

type visitor struct {
	count    int
	windowStart time.Time
}

// NewRateLimiter creates a rate limiter allowing 'rate' requests per 'window'
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
	}
	// Periodically clean up old entries
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(5 * time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.windowStart) > rl.window*2 {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Allow checks if a request from the given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	now := time.Now()

	if !exists || now.Sub(v.windowStart) > rl.window {
		rl.visitors[ip] = &visitor{count: 1, windowStart: now}
		return true
	}

	v.count++
	return v.count <= rl.rate
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Use RemoteAddr directly (don't trust X-Forwarded-For as it can be spoofed)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimitMiddleware wraps a handler with rate limiting
func rateLimitMiddleware(rl *RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		if !rl.Allow(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"success":false,"message":"Rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
