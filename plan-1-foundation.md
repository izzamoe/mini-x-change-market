# Plan 1: Project Foundation & Domain Layer
# Estimated: ~45 minutes
# Dependencies: None (first plan)

## Objective
Setup project structure, Go modules, configuration, and implement the core domain layer (entities, events, repository interfaces). This is the foundation that ALL other plans depend on.

---

## Tasks

### 1.1 Initialize Go Module & Project Structure
```bash
mkdir -p cmd/server
mkdir -p internal/{domain/{entity,event,repository},app/{order,trade,market},engine,worker}
mkdir -p internal/infra/{storage/{memory,postgres,redis},broker/{nats},auth}
mkdir -p internal/transport/{http/{handler,middleware},ws}
mkdir -p internal/simulator
mkdir -p pkg/{response,validator}
mkdir -p config
mkdir -p migrations
go mod init github.com/izzam/mini-exchange
```

### 1.2 Install Core Dependencies
```bash
go get github.com/go-chi/chi/v5
go get github.com/coder/websocket
go get github.com/google/uuid
go get github.com/golang-jwt/jwt/v5
go get golang.org/x/time
go get github.com/stretchr/testify
go get github.com/prometheus/client_golang
go get github.com/nats-io/nats.go
go get github.com/redis/go-redis/v9
go get github.com/jackc/pgx/v5
go get github.com/golang-migrate/migrate/v4
```

### 1.3 Configuration (config/config.go)
- Struct-based config with env var loading
- All settings from ARCHITECTURE.md Section 14
- Default values for development
- Validation function

### 1.4 Domain Entities (internal/domain/entity/)
Files to create:
- **order.go**: Order struct, Side enum (BUY/SELL), OrderStatus enum (OPEN/PARTIAL/FILLED/CANCELLED)
- **trade.go**: Trade struct (ID, StockCode, BuyOrderID, SellOrderID, Price, Qty, etc.)
- **ticker.go**: Ticker struct (StockCode, LastPrice, PrevClose, Change, ChangePct, High, Low, Volume)
- **orderbook.go**: PriceLevel struct, OrderBook struct (Bids/Asks sorted)
- **user.go**: User struct for auth (ID, Username, PasswordHash)
- **stock.go**: Stock struct (Code, Name, BasePrice, TickSize) + stock registry

### 1.5 Domain Events (internal/domain/event/event.go)
- EventType enum: OrderCreated, OrderUpdated, TradeExecuted, TickerUpdated, OrderBookUpdated
- Event struct: Type, StockCode, Payload, Timestamp
- EventBus interface: Publish(event), Subscribe(eventType, handler), Unsubscribe()

### 1.6 Repository Interfaces (internal/domain/repository/)
- **order.go**: OrderRepository interface (Save, FindByID, FindAll, Update)
- **trade.go**: TradeRepository interface (Save, FindByStockCode, FindAll)
- **market.go**: MarketRepository interface (GetTicker, UpdateTicker, GetOrderBook, UpdateOrderBook)

### 1.7 Shared Utilities (pkg/)
- **pkg/response/json.go**: Standardized JSON response helpers (Success, Error, Paginated)
- **pkg/validator/validator.go**: Input validation helpers (ValidateStockCode, ValidateSide, ValidatePrice, ValidateQuantity)

### 1.8 Basic main.go Skeleton (cmd/server/main.go)
- Parse config
- Logger setup (slog)
- Placeholder for dependency wiring
- Graceful shutdown skeleton (signal.NotifyContext)

---

## Files Created
```
cmd/server/main.go
config/config.go
internal/domain/entity/order.go
internal/domain/entity/trade.go
internal/domain/entity/ticker.go
internal/domain/entity/orderbook.go
internal/domain/entity/user.go
internal/domain/entity/stock.go
internal/domain/event/event.go
internal/domain/repository/order.go
internal/domain/repository/trade.go
internal/domain/repository/market.go
pkg/response/json.go
pkg/validator/validator.go
go.mod
go.sum
```

## Verification
```bash
go build ./...
go vet ./...
```

## Exit Criteria
- `go build ./...` succeeds
- All entities have proper JSON tags
- All repository interfaces are defined
- Config loads from env vars with defaults
- main.go starts and exits cleanly
