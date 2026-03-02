# 03. System Flow

Alur data lengkap dari order submission sampai broadcast ke clients.

---

## 🔄 Complete Order Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           ORDER LIFECYCLE FLOW                              │
└─────────────────────────────────────────────────────────────────────────────┘

Step 1: Client Submit Order
═══════════════════════════════════════════════════════════════════════════════
  Client
    │  POST /api/v1/orders
    │  {stock_code:BBCA, side:BUY, price:9500, quantity:100}
    ▼
┌─────────────────────────────────────────────────────────────┐
│ HTTP Handler (transport/http/handler/order.go)               │
│ - Parse JSON                                                 │
│ - Call orderService.CreateOrder()                            │
└─────────────────────────────────────────────────────────────┘

Step 2: Validation & Entity Creation
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Order Service (app/order/service.go)                         │
│                                                              │
│ 1. ValidateCreateOrder()                                     │
│    - Check stock exists                                      │
│    - Validate price range (1 - 999,999,999)                  │
│    - Validate quantity (1 - 1,000,000)                       │
│    - Check side is BUY/SELL                                  │
│                                                              │
│ 2. Create Order Entity                                       │
│    ID: uuid.NewString()                                      │
│    Status: OrderStatusOpen                                   │
│    FilledQuantity: 0                                         │
│    CreatedAt: time.Now()                                     │
└─────────────────────────────────────────────────────────────┘

Step 3: Persist Order
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Order Repository (infra/storage/memory/order_repo.go)        │
│                                                              │
│ 1. Acquire Write Lock (sync.RWMutex)                         │
│ 2. Check duplicate ID                                        │
│ 3. Deep copy order                                           │
│ 4. Store in map: byID[order.ID] = order                      │
│ 5. Append to slice: ordered = append(ordered, order)         │
│ 6. Release Write Lock                                        │
└─────────────────────────────────────────────────────────────┘

Step 4: Submit to Engine
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Engine Coordinator (engine/engine.go)                        │
│                                                              │
│ 1. Determine matcher for stock                               │
│    matcher := e.matchers[order.StockCode]                    │
│                                                              │
│ 2. Submit to matcher channel                                 │
│    select {                                                  │
│    case matcher.ch <- order:  // Success                    │
│    default:                    // Channel full               │
│        return error (backpressure)                           │
│    }                                                         │
└─────────────────────────────────────────────────────────────┘

Step 5: Partition Routing (Horizontal Scaling)
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Partition Router (partition/router.go)                       │
│                                                              │
│ 1. Check if this instance owns the stock                     │
│    owns := partitioner.OwnsStock(order.StockCode)            │
│                                                              │
│ 2. If NOT owned:                                             │
│    - Marshal order to JSON                                   │
│    - Publish to NATS: "engine.orders.{stock}"                │
│    - Return success (order queued)                           │
│                                                              │
│ 3. If OWNED:                                                 │
│    - Submit to local matcher                                 │
└─────────────────────────────────────────────────────────────┘

Step 6: Matcher Processing
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Matcher Goroutine (engine/matcher.go)                        │
│ One goroutine per stock - runs continuously                  │
│                                                              │
│ func (m *Matcher) run(ctx context.Context) {                │
│     for order := range m.ch {  // Block until order arrives │
│         m.matchOrder(ctx, order)                             │
│     }                                                        │
│ }                                                            │
└─────────────────────────────────────────────────────────────┘

Step 7: Order Book Matching
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Match Logic (engine/matcher.go:matchBuy/matchSell)           │
│                                                              │
│ IF BUY Order:                                                │
│   - Get best ask (lowest sell price)                         │
│   - If bestAsk.Price <= buy.Price:                          │
│       → Execute trade                                        │
│   - Else:                                                    │
│       → Insert into bids (sorted DESC by price)              │
│       → Stable sort (FIFO at same price)                     │
│                                                              │
│ IF SELL Order:                                               │
│   - Get best bid (highest buy price)                         │
│   - If bestBid.Price >= sell.Price:                         │
│       → Execute trade                                        │
│   - Else:                                                    │
│       → Insert into asks (sorted ASC by price)               │
└─────────────────────────────────────────────────────────────┘

