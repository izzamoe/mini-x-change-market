# Plan 3: Matching Engine
# Estimated: ~60 minutes
# Dependencies: Plan 1 (domain), Plan 2 (storage, event bus)

## Objective
Implement the core matching engine with FIFO matching, partial fill, per-stock goroutine isolation, and order book management. This is the heart of the trading system.

---

## Tasks

### 3.1 Order Book (internal/engine/orderbook.go)
- In-memory order book per stock
- Data structures:
  - Bids: sorted DESC by price, FIFO within same price level
  - Asks: sorted ASC by price, FIFO within same price level
  - Use `container/heap` or sorted slice for efficient insert/remove
- Methods:
  - `AddOrder(order)` — insert into correct side (bid/ask) at correct position
  - `RemoveOrder(orderID)` — remove from book (after fill)
  - `BestBid()` — highest bid price level
  - `BestAsk()` — lowest ask price level
  - `GetDepth(levels int)` — return top N price levels for both sides (for orderbook snapshot)
  - `UpdateOrder(orderID, filledQty)` — update remaining quantity after partial fill

### 3.2 Matcher (internal/engine/matcher.go)
- Per-stock matching logic (runs in dedicated goroutine)
- Receives orders via buffered channel (cap 1000)
- Core matching algorithm:
  ```
  func (m *Matcher) matchOrder(incomingOrder):
      if incomingOrder.Side == BUY:
          match against asks (lowest price first)
          while bestAsk != nil && bestAsk.Price <= incomingOrder.Price && incomingOrder.RemainingQty > 0:
              tradeQty = min(incomingOrder.RemainingQty, bestAsk.RemainingQty)
              tradePrice = bestAsk.Price  (passive order price)
              create Trade(tradeQty, tradePrice)
              update both orders (filled/partial)
              emit TradeExecuted event
              emit OrderUpdated events
              if bestAsk fully filled: remove from book
      else (SELL):
          match against bids (highest price first)
          (mirror logic)
      
      if incomingOrder still has remaining qty:
          add to order book (becomes resting order)
      
      emit OrderBookUpdated event (snapshot)
  ```
- Price-Time Priority: FIFO within same price level (arrival time)
- Partial Fill: order partially filled, remainder stays in book with status PARTIAL
- Full Fill: order fully filled, status changes to FILLED

### 3.3 Engine Manager (internal/engine/engine.go)
- Manages all per-stock matchers
- Methods:
  - `NewEngine(stocks, eventBus, orderRepo, tradeRepo, marketRepo)` — create engine with matchers for each stock
  - `SubmitOrder(order)` — route order to correct stock's matcher channel
    - Returns error if channel full (backpressure → 503)
  - `Start(ctx)` — start all matcher goroutines
  - `Stop()` — graceful shutdown, close all channels, wait for goroutines to finish
  - `GetOrderBook(stock)` — get current order book snapshot (thread-safe)
- Goroutine lifecycle:
  - Each matcher runs in its own goroutine
  - Exits when context is cancelled or channel is closed
  - Uses `sync.WaitGroup` to track running matchers

### 3.4 Concurrency Safety
- **Zero mutex in matcher**: Single goroutine per stock consumes from channel → no concurrent access to order book
- **Channel-based isolation**: Order book state is ONLY accessed by its matcher goroutine
- **Backpressure**: If order channel full, SubmitOrder returns error (no blocking)
- **Event emission**: Non-blocking publish to event bus

### 3.5 Unit Tests (internal/engine/)
- **orderbook_test.go**:
  - Test add/remove orders
  - Test bid/ask sorting (price DESC/ASC)
  - Test FIFO within same price level
  - Test depth snapshot
- **matcher_test.go**:
  - Test exact match (BUY 100@9500, SELL 100@9500 → 1 trade)
  - Test partial fill (BUY 100@9500, SELL 50@9500 → 1 trade, BUY has 50 remaining)
  - Test multi-level match (BUY 100@9600, SELL 50@9500 + SELL 50@9600 → 2 trades)
  - Test no match (BUY 100@9400, SELL 100@9500 → 0 trades, both in book)
  - Test FIFO priority (2 SELLs at same price, first one matches first)
  - Test price improvement (BUY@9600 matches SELL@9500, trade at 9500)
- **engine_test.go**:
  - Test submit order routing to correct stock
  - Test backpressure when channel full
  - Test graceful shutdown

---

## Files Created
```
internal/engine/orderbook.go
internal/engine/orderbook_test.go
internal/engine/matcher.go
internal/engine/matcher_test.go
internal/engine/engine.go
internal/engine/engine_test.go
```

## Verification
```bash
go test ./internal/engine/... -v -race -count=1
```

## Exit Criteria
- All matching scenarios pass (exact, partial, multi-level, no-match, FIFO)
- Tests pass with `-race` (no race conditions)
- Matching engine processes orders without blocking
- Order book maintains correct sorted order at all times
- Events are emitted for every trade and order update
