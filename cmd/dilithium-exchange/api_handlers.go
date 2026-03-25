package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// --- Auth handlers ---

func (s *Server) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	ethAddr := NormalizeETHAddress(req.Address)
	if !IsValidETHAddress(ethAddr) {
		writeError(w, 400, "invalid ethereum address")
		return
	}

	nonce, err := GenerateNonce()
	if err != nil {
		writeError(w, 500, "failed to generate nonce")
		return
	}

	expiresAt := time.Now().Add(nonceExpiry).Unix()
	if err := s.db.StoreNonce(nonce, ethAddr, expiresAt); err != nil {
		writeError(w, 500, "failed to store nonce")
		return
	}

	message := BuildSIWEMessage(req.Address, nonce) // use original case for display
	writeSuccess(w, map[string]string{"message": message, "nonce": nonce})
}

func (s *Server) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message   string `json:"message"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Recover signer address from signature
	recoveredAddr, err := VerifySIWESignature(req.Message, req.Signature)
	if err != nil {
		writeError(w, 401, fmt.Sprintf("signature verification failed: %v", err))
		return
	}

	// Extract address from the SIWE message (second line)
	lines := strings.Split(req.Message, "\n")
	if len(lines) < 2 {
		writeError(w, 400, "invalid SIWE message")
		return
	}
	claimedAddr := NormalizeETHAddress(strings.TrimSpace(lines[1]))

	if recoveredAddr != claimedAddr {
		writeError(w, 401, "signature does not match claimed address")
		return
	}

	// Extract and consume nonce
	nonce := extractNonce(req.Message)
	if nonce == "" {
		writeError(w, 400, "nonce not found in message")
		return
	}
	valid, err := s.db.ConsumeNonce(nonce, claimedAddr)
	if err != nil || !valid {
		writeError(w, 401, "invalid or expired nonce")
		return
	}

	// Get or create user
	user, err := s.db.GetUserByETH(claimedAddr)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	if user == nil {
		user, err = s.db.CreateUser(claimedAddr)
		if err != nil {
			writeError(w, 500, "failed to create user")
			return
		}
	} else {
		s.db.UpdateLastLogin(user.ID)
	}

	token, err := GenerateJWT(user.ID, claimedAddr, s.config.JWTSecret)
	if err != nil {
		writeError(w, 500, "failed to generate token")
		return
	}

	writeSuccess(w, map[string]interface{}{
		"token":       token,
		"eth_address": claimedAddr,
		"user_id":     user.ID,
	})
}

func (s *Server) handleLinkDLT(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	var req struct {
		DLTAddress string `json:"dlt_address"`
		Signature  string `json:"signature"`
		PublicKey  string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	dltAddr := strings.ToLower(strings.TrimSpace(req.DLTAddress))
	if !IsValidDLTAddress(dltAddr) {
		writeError(w, 400, "invalid DLT address (must be 40-char hex)")
		return
	}

	// Challenge the user signed: "link-dlt:{dltAddr}:{ethAddr}"
	challenge := fmt.Sprintf("link-dlt:%s:%s", dltAddr, claims.ETHAddress)

	valid, err := VerifyDLTOwnership(dltAddr, challenge, req.Signature, req.PublicKey)
	if err != nil || !valid {
		writeError(w, 401, fmt.Sprintf("DLT ownership verification failed: %v", err))
		return
	}

	// Ensure no other user has this DLT address
	existing, _ := s.db.GetUserByDLT(dltAddr)
	if existing != nil && existing.ID != claims.UserID {
		writeError(w, 409, "DLT address already linked to another account")
		return
	}

	if err := s.db.LinkDLTAddress(claims.UserID, dltAddr); err != nil {
		writeError(w, 500, "failed to link DLT address")
		return
	}

	// Initialize DLT balance row if not present
	s.db.CreditBalance(claims.UserID, "DLT", 0)

	writeSuccess(w, map[string]string{"dlt_address": dltAddr})
}

// --- Account handler ---

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	user, err := s.db.GetUserByID(claims.UserID)
	if err != nil || user == nil {
		writeError(w, 404, "user not found")
		return
	}

	balances, _ := s.db.GetAllBalances(claims.UserID)

	type balanceOut struct {
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}
	bOut := map[string]balanceOut{}
	for cur, b := range balances {
		switch cur {
		case "DLT":
			bOut[cur] = balanceOut{
				Available: FormatDLT(b.Available),
				Locked:    FormatDLT(b.Locked),
			}
		case "ETH":
			bOut[cur] = balanceOut{
				Available: formatWei(b.Available),
				Locked:    formatWei(b.Locked),
			}
		case "BTC":
			bOut[cur] = balanceOut{
				Available: formatSats(b.Available),
				Locked:    formatSats(b.Locked),
			}
		}
	}

	writeSuccess(w, map[string]interface{}{
		"eth_address":   user.ETHAddress,
		"dlt_address":   user.DLTAddress,
		"balances":      bOut,
		"balances_raw":  balances,
		"created_at":    user.CreatedAt,
		"last_login_at": user.LastLoginAt,
	})
}

// --- Deposit handlers ---

func (s *Server) handleGetETHDepositAddr(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	user, _ := s.db.GetUserByID(claims.UserID)
	if user == nil {
		writeError(w, 404, "user not found")
		return
	}

	addr, err := s.ethWallet.DeriveDepositAddress(user.ETHDepositIndex)
	if err != nil {
		writeError(w, 500, fmt.Sprintf("derive ETH address: %v", err))
		return
	}

	writeSuccess(w, map[string]interface{}{
		"address":  addr,
		"currency": "ETH",
		"note":     fmt.Sprintf("Send ETH to this address. Deposits are credited after %d confirmations (~%d minutes).", s.config.ETHConfirmations, s.config.ETHConfirmations/5),
	})
}

func (s *Server) handleGetBTCDepositAddr(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	user, _ := s.db.GetUserByID(claims.UserID)
	if user == nil {
		writeError(w, 404, "user not found")
		return
	}

	addr, err := s.btcWallet.DeriveDepositAddress(user.BTCDepositIndex)
	if err != nil {
		writeError(w, 500, fmt.Sprintf("derive BTC address: %v", err))
		return
	}

	writeSuccess(w, map[string]interface{}{
		"address":  addr,
		"currency": "BTC",
		"note":     fmt.Sprintf("Send BTC to this address. Deposits are credited after %d confirmations (~%d minutes).", s.config.BTCConfirmations, s.config.BTCConfirmations*10),
	})
}

func (s *Server) handleGetDLTDepositAddr(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	user, _ := s.db.GetUserByID(claims.UserID)
	if user == nil {
		writeError(w, 404, "user not found")
		return
	}
	if user.DLTAddress == "" {
		writeError(w, 400, "link a DLT address to your account first (POST /api/auth/link-dlt)")
		return
	}

	writeSuccess(w, map[string]interface{}{
		"address":       s.dltWallet.Address(),
		"currency":      "DLT",
		"from_address":  user.DLTAddress,
		"note":          "Send DLT from your linked address only. Deposits from other addresses cannot be attributed to your account.",
	})
}

// --- Market data handlers ---

func (s *Server) handleGetOrderBook(w http.ResponseWriter, r *http.Request) {
	pair, err := parsePair(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	snap, err := s.orderBook.GetSnapshot(pair)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeSuccess(w, snap)
}

func (s *Server) handleGetTrades(w http.ResponseWriter, r *http.Request) {
	pair, err := parsePair(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}
	trades, err := s.db.GetRecentTrades(pair, limit)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	writeSuccess(w, trades)
}

func (s *Server) handleGetTicker(w http.ResponseWriter, r *http.Request) {
	pair, err := parsePair(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	lastPrice, high, low, volume, err := s.db.Get24hStats(pair)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	writeSuccess(w, map[string]interface{}{
		"pair":       pair,
		"last_price": lastPrice,
		"high_24h":   high,
		"low_24h":    low,
		"volume_24h": volume,
		"timestamp":  time.Now().Unix(),
	})
}

// --- Order handlers ---

func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	var req struct {
		Pair      string  `json:"pair"`
		Side      string  `json:"side"`
		OrderType string  `json:"order_type"`
		Price     float64 `json:"price"`     // human-readable (e.g. 0.00001 ETH/DLT)
		Amount    float64 `json:"amount"`    // human-readable DLT amount
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	pair := strings.ToUpper(req.Pair)
	if !validPairs[pair] {
		writeError(w, 400, "invalid pair (use DLT-ETH or DLT-BTC)")
		return
	}
	if req.Side != "buy" && req.Side != "sell" {
		writeError(w, 400, "side must be 'buy' or 'sell'")
		return
	}
	if req.OrderType != "limit" && req.OrderType != "market" {
		writeError(w, 400, "order_type must be 'limit' or 'market'")
		return
	}
	if req.Amount <= 0 {
		writeError(w, 400, "amount must be positive")
		return
	}

	// Convert human-readable amounts to base units
	dltAmount := int64(req.Amount * float64(DLTUnit))
	if dltAmount <= 0 {
		writeError(w, 400, "amount too small")
		return
	}

	if req.OrderType == "market" {
		trades, err := s.orderBook.PlaceMarketOrder(claims.UserID, pair, req.Side, dltAmount)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeSuccess(w, map[string]interface{}{
			"order_type": "market",
			"trades":     trades,
		})
		return
	}

	// Limit order: convert price to base units
	// Price is: quote_currency_smallest_units / DLT_base_unit
	// For DLT-ETH: price in ETH per DLT → price * 1e18 / DLTUnit = wei per DLT_base_unit
	// For DLT-BTC: price in BTC per DLT → price * 1e8 / DLTUnit = sats per DLT_base_unit
	if req.Price <= 0 {
		writeError(w, 400, "price must be positive for limit orders")
		return
	}

	var priceBaseUnits int64
	switch pair {
	case "DLT-ETH":
		// req.Price is ETH per DLT, convert to wei per DLT_base_unit
		// 1 ETH = 1e18 wei, 1 DLT = 1e8 base units
		// price_wei_per_base_unit = req.Price * 1e18 / 1e8 = req.Price * 1e10
		priceBaseUnits = int64(req.Price * 1e10)
	case "DLT-BTC":
		// req.Price is BTC per DLT, convert to satoshis per DLT_base_unit
		// 1 BTC = 1e8 sats, 1 DLT = 1e8 base units
		// price_sats_per_base_unit = req.Price * 1e8 / 1e8 = req.Price
		priceBaseUnits = int64(req.Price * 1e8)
	}

	if priceBaseUnits <= 0 {
		writeError(w, 400, "price too small")
		return
	}

	order, trades, err := s.orderBook.PlaceLimitOrder(claims.UserID, pair, req.Side, priceBaseUnits, dltAmount)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeSuccess(w, map[string]interface{}{
		"order":  order,
		"trades": trades,
	})
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	// Extract order ID from path: /api/order/123
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) == 0 {
		writeError(w, 400, "missing order ID")
		return
	}
	idStr := parts[len(parts)-1]
	orderID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || orderID <= 0 {
		writeError(w, 400, "invalid order ID")
		return
	}

	if err := s.orderBook.CancelOrder(orderID, claims.UserID); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeSuccess(w, map[string]string{"status": "cancelled", "order_id": idStr})
}

func (s *Server) handleGetOrders(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	orders, err := s.db.GetUserOrders(claims.UserID)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	writeSuccess(w, orders)
}

// --- Withdrawal handler ---

func (s *Server) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())

	var req struct {
		Currency    string  `json:"currency"`
		Amount      float64 `json:"amount"`
		Destination string  `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	currency := strings.ToUpper(req.Currency)
	if currency != "ETH" && currency != "BTC" && currency != "DLT" {
		writeError(w, 400, "currency must be ETH, BTC, or DLT")
		return
	}
	if req.Amount <= 0 {
		writeError(w, 400, "amount must be positive")
		return
	}
	if req.Destination == "" {
		writeError(w, 400, "destination address required")
		return
	}

	var amount, fee int64
	switch currency {
	case "DLT":
		amount = int64(req.Amount * float64(DLTUnit))
		fee = s.config.WithdrawFeeDLT
	case "ETH":
		// amount in wei: req.Amount * 1e18
		amount = int64(req.Amount * 1e18)
		fee = s.config.WithdrawFeeETH
	case "BTC":
		// amount in satoshis
		amount = int64(req.Amount * 1e8)
		fee = s.config.WithdrawFeeBTC
	}

	if amount <= fee {
		writeError(w, 400, "withdrawal amount too small (less than fee)")
		return
	}

	// Debit available balance (amount + fee)
	total := amount + fee
	if err := s.db.DebitAvailable(claims.UserID, currency, total); err != nil {
		writeError(w, 400, fmt.Sprintf("insufficient balance: %v", err))
		return
	}

	withdrawal := &Withdrawal{
		UserID:      claims.UserID,
		Currency:    currency,
		Amount:      amount,
		Destination: req.Destination,
		Fee:         fee,
		Status:      "pending",
	}
	if err := s.db.CreateWithdrawal(withdrawal); err != nil {
		// Refund on DB error
		s.db.CreditBalance(claims.UserID, currency, total)
		writeError(w, 500, "failed to create withdrawal")
		return
	}

	writeSuccess(w, map[string]interface{}{
		"withdrawal_id": withdrawal.ID,
		"currency":      currency,
		"amount":        req.Amount,
		"destination":   req.Destination,
		"fee":           fee,
		"status":        "pending",
		"note":          "Withdrawal queued and will be processed shortly.",
	})
}

// --- Formatting helpers ---

func formatWei(wei int64) string {
	// Convert wei to ETH with 18 decimal places
	whole := wei / 1_000_000_000_000_000_000
	frac := wei % 1_000_000_000_000_000_000
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%018d", whole, frac)
}

func formatSats(sats int64) string {
	whole := sats / 100_000_000
	frac := sats % 100_000_000
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// extractNonce finds "Nonce: {value}" in a SIWE message.
func extractNonce(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(line, "Nonce: ") {
			return strings.TrimPrefix(line, "Nonce: ")
		}
	}
	return ""
}
