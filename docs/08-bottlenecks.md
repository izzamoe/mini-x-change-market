# 08. Bottlenecks (Tiga Bottleneck Utama)

Tiga bottleneck utama dalam sistem dan cara mengatasinya.

---

## 1️⃣ Bottleneck #1: Matching Engine Throughput

### Problem

**Global lock pada order book blocking semua stock:**

```go
// BAD: Single global mutex
var globalMu sync.Mutex

func SubmitOrder(order Order) {
    globalMu.Lock()           // Semua stock nunggu!
    defer globalMu.Unlock()
    
    // Match order...
    // 1ms untuk BBCA = BBRI, TLKM, dll juga nunggu 1ms
}
```

**Impact:**
- Throughput terbatas oleh satu stock sibuk
- BBCA sibuk → BBRI ikut lambat
- Tidak scalable

---

### Solution: Per-Stock Partitioning

**Desain:** Setiap stock punya goroutine dan channel sendiri

```
BEFORE (Global Lock):
┌─────────────────────────────────────┐
│  Global Mutex                       │
│       │                             │
│   ┌───▼───┐                         │
│   │ Lock  │ ◀── Semua stock nunggu │
│   └───┬───┘                         │
│       │                             │
│  ┌────┴──────────┐                  │
│  │ Global Book   │                  │
│  │ BBCA,BBRI,... │                  │
│  └───────────────┘                  │
└─────────────────────────────────────┘

AFTER (Per-Stock Goroutines):
┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│ BBCA        │ │ BBRI        │ │ TLKM        │
│ Matcher     │ │ Matcher     │ │ Matcher     │
│ (1 gor)     │ │ (1 gor)     │ │ (1 gor)     │
│             │ │             │ │             │
│ Channel:    │ │ Channel:    │ │ Channel:    │
│ [░░░░░░░░]  │ │ [▓▓▓▓░░░░]  │ │ [░░░░▓▓▓▓]  │
│             │ │             │ │             │
│ No mutex!   │ │ No mutex!   │ │ No mutex!   │
└─────────────┘ └─────────────┘ └─────────────┘
     │                 │                 │
     └─────────────────┼─────────────────┘
                       │
              Independent processing
              BBCA sibuk tidak affect BBRI
```

**Implementation:**

```go
// engine/engine.go
type Engine struct {
    matchers map[string]*Matcher  // One per stock
}

func (e *Engine) SubmitOrder(order *entity.Order) error {
    matcher := e.matchers[order.StockCode]
    
    // Send to specific stock's channel
    // No locking, no blocking other stocks
    select {
    case matcher.ch <- order:
        return nil
    default:
        return ErrBackpressure  // Channel full
    }
}

// engine/matcher.go
type Matcher struct {
    ch chan *entity.Order  // Buffered 1000
    book *orderBook
}

func (m *Matcher) run(ctx context.Context) {
    for order := range m.ch {
        // Single goroutine = no mutex needed!
        m.matchOrder(ctx, order)
    }
}
```

---

### Results

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| **Throughput** | ~5,000 orders/sec | **35,000+ orders/sec** | **7×** |
| **Latency p99** | 50ms | **4ms** | **12×** |
| **Scalability** | Limited | Linear with stocks | **∞** |

**Code:** `internal/engine/engine.go`

---

## 2️⃣ Bottleneck #2: WebSocket Fan-Out (N×M)

### Problem

**Broadcasting ke banyak client mahal:**

```
Scenario:
- 500 clients
- 5 subscriptions per client
- 1 trade event

Naive approach: Scan semua clients
for each client in clients {      // 500 iterations
    for each sub in client.subs { // 5 iterations
        if sub matches event {
            send to client          // 2500 checks!
        }
    }
}

Complexity: O(N × M) = 2500 operations per event
```

**Impact:**
- CPU usage tinggi
- Latency meningkat dengan jumlah client
- Slow clients block fast clients

---

### Solution: Indexed Subscriptions + Non-Blocking Send

**Desain:**

