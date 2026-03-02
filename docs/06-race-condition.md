# 06. Race Condition Prevention

Penjelasan lengkap bagaimana sistem mencegah race condition.

---

## ❓ Apa itu Race Condition?

**Race condition** terjadi ketika:
- Multiple goroutines mengakses shared data
- Minimal satu goroutine melakukan write
- Akses tidak terkoordinasi

**Contoh Bug:**
```go
// DANGEROUS: Race condition!
var counter int

func increment() {
    counter++  // Read → Modify → Write (3 operations!)
}

// Goroutine 1: Read counter (0)
// Goroutine 2: Read counter (0) ← Same value!
// Goroutine 1: Write counter (1)
// Goroutine 2: Write counter (1) ← Lost update!
// Expected: 2, Actual: 1 ❌
```

---

## 🛡️ Strategi Pencegahan

### 1. Single Writer Pattern (No Mutex Needed!)

**Konsep:** Setiap stock punya goroutine sendiri. Tidak ada sharing data antar stock.

```
┌─────────────────────────────────────────────────────────────┐
│  Traditional (Mutex Required)                               │
├─────────────────────────────────────────────────────────────┤
│  Global Order Book                                          │
│       │                                                     │
│   ┌───▼───┐                                                 │
│   │ Mutex │ ← Lock untuk setiap operasi                    │
│   └───┬───┘                                                 │
│       │                                                     │
│  ┌────┴────┐  ┌────────┐  ┌────────┐                       │
│  │ All     │  │ BBCA   │  │ BBRI   │  ← Shared data        │
│  │ Orders  │  │ Orders │  │ Orders │                       │
│  └─────────┘  └────────┘  └────────┘                       │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  Mini Exchange (No Mutex!)                                  │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │ BBCA        │  │ BBRI        │  │ TLKM        │         │
│  │ Matcher     │  │ Matcher     │  │ Matcher     │         │
│  │ (1 gor)     │  │ (1 gor)     │  │ (1 gor)     │         │
│  │             │  │             │  │             │         │
│  │ Owns:       │  │ Owns:       │  │ Owns:       │         │
│  │ - Channel   │  │ - Channel   │  │ - Channel   │         │
│  │ - OrderBook │  │ - OrderBook │  │ - OrderBook │         │
│  │ - State     │  │ - State     │  │ - State     │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│       │                 │                 │                 │
│       └─────────────────┼─────────────────┘                 │
│                         │                                   │
│              NO SHARING BETWEEN STOCKS!                     │
│              Each goroutine owns its data                   │
│              No mutex needed!                               │
└─────────────────────────────────────────────────────────────┘
```

**Code:** `internal/engine/engine.go`

```go
// Engine maintains one matcher per stock
type Engine struct {
    matchers map[string]*Matcher  // key: stock code
}

func (e *Engine) SubmitOrder(order *entity.Order) error {
    matcher := e.matchers[order.StockCode]
    
    // Send to specific matcher channel
    // No other goroutine touches this stock's data
    select {
    case matcher.ch <- order:
        return nil
    default:
        return ErrBackpressure
    }
}
```

**Benefits:**
- ✅ No mutex contention
- ✅ Natural FIFO ordering
- ✅ Lock-free matching
- ✅ Isolated failure domains

---

### 2. sync.RWMutex for Shared Repositories

**Untuk:** Repository yang diakses banyak goroutine (tapi beda stock)

```go
type OrderRepo struct {
    mu      sync.RWMutex          // Protects maps
    byID    map[string]*Order     // All orders by ID
    byStock map[string][]*Order   // Orders by stock code
}

// Read operation (multiple goroutines OK)
func (r *OrderRepo) FindByID(ctx context.Context, id string) (*Order, error) {
    r.mu.RLock()           // Read lock
    defer r.mu.RUnlock()
    
    order, ok := r.byID[id]
    if !ok {
        return nil, ErrNotFound
    }
    return copyOrder(order), nil  // Return copy!
}

// Write operation (exclusive)
func (r *OrderRepo) Save(ctx context.Context, order *Order) error {
    r.mu.Lock()            // Write lock
    defer r.mu.Unlock()
    
    cp := copyOrder(order)  // Deep copy
    r.byID[order.ID] = cp
    r.byStock[order.StockCode] = append(
        r.byStock[order.StockCode], 
        cp,
    )
    return nil
}
```

**Pattern:**
- **Read:** `RLock()` — Multiple readers allowed
- **Write:** `Lock()` — Exclusive access

**Code:** `internal/infra/storage/memory/order_repo.go`

---

### 3. Channel-Based Coordination (WebSocket Hub)

**Untuk:** Hub yang mengelola banyak client connections

```go
type Hub struct {
    // Semua akses ke state melalui channels
    register      chan *Client
    unregister    chan *Client
    subscribe     chan subscribeRequest
    unsubscribe   chan unsubscribeRequest
    broadcast     chan broadcastMessage
    
    // State (hanya diakses oleh Hub goroutine)
    clients       map[*Client]bool
    subs          map[Subscription]map[*Client]bool
    clientSubs    map[*Client]map[Subscription]bool
}

// SINGLE goroutine yang akses state
func (h *Hub) Run(ctx context.Context) {
    for {
        select {
        case client := <-h.register:
            h.clients[client] = true  // Akses state
            
        case client := <-h.unregister:
            delete(h.clients, client)  // Akses state
            
        case req := <-h.subscribe:
            h.subs[req.sub][req.client] = true  // Akses state
            
        case msg := <-h.broadcast:
            // Broadcast ke subscribers
            for client := range h.subs[msg.sub] {
                select {
                case client.send <- msg.data:
                default:
                    h.removeClient(client)
                }
            }
        }
    }
}
```

