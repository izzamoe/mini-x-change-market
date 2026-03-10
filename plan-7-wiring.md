# Plan 7: Wiring, Graceful Shutdown & Integration
# Estimated: ~40 minutes
# Dependencies: Plan 1-6 (all core components)

## Objective
Wire all components together in main.go with proper dependency injection, implement graceful shutdown, and verify the complete system works end-to-end.

---

## Tasks

### 7.1 Dependency Wiring (cmd/server/main.go)
Complete the main.go to wire all components:

```go
func main() {
    // 1. Load config
    cfg := config.Load()
    
    // 2. Setup logger
    setupLogger(cfg.LogLevel, cfg.LogFormat)
    
    // 3. Create context with signal handling
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()
    
    // 4. Initialize storage (in-memory, always)
    orderRepo := memory.NewOrderRepository()
    tradeRepo := memory.NewTradeRepository()
    marketRepo := memory.NewMarketRepository()
    
    // 5. Initialize event bus
    eventBus := broker.NewEventBus(10000)
    eventBus.Start(ctx)
    
    // 6. Initialize matching engine
    stocks := entity.DefaultStocks() // BBCA, BBRI, TLKM, ASII, GOTO
    eng := engine.NewEngine(stocks, eventBus, orderRepo, tradeRepo, marketRepo)
    eng.Start(ctx)
    
    // 7. Initialize application services
    orderSvc := order.NewService(orderRepo, eng, eventBus)
    tradeSvc := trade.NewService(tradeRepo)
    marketSvc := market.NewService(marketRepo, eng)
    
    // 8. Initialize price simulator
    sim := simulator.NewPriceSimulator(stocks, eventBus, marketRepo, cfg.Simulator)
    sim.Start(ctx)
    
    // 9. Initialize Binance feed (optional)
    if cfg.BinanceFeedEnabled {
        binanceFeed := simulator.NewBinanceFeed(cfg.Binance, eventBus, marketRepo)
        go binanceFeed.Start(ctx)
    }
    
    // 10. Initialize WebSocket hub
    hub := ws.NewHub(cfg.WebSocket)
    go hub.Run(ctx)
    
    // 11. Wire event bus → WebSocket hub
    wireEvents(eventBus, hub)
    
    // 12. Initialize HTTP handlers
    orderHandler := handler.NewOrderHandler(orderSvc)
    tradeHandler := handler.NewTradeHandler(tradeSvc)
    marketHandler := handler.NewMarketHandler(marketSvc)
    wsHandler := ws.NewHandler(hub, cfg.WebSocket)
    
    // 13. Initialize middleware
    // (rate limiter, auth if enabled, logging, recovery, cors)
    
    // 14. Build router
    router := httpTransport.NewRouter(
        orderHandler, tradeHandler, marketHandler, wsHandler,
        cfg,
    )
    
    // 15. Create HTTP server
    srv := &http.Server{
        Addr:         ":" + cfg.ServerPort,
        Handler:      router,
        ReadTimeout:  cfg.ServerReadTimeout,
        WriteTimeout: cfg.ServerWriteTimeout,
    }
    
    // 16. Start server in goroutine
    go func() {
        slog.Info("server starting", "port", cfg.ServerPort)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            slog.Error("server error", "error", err)
            cancel()
        }
    }()
    
    // 17. Wait for shutdown signal
    <-ctx.Done()
    slog.Info("shutting down...")
    
    // 18. Graceful shutdown
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
    defer shutdownCancel()
    
    srv.Shutdown(shutdownCtx)
    eng.Stop()
    sim.Stop()
    eventBus.Stop()
    hub.Stop()
    
    slog.Info("server stopped")
}
```

### 7.2 Event Wiring Function
```go
func wireEvents(bus event.EventBus, hub *ws.Hub) {
    bus.Subscribe(event.TradeExecuted, func(e event.Event) {
        hub.Broadcast("market.trade", e.StockCode, e.Payload)
    })
    
    bus.Subscribe(event.TickerUpdated, func(e event.Event) {
        hub.Broadcast("market.ticker", e.StockCode, e.Payload)
    })
    
    bus.Subscribe(event.OrderBookUpdated, func(e event.Event) {
        hub.Broadcast("market.orderbook", e.StockCode, e.Payload)
    })
    
    bus.Subscribe(event.OrderUpdated, func(e event.Event) {
        order := e.Payload.(*entity.Order)
        hub.BroadcastToUser("order.update", order.UserID, e.Payload)
    })
}
```

### 7.3 Graceful Shutdown Order
```
Signal received (SIGINT/SIGTERM)
    │
    ├── 1. Stop accepting new HTTP requests (srv.Shutdown)
    │       → Existing requests complete within timeout
    │
    ├── 2. Stop matching engine (eng.Stop)
    │       → Close order channels
    │       → Wait for in-flight matches to complete
    │       → WaitGroup.Wait()
    │
    ├── 3. Stop price simulator (sim.Stop)
    │       → Cancel context
    │       → Wait for goroutines to exit
    │
    ├── 4. Stop event bus (eventBus.Stop)
    │       → Drain remaining events
    │       → Close dispatch channel
    │
    ├── 5. Stop WebSocket hub (hub.Stop)
    │       → Close all client connections
    │       → Wait for all client goroutines to exit
    │
    └── 6. Exit
```

### 7.4 Health Check Endpoint
```go
func healthHandler(w http.ResponseWriter, r *http.Request) {
    response.JSON(w, http.StatusOK, map[string]interface{}{
        "status": "ok",
        "uptime": time.Since(startTime).String(),
        "goroutines": runtime.NumGoroutine(),
        "ws_clients": hub.ClientCount(),
    })
}
```

### 7.5 .env.example File
- Create `.env.example` with all config vars and their defaults
- Copy to `.env` for local development

### 7.6 Integration Test (End-to-End)
- **cmd/server/main_test.go** or **internal/integration_test.go**:
  1. Start full server (in-memory mode)
  2. Connect WS client, subscribe to `market.ticker` for BBCA
  3. Verify ticker updates arrive
  4. Create BUY order via HTTP POST
  5. Create matching SELL order via HTTP POST
  6. Verify trade event arrives via WS
  7. Verify order status updates via HTTP GET
  8. Verify trade history via HTTP GET
  9. Verify market snapshot via HTTP GET
  10. Shutdown cleanly

---

## Files Created/Updated
```
cmd/server/main.go          (complete wiring)
.env.example
internal/integration_test.go (optional end-to-end test)
```

## Verification
```bash
go build ./cmd/server/...
go run ./cmd/server
# In another terminal:
curl http://localhost:8080/healthz
curl http://localhost:8080/api/v1/market/ticker
npx wscat -c ws://localhost:8080/ws
# > {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}
# Should receive ticker updates
curl -X POST http://localhost:8080/api/v1/orders -H 'Content-Type: application/json' -d '{"stock_code":"BBCA","side":"BUY","price":9500,"quantity":100}'
curl -X POST http://localhost:8080/api/v1/orders -H 'Content-Type: application/json' -d '{"stock_code":"BBCA","side":"SELL","price":9500,"quantity":100}'
# Should see trade event in wscat
curl http://localhost:8080/api/v1/trades?stock=BBCA
# Kill with Ctrl+C → should shutdown gracefully
```

## Exit Criteria
- Server starts and all components initialize correctly
- Ticker simulation runs and broadcasts via WS
- Order creation → matching → trade → WS broadcast works end-to-end
- All REST endpoints return correct data
- Graceful shutdown exits cleanly (no goroutine leaks)
- Health check returns system info
