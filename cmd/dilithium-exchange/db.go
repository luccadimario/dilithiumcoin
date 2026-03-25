package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Database wraps the SQLite connection.
type Database struct {
	db *sql.DB
}

// --- Models ---

type User struct {
	ID              int64
	ETHAddress      string // lowercase, with 0x prefix
	DLTAddress      string // 40-char hex
	ETHDepositIndex int
	BTCDepositIndex int
	CreatedAt       int64
	LastLoginAt     int64
}

type Balance struct {
	UserID    int64
	Currency  string // "ETH", "BTC", "DLT"
	Available int64  // available for trading/withdrawal (smallest unit)
	Locked    int64  // locked in open orders
}

type Order struct {
	ID        int64
	UserID    int64
	Pair      string // "DLT-ETH" or "DLT-BTC"
	Side      string // "buy" or "sell"
	OrderType string // "limit" or "market"
	Price     int64  // price in quote-currency smallest units per 1 DLT base unit; 0 for market
	Amount    int64  // DLT amount in base units
	Filled    int64  // filled DLT base units
	Status    string // "open", "partial", "filled", "cancelled"
	CreatedAt int64
	UpdatedAt int64
}

type Trade struct {
	ID           int64
	Pair         string
	MakerOrderID int64
	TakerOrderID int64
	MakerUserID  int64
	TakerUserID  int64
	Price        int64 // execution price
	Amount       int64 // DLT base units traded
	Side         string // "buy" or "sell" from taker perspective
	ExecutedAt   int64
}

type Deposit struct {
	ID            int64
	UserID        int64
	Currency      string
	Amount        int64
	TxHash        string
	Confirmations int
	Status        string // "pending", "confirmed", "credited"
	DetectedAt    int64
	CreditedAt    int64
}

type Withdrawal struct {
	ID          int64
	UserID      int64
	Currency    string
	Amount      int64
	Destination string
	TxHash      string
	Fee         int64
	Status      string // "pending", "processing", "sent", "failed"
	RequestedAt int64
	ProcessedAt int64
}

// --- Init ---

