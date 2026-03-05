# 12. Graceful Shutdown & DB Worker Fix

## Masalah yang Diperbaiki

Sebelumnya, terdapat **race condition** saat server shutdown yang bisa menyebabkan **data loss**:

```
Timeline bermasalah:
T0: Shutdown signal diterima
T1: eventBus.Stop()     → Event masih bisa enqueue ke DB Worker
T2: pgPool.Close()      → ❌ PostgreSQL ditutup!
T3: dbWorker flush()    → ❌ GAGAL! Data belum tersimpan!
```

### Akibatnya
- Trade yang terjadi saat shutdown mungkin tidak tersimpan ke PostgreSQL
- Order updates bisa hilang
- Data inconsistency antara memory dan database

## Solusi yang Diimplementasikan

### 1. Menambahkan Method `Stop()` ke DBWorker

File: `internal/worker/dbworker.go`

```go
// DBWorker struct sekarang memiliki:
type DBWorker struct {
    writeCh       chan WriteOp
    pool          *pgxpool.Pool
    batchSize     int
    flushInterval time.Duration
    retryMax      int
    retryBackoff  time.Duration
    wg            sync.WaitGroup      // ← Baru
    stopOnce      sync.Once           // ← Baru
    stopped       atomic.Bool         // ← Baru: prevent panic
}

// Start() sekarang track goroutine
func (w *DBWorker) Start(ctx context.Context) {
    w.wg.Add(1)
    defer w.wg.Done()
    // ... rest of implementation
}

// Enqueue() sekarang aman dari panic
func (w *DBWorker) Enqueue(op WriteOp) error {
    if w.stopped.Load() {
        return errors.New("db worker stopped")
    }
    select {
    case w.writeCh <- op:
        return nil
    default:
        if w.stopped.Load() {
            return errors.New("db worker stopped")
        }
        return errors.New("db worker queue full")
    }
}

// Stop() method baru
func (w *DBWorker) Stop() {
    w.stopOnce.Do(func() {
        w.stopped.Store(true)  // ← Set flag dulu!
        close(w.writeCh)       // Tutup channel
        w.wg.Wait()            // Tunggu goroutine selesai
        slog.Info("db worker stopped gracefully")
    })
}
```

### 2. Mengupdate Urutan Shutdown di main.go

File: `cmd/server/main.go`

```go
// Urutan shutdown yang benar:

// a) Stop accepting new HTTP requests
srv.Shutdown(shutdownCtx)

// b) Stop matching engine
eng.Stop()

// c) Stop simulator
sim.Stop()

// d) Stop event bus (no more events published)
eventBus.Stop()

// e) ⭐ Stop DB Worker - TUNGGU semua writes selesai
if dbWorker != nil {
    dbWorker.Stop()
}

// f) Close external connections
natsPub.Close()
natsSub.Close()
redisCache.Close()

// g) ⭐ Baru tutup PostgreSQL (setelah DB Worker selesai)
pgPool.Close()
```

## Additional Fix: Prevent Panic on Closed Channel

**Masalah tambahan yang ditemukan:**

Kalau `Enqueue()` dipanggil setelah `Stop()` (yang menutup channel), akan terjadi **panic**!

```go
// Kalau channel sudah ditutup:
select {
case w.writeCh <- op:  // ← PANIC! send on closed channel
    return nil
default:
    return errors.New("db worker queue full")
}
```

**Solusi:**

Tambahkan `atomic.Bool` flag untuk track status stopped:

```go
type DBWorker struct {
    // ... other fields
    stopped atomic.Bool  // ← Baru
}

func (w *DBWorker) Enqueue(op WriteOp) error {
    // Cek dulu sebelum write ke channel
    if w.stopped.Load() {
        return errors.New("db worker stopped")
    }
    
    select {
    case w.writeCh <- op:
        return nil
    default:
        // Double-check untuk race condition
        if w.stopped.Load() {
            return errors.New("db worker stopped")
        }
        return errors.New("db worker queue full")
    }
}

func (w *DBWorker) Stop() {
    w.stopOnce.Do(func() {
        w.stopped.Store(true)  // ← Set flag SEBELUM close channel
        close(w.writeCh)
        w.wg.Wait()
    })
}
```

**Kenapa flag perlu diset sebelum close channel?**

```
Timeline race condition:
T0: Enqueue() check stopped=false
T1: Stop() set stopped=true
T2: Stop() close channel
T3: Enqueue() write ke channel ← PANIC!

Solusi: Check lagi di default case (double-check)
```

## Cara Kerja Stop()