**Key Points:**
- Hanya **satu goroutine** (Hub.Run) yang akses state
- Goroutines lain kirim message via **channels**
- No mutex needed di Hub!

**Code:** `internal/transport/ws/hub.go`

---

### 4. Snapshot Pattern

**Problem:** Order pointer di-share antara service dan engine

**Solusi:** Deep copy sebelum submit ke engine

```go
func (s *Service) CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
    // Create order
    order := &Order{
        ID:       uuid.NewString(),
        StockCode: req.StockCode,
        // ...
    }
    
    // Save to repo (snapshot)
    if err := s.orderRepo.Save(ctx, order); err != nil {
        return nil, err
    }
    
    // Get snapshot for return
    snapshot := copyOrder(order)
    
    // Submit to engine (engine owns the pointer)
    // Service tidak boleh modify lagi!
    if err := s.engine.SubmitOrder(order); err != nil {
        return snapshot, nil  // Return snapshot anyway
    }
    
    return snapshot, nil
}
```

**Rule:**
- Engine owns original pointer (boleh modify)
- Service works dengan snapshot (read-only)

---

### 5. Event Bus: Single Dispatcher

**Untuk:** Event publishing dan subscription

```go
type EventBus struct {
    ch   chan Event       // Buffered 10,000
    subs map[Type][]subscription
    mu   sync.RWMutex     // Protects subs map
}

func (b *EventBus) Publish(e Event) {
    select {
    case b.ch <- e:  // Non-blocking
    default:
        // Drop if full (backpressure)
    }
}

// SINGLE goroutine untuk dispatch
func (b *EventBus) dispatch() {
    for e := range b.ch {
        b.mu.RLock()
        handlers := b.subs[e.Type]  // Copy subscribers
        b.mu.RUnlock()
        
        // Call handlers
        for _, h := range handlers {
            h(e)  // Handler tidak boleh block!
        }
    }
}
```

**Pattern:**
- Publish: Non-blocking enqueue
- Dispatch: Single goroutine, sequential
- Handlers: Must not block

---

### 6. Data Consistency: Atomic Operations

**Trade Creation (Atomic):**

```go
func (m *Matcher) executeTrade(ctx context.Context, buy, sell *Order, qty, price int64) {
    // Semua operasi dalam satu method
    // Tidak ada yield point (tidak ada channel send di tengah)
    
    // 1. Create trade
    trade := &Trade{...}
    
    // 2. Save trade
    m.tradeRepo.Save(ctx, trade)
    
    // 3. Update orders
    buy.Fill(qty)
    sell.Fill(qty)
    
    // 4. Save orders
    m.orderRepo.Update(ctx, buy)
    m.orderRepo.Update(ctx, sell)
    
    // 5. Update ticker
    ticker := m.updateTicker(price, qty)
    
    // 6. Publish events
    m.bus.Publish(TradeExecuted{...})
    m.bus.Publish(OrderUpdated{...})
    m.bus.Publish(TickerUpdated{...})
}
```

**Semua dalam satu goroutine (Matcher), sehingga:**
- Tidak ada interleaving dari goroutine lain
- State konsisten setelah method return

---

## 📊 Race Prevention Summary

| Resource | Strategy | Mutex? | Location |
|----------|----------|--------|----------|
| **Order Book** | Single goroutine per stock | ❌ No | `engine/matcher.go` |
| **Order Storage** | RWMutex | ✅ Yes | `storage/memory/order_repo.go` |
| **Trade Storage** | RWMutex | ✅ Yes | `storage/memory/trade_repo.go` |
| **Hub State** | Channel-based | ❌ No | `transport/ws/hub.go` |
| **Event Bus** | Single dispatcher | ✅ Yes (RWMutex) | `broker/eventbus.go` |
| **Ticker Data** | RWMutex | ✅ Yes | `storage/memory/market_repo.go` |
| **Order Pointer** | Snapshot pattern | ❌ No | `app/order/service.go` |

---

## ✅ Verification

### Race Detector

```bash
# Run all tests with race detector
go test -race ./...

# Expected: PASS (no races detected)
```

### Integration Tests

```bash
# Concurrent order submission test
go test ./cmd/server -run TestIntegration_ConcurrentOrders -race

# WebSocket concurrent client test
go test ./internal/transport/ws -run TestHub_Concurrent -race
```

### Load Test

```bash
# 400 concurrent workers, 50 WS clients
go run ./cmd/stresstest -max-workers 400 -ws-clients 50

# Verify: No errors, consistent trade count
```

---

## 🎯 Key Principles

1. **Avoid Shared State** → Prefer message passing (channels)
2. **Single Writer** → One goroutine owns data
3. **Immutable Data** → Return copies, not pointers
4. **Buffer Size Matters** → Prevent blocking, enable backpressure
5. **Test with -race** → Always!

---

## 📖 References

- [Go Memory Model](https://golang.org/ref/mem)
- [Go Race Detector](https://golang.org/doc/articles/race_detector.html)
- [Share Memory By Communicating](https://blog.golang.org/codelab-share)
