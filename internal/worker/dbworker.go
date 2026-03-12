// Package worker provides the DB Worker: a dedicated goroutine that batches
// write operations and flushes them to PostgreSQL asynchronously.
//
// Design goals:
//   - Decouple the hot path (matching engine, HTTP handlers) from PG latency
//   - Batch up to 100 ops or flush every 100 ms, whichever comes first
//   - Retry failed batches up to RetryMax times with exponential back-off
//   - On graceful shutdown, drain the channel and flush remaining ops
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// OpType identifies the kind of write operation.
type OpType string

const (
	OpOrderSave    OpType = "order_save"
	OpOrderUpdate  OpType = "order_update"
	OpTradeSave    OpType = "trade_save"
	OpTickerUpdate OpType = "ticker_update"
)

// WriteOp is a single unit of work for the DB Worker.
type WriteOp struct {
	Type    OpType
	Payload interface{} // *entity.Order | *entity.Trade | *entity.Ticker
}

// DBWorker batches write operations and persists them to PostgreSQL.
type DBWorker struct {
	writeCh       chan WriteOp
	pool          *pgxpool.Pool
	batchSize     int
	flushInterval time.Duration
	retryMax      int
	retryBackoff  time.Duration
	wg            sync.WaitGroup
	stopOnce      sync.Once
	stopped       atomic.Bool // ← Baru: flag untuk cek status
}

// DBWorkerConfig holds the configuration for a DBWorker.
type DBWorkerConfig struct {
	BatchSize     int
	FlushInterval time.Duration
	ChannelSize   int
	RetryMax      int
	RetryBackoff  time.Duration
}

// DefaultDBWorkerConfig returns sensible production defaults.
func DefaultDBWorkerConfig() DBWorkerConfig {
	return DBWorkerConfig{
		BatchSize:     100,
		FlushInterval: 100 * time.Millisecond,
		ChannelSize:   5000,
		RetryMax:      3,
		RetryBackoff:  time.Second,
	}
}

// NewDBWorker creates a DBWorker.  pool may be nil during testing to use the
// no-op flush path.
func NewDBWorker(pool *pgxpool.Pool, cfg DBWorkerConfig) *DBWorker {
	w := &DBWorker{
		writeCh:       make(chan WriteOp, cfg.ChannelSize),
		pool:          pool,
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
		retryMax:      cfg.RetryMax,
		retryBackoff:  cfg.RetryBackoff,
	}
	// Pre-increment the WaitGroup before Start() is launched so that Stop()
	// can safely call wg.Wait() at any time without racing against wg.Add.
	w.wg.Add(1)
	return w
}

// Enqueue submits a WriteOp to the worker's channel without blocking.
// Returns an error if the channel is full or the worker is stopped.
func (w *DBWorker) Enqueue(op WriteOp) (err error) {
	// Fast path: worker already stopped before we try anything.
	if w.stopped.Load() {
		return errors.New("db worker stopped")
	}

	// Guard against the race where Stop() closes writeCh between the
	// stopped.Load() check above and the channel send below.
	// A send on a closed channel panics; we recover and return an error.
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("db worker stopped")
		}
	}()

	select {
	case w.writeCh <- op:
		return nil
	default:
		return errors.New("db worker queue full")
	}
}

// QueueLen returns the current number of pending operations.
func (w *DBWorker) QueueLen() int {
	return len(w.writeCh)
}

// Start runs the worker loop until ctx is cancelled or Stop() is called.
// It is designed to run in its own goroutine.
func (w *DBWorker) Start(ctx context.Context) {
	// wg.Add(1) was called in NewDBWorker; defer the corresponding Done here.
	defer w.wg.Done()

	batch := make([]WriteOp, 0, w.batchSize)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	// Use a background context for storage writes so that a cancelled ctx
	// (shutdown signal) does not abort in-flight flush operations.
	flushCtx := context.Background()

	flush := func(ops []WriteOp) {
		if len(ops) == 0 {
			return
		}
		if err := w.flushWithRetry(flushCtx, ops); err != nil {
			slog.Error("db worker flush failed", "error", err, "ops", len(ops))
		}
	}

	for {
		select {
		case op, ok := <-w.writeCh:
			if !ok {
				// Channel closed by Stop() — drain complete, flush and exit.
				flush(batch)
				return
			}
			batch = append(batch, op)
			if len(batch) >= w.batchSize {
				flush(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			flush(batch)
			batch = batch[:0]

		case <-ctx.Done():
			// Context cancelled — signal Stop() to close the channel so we
			// drain via the channel-close path above. Do NOT return here;
			// items may still be enqueued between now and the close.
			// Stop() is idempotent so calling it here is safe.
			go w.Stop()
		}
	}
}

// flushWithRetry attempts to persist ops, retrying on transient failures.
func (w *DBWorker) flushWithRetry(ctx context.Context, ops []WriteOp) error {
	var lastErr error
	backoff := w.retryBackoff
	for attempt := 0; attempt <= w.retryMax; attempt++ {
		if attempt > 0 {
			slog.Warn("db worker retry", "attempt", attempt, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
		}
		if err := w.flush(ctx, ops); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("after %d retries: %w", w.retryMax, lastErr)
}

// flush persists a batch of ops using a pgx.Batch for efficiency.
func (w *DBWorker) flush(ctx context.Context, ops []WriteOp) error {
	if w.pool == nil {
		// No-op in memory-only mode (pool not configured).
		return nil
	}

	batch := &pgx.Batch{}
	for _, op := range ops {
		switch op.Type {
		case OpOrderSave, OpOrderUpdate:
			o, ok := op.Payload.(*entity.Order)
			if !ok {
				continue
			}
			batch.Queue(`
				INSERT INTO orders (id, user_id, stock_code, side, price, quantity,
				                    filled_quantity, status, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (id) DO UPDATE
				  SET filled_quantity = EXCLUDED.filled_quantity,
				      status          = EXCLUDED.status,
				      updated_at      = EXCLUDED.updated_at`,
				o.ID, o.UserID, o.StockCode, string(o.Side),
				o.Price, o.Quantity, o.FilledQuantity, string(o.Status),
				o.CreatedAt, o.UpdatedAt,
			)

		case OpTradeSave:
			t, ok := op.Payload.(*entity.Trade)
			if !ok {
				continue
			}
			batch.Queue(`
				INSERT INTO trades (id, stock_code, buy_order_id, sell_order_id, price,
				                    quantity, buyer_user_id, seller_user_id, executed_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (id) DO NOTHING`,
				t.ID, t.StockCode, t.BuyOrderID, t.SellOrderID,
				t.Price, t.Quantity, t.BuyerUserID, t.SellerUserID, t.ExecutedAt,
			)

		case OpTickerUpdate:
			// Tickers are derived data; we skip PG persistence to keep
			// schema simple. They live in Redis cache instead.
		}
	}

	if batch.Len() == 0 {
		return nil
	}

	results := w.pool.SendBatch(ctx, batch)
	defer results.Close()

	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("batch exec at index %d: %w", i, err)
		}
	}
	return nil
}

// Stop signals the worker to shut down and waits for all pending operations
// to be flushed to PostgreSQL. It is safe to call multiple times.
// This method must be called before closing the PostgreSQL connection pool.
func (w *DBWorker) Stop() {
	w.stopOnce.Do(func() {
		// Set flag dulu sebelum close channel
		// Biar Enqueue() yang dipanggil concurrently bisa lihat stopped=true
		w.stopped.Store(true)
		close(w.writeCh)
		w.wg.Wait()
		slog.Info("db worker stopped gracefully")
	})
}
