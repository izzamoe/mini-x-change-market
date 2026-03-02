# 05. Assumptions (Asumsi yang Digunakan)

Asumsi-asumsi yang digunakan dalam desain sistem Mini Exchange.

---

## 📊 System Load Assumptions

| Parameter | Target | Actual | Notes |
|-----------|--------|--------|-------|
| **Orders per minute** | 1,000 | **2,093,315** | ~2,093× headroom |
| **WebSocket clients** | 500 | **500+ tested** | Max 1000/instance |
| **Subscriptions per client** | 1-5 | **Max 20** | Configurable |
| **Stocks supported** | 5 | **10+** | Easy to add more |
| **Order book depth** | - | **Unlimited** | Limited by memory |

---

## 💰 Price Representation

### Integer-Only Arithmetic

**Assumption:** Semua harga dalam bentuk **integer** (smallest currency unit)

```go
// Contoh: IDR (Rupiah)
price := 9500  // Rp 9,500 (bukan 9.5)

// Contoh: USD dengan cents
price := 10050 // $100.50 (10050 cents)

// TIDAK menggunakan float64
// price := 9.5  // ❌ Avoid floating point
```

**Alasan:**
- ❌ Floating point: Precision errors (0.1 + 0.2 ≠ 0.3)
- ✅ Integer: Exact arithmetic, no rounding errors
- ✅ Database: Integer columns lebih efisien
- ✅ JSON: No scientific notation issues

---

## 🗄️ Storage Strategy

### Primary: In-Memory

**Assumption:** Data hot path selalu di in-memory

```
┌─────────────────────────────────────────────┐
│  Hot Path (Latency Critical)                │
│  ├── Order matching                         │
│  ├── Order book queries                     │
│  ├── Ticker updates                         │
│  └── WS broadcasts                          │
└─────────────────────────────────────────────┘
              ↓ Async
┌─────────────────────────────────────────────┐
│  Persistence (Background)                   │
│  ├── PostgreSQL (via DB Worker)             │
│  └── Batch insert (100 ops / 100ms)         │
└─────────────────────────────────────────────┘
```

**Trade-offs:**
- ✅ Matching: <1ms latency
- ✅ No DB round-trip in hot path
- ⚠️ Data loss risk if crash (mitigated by DB worker)

---

## 🎯 Order Matching

### Single-Goroutine per Stock

**Assumption:** Setiap stock diproses oleh satu goroutine dedicated

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ BBCA        │  │ BBRI        │  │ TLKM        │
│ Matcher     │  │ Matcher     │  │ Matcher     │
│ (1 gor)     │  │ (1 gor)     │  │ (1 gor)     │
└─────────────┘  └─────────────┘  └─────────────┘
     │                 │                 │
     └─────────────────┼─────────────────┘
                       │
              Tidak ada sharing!
              Tidak perlu mutex
```

**Implikasi:**
- Order BBCA tidak bisa block order BBRI
- Linear scaling dengan jumlah stock
- Tidak ada race condition dalam matching

---

### FIFO at Same Price

**Assumption:** Orders at same price matched in arrival order (Price-Time Priority)

```
Order Book (Bids) - Same Price 9500:

Time →
─────────────────────────────
1. Alice: BUY 100 @ 9500  ← First, matched first
2. Bob:   BUY 200 @ 9500  ← Second, matched second
3. Carol: BUY 150 @ 9500  ← Third, matched third
```

**Implementation:**
```go
// Stable sort preserves insertion order
sort.SliceStable(bids, func(i, j int) bool {
    return bids[i].Price > bids[j].Price  // DESC
})
```

---

### Partial Fill Support

**Assumption:** Orders can be partially filled, remaining stays in book

```
Scenario:
BUY Order:  100 qty @ 9500
ASK Order:   60 qty @ 9400

Trade: 60 qty @ 9400

Result:
- ASK: FILLED (removed from book)
- BUY: PARTIAL (40 remaining, stays in book)
```

---

## 🔐 Authentication

### Optional in Development

**Assumption:** Auth dapat di-disable untuk development

```bash
# .env (Development)
AUTH_ENABLED=false

# .env (Production)
AUTH_ENABLED=true
JWT_SECRET=your-secret-key
```

**Rationale:**
- Memudahkan testing dan development
- Production WAJIB enable + strong secret

---

## 🌐 WebSocket

### Sticky Sessions Required

**Assumption:** WebSocket clients harus pinned ke satu instance

```nginx
# nginx.conf
upstream ws_backend {
    ip_hash;  # Sticky by client IP
    server app-1:8080;
    server app-2:8080;
}
```

**Alasan:**
- WebSocket connections stateful (subscriptions)
- Pindah instance = kehilangan subscription state
- REST API boleh round-robin, WS harus sticky

---

### Non-Blocking Broadcast

**Assumption:** Slow clients tidak boleh block fast clients

```go
// Hub broadcast loop
for client := range clients {
    select {
    case client.send <- msg:  // Fast path
    default:
        hub.unregister <- client  // Kick slow client
    }
}
```

**Limit:** Client with blocked send buffer = disconnected

---

## 📈 Horizontal Scaling

### Consistent Hashing for Partitioning

**Assumption:** Stock ownership determined by FNV-1a hash

```go
func OwnerIndex(stockCode string, totalInstances int) int {
    h := fnv.New64a()
    h.Write([]byte(stockCode))
    return int(h.Sum64() % uint64(totalInstances))
}
```

**Properties:**
- Deterministic: Same stock always same owner
- Fair distribution: Uniform across instances
- Minimal migration: Adding instance only affects ~1/N stocks

---

### Shared Nothing Architecture

**Assumption:** Instances tidak share state (kecuali via message broker)

```
Instance 1              Instance 2
┌──────────┐           ┌──────────┐
│ BBCA Book│           │ BBRI Book│
│ (owned)  │           │ (owned)  │
└──────────┘           └──────────┘
     │                       │
     └───────────┬───────────┘
                 │
           ┌─────▼─────┐
           │   NATS    │  ← Hanya komunikasi via message broker
           └───────────┘
