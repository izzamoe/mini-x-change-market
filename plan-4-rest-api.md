# Plan 4: Application Services & REST API
# Estimated: ~50 minutes
# Dependencies: Plan 1 (domain), Plan 2 (storage), Plan 3 (engine)

## Objective
Implement application service layer (use cases) and REST API handlers using Chi router. This connects the HTTP transport layer to the business logic.

---

## Tasks

### 4.1 Application Services

#### Order Service (internal/app/order/service.go)
```go
type Service struct {
    orderRepo  repository.OrderRepository
    engine     *engine.Engine
    eventBus   event.EventBus
}

func (s *Service) CreateOrder(ctx, req CreateOrderRequest) (*entity.Order, error)
    // Validate input
    // Create Order entity (ID=uuid, Status=OPEN, CreatedAt=now)
    // Save to repository
    // Submit to matching engine (non-blocking)
    // Publish OrderCreated event
    // Return order

func (s *Service) GetOrders(ctx, filter OrderFilter) ([]*entity.Order, int64, error)
    // Delegate to repository with filter (stock, status, user_id, pagination)

func (s *Service) GetOrderByID(ctx, id string) (*entity.Order, error)
    // Delegate to repository
```

#### Trade Service (internal/app/trade/service.go)
```go
type Service struct {
    tradeRepo repository.TradeRepository
}

func (s *Service) GetTradeHistory(ctx, filter TradeFilter) ([]*entity.Trade, int64, error)
    // Delegate to repository with filter (stock, pagination)

func (s *Service) GetTradesByStock(ctx, stock string, limit int) ([]*entity.Trade, error)
    // Recent trades for a stock
```

#### Market Service (internal/app/market/service.go)
```go
type Service struct {
    marketRepo repository.MarketRepository
    engine     *engine.Engine
}

func (s *Service) GetTicker(ctx, stock string) (*entity.Ticker, error)
func (s *Service) GetAllTickers(ctx) ([]*entity.Ticker, error)
func (s *Service) GetOrderBook(ctx, stock string) (*entity.OrderBook, error)
func (s *Service) GetRecentTrades(ctx, stock string, limit int) ([]*entity.Trade, error)
```

### 4.2 HTTP Router (internal/transport/http/router.go)
- Chi router setup with route groups
- Middleware chain: Recovery → Logger → CORS → RateLimit → (Auth for protected routes)
- Route registration:
```
r := chi.NewRouter()
r.Use(middleware.Recovery)
r.Use(middleware.Logger)
r.Use(middleware.CORS)
r.Use(middleware.RateLimit)

r.Route("/api/v1", func(r chi.Router) {
    // Public routes
    r.Post("/auth/register", authHandler.Register)
    r.Post("/auth/login", authHandler.Login)

    // Protected routes (optional: JWT middleware)
    r.Group(func(r chi.Router) {
        r.Use(middleware.OptionalAuth)  // Auth if enabled, pass-through if not
        
        r.Post("/orders", orderHandler.Create)
        r.Get("/orders", orderHandler.List)
        r.Get("/orders/{id}", orderHandler.GetByID)
        
        r.Get("/trades", tradeHandler.List)
        
        r.Get("/market/ticker", marketHandler.AllTickers)
        r.Get("/market/ticker/{stock}", marketHandler.Ticker)
        r.Get("/market/orderbook/{stock}", marketHandler.OrderBook)
        r.Get("/market/trades/{stock}", marketHandler.RecentTrades)
    })
})

r.Get("/healthz", healthHandler)
r.Handle("/metrics", promhttp.Handler())
```

### 4.3 HTTP Handlers (internal/transport/http/handler/)

#### Order Handler (handler/order.go)
- `Create(w, r)` — Parse JSON body, validate, call service, return 201
- `List(w, r)` — Parse query params (stock, status, page, per_page), call service, return 200
- `GetByID(w, r)` — Extract {id} from URL, call service, return 200 or 404

#### Trade Handler (handler/trade.go)
- `List(w, r)` — Parse query params (stock, limit, page), call service, return 200

#### Market Handler (handler/market.go)
- `AllTickers(w, r)` — Call service, return all tickers
- `Ticker(w, r)` — Extract {stock}, return single ticker or 404
- `OrderBook(w, r)` — Extract {stock}, return order book depth
- `RecentTrades(w, r)` — Extract {stock}, return last N trades

### 4.4 Middleware (internal/transport/http/middleware/)

#### Logging Middleware (middleware/logging.go)
- Log request method, path, status, duration, IP using slog
- Structured JSON output

#### Recovery Middleware (middleware/recovery.go)
- Recover from panics in handlers
- Log stack trace
- Return 500 Internal Server Error

#### CORS Middleware (middleware/cors.go)
- Allow all origins (dev mode)
- Configurable allowed methods, headers

#### Rate Limit Middleware (middleware/ratelimit.go) — BONUS
- Token bucket per IP using `golang.org/x/time/rate`
- Configurable RPS and burst
- Returns 429 Too Many Requests when exceeded
- Cleanup expired limiters periodically

### 4.5 Unit Tests
- **handler/order_test.go**: Test create order (success, validation error, engine full)
- **handler/trade_test.go**: Test list trades with filters
- **handler/market_test.go**: Test ticker, orderbook, recent trades
- **middleware/ratelimit_test.go**: Test rate limiting behavior

---

## Files Created
```
internal/app/order/service.go
internal/app/trade/service.go
internal/app/market/service.go
internal/transport/http/router.go
internal/transport/http/handler/order.go
internal/transport/http/handler/trade.go
internal/transport/http/handler/market.go
internal/transport/http/middleware/logging.go
internal/transport/http/middleware/recovery.go
internal/transport/http/middleware/cors.go
internal/transport/http/middleware/ratelimit.go
internal/transport/http/handler/order_test.go
internal/transport/http/handler/market_test.go
internal/transport/http/middleware/ratelimit_test.go
```

## Verification
```bash
go test ./internal/app/... -v
go test ./internal/transport/http/... -v -race
# Manual test:
# curl -X POST http://localhost:8080/api/v1/orders -d '{"stock_code":"BBCA","side":"BUY","price":9500,"quantity":100}'
# curl http://localhost:8080/api/v1/orders?stock=BBCA
# curl http://localhost:8080/api/v1/market/ticker
# curl http://localhost:8080/api/v1/market/orderbook/BBCA
```

## Exit Criteria
- All REST endpoints respond correctly
- Rate limiting returns 429 when exceeded
- Validation errors return proper error response
- Order creation triggers matching engine
- Tests pass with `-race`