func NewDatabase(path string) (*Database, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	d := &Database{db: db}
	if err := d.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return d, nil
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) initSchema() error {
	_, err := d.db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	eth_address TEXT UNIQUE NOT NULL,
	dlt_address TEXT,
	eth_deposit_index INTEGER NOT NULL DEFAULT 0,
	btc_deposit_index INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	last_login_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_eth ON users(eth_address);
CREATE INDEX IF NOT EXISTS idx_users_dlt ON users(dlt_address);

CREATE TABLE IF NOT EXISTS balances (
	user_id INTEGER NOT NULL,
	currency TEXT NOT NULL,
	available INTEGER NOT NULL DEFAULT 0,
	locked INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, currency),
	FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS orders (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	pair TEXT NOT NULL,
	side TEXT NOT NULL,
	order_type TEXT NOT NULL,
	price INTEGER NOT NULL DEFAULT 0,
	amount INTEGER NOT NULL,
	filled INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'open',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_pair_status ON orders(pair, status);

CREATE TABLE IF NOT EXISTS trades (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pair TEXT NOT NULL,
	maker_order_id INTEGER NOT NULL,
	taker_order_id INTEGER NOT NULL,
	maker_user_id INTEGER NOT NULL,
	taker_user_id INTEGER NOT NULL,
	price INTEGER NOT NULL,
	amount INTEGER NOT NULL,
	side TEXT NOT NULL,
	executed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trades_pair_time ON trades(pair, executed_at);

CREATE TABLE IF NOT EXISTS deposits (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	currency TEXT NOT NULL,
	amount INTEGER NOT NULL,
	tx_hash TEXT NOT NULL,
	confirmations INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'pending',
	detected_at INTEGER NOT NULL,
	credited_at INTEGER,
	FOREIGN KEY (user_id) REFERENCES users(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_deposits_tx ON deposits(currency, tx_hash);
CREATE INDEX IF NOT EXISTS idx_deposits_status ON deposits(status);

CREATE TABLE IF NOT EXISTS withdrawals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	currency TEXT NOT NULL,
	amount INTEGER NOT NULL,
	destination TEXT NOT NULL,
	tx_hash TEXT,
	fee INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'pending',
	requested_at INTEGER NOT NULL,
	processed_at INTEGER,
	FOREIGN KEY (user_id) REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS idx_withdrawals_user ON withdrawals(user_id);
CREATE INDEX IF NOT EXISTS idx_withdrawals_status ON withdrawals(status);

CREATE TABLE IF NOT EXISTS nonces (
	nonce TEXT PRIMARY KEY,
	eth_address TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`)
	return err
}

// --- User methods ---

func (d *Database) CreateUser(ethAddr string) (*User, error) {
	now := time.Now().Unix()

	// Get next deposit indices atomically (just use user count as base)
	var count int
	d.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	ethIdx := count
	btcIdx := count

	res, err := d.db.Exec(
		`INSERT INTO users (eth_address, eth_deposit_index, btc_deposit_index, created_at, last_login_at)
		 VALUES (?, ?, ?, ?, ?)`,
		ethAddr, ethIdx, btcIdx, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	// Initialize zero balances
	for _, cur := range []string{"ETH", "BTC", "DLT"} {
		d.db.Exec(`INSERT OR IGNORE INTO balances (user_id, currency, available, locked) VALUES (?, ?, 0, 0)`, id, cur)
	}

	return &User{
		ID: id, ETHAddress: ethAddr,
		ETHDepositIndex: ethIdx, BTCDepositIndex: btcIdx,
		CreatedAt: now, LastLoginAt: now,
	}, nil
}

func (d *Database) GetUserByETH(ethAddr string) (*User, error) {
	u := &User{}
	err := d.db.QueryRow(
		`SELECT id, eth_address, COALESCE(dlt_address,''), eth_deposit_index, btc_deposit_index, created_at, last_login_at
		 FROM users WHERE eth_address = ?`, ethAddr,
	).Scan(&u.ID, &u.ETHAddress, &u.DLTAddress, &u.ETHDepositIndex, &u.BTCDepositIndex, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *Database) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := d.db.QueryRow(
		`SELECT id, eth_address, COALESCE(dlt_address,''), eth_deposit_index, btc_deposit_index, created_at, last_login_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.ETHAddress, &u.DLTAddress, &u.ETHDepositIndex, &u.BTCDepositIndex, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *Database) GetUserByDLT(dltAddr string) (*User, error) {
	u := &User{}
	err := d.db.QueryRow(
		`SELECT id, eth_address, COALESCE(dlt_address,''), eth_deposit_index, btc_deposit_index, created_at, last_login_at
		 FROM users WHERE dlt_address = ?`, dltAddr,
	).Scan(&u.ID, &u.ETHAddress, &u.DLTAddress, &u.ETHDepositIndex, &u.BTCDepositIndex, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *Database) LinkDLTAddress(userID int64, dltAddr string) error {
	_, err := d.db.Exec(`UPDATE users SET dlt_address = ? WHERE id = ?`, dltAddr, userID)
	return err
}

func (d *Database) UpdateLastLogin(userID int64) error {
	_, err := d.db.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), userID)
	return err
}

func (d *Database) GetAllUsers() ([]*User, error) {
	rows, err := d.db.Query(
		`SELECT id, eth_address, COALESCE(dlt_address,''), eth_deposit_index, btc_deposit_index, created_at, last_login_at FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		rows.Scan(&u.ID, &u.ETHAddress, &u.DLTAddress, &u.ETHDepositIndex, &u.BTCDepositIndex, &u.CreatedAt, &u.LastLoginAt)
		users = append(users, u)
	}
	return users, nil
}

// --- Balance methods ---

func (d *Database) GetBalance(userID int64, currency string) (*Balance, error) {
	b := &Balance{UserID: userID, Currency: currency}
	err := d.db.QueryRow(
		`SELECT available, locked FROM balances WHERE user_id = ? AND currency = ?`,
		userID, currency,
	).Scan(&b.Available, &b.Locked)
	if err == sql.ErrNoRows {
		return &Balance{UserID: userID, Currency: currency}, nil
	}
	return b, err
}

func (d *Database) GetAllBalances(userID int64) (map[string]*Balance, error) {
	rows, err := d.db.Query(`SELECT currency, available, locked FROM balances WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]*Balance{}
	for rows.Next() {
		b := &Balance{UserID: userID}
		rows.Scan(&b.Currency, &b.Available, &b.Locked)
		result[b.Currency] = b
	}
	return result, nil
}

// CreditBalance adds amount to available balance. Uses a transaction for atomicity.
func (d *Database) CreditBalance(userID int64, currency string, amount int64) error {
	_, err := d.db.Exec(
		`INSERT INTO balances (user_id, currency, available, locked) VALUES (?, ?, ?, 0)
		 ON CONFLICT(user_id, currency) DO UPDATE SET available = available + ?`,
		userID, currency, amount, amount,
	)
	return err
}

// LockBalance moves amount from available → locked (for open orders).
func (d *Database) LockBalance(userID int64, currency string, amount int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var available int64
	err = tx.QueryRow(`SELECT available FROM balances WHERE user_id = ? AND currency = ?`, userID, currency).Scan(&available)
	if err != nil || available < amount {
		return fmt.Errorf("insufficient %s balance", currency)
	}
	_, err = tx.Exec(
		`UPDATE balances SET available = available - ?, locked = locked + ? WHERE user_id = ? AND currency = ?`,
		amount, amount, userID, currency,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UnlockBalance moves amount from locked → available (order cancel).
func (d *Database) UnlockBalance(userID int64, currency string, amount int64) error {
	_, err := d.db.Exec(
		`UPDATE balances SET available = available + ?, locked = locked - ? WHERE user_id = ? AND currency = ?`,
		amount, amount, userID, currency,
	)
	return err
}

// DebitLocked removes amount from locked balance (trade settlement).
func (d *Database) DebitLocked(userID int64, currency string, amount int64) error {
	_, err := d.db.Exec(
		`UPDATE balances SET locked = locked - ? WHERE user_id = ? AND currency = ?`,
		amount, userID, currency,
	)
	return err
}

// DebitAvailable removes amount from available balance (withdrawal).
func (d *Database) DebitAvailable(userID int64, currency string, amount int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var available int64
	tx.QueryRow(`SELECT available FROM balances WHERE user_id = ? AND currency = ?`, userID, currency).Scan(&available)
	if available < amount {
		return fmt.Errorf("insufficient %s balance", currency)
	}
	tx.Exec(`UPDATE balances SET available = available - ? WHERE user_id = ? AND currency = ?`, amount, userID, currency)
	return tx.Commit()
}

// --- Order methods ---

func (d *Database) CreateOrder(o *Order) error {
	now := time.Now().Unix()
	o.CreatedAt = now
	o.UpdatedAt = now
	res, err := d.db.Exec(
		`INSERT INTO orders (user_id, pair, side, order_type, price, amount, filled, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 'open', ?, ?)`,
		o.UserID, o.Pair, o.Side, o.OrderType, o.Price, o.Amount, now, now,
	)
	if err != nil {
		return err
	}
	o.ID, _ = res.LastInsertId()
	return nil
}

func (d *Database) GetOrder(id int64) (*Order, error) {
	o := &Order{}
	err := d.db.QueryRow(
		`SELECT id, user_id, pair, side, order_type, price, amount, filled, status, created_at, updated_at
		 FROM orders WHERE id = ?`, id,
	).Scan(&o.ID, &o.UserID, &o.Pair, &o.Side, &o.OrderType, &o.Price, &o.Amount, &o.Filled, &o.Status, &o.CreatedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return o, err
}

func (d *Database) UpdateOrder(o *Order) error {
	o.UpdatedAt = time.Now().Unix()
	_, err := d.db.Exec(
		`UPDATE orders SET filled = ?, status = ?, updated_at = ? WHERE id = ?`,
		o.Filled, o.Status, o.UpdatedAt, o.ID,
	)
	return err
}

// GetOpenOrders returns open/partial orders for a pair+side, sorted for matching.
// Buy side: price DESC (highest bids first), then time ASC
// Sell side: price ASC (lowest asks first), then time ASC
func (d *Database) GetOpenOrders(pair, side string) ([]*Order, error) {
	orderClause := "price ASC, created_at ASC"
	if side == "buy" {
		orderClause = "price DESC, created_at ASC"
	}
	rows, err := d.db.Query(
		`SELECT id, user_id, pair, side, order_type, price, amount, filled, status, created_at, updated_at
		 FROM orders WHERE pair = ? AND side = ? AND status IN ('open','partial')
		 ORDER BY `+orderClause, pair, side,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		o := &Order{}
		rows.Scan(&o.ID, &o.UserID, &o.Pair, &o.Side, &o.OrderType, &o.Price, &o.Amount, &o.Filled, &o.Status, &o.CreatedAt, &o.UpdatedAt)
		orders = append(orders, o)
	}
	return orders, nil
}

func (d *Database) GetUserOrders(userID int64) ([]*Order, error) {
	rows, err := d.db.Query(
		`SELECT id, user_id, pair, side, order_type, price, amount, filled, status, created_at, updated_at
		 FROM orders WHERE user_id = ? ORDER BY created_at DESC LIMIT 100`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		o := &Order{}
		rows.Scan(&o.ID, &o.UserID, &o.Pair, &o.Side, &o.OrderType, &o.Price, &o.Amount, &o.Filled, &o.Status, &o.CreatedAt, &o.UpdatedAt)
		orders = append(orders, o)
	}
	return orders, nil
}

// --- Trade methods ---

func (d *Database) RecordTrade(t *Trade) error {
	t.ExecutedAt = time.Now().Unix()
	res, err := d.db.Exec(
		`INSERT INTO trades (pair, maker_order_id, taker_order_id, maker_user_id, taker_user_id, price, amount, side, executed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Pair, t.MakerOrderID, t.TakerOrderID, t.MakerUserID, t.TakerUserID, t.Price, t.Amount, t.Side, t.ExecutedAt,
	)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

func (d *Database) GetRecentTrades(pair string, limit int) ([]*Trade, error) {
	rows, err := d.db.Query(
		`SELECT id, pair, maker_order_id, taker_order_id, maker_user_id, taker_user_id, price, amount, side, executed_at
		 FROM trades WHERE pair = ? ORDER BY executed_at DESC LIMIT ?`, pair, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var trades []*Trade
	for rows.Next() {
		t := &Trade{}
		rows.Scan(&t.ID, &t.Pair, &t.MakerOrderID, &t.TakerOrderID, &t.MakerUserID, &t.TakerUserID, &t.Price, &t.Amount, &t.Side, &t.ExecutedAt)
		trades = append(trades, t)
	}
	return trades, nil
}

func (d *Database) Get24hStats(pair string) (lastPrice, high, low, volume int64, err error) {
	since := time.Now().Unix() - 86400
	err = d.db.QueryRow(
		`SELECT COALESCE(MAX(price),0), COALESCE(MIN(price),0), COALESCE(SUM(amount),0)
		 FROM trades WHERE pair = ? AND executed_at >= ?`, pair, since,
	).Scan(&high, &low, &volume)
	if err != nil {
		return
	}
	d.db.QueryRow(`SELECT COALESCE(price,0) FROM trades WHERE pair = ? ORDER BY executed_at DESC LIMIT 1`, pair).Scan(&lastPrice)
	return
}

// --- Deposit methods ---

func (d *Database) CreateDeposit(dep *Deposit) error {
	dep.DetectedAt = time.Now().Unix()
	res, err := d.db.Exec(
		`INSERT OR IGNORE INTO deposits (user_id, currency, amount, tx_hash, confirmations, status, detected_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		dep.UserID, dep.Currency, dep.Amount, dep.TxHash, dep.Confirmations, dep.DetectedAt,
	)
	if err != nil {
		return err
	}
	dep.ID, _ = res.LastInsertId()
	return nil
}

func (d *Database) UpdateDepositConfirmations(currency, txHash string, confirmations int) error {
	_, err := d.db.Exec(
		`UPDATE deposits SET confirmations = ?, status = CASE WHEN ? >= 1 THEN 'confirmed' ELSE status END
		 WHERE currency = ? AND tx_hash = ? AND status = 'pending'`,
		confirmations, confirmations, currency, txHash,
	)
	return err
}

func (d *Database) GetConfirmedDeposits() ([]*Deposit, error) {
	rows, err := d.db.Query(
		`SELECT id, user_id, currency, amount, tx_hash, confirmations, status, detected_at, COALESCE(credited_at,0)
		 FROM deposits WHERE status = 'confirmed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []*Deposit
	for rows.Next() {
		dep := &Deposit{}
		rows.Scan(&dep.ID, &dep.UserID, &dep.Currency, &dep.Amount, &dep.TxHash, &dep.Confirmations, &dep.Status, &dep.DetectedAt, &dep.CreditedAt)
		deps = append(deps, dep)
	}
	return deps, nil
}

func (d *Database) CreditDeposit(id int64, userID int64, currency string, amount int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	res, err := tx.Exec(
		`UPDATE deposits SET status = 'credited', credited_at = ? WHERE id = ? AND status = 'confirmed'`,
		now, id,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil // already credited
	}
	_, err = tx.Exec(
		`INSERT INTO balances (user_id, currency, available, locked) VALUES (?, ?, ?, 0)
		 ON CONFLICT(user_id, currency) DO UPDATE SET available = available + ?`,
		userID, currency, amount, amount,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) DepositExists(currency, txHash string) (bool, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM deposits WHERE currency = ? AND tx_hash = ?`, currency, txHash).Scan(&count)
	return count > 0, err
}

// --- Withdrawal methods ---

func (d *Database) CreateWithdrawal(w *Withdrawal) error {
	w.RequestedAt = time.Now().Unix()
	res, err := d.db.Exec(
		`INSERT INTO withdrawals (user_id, currency, amount, destination, fee, status, requested_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		w.UserID, w.Currency, w.Amount, w.Destination, w.Fee, w.RequestedAt,
	)
	if err != nil {
		return err
	}
	w.ID, _ = res.LastInsertId()
	return nil
}

func (d *Database) GetPendingWithdrawals() ([]*Withdrawal, error) {
	rows, err := d.db.Query(
		`SELECT id, user_id, currency, amount, destination, COALESCE(tx_hash,''), fee, status, requested_at
		 FROM withdrawals WHERE status = 'pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ws []*Withdrawal
	for rows.Next() {
		w := &Withdrawal{}
		rows.Scan(&w.ID, &w.UserID, &w.Currency, &w.Amount, &w.Destination, &w.TxHash, &w.Fee, &w.Status, &w.RequestedAt)
		ws = append(ws, w)
	}
	return ws, nil
}

func (d *Database) UpdateWithdrawalStatus(id int64, status, txHash string) error {
	now := time.Now().Unix()
	_, err := d.db.Exec(
		`UPDATE withdrawals SET status = ?, tx_hash = ?, processed_at = ? WHERE id = ?`,
		status, txHash, now, id,
	)
	return err
}

// --- Nonce methods (SIWE) ---

func (d *Database) StoreNonce(nonce, ethAddr string, expiresAt int64) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO nonces (nonce, eth_address, expires_at) VALUES (?, ?, ?)`,
		nonce, ethAddr, expiresAt,
	)
	return err
}

func (d *Database) ConsumeNonce(nonce, ethAddr string) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var storedAddr string
	var expiresAt int64
	err = tx.QueryRow(`SELECT eth_address, expires_at FROM nonces WHERE nonce = ?`, nonce).Scan(&storedAddr, &expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if storedAddr != ethAddr || time.Now().Unix() > expiresAt {
		tx.Exec(`DELETE FROM nonces WHERE nonce = ?`, nonce)
		tx.Commit()
		return false, nil
	}
	tx.Exec(`DELETE FROM nonces WHERE nonce = ?`, nonce)
	return true, tx.Commit()
}

func (d *Database) CleanExpiredNonces() error {
	_, err := d.db.Exec(`DELETE FROM nonces WHERE expires_at < ?`, time.Now().Unix())
	return err
}

// --- Meta (last-seen block tracking) ---

func (d *Database) GetMeta(key string) (string, error) {
	var val string
	err := d.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (d *Database) SetMeta(key, value string) error {
	_, err := d.db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, value)
	return err
}