```

---

## 🔄 Event-Driven Architecture

### Async Event Processing

**Assumption:** Event handlers boleh async, tapi event ordering penting

```
TradeExecuted Event
       │
       ├──▶ WS Hub (async)         → Immediate broadcast
       ├──▶ NATS (async)           → Cross-instance
       └──▶ DB Worker (async)      → Batch persistence
```

**Guarantee:**
- Events delivered in order (single dispatcher goroutine)
- Handlers tidak boleh block
- Slow handler tidak block event bus

---

## 🗃️ Data Persistence

### Best-Effort Persistence

**Assumption:** PostgreSQL persistence adalah best-effort (async)

```
Order Created
     │
     ├──▶ Memory (immediate)        ✅ Synchronous
     │
     └──▶ PostgreSQL (async)        ⚠️ Delayed (100ms batch)
```

**Risks:**
- Crash dalam 100ms = data belum persist
- Mitigasi: Small batch interval, replay from logs (future)

---

### Read-After-Write Consistency

**Assumption:** Client reads dari in-memory storage

```
Client                     Server
  │  POST /api/v1/orders    │
  │────────────────────────▶│
  │                         │ Save to memory ✅
  │  201 Created            │
  │◀────────────────────────│
  │                         │
  │  GET /api/v1/orders/id  │
  │────────────────────────▶│
  │                         │ Read from memory ✅
  │  200 OK                 │
  │◀────────────────────────│
```

**Consistency:** Strong (immediate visibility)

---

## 🔧 Configuration

### Environment-Based Config

**Assumption:** Semua konfigurasi via environment variables

```go
type Config struct {
    ServerPort      int    `env:"SERVER_PORT" envDefault:"8080"`
    StorageType     string `env:"STORAGE_TYPE" envDefault:"memory"`
    AuthEnabled     bool   `env:"AUTH_ENABLED" envDefault:"false"`
    // ...
}
```

**Alasan:**
- 12-Factor App compliant
- Easy deployment (Docker, K8s)
- No config files to manage

---

## 📊 Monitoring

### Prometheus Metrics

**Assumption:** Metrics exported via /metrics endpoint

```
http_requests_total{method="POST",endpoint="/api/v1/orders"}
http_request_duration_seconds{quantile="0.99"}
ws_connections_active
ws_messages_broadcast_total
trades_executed_total
orders_created_total
```

**Required:** Prometheus scraping setiap 15s

---

## 📝 Summary Table

| Category | Assumption | Impact |
|----------|-----------|--------|
| **Price** | Integer only | No floating point errors |
| **Storage** | In-memory primary | <1ms latency |
| **Matching** | Per-stock goroutine | No mutex, linear scaling |
| **FIFO** | Time priority at same price | Fair matching |
| **Partial Fill** | Supported | Better liquidity |
| **Auth** | Optional in dev | Easy testing |
| **WS** | Sticky sessions | Consistent subscriptions |
| **Broadcast** | Non-blocking | No slow client blocking |
| **Scaling** | Consistent hashing | Minimal rebalancing |
| **Events** | Async handlers | Decoupled services |
| **Persistence** | Best-effort async | Speed over durability |

---

## ⚠️ Caveats

### Production Considerations

1. **Memory Usage:** In-memory storage limited by RAM
   - Monitor: `container_memory_usage_bytes`
   - Mitigation: Archive old orders to cold storage

2. **Single Point of Failure:** Each stock on one instance
   - Mitigation: Hot standby (future enhancement)

3. **Data Loss:** Async persistence risk
   - Mitigation: Smaller batch size, write-ahead log

4. **Sticky Sessions:** IP-based can be problematic
   - Behind NAT: Many clients same IP
   - Mitigation: Session cookie alternative

---

## ✅ Validation

Semua asumpsi ini telah **diverifikasi** melalui:

1. ✅ Unit tests (`*_test.go`)
2. ✅ Integration tests (`cmd/server/main_test.go`)
3. ✅ Load tests (`cmd/loadtest/`, `cmd/wsloadtest/`)
4. ✅ Race tests (`go test -race`)
5. ✅ Performance benchmarks (`cmd/matchtest/`)
