package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Server is the HTTP API server for the exchange.
type Server struct {
	config    *Config
	db        *Database
	orderBook *OrderBook
	ethWallet *ETHWallet
	btcWallet *BTCWallet
	dltWallet *DLTWallet
	server    *http.Server
}

func NewServer(config *Config, db *Database, ob *OrderBook, eth *ETHWallet, btc *BTCWallet, dlt *DLTWallet) *Server {
	return &Server{
		config:    config,
		db:        db,
		orderBook: ob,
		ethWallet: eth,
		btcWallet: btc,
		dltWallet: dlt,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	handler := corsMiddleware(mux)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.HTTPPort),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Printf("[exchange] API listening on :%d\n", s.config.HTTPPort)
	return s.server.ListenAndServe()
}

func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Auth (public)
	mux.HandleFunc("POST /api/auth/challenge", s.handleAuthChallenge)
	mux.HandleFunc("POST /api/auth/verify", s.handleAuthVerify)

	// Auth (requires JWT)
	mux.HandleFunc("POST /api/auth/link-dlt", s.requireAuth(s.handleLinkDLT))
	mux.HandleFunc("GET /api/account", s.requireAuth(s.handleGetAccount))

	// Deposits (requires JWT)
	mux.HandleFunc("GET /api/deposit/eth", s.requireAuth(s.handleGetETHDepositAddr))
	mux.HandleFunc("GET /api/deposit/btc", s.requireAuth(s.handleGetBTCDepositAddr))
	mux.HandleFunc("GET /api/deposit/dlt", s.requireAuth(s.handleGetDLTDepositAddr))

	// Market data (public)
	mux.HandleFunc("GET /api/orderbook", s.handleGetOrderBook)
	mux.HandleFunc("GET /api/trades", s.handleGetTrades)
	mux.HandleFunc("GET /api/ticker", s.handleGetTicker)

	// Orders (requires JWT)
	mux.HandleFunc("POST /api/order", s.requireAuth(s.handlePlaceOrder))
	mux.HandleFunc("DELETE /api/order/", s.requireAuth(s.handleCancelOrder))
	mux.HandleFunc("GET /api/orders", s.requireAuth(s.handleGetOrders))

	// Withdrawal (requires JWT)
	mux.HandleFunc("POST /api/withdraw", s.requireAuth(s.handleWithdraw))

	// Health check
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
}

// --- Middleware ---

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, 401, "missing authorization header")
			return
		}
		claims, err := ValidateJWT(authHeader, s.config.JWTSecret)
		if err != nil {
			writeError(w, 401, "invalid or expired token")
			return
		}
		// Attach claims to request context
		ctx := withClaims(r.Context(), claims)
		next(w, r.WithContext(ctx))
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Context helpers ---

type contextKey string

const claimsKey contextKey = "claims"

func withClaims(ctx context.Context, claims *JWTClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

func claimsFromCtx(ctx context.Context) *JWTClaims {
	c, _ := ctx.Value(claimsKey).(*JWTClaims)
	return c
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeSuccess(w http.ResponseWriter, data interface{}) {
	writeJSON(w, 200, map[string]interface{}{"success": true, "data": data})
}

func parsePair(r *http.Request) (string, error) {
	pair := strings.ToUpper(r.URL.Query().Get("pair"))
	if pair == "" {
		pair = "DLT-ETH"
	}
	if !validPairs[pair] {
		return "", fmt.Errorf("unknown pair: %s (use DLT-ETH or DLT-BTC)", pair)
	}
	return pair, nil
}
