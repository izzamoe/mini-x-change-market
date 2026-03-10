# Plan 2: In-Memory Storage & Event Bus
# Estimated: ~40 minutes
# Dependencies: Plan 1 (domain layer must exist)

## Objective
Implement the in-memory storage layer (WAJIB) and the internal event bus. These are the core infrastructure components that the matching engine and WebSocket hub will depend on.

---

## Tasks

### 2.1 In-Memory Order Repository (internal/infra/storage/memory/order_repo.go)
- Implements `repository.OrderRepository`
- Uses `sync.RWMutex` for thread-safe access
- Internal storage: `map[string]*entity.Order` (by ID) + `[]*entity.Order` (ordered list)
- Methods:
  - `Save(ctx, order)` — add new order, write lock
  - `FindByID(ctx, id)` — read lock
  - `FindAll(ctx, filter)` — filter by StockCode, Status, UserID; read lock; pagination support
  - `Update(ctx, order)` — update existing order, write lock

### 2.2 In-Memory Trade Repository (internal/infra/storage/memory/trade_repo.go)
- Implements `repository.TradeRepository`
- Uses `sync.RWMutex`
- Internal storage: `map[string]*entity.Trade` (by ID) + `map[string][]*entity.Trade` (by stock)
- Methods:
  - `Save(ctx, trade)` — add new trade
  - `FindByStockCode(ctx, stock, limit)` — get recent trades for stock
  - `FindAll(ctx, filter)` — all trades with filter + pagination

### 2.3 In-Memory Market Repository (internal/infra/storage/memory/market_repo.go)
- Implements `repository.MarketRepository`
- Uses `atomic.Value` for lock-free ticker reads (high read frequency)
- Uses `sync.RWMutex` for orderbook (less frequent reads)
- Methods:
  - `GetTicker(ctx, stock)` — atomic load
  - `GetAllTickers(ctx)` — iterate all stocks
  - `UpdateTicker(ctx, ticker)` — atomic store
  - `GetOrderBook(ctx, stock)` — read lock
  - `UpdateOrderBook(ctx, book)` — write lock

### 2.4 Event Bus (internal/infra/broker/eventbus.go)
- Implements `event.EventBus` interface
- In-memory fan-out pattern
- Uses dedicated goroutine for dispatching
- Subscriber management with `sync.RWMutex`
- Non-blocking publish (buffered channel, 10000 cap)
- Features:
  - `Publish(event)` — non-blocking send to dispatch channel
  - `Subscribe(eventType, handler)` — register handler function
  - `Unsubscribe(id)` — remove handler
  - `Start(ctx)` — start dispatcher goroutine
  - `Stop()` — graceful shutdown, drain remaining events

### 2.5 Unit Tests
- **memory/order_repo_test.go**: Test CRUD, concurrent access, filtering
- **memory/trade_repo_test.go**: Test save, query by stock, pagination
- **memory/market_repo_test.go**: Test atomic ticker updates, orderbook updates
- **broker/eventbus_test.go**: Test publish/subscribe, fan-out, non-blocking behavior

---

## Files Created
```
internal/infra/storage/memory/order_repo.go
internal/infra/storage/memory/order_repo_test.go
internal/infra/storage/memory/trade_repo.go
internal/infra/storage/memory/trade_repo_test.go
internal/infra/storage/memory/market_repo.go
internal/infra/storage/memory/market_repo_test.go
internal/infra/broker/eventbus.go
internal/infra/broker/eventbus_test.go
```

## Verification
```bash
go test ./internal/infra/storage/memory/... -v -race
go test ./internal/infra/broker/... -v -race
```

## Exit Criteria
- All tests pass with `-race` flag (no race conditions)
- Storage is thread-safe under concurrent access
- Event bus dispatches events to multiple subscribers
- Event bus is non-blocking (slow subscriber doesn't block publisher)