Step 8: Trade Execution
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Execute Trade (engine/matcher.go:executeTrade)               │
│                                                              │
│ 1. Calculate match quantity:                                 │
│    qty := min(buy.Remaining(), sell.Remaining())             │
│                                                              │
│ 2. Create Trade Entity:                                      │
│    trade := &entity.Trade{                                   │
│        ID: uuid.NewString(),                                 │
│        StockCode: m.stockCode,                               │
│        BuyOrderID: buy.ID,                                   │
│        SellOrderID: sell.ID,                                 │
│        Price: sell.Price,  // Passive order price             │
│        Quantity: qty,                                        │
│        ExecutedAt: time.Now(),                               │
│    }                                                         │
│                                                              │
│ 3. Persist Trade                                            │
│    m.tradeRepo.Save(ctx, trade)                              │
│                                                              │
│ 4. Update Orders                                            │
│    buy.Fill(qty)   // FilledQuantity += qty                  │
│    sell.Fill(qty)  // Update status (OPEN/PARTIAL/FILLED)    │
│                                                              │
│ 5. Update Repositories                                      │
│    m.orderRepo.Update(ctx, buy)                              │
│    m.orderRepo.Update(ctx, sell)                             │
│    If fully filled: remove from book                         │
│    If partial: update in place                               │
│                                                              │
│ 6. Update Ticker                                            │
│    ticker.LastPrice = trade.Price                            │
│    ticker.Volume += trade.Quantity                           │
│    m.marketRepo.UpdateTicker(ctx, ticker)                    │
└─────────────────────────────────────────────────────────────┘

Step 9: Event Publishing
═══════════════════════════════════════════════════════════════════════════════
    ▼
┌─────────────────────────────────────────────────────────────┐
│ Event Bus (infra/broker/eventbus.go)                         │
│                                                              │
│ Events Published:                                            │
│ 1. TradeExecuted  → WS broadcast, NATS, DB persistence       │
│ 2. OrderUpdated   → User notification (x2: buyer + seller)   │
│ 3. TickerUpdated  → Price subscribers                        │
│ 4. OrderBookUpdated → Book depth subscribers                 │
│                                                              │
│ Publishing:                                                  │
│    bus.Publish(event.Event{                                  │
│        Type: event.TradeExecuted,                            │
│        StockCode: stock,                                     │
│        Payload: trade,                                       │
│    })                                                        │
└─────────────────────────────────────────────────────────────┘

Step 10: Event Fan-Out
═══════════════════════════════════════════════════════════════════════════════
    ▼
           ┌─────────────────────────────────────────────┐
           │           EVENT CONSUMERS                   │
           └──────────────┬──────────────────────────────┘
                          │
        ┌─────────────────┼─────────────────┐
        │                 │                 │
        ▼                 ▼                 ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  WS Hub      │  │ NATS         │  │ DB Worker    │
│  (Local)     │  │ (Cross-inst) │  │ (Async)      │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │
       │                 │                 │
Step 11: Broadcast to Clients
═══════════════════════════════════════════════════════════════════════════════
       │                 │                 │
       │                 ▼                 │
       │        ┌──────────────┐           │
       │        │ Other        │           │
       │        │ Instances    │           │
       │        └──────┬───────┘           │
       │               │                   │
       └───────────────┼───────────────────┘
                       │
                       ▼
            ┌─────────────────────┐
            │  All WebSocket      │
            │  Clients Receive    │
            │  Real-time Events   │
            └─────────────────────┘
```

---

## 🔄 WebSocket Broadcast Flow Detail

```
Hub.Run() Goroutine (Single instance)
    │
    ├──▶ Register new client
    │    - Add to clients map
    │    - Start readPump + writePump goroutines
    │
    ├──▶ Handle Subscribe request
    │    - Add to subs[subscription][client] map
    │    - Acknowledge with "subscribed" message
    │
    ├──▶ Handle Broadcast
    │    │
    │    ├──▶ Lookup subscribers
    │    │    clients := h.subs[subscription]  // O(1)
    │    │
    │    ├──▶ Iterate subscribers
    │    │    for client := range clients {
    │    │
    │    │        select {
    │    │        case client.send <- data:  // Buffered 256
    │    │            // Success
    │    │        default:
    │    │            // Channel full - client is slow
    │    │            h.removeClient(client)  // Kick
    │    │        }
    │    │    }
    │    │
    │    └──▶ Done
    │
    └──▶ Handle Unregister
         - Remove from all subscriptions
         - Close client.send channel
         - Goroutines exit
