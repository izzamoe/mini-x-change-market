package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// newTestWorker returns a DBWorker with no PG pool (no-op flush) and a fast
// flush interval for deterministic testing.
func newTestWorker(batchSize int, flushInterval time.Duration) *DBWorker {
	return NewDBWorker(nil, DBWorkerConfig{
		BatchSize:     batchSize,
		FlushInterval: flushInterval,
		ChannelSize:   1000,
		RetryMax:      0,
		RetryBackoff:  time.Millisecond,
	})
}

func TestDBWorker_EnqueueAndFlushByInterval(t *testing.T) {
	w := newTestWorker(100, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	order := &entity.Order{ID: "o1", StockCode: "BBCA", Status: entity.OrderStatusOpen}
	require.NoError(t, w.Enqueue(WriteOp{Type: OpOrderSave, Payload: order}))

	// Wait for flush interval to fire.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, w.QueueLen(), "queue should be empty after flush")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestDBWorker_FlushOnBatchFull(t *testing.T) {
	const batchSize = 5
	w := newTestWorker(batchSize, time.Hour) // very long interval — flush only on batch full

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()

	order := &entity.Order{ID: "ox", StockCode: "BBCA", Status: entity.OrderStatusOpen}
	for i := 0; i < batchSize; i++ {
		require.NoError(t, w.Enqueue(WriteOp{Type: OpOrderSave, Payload: order}))
	}

	// Give the worker loop a moment to process.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, w.QueueLen())
}

func TestDBWorker_GracefulDrain(t *testing.T) {
	w := newTestWorker(100, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	// Enqueue a few ops, then cancel — all must be processed before Start returns.
	for i := 0; i < 10; i++ {
		_ = w.Enqueue(WriteOp{Type: OpOrderSave, Payload: &entity.Order{ID: "drain"}})
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not drain and stop")
	}
	assert.Equal(t, 0, w.QueueLen())
}

func TestDBWorker_QueueFullReturnsError(t *testing.T) {
	w := NewDBWorker(nil, DBWorkerConfig{
		BatchSize:     100,
		FlushInterval: time.Hour,
		ChannelSize:   1, // very small to force back-pressure
		RetryMax:      0,
		RetryBackoff:  time.Millisecond,
	})

	order := &entity.Order{ID: "o1"}
	// Fill the channel.
	_ = w.Enqueue(WriteOp{Type: OpOrderSave, Payload: order})
	// Next enqueue must fail.
	err := w.Enqueue(WriteOp{Type: OpOrderSave, Payload: order})
	assert.Error(t, err)
}

func TestDBWorker_ConcurrentEnqueue(t *testing.T) {
	w := newTestWorker(50, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	var enqueued atomic.Int64
	const goroutines = 10
	const opsPerGoroutine = 20
	errs := make(chan error, goroutines*opsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < opsPerGoroutine; i++ {
				if err := w.Enqueue(WriteOp{
					Type:    OpTradeSave,
					Payload: &entity.Trade{ID: "t1"},
				}); err == nil {
					enqueued.Add(1)
				} else {
					errs <- err
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	// Verify no panics — exact count varies due to queue size.
	t.Logf("enqueued %d / %d ops", enqueued.Load(), goroutines*opsPerGoroutine)
}

func TestDBWorker_EnqueueAfterStop(t *testing.T) {
	w := newTestWorker(10, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	// Enqueue some ops
	order := &entity.Order{ID: "o1"}
	err := w.Enqueue(WriteOp{Type: OpOrderSave, Payload: order})
	require.NoError(t, err)

	// Stop the worker
	w.Stop()
	cancel()
	<-done

	// Try to enqueue after stop - should return error, not panic
	err = w.Enqueue(WriteOp{Type: OpOrderSave, Payload: order})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stopped")
}
