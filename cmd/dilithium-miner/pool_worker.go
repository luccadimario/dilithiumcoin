package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// PoolClient connects to a pool server as a worker
type PoolClient struct {
	poolAddr string
	address  string // Worker's payout address
	threads  int
	conn     net.Conn
	stopCh   chan struct{}
	wg       sync.WaitGroup

	// Current mining state
	miningMu   sync.Mutex
	cancelCh   chan struct{} // close to cancel current mining
	totalHashes int64
	shares      int64
	startTime   time.Time
}

// NewPoolClient creates a new pool worker client
func NewPoolClient(poolAddr, address string, threads int) *PoolClient {
	return &PoolClient{
		poolAddr: poolAddr,
		address:  address,
		threads:  threads,
		stopCh:   make(chan struct{}),
	}
}

// Start connects to the pool and begins mining
func (pc *PoolClient) Start() {
	pc.startTime = time.Now()
	pc.wg.Add(1)
	go pc.run()
}

// Stop disconnects from the pool
func (pc *PoolClient) Stop() {
	close(pc.stopCh)
	if pc.conn != nil {
		pc.conn.Close()
	}
	// Cancel any current mining
	pc.miningMu.Lock()
	if pc.cancelCh != nil {
		select {
		case <-pc.cancelCh:
		default:
			close(pc.cancelCh)
		}
	}
	pc.miningMu.Unlock()
	pc.wg.Wait()

	hashes := atomic.LoadInt64(&pc.totalHashes)
	shares := atomic.LoadInt64(&pc.shares)
	elapsed := time.Since(pc.startTime).Seconds()
	if elapsed > 0 {
		fmt.Printf("Pool worker stats: %d hashes, %d shares, %.0f H/s avg\n",
			hashes, shares, float64(hashes)/elapsed)
	}
}

func (pc *PoolClient) run() {
	defer pc.wg.Done()

	for {
		select {
		case <-pc.stopCh:
			return
		default:
		}

		err := pc.connectAndWork()
		if err != nil {
			fmt.Printf("Pool connection error: %v\n", err)
		}

		// Reconnect after delay
		select {
		case <-pc.stopCh:
			return
		case <-time.After(5 * time.Second):
			fmt.Println("Reconnecting to pool...")
		}
	}
}

func (pc *PoolClient) connectAndWork() error {
	var err error
	pc.conn, err = net.DialTimeout("tcp", pc.poolAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to pool: %w", err)
	}
	defer pc.conn.Close()

	fmt.Printf("Connected to pool at %s\n", pc.poolAddr)

	// Send registration message
	if err := pc.sendRegistration(); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	scanner := bufio.NewScanner(pc.conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-pc.stopCh:
			return nil
		default:
		}

		var msg PoolMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "work":
			pc.handleWork(&msg)
		case "stats":
			if msg.Earnings != "" {
				fmt.Printf("Pool: %d workers | %d blocks found | your shares: %d | earnings: %s | fee: %s\n",
					msg.Workers, msg.Found, msg.Shares, msg.Earnings, msg.PoolFee)
			} else {
				fmt.Printf("Pool: %d workers | %d blocks found | your shares: %d\n",
					msg.Workers, msg.Found, msg.Shares)
			}
		}
	}

	return fmt.Errorf("pool connection closed")
}