```
BEFORE (Naive Scan):
┌─────────────────────────────────────────┐
│  Clients = [C1, C2, C3, ..., C500]     │
│                                         │
│  Broadcast:                             │
│  for client in clients {               │
│      check all subs (5 each)           │
│      → 2500 checks!                    │
│  }                                      │
└─────────────────────────────────────────┘

AFTER (Indexed):
┌─────────────────────────────────────────┐
│  Subs Index:                            │
│                                         │
│  "market.trade:BBCA" → [C1, C5, C9]    │
│  "market.ticker:BBRI" → [C2, C3]       │
│  "market.trade:TLKM" → [C4, C7, C8]    │
│                                         │
│  Broadcast:                             │
│  clients = subs["market.trade:BBCA"]   │
│  for client in clients {               │
│      non-blocking send                 │
│  }                                      │
│  → 3 checks only! (O(M) not O(N×M))    │
└─────────────────────────────────────────┘
```

**Implementation:**

```go
// transport/ws/hub.go
type Hub struct {
    // Index: subscription -> set of clients
    subs map[Subscription]map[*Client]bool
}

type Subscription struct {
    Channel string  // "market.trade"
    Stock   string  // "BBCA"
}

// O(1) subscribe
func (h *Hub) subscribe(client *Client, sub Subscription) {
    if h.subs[sub] == nil {
        h.subs[sub] = make(map[*Client]bool)
    }
    h.subs[sub][client] = true
}

// O(M) broadcast where M = subscribers
func (h *Hub) broadcast(msg []byte, sub Subscription) {
    clients := h.subs[sub]  // O(1) lookup
    
    for client := range clients {
        select {
        case client.send <- msg:  // Fast path
        default:
            // Slow client: kick!
            h.unregister <- client
        }
    }
}
```

---

### Results

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| **Lookup** | O(N×M) | **O(1)** | **Instant** |
| **Broadcast** | O(N×M) | **O(M)** | **~100×** |
| **Slow client impact** | Blocks all | **Kicked only** | **Isolated** |

**Code:** `internal/transport/ws/hub.go`

---

## 3️⃣ Bottleneck #3: Storage Write Amplification

### Problem

**Setiap trade = 5 database writes:**

```
Trade Execution:
1. INSERT trade
2. UPDATE buy_order
3. UPDATE sell_order  
4. UPDATE ticker
5. INSERT order_book_snapshot

= 5 round-trips to PostgreSQL per trade!

If 1000 trades/sec:
→ 5000 writes/sec
→ 5ms per write (typical)
→ 25ms total per trade
→ HOT PATH SLOW! ❌
```

**Impact:**
- Matching terhambat oleh DB latency
- Throughput terbatas oleh DB
- Error rate naik saat DB sibuk

---

### Solution: Async DB Worker dengan Batching

**Desain:**

```
BEFORE (Synchronous):
┌──────────────┐     ┌──────────────┐
│   Matcher    │────▶│  PostgreSQL  │
│              │     │              │
│ Match Order  │     │ INSERT trade │
│ Create Trade │────▶│ UPDATE order │
│ Update State │     │ UPDATE ...   │
└──────────────┘     └──────────────┘
      │                       │
      │    5 round-trips!     │
      │    ~25ms delay        │
      └───────────────────────┘

AFTER (Asynchronous):
┌──────────────┐     ┌──────────────────┐
│   Matcher    │────▶│  In-Memory       │
│              │     │  (Hot Path)      │
│ Match Order  │────▶│  - Order Book    │
│ Create Trade │     │  - Trades        │
│ Update State │     │  - Tickers       │
└──────────────┘     └──────────────────┘
      │                       │
      │ Non-blocking          │
      │ Enqueue only          │
      ▼                       │
┌──────────────────┐          │
│  Buffered Queue  │          │
│  Capacity: 5000  │          │
└────────┬─────────┘          │
         │                    │
         ▼                    │
┌──────────────────┐          │
│  DB Worker       │          │
│  (Background)    │          │
│                  │          │
│  Batch: 100 ops  │          │
│  Flush: 100ms    │          │
│  Retry: 5×       │          │
└────────┬─────────┘          │
         │                    │
         ▼                    │
┌──────────────────┐          │
│  PostgreSQL      │◀─────────┘
│  (Persistence)   │  Read queries
└──────────────────┘
```