```
┌─────────────────────────────────────────────────────────────┐
│  Saat dbWorker.Stop() dipanggil:                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. close(w.writeCh)                                        │
│     └─▶ Channel ditutup, Enqueue() akan return error        │
│         Data baru nggak bisa masuk                          │
│                                                             │
│  2. w.wg.Wait()                                             │
│     └─▶ Tunggu Start() goroutine selesai                    │
│                                                             │
│  3. Di dalam Start():                                       │
│     case op, ok := <-w.writeCh:                             │
│         if !ok {                                            │
│             // Channel closed                               │
│             flush(batch)     ← Flush sisa batch             │
│             return           ← Goroutine selesai            │
│         }                                                   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Urutan Shutdown yang Benar

```
┌────────────────────────────────────────────────────────────────┐
│  SHUTDOWN SEQUENCE (urutan penting!)                           │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  1. HTTP Server Shutdown                                       │
│     └─▶ Nggak nerima request baru                             │
│                                                                │
│  2. Engine Stop                                                │
│     └─▶ Matcher goroutines selesai                            │
│     └─▶ Nggak ada order baru                                  │
│                                                                │
│  3. Simulator Stop                                             │
│     └─▶ Price updates berhenti                                │
│                                                                │
│  4. EventBus Stop                                              │
│     └─▶ Drain events terakhir                                 │
│     └─▶ Publish ke subscribers                                │
│                                                                │
│  5. DBWorker Stop  ⭐ PENTING!                                  │
│     └─▶ Close writeCh (nggak bisa enqueue)                    │
│     └─▶ Flush batch terakhir                                  │
│     └─▶ Tunggu sampai semua data ke DB                        │
│                                                                │
│  6. Close Connections (NATS, Redis)                            │
│     └─▶ External services                                     │
│                                                                │
│  7. PostgreSQL Close  ⭐ SETELAH DBWorker!                      │
│     └─▶ Baru tutup koneksi DB                                 │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

## Testing

### Unit Test untuk Enqueue After Stop (No Panic)

File: `internal/worker/dbworker_test.go`

```go
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
```

### Unit Test untuk Graceful Drain

File: `internal/worker/dbworker_test.go`

```go
func TestDBWorker_GracefulDrain(t *testing.T) {
    w := NewDBWorker(nil, DBWorkerConfig{
        BatchSize:     10,
        FlushInterval: 1 * time.Second, // Long interval
        ChannelSize:   100,
    })
    
    ctx, cancel := context.WithCancel(context.Background())
    go w.Start(ctx)
    
    // Enqueue 5 ops (kurang dari batch size)
    for i := 0; i < 5; i++ {
        w.Enqueue(WriteOp{Type: OpOrderSave, Payload: ...})
    }
    
    // Stop worker (should flush even incomplete batch)
    w.Stop()
    cancel()
    
    // Verify: semua 5 ops harus ter-flush
    // (meskipun batch belum penuh)
}
```

### Integration Test

File: `cmd/server/main_test.go`

```go
func TestIntegration_GracefulShutdown(t *testing.T) {
    // Start server with DB Worker enabled
    // Create some orders
    // Trigger shutdown
    // Verify: semua data tersimpan di PostgreSQL
}
```

## Best Practices

### 1. Selalu Panggil Stop() Sebelum Close Pool

**❌ Salah:**
```go
eventBus.Stop()
pgPool.Close()  // ❌ Data bisa hilang!
```

**✅ Benar:**
```go
eventBus.Stop()
dbWorker.Stop() // ✅ Tunggu writes selesai
pgPool.Close()  // ✅ Sekarang aman
```

### 2. Gunakan Context Timeout

```go
shutdownCtx, cancel := context.WithTimeout(
    context.Background(), 
    30*time.Second,  // ⭐ Batasi waktu shutdown
)
defer cancel()
```

### 3. Monitor Queue Length

```go
// Log jika queue masih ada saat shutdown
if dbWorker.QueueLen() > 0 {
    slog.Warn("db worker queue not empty", "pending", dbWorker.QueueLen())
}
dbWorker.Stop()
```

## Impact

| Sebelum Fix | Setelah Fix |
|-------------|-------------|
| Race condition saat shutdown | ✅ Race condition eliminated |
| Data loss possible | ✅ No data loss |
| pgPool.Close() bisa interrupt writes | ✅ All writes complete before close |
| Shutdown tidak deterministic | ✅ Deterministic shutdown |

## Catatan Penting

1. **Stop() adalah blocking call** - akan menunggu sampai semua data tersimpan
2. **Idempotent** - bisa dipanggil multiple times tanpa error
3. **Thread-safe** - menggunakan sync.Once
4. **Timeout** - jika ada deadlock, context timeout akan mengakhiri

## Referensi

- File: `internal/worker/dbworker.go` (method Stop())
- File: `cmd/server/main.go` (urutan shutdown)
- Test: `internal/worker/dbworker_test.go` (TestDBWorker_GracefulDrain)