func (pc *PoolClient) sendRegistration() error {
	msg := PoolMessage{
		Type:    "register",
		Address: pc.address,
		Threads: pc.threads,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	pc.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = pc.conn.Write(data)
	if err != nil {
		return err
	}

	fmt.Printf("Registered with pool: address=%s, threads=%d\n", pc.address, pc.threads)
	return nil
}

func (pc *PoolClient) handleWork(msg *PoolMessage) {
	// Cancel any existing mining
	pc.miningMu.Lock()
	if pc.cancelCh != nil {
		select {
		case <-pc.cancelCh:
		default:
			close(pc.cancelCh)
		}
	}
	pc.cancelCh = make(chan struct{})
	cancelCh := pc.cancelCh
	pc.miningMu.Unlock()

	if msg.Template == nil {
		return
	}

	fmt.Printf("Received work: block #%d (difficulty bits: %d, share bits: %d)\n",
		msg.Template.Index, msg.Template.DifficultyBits, msg.ShareBits)

	// Mine in background
	pc.wg.Add(1)
	go func() {
		defer pc.wg.Done()
		pc.mineForPool(msg, cancelCh)
	}()
}

func (pc *PoolClient) mineForPool(msg *PoolMessage, cancelCh chan struct{}) {
	template := msg.Template

	// Build coinbase + transactions
	coinbase := &Transaction{
		From:      "SYSTEM",
		To:        msg.Address,
		Amount:    template.Reward,
		Timestamp: time.Now().Unix(),
		Signature: fmt.Sprintf("coinbase-%d-%d", template.Index, time.Now().UnixNano()),
	}

	txs := make([]*Transaction, 0, len(msg.Txs)+1)
	txs = append(txs, coinbase)
	txs = append(txs, msg.Txs...)

	baseBlock := &Block{
		Index:          template.Index,
		Timestamp:      time.Now().Unix(),
		Transactions:   txs,
		PreviousHash:   template.PreviousHash,
		Difficulty:     template.Difficulty,
		DifficultyBits: template.DifficultyBits,
	}

	useBits := template.DifficultyBits > 0
	hashPrefix := strings.Repeat("0", template.Difficulty)
	shareBits := msg.ShareBits

	resultCh := make(chan *Block, 1)
	shareCh := make(chan *Block, 16)
	var mineCancel chan struct{} = make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < pc.threads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			nonce := int64(threadID)
			var localHashes int64

			threadBlock := &Block{
				Index:          baseBlock.Index,
				Timestamp:      baseBlock.Timestamp,
				Transactions:   baseBlock.Transactions,
				PreviousHash:   baseBlock.PreviousHash,
				Difficulty:     baseBlock.Difficulty,
				DifficultyBits: baseBlock.DifficultyBits,
			}

			for {
				select {
				case <-mineCancel:
					atomic.AddInt64(&pc.totalHashes, localHashes)
					return
				case <-cancelCh:
					atomic.AddInt64(&pc.totalHashes, localHashes)
					return
				case <-pc.stopCh:
					atomic.AddInt64(&pc.totalHashes, localHashes)
					return
				default:
				}

				threadBlock.Nonce = nonce
				hash := calculateBlockHash(threadBlock)
				localHashes++

				// Check full difficulty first
				var meetsFullDifficulty bool
				if useBits {
					meetsFullDifficulty = meetsDifficultyBits(hash, template.DifficultyBits)
				} else {
					meetsFullDifficulty = strings.HasPrefix(hash, hashPrefix)
				}

				if meetsFullDifficulty {
					threadBlock.Hash = hash
					atomic.AddInt64(&pc.totalHashes, localHashes)
					select {
					case resultCh <- threadBlock:
						close(mineCancel)
					default:
					}
					return
				}

				// Check share difficulty
				if meetsDifficultyBits(hash, shareBits) {
					shareBlock := &Block{
						Index: threadBlock.Index,
						Nonce: nonce,
						Hash:  hash,
					}
					select {
					case shareCh <- shareBlock:
					default:
					}
				}

				nonce += int64(pc.threads)

				if localHashes%100000 == 0 {
					atomic.AddInt64(&pc.totalHashes, localHashes)
					localHashes = 0
				}
			}
		}(i)
	}

	// Share submitter goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case share := <-shareCh:
				pc.submitShare(share)
			case <-mineCancel:
				// Drain remaining shares
				for {
					select {
					case share := <-shareCh:
						pc.submitShare(share)
					default:
						return
					}
				}
			case <-cancelCh:
				return
			case <-pc.stopCh:
				return
			}
		}
	}()

	// Wait for block found or cancellation
	select {
	case block := <-resultCh:
		if block != nil {
			fmt.Printf("BLOCK FOUND! #%d hash: %s\n", block.Index, block.Hash[:16])
			pc.submitBlockToPool(block)
		}
	case <-cancelCh:
		close(mineCancel)
	case <-pc.stopCh:
		close(mineCancel)
	}

	wg.Wait()
}

func (pc *PoolClient) submitShare(share *Block) {
	msg := PoolMessage{
		Type:  "share",
		Nonce: share.Nonce,
		Hash:  share.Hash,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	data = append(data, '\n')

	if pc.conn != nil {
		pc.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		pc.conn.Write(data)
		atomic.AddInt64(&pc.shares, 1)
	}
}

func (pc *PoolClient) submitBlockToPool(block *Block) {
	msg := PoolMessage{
		Type:  "block",
		Nonce: block.Nonce,
		Hash:  block.Hash,
		Block: block,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	data = append(data, '\n')

	if pc.conn != nil {
		pc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		pc.conn.Write(data)
	}
}