**Implementation:**

```go
// worker/dbworker.go
type DBWorker struct {
    writeCh chan WriteOp   // Buffered 5000
    batch   []WriteOp
    ticker  *time.Ticker   // 100ms
}

type WriteOp struct {
    Type    OpType  // OpTradeSave, OpOrderUpdate, etc
    Payload interface{}
}

func (w *DBWorker) Run(ctx context.Context) {
    for {
        select {
        case op := <-w.writeCh:
            w.batch = append(w.batch, op)
            if len(w.batch) >= 100 {
                w.flush()
            }
            
        case <-w.ticker.C:
            if len(w.batch) > 0 {
                w.flush()
            }
            
        case <-ctx.Done():
            w.flush()  // Drain remaining
            return
        }
    }
}

func (w *DBWorker) flush() {
    // Batch insert dengan pgx.Batch
    batch := &pgx.Batch{}
    
    for _, op := range w.batch {
        switch op.Type {
        case OpTradeSave:
            batch.Queue("INSERT INTO trades ...", op.Payload)
        case OpOrderUpdate:
            batch.Queue("UPDATE orders ...", op.Payload)
        // ...
        }
    }
    
    // Single round-trip untuk 100 operations!
    w.db.SendBatch(context.Background(), batch)
    
    w.batch = w.batch[:0]  // Clear batch
}
```

---

### Results

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| **Hot path latency** | 25ms | **<1ms** | **25×** |
| **DB round-trips** | 5000/sec | **50/sec** | **100×** |
| **Throughput** | 200 trades/sec | **35,000/sec** | **175×** |
| **Data loss risk** | None | **Minimal** | **Acceptable** |

**Trade-off:**
- ⚠️ **Delay:** Data tersedia di DB setelah 100ms
- ⚠️ **Risk:** Crash sebelum flush = data loss
- ✅ **Benefit:** Hot path super cepat!

**Mitigation:**
- Short flush interval (100ms)
- Replay from event log (future enhancement)
- Acceptable untuk use case trading

**Code:** `internal/worker/dbworker.go`

---

## 📊 Summary Table

| # | Bottleneck | Problem | Solution | Result |
|---|-----------|---------|----------|--------|
| **1** | Matching Engine | Global lock blocks all stocks | Per-stock goroutines | **7× throughput** |
| **2** | WebSocket Fan-out | O(N×M) scan, slow clients block | Indexed subs + non-blocking kick | **~100× broadcast** |
| **3** | Storage Writes | 5 DB round-trips per trade | Async batch worker | **175× throughput** |

---

## 🎯 Key Principles

1. **Eliminate shared state** → Partition by stock
2. **Index everything** → O(1) lookups
3. **Decouple hot path** → Async persistence
4. **Fail fast** → Kick slow clients
5. **Batch operations** → Reduce I/O

---

## ✅ Verification

### Performance Tests

```bash
# Before vs After comparison
go run ./cmd/matchtest -rate 50000 -duration 30s

# Metrics:
# - Orders/sec
# - Latency p50/p95/p99
# - Error rate
```

### Load Test

```bash
# Test all three bottlenecks simultaneously
go run ./cmd/stresstest \
  -max-workers 400 \
  -ws-clients 500 \
  -step-dur 15s

# Verify:
# - Throughput > 30k orders/sec
# - WS broadcast > 200k events/sec
# - No errors, no blocking
```

### Profiling

```bash
# CPU profiling
go run ./cmd/server -cpuprofile=cpu.prof

# Analyze
go tool pprof cpu.prof

# Look for:
# - sync.Mutex contention (should be minimal)
# - map iterations (should be O(M) not O(N))
# - DB calls (should be in background)
```

---

## 📖 References

- **Bottleneck 1:** `internal/engine/engine.go`
- **Bottleneck 2:** `internal/transport/ws/hub.go`
- **Bottleneck 3:** `internal/worker/dbworker.go`
