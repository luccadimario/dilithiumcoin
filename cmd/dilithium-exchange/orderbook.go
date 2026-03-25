package main

import (
	"fmt"
	"sync"
)

// OrderBook is the in-memory price-time priority order matching engine.
// It is backed by the database for persistence.
type OrderBook struct {
	db     *Database
	config *Config
	pairs  map[string]*TradingPair
	mu     sync.Mutex
}

// TradingPair holds the live order book for a single pair.
type TradingPair struct {
	Pair string
	Bids []*Order // sorted: price DESC, time ASC
	Asks []*Order // sorted: price ASC, time ASC
}

// OrderBookLevel is a price level in the order book snapshot.
type OrderBookLevel struct {
	Price  int64 `json:"price"`  // quote currency smallest unit per DLT base unit
	Amount int64 `json:"amount"` // total DLT base units at this price
}

// OrderBookSnapshot is the serialisable order book state.
type OrderBookSnapshot struct {
	Pair string           `json:"pair"`
	Bids []OrderBookLevel `json:"bids"` // sorted price DESC
	Asks []OrderBookLevel `json:"asks"` // sorted price ASC
}

var validPairs = map[string]bool{
	"DLT-ETH": true,
	"DLT-BTC": true,
}

// NewOrderBook creates an order book and loads open orders from the DB.
func NewOrderBook(db *Database, config *Config) (*OrderBook, error) {
	ob := &OrderBook{
		db:     db,
		config: config,
		pairs:  make(map[string]*TradingPair),
	}
	for pair := range validPairs {
		ob.pairs[pair] = &TradingPair{Pair: pair}
	}
	return ob, ob.loadFromDB()
}

// loadFromDB populates the in-memory order book from the database on startup.
func (ob *OrderBook) loadFromDB() error {
	for pair := range validPairs {
		bids, err := ob.db.GetOpenOrders(pair, "buy")
		if err != nil {
			return fmt.Errorf("load bids for %s: %w", pair, err)
		}
		asks, err := ob.db.GetOpenOrders(pair, "sell")
		if err != nil {
			return fmt.Errorf("load asks for %s: %w", pair, err)
		}
		ob.pairs[pair].Bids = bids
		ob.pairs[pair].Asks = asks
	}
	return nil
}