```

---

## 🔄 Horizontal Scaling Flow

### Cross-Instance Event Broadcasting

```
Instance 0 (owns BBCA)
    │
    │ Trade terjadi di BBCA
    ▼
┌─────────────────────────────────────────────┐
│ Publish ke NATS: "trade.BBCA"               │
│ Payload: JSON trade object                  │
└────────────────────┬────────────────────────┘
                     │
                     ▼
            ┌────────────────┐
            │  NATS Server   │
            │  (Message Bus) │
            └───────┬────────┘
                    │
    ┌───────────────┼───────────────┐
    │               │               │
    ▼               ▼               ▼
Instance 0    Instance 1    Instance 2
Subscriber    Subscriber    Subscriber
    │               │               │
    ▼               ▼               ▼
Hub.Broadcast Hub.Broadcast Hub.Broadcast
(to local     (to local     (to local
 clients)      clients)      clients)
    │               │               │
    └───────────────┴───────────────┘
                    │
                    ▼
         ┌──────────────────┐
         │ All clients      │
         │ receive events   │
         │ regardless of    │
         │ which instance   │
         │ processed order  │
         └──────────────────┘
```

---

## 📊 Data Structures

### Order
```go
type Order struct {
    ID              string
    UserID          string
    StockCode       string
    Side            Side        // BUY or SELL
    Price           int64       // Integer, smallest currency unit
    Quantity        int64
    FilledQuantity  int64
    Status          OrderStatus // OPEN, PARTIAL, FILLED, CANCELLED
    CreatedAt       time.Time
    UpdatedAt       time.Time
}

func (o *Order) Remaining() int64 {
    return o.Quantity - o.FilledQuantity
}

func (o *Order) Fill(qty int64) {
    o.FilledQuantity += qty
    if o.FilledQuantity >= o.Quantity {
        o.Status = OrderStatusFilled
    } else {
        o.Status = OrderStatusPartial
    }
}
```

### Trade
```go
type Trade struct {
    ID           string
    StockCode    string
    BuyOrderID   string
    SellOrderID  string
    BuyerUserID  string
    SellerUserID string
    Price        int64
    Quantity     int64
    ExecutedAt   time.Time
}
```

### OrderBook
```go
type orderBook struct {
    stockCode string
    bids      []*orderEntry  // Sorted DESC by price, FIFO at same price
    asks      []*orderEntry  // Sorted ASC by price, FIFO at same price
}

type orderEntry struct {
    order     *entity.Order
    remaining int64
}
```

---

## 🔄 Simulator Flow

```
Simulator (per stock)
    │
    ├──▶ Initialize
    │    - Set initial price
    │    - Start goroutine
    │
    └──▶ Main Loop
         │
         ├──▶ Sleep(random 1-3s)
         │
         ├──▶ Calculate new price
         │    - Random walk ±0.5%
         │    - Mean reversion to base price
         │
         ├──▶ Update MarketRepo
         │    - Save new ticker
         │
         ├──▶ Publish Event
         │    - EventBus.Publish(TickerUpdated)
         │
         └──▶ Repeat
```

---

## 🔄 DB Worker Flow (Async Persistence)

```
DB Worker
    │
    ├──▶ Receive WriteOp via channel
    │    - OpTradeSave
    │    - OpOrderUpdate
    │    - OpTickerUpdate
    │
    ├──▶ Batch Operations
    │    - Collect up to 100 ops
    │    - Or wait 100ms
    │
    ├──▶ Begin Transaction
    │    - pgx.Begin()
    │
    ├──▶ Execute Batch
    │    - pgx.Batch
    │    - INSERT trades
    │    - UPDATE orders
    │    - INSERT/UPDATE tickers
    │
    ├──▶ Commit
    │    - tx.Commit()
    │
    └──▶ Retry on Failure
         - Exponential backoff
         - Max 5 retries
```

---

## 📈 Performance Optimizations

### 1. Single Writer Pattern
- No mutex contention in matching engine
- Natural FIFO ordering
- Lock-free within stock

### 2. Indexed Subscriptions
- O(1) lookup for broadcast targets
- No full scan of all clients

### 3. Buffered Channels
- Decouple producers from consumers
- Handle burst traffic
- Backpressure when full

### 4. Async Persistence
- Hot path remains in-memory
- DB writes in background
- Batch operations for efficiency

### 5. Connection Pooling
- HTTP client reuse
- DB connection pooling
- NATS auto-reconnect
