# Plan 6: Price Simulator & Binance Feed
# Estimated: ~40 minutes
# Dependencies: Plan 1 (domain), Plan 2 (event bus, market repo)

## Objective
Implement the internal price simulator (random walk + mean reversion) and optional Binance external WebSocket feed for real crypto market data. Both feed into the event bus → WebSocket hub pipeline.

---

## Tasks

### 6.1 Internal Price Simulator (internal/simulator/price.go)
- One goroutine per stock
- Algorithm: Random Walk with Mean Reversion
```go
type Simulator struct {
    stocks    []entity.Stock
    eventBus  event.EventBus
    marketRepo repository.MarketRepository
    config    SimulatorConfig
}

// Per-stock simulation goroutine
func (s *Simulator) simulateStock(ctx context.Context, stock entity.Stock) {
    currentPrice := stock.BasePrice
    prevClose := stock.BasePrice
    high := stock.BasePrice
    low := stock.BasePrice
    volume := int64(0)

    for {
        select {
        case <-ctx.Done():
            return
        case <-time.After(randomDuration(config.IntervalMin, config.IntervalMax)):
            // Random walk
            delta := float64(currentPrice) * 0.005 * (rand.Float64()*2 - 1)
            
            // Mean reversion (pull back toward base price)
            delta += float64(stock.BasePrice-currentPrice) * 0.01
            
            // Apply delta
            newPrice := currentPrice + int64(delta)
            
            // Round to tick size
            newPrice = roundToTick(newPrice, stock.TickSize)
            
            // Clamp to ±10% of base
            newPrice = clamp(newPrice, stock.BasePrice*90/100, stock.BasePrice*110/100)
            
            // Update stats
            tickVolume := rand.Int63n(450) + 50  // 50-500
            volume += tickVolume
            if newPrice > high { high = newPrice }
            if newPrice < low { low = newPrice }
            
            // Create ticker
            ticker := &entity.Ticker{
                StockCode: stock.Code,
                LastPrice: newPrice,
                PrevClose: prevClose,
                Change:    newPrice - prevClose,
                ChangePct: float64(newPrice-prevClose) / float64(prevClose) * 100,
                High:      high,
                Low:       low,
                Volume:    volume,
                UpdatedAt: time.Now(),
            }
            
            // Store and broadcast
            s.marketRepo.UpdateTicker(ctx, ticker)
            s.eventBus.Publish(event.Event{
                Type:      event.TickerUpdated,
                StockCode: stock.Code,
                Payload:   ticker,
                Timestamp: time.Now(),
            })
            
            currentPrice = newPrice
        }
    }
}
```

- Methods:
  - `Start(ctx)` — launch goroutine per stock
  - `Stop()` — cancel context, wait for goroutines
  - `UpdateFromTrade(stock, price, qty)` — override simulation with real trade price

### 6.2 Trade-Based Price Updates
- When a trade occurs, update the ticker immediately (don't wait for next simulation tick)
- Simulator subscribes to `TradeExecuted` events from event bus
- On trade event: update currentPrice, volume, high, low → emit new TickerUpdated event
- This makes the ticker reflect both simulated AND real trading activity

### 6.3 Binance External Feed (internal/simulator/binance.go) — BONUS
```go
type BinanceFeed struct {
    config    BinanceConfig
    eventBus  event.EventBus
    marketRepo repository.MarketRepository
    stockMap  map[string]string  // binance symbol → local stock code
}

// Connect to Binance public WebSocket
func (b *BinanceFeed) Start(ctx context.Context) error {
    // Build combined stream URL
    // wss://stream.binance.com:9443/stream?streams=btcusdt@miniTicker/ethusdt@miniTicker/...
    
    for {
        select {
        case <-ctx.Done():
            return nil
        default:
            err := b.connect(ctx)
            if err != nil {
                slog.Error("binance feed disconnected", "error", err)
                // Exponential backoff reconnect
                backoff(attempt)
                continue
            }
        }
    }
}

func (b *BinanceFeed) connect(ctx context.Context) error {
    conn, _, err := websocket.Dial(ctx, b.config.WSURL+"/stream?streams="+b.streams(), nil)
    // Read loop
    for {
        _, msg, err := conn.Read(ctx)
        // Parse Binance miniTicker message
        // {
        //   "stream": "btcusdt@miniTicker",
        //   "data": {
        //     "e": "24hrMiniTicker",
        //     "s": "BTCUSDT",
        //     "c": "60123.45",  // close price
        //     "h": "61000.00",  // high
        //     "l": "59500.00",  // low
        //     "v": "12345.67",  // volume
        //   }
        // }
        
        // Map to local stock code
        localStock := b.stockMap[symbol]
        
        // Create ticker entity
        // Store in market repo
        // Emit TickerUpdated event
    }
}
```

- Features:
  - Auto-reconnect with exponential backoff (1s → 2s → 4s → ... → 30s max)
  - Graceful disconnect on context cancel
  - Falls back to internal simulator if Binance connection fails
  - Maps: btcusdt→BTCUSDT, ethusdt→ETHUSDT, bnbusdt→BNBUSDT, solusdt→SOLUSDT, adausdt→ADAUSDT

### 6.4 Simulator Interface (internal/simulator/simulator.go)
```go
type PriceFeeder interface {
    Start(ctx context.Context) error
    Stop()
}
```
- Both `Simulator` and `BinanceFeed` implement this interface
- Config determines which one(s) to start
- Can run BOTH simultaneously (different stocks)

### 6.5 Unit Tests
- **simulator/price_test.go**:
  - Test price stays within ±10% of base
  - Test tick size rounding
  - Test volume accumulation
  - Test trade-based update overrides simulation
- **simulator/binance_test.go**:
  - Test message parsing
  - Test stock mapping
  - Test reconnect logic (mock server)

---

## Files Created
```
internal/simulator/simulator.go
internal/simulator/price.go
internal/simulator/price_test.go
internal/simulator/binance.go
internal/simulator/binance_test.go
```

## Verification
```bash
go test ./internal/simulator/... -v -race
# Manual verification: start server, connect via wscat, see ticker updates every 1-3s
```

## Exit Criteria
- Internal simulator generates realistic price movements
- Prices stay within ±10% of base price
- Ticker updates broadcast to WS subscribers
- Trade-based updates override simulation
- Binance feed connects and maps data correctly (when enabled)
- Auto-reconnect works on disconnect