// PlaceLimitOrder places a limit order, matches against the book, and returns
// executed trades. Any unfilled portion stays on the book.
func (ob *OrderBook) PlaceLimitOrder(userID int64, pair, side string, price, amount int64) (*Order, []*Trade, error) {
	if !validPairs[pair] {
		return nil, nil, fmt.Errorf("unknown pair: %s", pair)
	}
	if price <= 0 || amount <= 0 {
		return nil, nil, fmt.Errorf("price and amount must be positive")
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	// Determine which currency to lock for the order.
	// Buy DLT-ETH: lock ETH (amount × price in wei) — but we store price as integer units
	// Sell DLT-ETH: lock DLT (amount)
	lockCurrency, lockAmount, err := ob.requiredLock(pair, side, price, amount)
	if err != nil {
		return nil, nil, err
	}

	order := &Order{
		UserID:    userID,
		Pair:      pair,
		Side:      side,
		OrderType: "limit",
		Price:     price,
		Amount:    amount,
		Status:    "open",
	}

	// Lock funds before persisting the order
	if err := ob.db.LockBalance(userID, lockCurrency, lockAmount); err != nil {
		return nil, nil, fmt.Errorf("lock balance: %w", err)
	}

	if err := ob.db.CreateOrder(order); err != nil {
		ob.db.UnlockBalance(userID, lockCurrency, lockAmount)
		return nil, nil, fmt.Errorf("create order: %w", err)
	}

	// Try to match
	trades, err := ob.match(order)
	if err != nil {
		return order, nil, err
	}

	// If not fully filled, add to the book
	if order.Status != "filled" {
		tp := ob.pairs[pair]
		if side == "buy" {
			tp.Bids = insertSorted(tp.Bids, order, true)
		} else {
			tp.Asks = insertSorted(tp.Asks, order, false)
		}
	}

	return order, trades, nil
}

// PlaceMarketOrder executes immediately at best available prices.
// Any unfilled portion is cancelled (not left on book).
func (ob *OrderBook) PlaceMarketOrder(userID int64, pair, side string, amount int64) ([]*Trade, error) {
	if !validPairs[pair] {
		return nil, fmt.Errorf("unknown pair: %s", pair)
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	tp := ob.pairs[pair]
	var counterOrders []*Order
	if side == "buy" {
		counterOrders = tp.Asks
	} else {
		counterOrders = tp.Bids
	}
	if len(counterOrders) == 0 {
		return nil, fmt.Errorf("no liquidity for market order")
	}

	// Estimate cost for locking: use best counter price × amount
	bestPrice := counterOrders[0].Price
	lockCurrency, lockAmount, err := ob.requiredLock(pair, side, bestPrice, amount)
	if err != nil {
		return nil, err
	}
	if err := ob.db.LockBalance(userID, lockCurrency, lockAmount); err != nil {
		return nil, fmt.Errorf("lock balance: %w", err)
	}

	order := &Order{
		UserID:    userID,
		Pair:      pair,
		Side:      side,
		OrderType: "market",
		Price:     0, // market price
		Amount:    amount,
		Status:    "open",
	}
	if err := ob.db.CreateOrder(order); err != nil {
		ob.db.UnlockBalance(userID, lockCurrency, lockAmount)
		return nil, err
	}

	trades, err := ob.match(order)
	if err != nil {
		return nil, err
	}

	// Cancel any unfilled portion (return locked funds)
	remaining := order.Amount - order.Filled
	if remaining > 0 && order.Status != "filled" {
		refundCurrency, refundAmount, _ := ob.requiredLock(pair, side, bestPrice, remaining)
		ob.db.UnlockBalance(userID, refundCurrency, refundAmount)
		ob.db.DebitAvailable(userID, refundCurrency, 0) // no-op but triggers balance consistency check
		order.Status = "cancelled"
		ob.db.UpdateOrder(order)
	}

	return trades, nil
}

// CancelOrder cancels an open order, returning locked funds to available.
func (ob *OrderBook) CancelOrder(orderID, userID int64) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	order, err := ob.db.GetOrder(orderID)
	if err != nil || order == nil {
		return fmt.Errorf("order not found")
	}
	if order.UserID != userID {
		return fmt.Errorf("not your order")
	}
	if order.Status != "open" && order.Status != "partial" {
		return fmt.Errorf("order is already %s", order.Status)
	}

	remaining := order.Amount - order.Filled
	lockCurrency, lockAmount, _ := ob.requiredLock(order.Pair, order.Side, order.Price, remaining)

	order.Status = "cancelled"
	if err := ob.db.UpdateOrder(order); err != nil {
		return err
	}

	// Remove from in-memory book
	tp := ob.pairs[order.Pair]
	if order.Side == "buy" {
		tp.Bids = removeOrder(tp.Bids, orderID)
	} else {
		tp.Asks = removeOrder(tp.Asks, orderID)
	}

	return ob.db.UnlockBalance(userID, lockCurrency, lockAmount)
}

// GetSnapshot returns the current order book state for a pair.
func (ob *OrderBook) GetSnapshot(pair string) (*OrderBookSnapshot, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	tp, ok := ob.pairs[pair]
	if !ok {
		return nil, fmt.Errorf("unknown pair: %s", pair)
	}

	snap := &OrderBookSnapshot{Pair: pair}

	// Aggregate by price level
	snap.Bids = aggregateLevels(tp.Bids)
	snap.Asks = aggregateLevels(tp.Asks)
	return snap, nil
}

// --- Internal matching ---

// match attempts to match a taker order against the book.
// Must be called with ob.mu held.
func (ob *OrderBook) match(taker *Order) ([]*Trade, error) {
	tp := ob.pairs[taker.Pair]
	var makerOrders *[]*Order

	if taker.Side == "buy" {
		makerOrders = &tp.Asks // buy taker matches with asks
	} else {
		makerOrders = &tp.Bids // sell taker matches with bids
	}

	var trades []*Trade
	takerRemaining := taker.Amount - taker.Filled

	for len(*makerOrders) > 0 && takerRemaining > 0 {
		maker := (*makerOrders)[0]

		// Skip own orders (self-trade prevention)
		if maker.UserID == taker.UserID {
			break
		}

		// Price check for limit orders
		if taker.OrderType == "limit" {
			if taker.Side == "buy" && taker.Price < maker.Price {
				break // taker bid < ask
			}
			if taker.Side == "sell" && taker.Price > maker.Price {
				break // taker ask > bid
			}
		}

		// Execute at maker price
		execPrice := maker.Price
		makerRemaining := maker.Amount - maker.Filled
		execAmount := min64(takerRemaining, makerRemaining)

		trade, err := ob.executeTrade(taker, maker, execPrice, execAmount)
		if err != nil {
			return trades, err
		}
		trades = append(trades, trade)

		taker.Filled += execAmount
		takerRemaining -= execAmount
		maker.Filled += execAmount

		// Update maker status
		if maker.Filled >= maker.Amount {
			maker.Status = "filled"
			*makerOrders = (*makerOrders)[1:] // remove from book
		} else {
			maker.Status = "partial"
		}
		ob.db.UpdateOrder(maker)
	}

	// Update taker status
	if taker.Filled >= taker.Amount {
		taker.Status = "filled"
	} else if taker.Filled > 0 {
		taker.Status = "partial"
	}
	ob.db.UpdateOrder(taker)

	return trades, nil
}

// executeTrade settles a single match between two orders.
// Moves funds between maker and taker balances, records the trade.
func (ob *OrderBook) executeTrade(taker, maker *Order, price, amount int64) (*Trade, error) {
	pair := taker.Pair
	quoteCurrency := quoteCurrencyForPair(pair) // "ETH" or "BTC"

	// Calculate quote amount (price × amount / DLTUnit)
	// price is in quote-currency smallest units per 1 DLT base unit
	quoteAmount := price * amount / DLTUnit
	if quoteAmount == 0 {
		quoteAmount = 1 // minimum
	}

	// Apply taker fee
	takerFeeRate := ob.config.TakerFee
	makerFeeRate := ob.config.MakerFee

	if taker.Side == "buy" {
		// Taker (buyer): pays quoteAmount (ETH/BTC), receives DLT
		// Maker (seller): pays DLT, receives quoteAmount (ETH/BTC)

		takerFee := int64(float64(quoteAmount) * takerFeeRate)
		makerFee := int64(float64(amount) * makerFeeRate)

		// Debit taker's locked quote balance
		ob.db.DebitLocked(taker.UserID, quoteCurrency, quoteAmount+takerFee)
		// Credit taker with DLT (minus nothing — DLT comes from maker's locked)
		ob.db.CreditBalance(taker.UserID, "DLT", amount-makerFee)

		// Debit maker's locked DLT
		ob.db.DebitLocked(maker.UserID, "DLT", amount)
		// Credit maker with quote
		ob.db.CreditBalance(maker.UserID, quoteCurrency, quoteAmount-makerFee)
	} else {
		// Taker (seller): pays DLT, receives quoteAmount (ETH/BTC)
		// Maker (buyer): pays quoteAmount (ETH/BTC), receives DLT

		takerFee := int64(float64(amount) * takerFeeRate)
		makerFee := int64(float64(quoteAmount) * makerFeeRate)

		// Debit taker's locked DLT
		ob.db.DebitLocked(taker.UserID, "DLT", amount)
		// Credit taker with quote
		ob.db.CreditBalance(taker.UserID, quoteCurrency, quoteAmount-takerFee)

		// Debit maker's locked quote
		ob.db.DebitLocked(maker.UserID, quoteCurrency, quoteAmount+makerFee)
		// Credit maker with DLT
		ob.db.CreditBalance(maker.UserID, "DLT", amount-makerFee)
	}

	trade := &Trade{
		Pair:         pair,
		MakerOrderID: maker.ID,
		TakerOrderID: taker.ID,
		MakerUserID:  maker.UserID,
		TakerUserID:  taker.UserID,
		Price:        price,
		Amount:       amount,
		Side:         taker.Side,
	}
	ob.db.RecordTrade(trade)
	return trade, nil
}

// --- Helpers ---

// requiredLock returns the currency and amount that must be locked to place this order.
func (ob *OrderBook) requiredLock(pair, side string, price, amount int64) (string, int64, error) {
	quoteCurrency := quoteCurrencyForPair(pair)
	if side == "buy" {
		// Buyer locks quote currency: price × amount / DLTUnit
		quoteAmount := price * amount / DLTUnit
		if quoteAmount == 0 {
			quoteAmount = 1
		}
		// Add taker fee buffer
		quoteAmount = int64(float64(quoteAmount) * (1 + ob.config.TakerFee))
		return quoteCurrency, quoteAmount, nil
	}
	// Seller locks DLT
	return "DLT", amount, nil
}

func quoteCurrencyForPair(pair string) string {
	switch pair {
	case "DLT-ETH":
		return "ETH"
	case "DLT-BTC":
		return "BTC"
	default:
		return "ETH"
	}
}

// insertSorted inserts an order into a sorted slice.
// descending=true for bids (highest price first), false for asks.
func insertSorted(orders []*Order, o *Order, descending bool) []*Order {
	i := 0
	for i < len(orders) {
		if descending {
			if o.Price > orders[i].Price || (o.Price == orders[i].Price && o.CreatedAt < orders[i].CreatedAt) {
				break
			}
		} else {
			if o.Price < orders[i].Price || (o.Price == orders[i].Price && o.CreatedAt < orders[i].CreatedAt) {
				break
			}
		}
		i++
	}
	orders = append(orders, nil)
	copy(orders[i+1:], orders[i:])
	orders[i] = o
	return orders
}

func removeOrder(orders []*Order, id int64) []*Order {
	for i, o := range orders {
		if o.ID == id {
			return append(orders[:i], orders[i+1:]...)
		}
	}
	return orders
}

func aggregateLevels(orders []*Order) []OrderBookLevel {
	levelMap := map[int64]int64{}
	var levelPrices []int64
	seen := map[int64]bool{}
	for _, o := range orders {
		remaining := o.Amount - o.Filled
		if remaining <= 0 {
			continue
		}
		levelMap[o.Price] += remaining
		if !seen[o.Price] {
			levelPrices = append(levelPrices, o.Price)
			seen[o.Price] = true
		}
	}
	var levels []OrderBookLevel
	for _, price := range levelPrices {
		levels = append(levels, OrderBookLevel{Price: price, Amount: levelMap[price]})
	}
	return levels
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
