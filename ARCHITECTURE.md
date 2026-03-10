# Mini Exchange - Realtime Trading System
# Complete Architecture Document

---

## 1. OVERVIEW

Membangun **mini realtime trading backend** (Mini Exchange) menggunakan Go 1.26 yang mampu:
- Menerima dan memproses order (buy/sell) via REST API
- Melakukan matching order secara otomatis (matching engine)
- Broadcast realtime data (ticker, trade, orderbook) via WebSocket
- Menjalankan simulasi harga secara periodik
- Handle 1000 orders/min, 500 concurrent WS clients, non-blocking broadcast

---

## 2. TECH STACK & JUSTIFICATION

### Core (Wajib)
| Technology | Package | Alasan |
|---|---|---|
| **Go 1.25** | stdlib | Latest stable version, leveraging Go 1.22+ features |
| **HTTP Router** | `github.com/go-chi/chi/v5` | Lightweight, idiomatic Go router. Berbasis `net/http.Handler` standard (bukan custom context seperti Gin). Middleware composable, routing tree efficient. Production-proven di banyak Go services. |
| **WebSocket** | `github.com/coder/websocket` | Fork maintained dari nhooyr.io/websocket. Context-aware, clean API, production-ready. gorilla/websocket sudah archived. |
| **UUID** | `github.com/google/uuid` | Standard UUID generation untuk order/trade ID |
| **Structured Logging** | `log/slog` (stdlib) | Built-in structured logging sejak Go 1.25. Tidak perlu external logger. |
| **Config** | `os` + env vars | Simple env-based config, no external deps needed |

### Bonus (Nice to Have - Semua diimplementasi)
| Technology | Package | Alasan |
|---|---|---|
| **NATS** | `github.com/nats-io/nats.go` | Lightweight message broker, Go-native. Lebih simple dari Kafka untuk skala ini. Dipakai untuk internal event streaming + horizontal scaling. |
| **Redis** | `github.com/redis/go-redis/v9` | Pub/sub untuk cross-instance WS broadcast + caching market snapshot. Enables horizontal scaling. |
| **PostgreSQL** | `github.com/jackc/pgx/v5` | Persistent storage, production-grade. pgx is the fastest pure-Go Postgres driver. |
| **JWT Auth** | `github.com/golang-jwt/jwt/v5` | Industry standard untuk API authentication |
| **Rate Limiting** | `golang.org/x/time/rate` | Token bucket algorithm, stdlib extension |
| **Monitoring** | `github.com/prometheus/client_golang` | Prometheus metrics: order count, latency, WS connections, matching engine throughput |
| **Testing** | `github.com/stretchr/testify` | Assertions + mocks untuk unit test |
| **Migration** | `github.com/golang-migrate/migrate/v4` | Database schema migration |

### Infrastructure (via Docker Compose)
| Service | Image | Port |
|---|---|---|
| **App** | Build from Dockerfile | 8080 (HTTP+WS) |
| **NATS** | `nats:latest` | 4222 |
| **Redis** | `redis:7-alpine` | 6379 |
| **PostgreSQL** | `postgres:16-alpine` | 5432 |

### Kenapa Chi (bukan Gin/Echo/Fiber/stdlib)?
- **Chi** berbasis `net/http.Handler` standard -- middleware dan handler bisa reuse dari ecosystem Go manapun
- **Gin** punya custom `gin.Context` yang non-standard, membuat code tightly coupled ke Gin
- **Echo/Fiber** juga custom context, Fiber bahkan pakai fasthttp yang tidak compatible dengan `net/http`
- **stdlib mux** sudah bagus di Go 1.22+ tapi tidak punya middleware chaining, route grouping, dan URL parameter extraction yang ergonomis
- Chi memberikan balance sempurna: **idiomatic Go + productive routing + zero vendor lock-in**

### Kenapa BUKAN:
- **Kafka**: Overkill untuk mini exchange. NATS lebih ringan, startup instant, Go-native.
- **gorilla/websocket**: Archived/unmaintained. `coder/websocket` adalah successor yang active.
- **GORM**: Terlalu heavy. Raw query dengan pgx lebih performant dan explicit.
- **External config lib**: Overkill. Env vars + simple parser cukup.

---

## 3. PROJECT STRUCTURE (Clean Architecture)

```
mini-exchange/
├── cmd/
│   └── server/
│       ├── main.go                    # Entry point, wire dependencies, start server
│       └── main_test.go               # End-to-end integration tests
├── internal/
│   ├── domain/                        # Layer 1: Domain (innermost, zero dependencies)
│   │   ├── entity/
│   │   │   ├── order.go               # Order entity (ID, StockCode, Side, Price, Qty, Status, etc.)
│   │   │   ├── trade.go               # Trade entity (ID, BuyOrderID, SellOrderID, Price, Qty, etc.)
│   │   │   ├── ticker.go              # Ticker/market data entity
│   │   │   ├── orderbook.go           # OrderBook entity (bids, asks, depth)
│   │   │   ├── stock.go               # Stock entity (code, name, base price)
│   │   │   └── user.go                # User entity (ID, username, password hash)
│   │   ├── event/
│   │   │   └── event.go               # Domain events (OrderCreated, TradeExecuted, TickerUpdated, etc.)
│   │   └── repository/
│   │       ├── errors.go              # Sentinel errors (ErrNotFound, ErrDuplicate, etc.)
│   │       ├── order.go               # OrderRepository interface
│   │       ├── trade.go               # TradeRepository interface
│   │       ├── market.go              # MarketRepository interface
│   │       └── user.go                # UserRepository interface (Save, FindByUsername, FindAll)
│   │
│   ├── app/                           # Layer 2: Application (use cases / services)
│   │   ├── order/
│   │   │   └── service.go             # CreateOrder, GetOrders use case
│   │   ├── trade/
│   │   │   └── service.go             # GetTradeHistory use case
│   │   └── market/
│   │       └── service.go             # GetTicker, GetOrderBook, GetRecentTrades use case
│   │
│   ├── engine/                        # Layer 2.5: Matching Engine (core business logic)
│   │   ├── matcher.go                 # Matching engine core (per-stock goroutine, channel-based)
│   │   ├── orderbook.go               # In-memory order book (sorted bids/asks, FIFO per price level)
│   │   └── engine.go                  # Engine manager (manages matchers for all stocks)
│   │
│   ├── worker/                        # Background Workers
│   │   └── dbworker.go               # DB Worker: async batch persist to PostgreSQL
│   │                                  #   - Collects writes via buffered channel
│   │                                  #   - Flushes every 100ms or 100 writes (whichever first)
│   │                                  #   - Retry logic on failure
│   │                                  #   - Graceful drain on shutdown
│   │
│   ├── infra/                         # Layer 3: Infrastructure (implementations)
│   │   ├── storage/
│   │   │   ├── memory/                # In-memory storage (WAJIB)
│   │   │   │   ├── order_repo.go
│   │   │   │   ├── trade_repo.go
│   │   │   │   └── market_repo.go
│   │   │   ├── postgres/              # PostgreSQL storage (BONUS)
│   │   │   │   ├── order_repo.go
│   │   │   │   ├── trade_repo.go
│   │   │   │   └── user_repo.go      # UserRepository: Save, FindByUsername, FindAll
│   │   │   └── redis/                 # Redis cache layer (BONUS)
│   │   │       ├── cache.go
│   │   │       └── pubsub.go
│   │   ├── broker/
│   │   │   ├── eventbus.go            # In-memory event bus (default)
│   │   │   └── natsbroker/            # NATS event bus (BONUS)
│   │   │       ├── publisher.go
│   │   │       └── subscriber.go
│   │   └── auth/
│   │       └── jwt.go                 # JWT token generation & validation (BONUS)
│   │                                  #   SetUserRepo(repo) — attach PostgreSQL user repo
│   │                                  #   LoadUsers(ctx)    — warm in-memory cache from PG on startup
│   │                                  #   Register()        — persist new user to PG + in-memory cache
│   │
│   ├── transport/                     # Layer 4: Transport / Delivery
│   │   ├── http/
│   │   │   ├── router.go             # HTTP route registration
│   │   │   ├── handler/
│   │   │   │   ├── order.go           # Order HTTP handlers
│   │   │   │   ├── trade.go           # Trade HTTP handlers
│   │   │   │   ├── market.go          # Market HTTP handlers
│   │   │   │   └── auth.go            # Auth HTTP handlers (BONUS)
│   │   │   └── middleware/
│   │   │       ├── auth.go            # JWT auth middleware (BONUS)
│   │   │       ├── ratelimit.go       # Rate limiting middleware (BONUS)
│   │   │       ├── logging.go         # Request logging middleware
│   │   │       ├── recovery.go        # Panic recovery middleware
│   │   │       └── cors.go            # CORS middleware
│   │   └── ws/
│   │       ├── hub.go                 # WebSocket Hub (connection manager, fan-out broadcaster)
│   │       ├── client.go              # WebSocket Client (per-connection read/write goroutines)
│   │       ├── handler.go             # WebSocket upgrade handler
│   │       └── message.go             # WS message types (subscribe, unsubscribe, data)
│   │
│   └── simulator/                     # Price Simulation
│       ├── simulator.go               # Simulator orchestrator (manages per-stock goroutines)
│       ├── price.go                   # Internal price simulator (random walk + mean reversion)
│       └── binance.go                 # External Binance WebSocket feed (real market data)
│                                      #   - Connects to wss://stream.binance.com:9443
│                                      #   - Maps crypto pairs (BTCUSDT, ETHUSDT) to local stocks
│                                      #   - Fallback to internal simulator if Binance unavailable
│
├── migrations/                        # SQL migration files (up/down)
│   ├── 001_create_orders.up.sql
│   ├── 001_create_orders.down.sql
│   ├── 002_create_trades.up.sql
│   ├── 002_create_trades.down.sql
│   ├── 003_create_users.up.sql
│   └── 003_create_users.down.sql
├── pkg/                               # Shared utilities (bisa dipakai external)
│   ├── response/
│   │   └── json.go                    # Standardized JSON response helper
│   └── validator/
│       └── validator.go               # Input validation helpers
│
├── config/
│   └── config.go                      # Configuration struct + loader
│
├── docker-compose.yml                 # Docker Compose (app + NATS + Redis + PostgreSQL)
├── Dockerfile                         # Multi-stage Go build
├── Makefile                           # Build, run, test commands
├── .env.example                       # Environment variables template
├── go.mod
├── go.sum
└── README.md                          # Complete documentation
```

### Kenapa Clean Architecture?
1. **Testability**: Domain & app layer bisa ditest tanpa infrastructure
2. **Swappable**: Storage bisa diganti dari memory ke postgres tanpa ubah business logic
3. **Readable**: Setiap layer punya responsibility jelas
4. **Scalable**: Mudah tambah fitur baru tanpa ganggu existing code
5. **Standard international**: Mengikuti pola yang umum di production Go codebases

### Dependency Rule (Inward Only):
```
Transport → Application → Domain ← Infrastructure
     ↓           ↓
   Engine    Event Bus
```
- Domain **tidak depend** pada apapun
- Application depend pada Domain interfaces
- Infrastructure **implements** Domain interfaces
- Transport depend pada Application services
- Engine depend pada Domain entities & events

---

## 4. APPLICATION FLOW

### 4.1 Order Creation Flow
```
Client (HTTP POST /api/v1/orders)
    │
    ▼
[Middleware Chain]
    │ → Rate Limiter (token bucket, 100 req/s per IP)
    │ → JWT Auth (validate token, extract user_id)
    │ → Request Logger (slog structured log)
    │ → Panic Recovery
    │
    ▼
[Order Handler] ─── Validate input (stock_code, side, price, qty)
    │
    ▼
[Order Service] ─── Create Order entity (status=OPEN, timestamp=now)
    │
    ├──▶ [OrderRepository.Save()] ─── Persist order (memory/postgres)
    │
    ├──▶ [EventBus.Publish(OrderCreated)] ─── Emit event
    │         │
    │         ├──▶ [NATS Publish] (if enabled) ─── For horizontal scaling
    │         │
    │         └──▶ [WS Hub] ─── Broadcast to order.update subscribers
    │
    └──▶ [Matching Engine Channel] ─── Send order to matcher (non-blocking)
              │
              ▼
         [Per-Stock Matcher Goroutine]
              │
              ├── Insert into OrderBook (sorted by price, FIFO per level)
              │
              ├── Try Match:
              │     BUY order: match with lowest ASK where ask_price <= buy_price
              │     SELL order: match with highest BID where bid_price >= sell_price
              │
              ├── If Match Found:
              │     ├── Create Trade entity
              │     ├── Update order statuses (FILLED / PARTIAL)
              │     ├── EventBus.Publish(TradeExecuted)
              │     │     ├──▶ [WS Hub] → market.trade channel
              │     │     └──▶ [WS Hub] → market.ticker channel (update last price)
              │     ├── EventBus.Publish(OrderBookUpdated)
              │     │     └──▶ [WS Hub] → market.orderbook channel
              │     └── EventBus.Publish(OrderUpdated)
              │           └──▶ [WS Hub] → order.update channel
              │
              └── If No Match:
                    └── Order stays in book (waiting for counter-party)

    ▼
[HTTP Response] ─── Return created order (201 Created)
```

### 4.2 Matching Engine Detail Flow
```
Per-Stock Matching Engine:

                    ┌─────────────────────────┐
                    │   ORDER BOOK (BBCA)      │
                    │                          │
                    │  BIDS (Buy)   │  ASKS (Sell)  │
                    │  sorted DESC  │  sorted ASC   │
                    │               │               │
                    │  9500 x 100   │  9600 x 50    │
                    │  9450 x 200   │  9650 x 150   │
                    │  9400 x 50    │  9700 x 200   │
                    └─────────────────────────┘

New BUY order: BBCA @ 9650 x 80
    → Match with ASK @ 9600 x 50  → Trade: 50 @ 9600 (ASK fully filled)
    → Match with ASK @ 9650 x 150 → Trade: 30 @ 9650 (partial, 120 remaining on ASK)
    → BUY order fully filled (50 + 30 = 80)

Matching Rules:
1. Price-Time Priority (FIFO): Orders at same price matched by arrival time
2. Partial Fill: Order dapat diisi sebagian, sisa tetap di book
3. Best Price First: BUY matches lowest ASK, SELL matches highest BID
4. Atomic: Matching per stock di-serialize via single goroutine (no race condition)
```

### 4.3 WebSocket Flow
```
Client WebSocket Connection:

1. CONNECT
   Client ──────── WS Upgrade ──────── Server
                  GET /ws
                  Upgrade: websocket

2. SUBSCRIBE
   Client ──▶ {"action": "subscribe", "channel": "market.ticker", "stock": "BBCA"}
   Server ◀── {"type": "subscribed", "channel": "market.ticker", "stock": "BBCA"}

3. RECEIVE DATA (continuous)
   Server ──▶ {"type": "ticker", "stock": "BBCA", "data": {"last": 9600, "change": 50, "volume": 12500}}
   Server ──▶ {"type": "trade", "stock": "BBCA", "data": {"price": 9600, "qty": 50, "side": "BUY", "ts": "..."}}
   Server ──▶ {"type": "orderbook", "stock": "BBCA", "data": {"bids": [...], "asks": [...]}}
   Server ──▶ {"type": "order_update", "data": {"order_id": "...", "status": "FILLED", ...}}

4. UNSUBSCRIBE
   Client ──▶ {"action": "unsubscribe", "channel": "market.ticker", "stock": "BBCA"}
   Server ◀── {"type": "unsubscribed", "channel": "market.ticker", "stock": "BBCA"}

5. DISCONNECT
   Client closes connection → Hub removes client from all subscriptions
```

### 4.4 Price Simulation Flow
```
Simulator Goroutine (per stock):

    ┌─────────────────────────────┐
    │    Price Simulator          │
    │                             │
    │  Base Prices:               │
    │    BBCA = 9500              │
    │    BBRI = 5000              │
    │    TLKM = 3500              │
    │    ASII = 6000              │
    │    GOTO = 100               │
    │                             │
    │  Algorithm:                 │
    │  1. Random Walk + Mean      │
    │     Reversion               │
    │  2. Volatility: ±0.5%      │
    │     per tick                │
    │  3. Tick interval: 1-3s    │
    │     (random)               │
    │  4. Volume: random          │
    │     50-500 per tick        │
    │                             │
    │  On each tick:              │
    │  → Emit TickerUpdated event │
    │  → Update market snapshot   │
    └─────────────────────────────┘
         │
         ▼
    [EventBus] ──▶ [WS Hub] ──▶ market.ticker subscribers
```

---

## 5. CONCURRENCY ARCHITECTURE

### 5.1 Goroutine Map
```
Main Goroutine
    │
    ├── HTTP Server (net/http.ListenAndServe via Chi)
    │     └── Per-request goroutine (automatic by net/http)
    │
    ├── WebSocket Hub Goroutine (1x)
    │     └── Manages register/unregister/broadcast via channels
    │           └── Per-Client Write Goroutine (Nx)
    │                 └── Reads from client's send channel, writes to WS conn
    │           └── Per-Client Read Goroutine (Nx)
    │                 └── Reads from WS conn, processes subscribe/unsubscribe
    │
    ├── Matching Engine Goroutines (1 per stock)
    │     └── BBCA Matcher Goroutine
    │     └── BBRI Matcher Goroutine
    │     └── ... (one per registered stock)
    │
    ├── DB Worker Goroutine (1x) ──── DEDICATED ASYNC DB PERSISTENCE
    │     └── Consumes from write channel (buffered, 5000)
    │     └── Batch flush every 100ms or 100 writes
    │     └── Retry on failure (exponential backoff)
    │     └── Graceful drain on shutdown
    │
    ├── Price Simulator Goroutines (1 per stock)
    │     └── BBCA Simulator
    │     └── BBRI Simulator
    │     └── ...
    │
    ├── Binance Feed Goroutine (1x, optional)
    │     └── Connects to Binance public WebSocket
    │     └── Receives realtime ticker/trade from crypto market
    │     └── Maps to local stock codes
    │     └── Auto-reconnect on disconnect
    │
    ├── Event Bus Dispatcher Goroutine (1x)
    │     └── Receives events, fans out to subscribers
    │
    └── NATS Subscriber Goroutines (if NATS enabled)
```

### 5.2 Channel Architecture
```
┌──────────────┐    order channel      ┌────────────────────┐
│ HTTP Handler │ ────────────────────▶ │ Matching Engine     │
└──────────────┘    (buffered, 1000)   │ (per-stock goroutine)│
                                       └────────────────────┘
                                              │
                                        trade/event channel
                                              │
                                              ▼
                                       ┌────────────────┐
                                       │  Event Bus     │
                                       │  (fan-out)     │
                                       └────────────────┘
                                              │
                              ┌───────────────┼───────────────┬───────────────┐
                              ▼               ▼               ▼               ▼
                       ┌───────────┐   ┌───────────┐   ┌───────────┐   ┌───────────┐
                       │  WS Hub   │   │   NATS    │   │  Metrics  │   │ DB Worker │
                       │           │   │ Publisher  │   │ Collector │   │ (async)   │
                       └───────────┘   └───────────┘   └───────────┘   └───────────┘
                              │                                              │
                    ┌─────────┼─────────┐                              ┌─────┴──────┐
                    ▼         ▼         ▼                              │ PostgreSQL │
              ┌─────────┐ ┌─────────┐ ┌─────────┐                     └────────────┘
              │Client 1 │ │Client 2 │ │Client N │
              │send chan │ │send chan │ │send chan │
              │(buf 256)│ │(buf 256)│ │(buf 256)│
              └─────────┘ └─────────┘ └─────────┘

DB Worker Detail:
┌──────────────┐   write channel     ┌──────────────┐   batch insert    ┌────────────┐
│ Memory Store │ ──────────────────▶ │  DB Worker   │ ───────────────▶ │ PostgreSQL │
│ (on write)   │   (buffered, 5000)  │  goroutine   │   every 100ms    │            │
└──────────────┘                     │              │   or 100 writes   └────────────┘
                                     │  - Batching  │
                                     │  - Retry     │
                                     │  - Backoff   │
                                     └──────────────┘
```

### 5.3 Race Condition Prevention
| Resource | Strategy | Detail |
|---|---|---|
| Order Book (per stock) | **Single goroutine** | Satu goroutine per stock yang consume dari channel. Tidak perlu mutex karena hanya 1 goroutine yang access. |
| Order Storage | **sync.RWMutex** | Read-heavy (list orders) vs write (create order). RWMutex memungkinkan concurrent reads. |
| Trade Storage | **sync.RWMutex** | Same pattern sebagai order storage. |
| WS Client Map | **Hub goroutine + channels** | Register/unregister via channels, hanya Hub goroutine yang mutate map. Zero mutex needed. |
| Market Snapshot | **atomic.Value** | Snapshot di-store sebagai atomic.Value. Reads lock-free, writes atomic swap. |
| Event Bus subscribers | **sync.RWMutex** | Subscribe/unsubscribe jarang, publish sering. |

### 5.4 Non-Blocking Broadcast Strategy
```
Untuk setiap WS client:
1. Setiap client punya buffered send channel (cap=256)
2. Saat broadcast:
   - select {
       case client.send <- message:
           // sent successfully
       default:
           // client lambat, channel penuh
           // close client connection (kick slow client)
     }
3. Ini memastikan:
   - Broadcast TIDAK PERNAH blocking
   - Client lambat ter-disconnect otomatis
   - Client lain tidak terpengaruh
```

### 5.5 Goroutine Leak Prevention
1. **Context-based cancellation**: Semua goroutine menerima `context.Context` dan exit saat context cancelled
2. **WS Hub**: Saat client disconnect, Hub goroutine cleanup semua subscriptions
3. **Matching Engine**: Channel di-close saat shutdown, goroutine exit via `for range`
4. **Simulator**: Context cancelled saat shutdown, goroutine exit
5. **DB Worker**: Drains remaining writes from channel before exit
6. **Binance Feed**: Auto-reconnect with backoff, clean exit on context cancel
7. **Graceful Shutdown**: `signal.NotifyContext(SIGINT, SIGTERM)` → cancel root context → all goroutines exit

### 5.6 Connection & Rate Limits (IMPLEMENTED)
```
Limits yang di-enforce:

1. HTTP Rate Limiting:
   - Token bucket per IP: 100 requests/second
   - Burst: 200 requests
   - Returns 429 Too Many Requests when exceeded
   - Implemented via golang.org/x/time/rate + Chi middleware

2. WebSocket Connection Limits:
   - Max total connections: 1000
   - Max connections per IP: 10
   - Max subscriptions per client: 20
   - Max message size: 4096 bytes
   - Idle timeout: 60 seconds (no pong)
   - Returns error message and closes connection when exceeded

3. Order Limits:
   - Max price: 999999999 (prevent overflow)
   - Min price: 1
   - Max quantity: 1000000
   - Min quantity: 1
   - Valid stock codes only (whitelist)
   - Valid sides only (BUY/SELL)

4. Matching Engine:
   - Order channel buffer: 1000 per stock
   - If channel full: return 503 Service Unavailable (backpressure)
```

---

## 6. API DESIGN

### 6.1 REST API Endpoints

#### Authentication (BONUS)
```
POST   /api/v1/auth/register       # Register new user
POST   /api/v1/auth/login           # Login, returns JWT token
```

#### Orders
```
POST   /api/v1/orders               # Create order (buy/sell)
GET    /api/v1/orders                # List orders (?stock=BBCA&status=OPEN)
GET    /api/v1/orders/:id            # Get order by ID
```

#### Trades
```
GET    /api/v1/trades                # Trade history (?stock=BBCA&limit=50)
```

#### Market
```
GET    /api/v1/market/ticker         # All tickers snapshot
GET    /api/v1/market/ticker/:stock  # Single stock ticker (last, change, volume)
GET    /api/v1/market/orderbook/:stock # Order book depth (bids/asks)
GET    /api/v1/market/trades/:stock  # Recent trades for stock (last N)
```

#### System
```
GET    /healthz                      # Health check
GET    /metrics                      # Prometheus metrics (BONUS)
```

### 6.2 Request/Response Format

#### Create Order Request
```json
{
  "stock_code": "BBCA",
  "side": "BUY",
  "price": 9500,
  "quantity": 100
}
```

#### Create Order Response (201)
```json
{
  "success": true,
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "stock_code": "BBCA",
    "side": "BUY",
    "price": 9500,
    "quantity": 100,
    "filled_quantity": 0,
    "status": "OPEN",
    "user_id": "user-123",
    "created_at": "2026-03-01T10:00:00Z",
    "updated_at": "2026-03-01T10:00:00Z"
  }
}
```

#### Order List Response (200)
```json
{
  "success": true,
  "data": [
    {
      "id": "...",
      "stock_code": "BBCA",
      "side": "BUY",
      "price": 9500,
      "quantity": 100,
      "filled_quantity": 50,
      "status": "PARTIAL",
      "created_at": "..."
    }
  ],
  "meta": {
    "total": 150,
    "page": 1,
    "per_page": 20
  }
}
```

#### Ticker Response (200)
```json
{
  "success": true,
  "data": {
    "stock_code": "BBCA",
    "last_price": 9600,
    "prev_close": 9500,
    "change": 100,
    "change_pct": 1.05,
    "high": 9700,
    "low": 9400,
    "volume": 125000,
    "updated_at": "..."
  }
}
```

#### Error Response
```json
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Invalid stock code",
    "details": [
      {"field": "stock_code", "message": "stock_code is required"}
    ]
  }
}
```

### 6.3 WebSocket Protocol

#### Connection
```
ws://localhost:8080/ws
ws://localhost:8080/ws?token=<jwt_token>   # Authenticated (bonus)
```

#### Client → Server Messages
```json
// Subscribe
{"action": "subscribe", "channel": "market.ticker", "stock": "BBCA"}
{"action": "subscribe", "channel": "market.trade", "stock": "BBCA"}
{"action": "subscribe", "channel": "market.orderbook", "stock": "BBCA"}
{"action": "subscribe", "channel": "order.update"}

// Unsubscribe
{"action": "unsubscribe", "channel": "market.ticker", "stock": "BBCA"}

// Ping (keepalive)
{"action": "ping"}
```

#### Server → Client Messages
```json
// Subscription confirmation
{"type": "subscribed", "channel": "market.ticker", "stock": "BBCA"}
{"type": "unsubscribed", "channel": "market.ticker", "stock": "BBCA"}

// Ticker update
{
  "type": "market.ticker",
  "stock": "BBCA",
  "data": {
    "last_price": 9600,
    "change": 100,
    "change_pct": 1.05,
    "volume": 125000,
    "timestamp": "2026-03-01T10:00:00Z"
  }
}

// Trade stream
{
  "type": "market.trade",
  "stock": "BBCA",
  "data": {
    "trade_id": "...",
    "price": 9600,
    "quantity": 50,
    "buyer_order_id": "...",
    "seller_order_id": "...",
    "timestamp": "2026-03-01T10:00:00Z"
  }
}

// Order book update
{
  "type": "market.orderbook",
  "stock": "BBCA",
  "data": {
    "bids": [
      {"price": 9500, "quantity": 300},
      {"price": 9450, "quantity": 200}
    ],
    "asks": [
      {"price": 9600, "quantity": 150},
      {"price": 9650, "quantity": 400}
    ]
  }
}

// Order update (per user, bonus)
{
  "type": "order.update",
  "data": {
    "order_id": "...",
    "status": "FILLED",
    "filled_quantity": 100,
    "remaining_quantity": 0
  }
}

// Pong
{"type": "pong"}

// Error
{"type": "error", "message": "unknown channel: xyz"}
```

---

## 7. DATA MODELS

### 7.1 Order
```go
type Side string
const (
    SideBuy  Side = "BUY"
    SideSell Side = "SELL"
)

type OrderStatus string
const (
    OrderStatusOpen      OrderStatus = "OPEN"
    OrderStatusPartial   OrderStatus = "PARTIAL"
    OrderStatusFilled    OrderStatus = "FILLED"
    OrderStatusCancelled OrderStatus = "CANCELLED"
)

type Order struct {
    ID             string      `json:"id"`
    UserID         string      `json:"user_id"`
    StockCode      string      `json:"stock_code"`
    Side           Side        `json:"side"`
    Price          int64       `json:"price"`         // in cents/smallest unit
    Quantity       int64       `json:"quantity"`
    FilledQuantity int64       `json:"filled_quantity"`
    Status         OrderStatus `json:"status"`
    CreatedAt      time.Time   `json:"created_at"`
    UpdatedAt      time.Time   `json:"updated_at"`
}
```

### 7.2 Trade
```go
type Trade struct {
    ID            string    `json:"id"`
    StockCode     string    `json:"stock_code"`
    BuyOrderID    string    `json:"buy_order_id"`
    SellOrderID   string    `json:"sell_order_id"`
    Price         int64     `json:"price"`
    Quantity      int64     `json:"quantity"`
    BuyerUserID   string    `json:"buyer_user_id"`
    SellerUserID  string    `json:"seller_user_id"`
    ExecutedAt    time.Time `json:"executed_at"`
}
```

### 7.3 Ticker
```go
type Ticker struct {
    StockCode  string    `json:"stock_code"`
    LastPrice  int64     `json:"last_price"`
    PrevClose  int64     `json:"prev_close"`
    Change     int64     `json:"change"`
    ChangePct  float64   `json:"change_pct"`
    High       int64     `json:"high"`
    Low        int64     `json:"low"`
    Volume     int64     `json:"volume"`
    UpdatedAt  time.Time `json:"updated_at"`
}
```

### 7.4 OrderBook
```go
type PriceLevel struct {
    Price    int64 `json:"price"`
    Quantity int64 `json:"quantity"`
    Count    int   `json:"count"`    // number of orders at this level
}

type OrderBook struct {
    StockCode string       `json:"stock_code"`
    Bids      []PriceLevel `json:"bids"`   // sorted DESC by price
    Asks      []PriceLevel `json:"asks"`   // sorted ASC by price
    UpdatedAt time.Time    `json:"updated_at"`
}
```

---

## 8. STORAGE STRATEGY

### Layer 1: In-Memory (WAJIB) — Primary Storage
- **Primary storage** untuk semua data (source of truth saat runtime)
- `sync.RWMutex` protected maps/slices
- Fastest possible reads/writes (microseconds)
- Data hilang saat restart (acceptable untuk test)

### Layer 2: PostgreSQL + DB Worker (BONUS) — Persistent Storage
- **DB Worker** (dedicated goroutine) melakukan async batch persistence
- Flow: `Memory Write → DB Worker Channel → Batch Insert/Update → PostgreSQL`
- **Write-behind pattern**: write to memory FIRST (fast), async persist ke Postgres (eventual)
- DB Worker specs:
  - Buffered channel: 5000 capacity
  - Batch size: 100 writes per flush
  - Flush interval: 100ms
  - Retry: 3 attempts with exponential backoff
  - Graceful shutdown: drain remaining writes
- Schema migrations via golang-migrate
- On restart: load from PostgreSQL → populate in-memory store

### Layer 3: Redis (BONUS) — Cache + Pub/Sub
- **Cache layer** untuk market snapshots (ticker, orderbook)
- **Pub/Sub** untuk cross-instance event broadcasting
- Enables horizontal scaling (multiple app instances share events via Redis)
- TTL-based cache expiry untuk stale data

### Storage Interface (Repository Pattern)
```go
type OrderRepository interface {
    Save(ctx context.Context, order *entity.Order) error
    FindByID(ctx context.Context, id string) (*entity.Order, error)
    FindAll(ctx context.Context, filter OrderFilter) ([]*entity.Order, error)
    Update(ctx context.Context, order *entity.Order) error
}
```
- In-memory, Postgres, dan Redis semua implement interface yang sama
- Swap via config tanpa ubah business logic
- DB Worker wraps memory repo: intercepts writes, forwards to both memory and DB channel

---

## 9. HORIZONTAL SCALING STRATEGY (IMPLEMENTED & DOCUMENTED)

### Architecture Diagram
```
                    ┌──────────────┐
                    │   NGINX /    │
                    │ Load Balancer│
                    │ (sticky WS)  │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Instance │ │ Instance │ │ Instance │
        │    1     │ │    2     │ │    3     │
        │ stocks   │ │ stocks   │ │ stocks   │
        │ A-H      │ │ I-P      │ │ Q-Z      │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │             │             │
             └──────┬──────┴──────┬──────┘
                    │             │
              ┌─────┴─────┐ ┌────┴─────┐
              │   NATS    │ │  Redis   │
              │  Cluster  │ │ Cluster  │
              └───────────┘ └──────────┘
                                │
                          ┌─────┴──────┐
                          │ PostgreSQL │
                          │  (shared)  │
                          └────────────┘
```

### Strategi Detail (diimplementasi via NATS + Redis)

#### 1. REST API Scaling
- **Stateless**: Setiap instance bisa handle request apapun
- **Load balancer**: Round-robin distribution
- **No session state**: JWT token self-contained

#### 2. Matching Engine Scaling
- **Partition by stock code**: Consistent hashing determines which instance handles which stocks
- **NATS routing**: Order untuk stock X di-route ke instance yang handle stock X
- **Single writer per stock**: Tetap 1 goroutine per stock (no race condition)
- **If instance dies**: NATS re-routes to standby instance

#### 3. WebSocket Scaling
- **Sticky sessions**: Load balancer routes same client to same instance (IP hash / cookie)
- **Cross-instance broadcast via Redis Pub/Sub**:
  - Instance 1 has trade event → publishes to Redis channel `trade.BBCA`
  - Instance 2 subscribes to `trade.BBCA` → broadcasts to its local WS clients
  - Result: ALL clients across ALL instances receive the event
- **Alternative: NATS Pub/Sub** (same pattern, lower latency)

#### 4. Storage Scaling
- **PostgreSQL (shared)**: All instances read/write to same DB
- **Redis (shared cache)**: All instances share market snapshot cache
- **DB Worker per instance**: Each instance has its own DB Worker for async persistence
- **Read replicas**: PostgreSQL read replicas for scaling read queries

#### 5. Concurrency Guarantees Across Instances
- **Matching**: Only 1 instance processes orders for a given stock (partition)
- **WS Broadcast**: Redis/NATS ensures eventual consistency across instances
- **Order ID**: UUID guarantees uniqueness across instances
- **Trade**: Generated only by matching engine owner (no duplication)

---

## 10. THREE MAIN BOTTLENECKS & SOLUTIONS (ALL IMPLEMENTED)

### Bottleneck 1: Matching Engine Throughput — IMPLEMENTED
- **Problem**: Single goroutine per stock bisa jadi bottleneck jika 1 stock menerima >10K orders/sec
- **Solution (diimplementasi)**:
  - Batch processing: accumulate orders per tick (1ms), process batch
  - Backpressure: jika order channel penuh (1000 buffer), return 503 ke client
  - Per-stock isolation: 1 stock lambat tidak mempengaruhi stock lain
  - Current design handles 1000 orders/min easily (single goroutine can process ~100K operations/sec)

### Bottleneck 2: WebSocket Fan-out — IMPLEMENTED
- **Problem**: Broadcasting ke 500 clients dengan 5 subscriptions each = 2500 message dispatches per event
- **Solution (diimplementasi)**:
  - Per-client buffered channel (256 cap) + non-blocking send with `select/default`
  - Slow client auto-disconnect (channel penuh → kick)
  - Subscription-indexed map: broadcast hanya ke client yang subscribe channel+stock tertentu
  - Connection limits: max 1000 total, max 10 per IP

### Bottleneck 3: Storage Write Amplification — IMPLEMENTED via DB Worker
- **Problem**: Setiap trade = update 2 orders + create 1 trade + update ticker + update orderbook = 5 writes
- **Solution (diimplementasi)**:
  - In-memory as primary storage (microsecond writes, zero I/O)
  - **Dedicated DB Worker goroutine** (async batch persist ke PostgreSQL):
    - Collects writes via buffered channel (cap 5000)
    - Flushes batch every 100ms OR when 100 writes accumulated (whichever first)
    - Uses PostgreSQL batch insert/update (pgx.Batch) for efficiency
    - Retry with exponential backoff on failure
    - Graceful drain on shutdown (process remaining items)
  - Redis cache for read-heavy market data (ticker snapshots)
  - Write coalescing: multiple updates to same order combined in batch

---

## 11. PRICE SIMULATION ALGORITHM

### 11.1 Internal Simulator (Default)
```
Algorithm: Random Walk with Mean Reversion + Trade-Based Updates

For each stock:
1. Initialize: base_price, volatility (0.5%), mean_reversion_speed (0.01)
2. Every 1-3 seconds (random interval):
   a. Generate random delta: Δ = price * volatility * random(-1, 1)
   b. Apply mean reversion: Δ += (base_price - current_price) * mean_reversion_speed
   c. New price = current_price + Δ
   d. Clamp to reasonable range (±10% from base)
   e. Round to tick size (e.g., 25 for IDX stocks)
   f. Generate random volume: rand(50, 500)
   g. Update ticker snapshot
   h. Emit TickerUpdated event

When a trade occurs:
1. Update last_price to trade price
2. Update volume += trade quantity
3. Recalculate change, change_pct, high, low
4. Emit TickerUpdated event (overrides simulation for that moment)
```

### 11.2 Binance External Feed (Optional, BONUS)
```
Koneksi ke Binance Public WebSocket (tanpa API key):

URL: wss://stream.binance.com:9443/ws
Streams: Combined streams via /stream?streams=<stream1>/<stream2>/...

Mapping Binance → Local Stocks:
┌───────────┬────────────────┬─────────────────┐
│ Local     │ Binance Symbol │ Binance Stream  │
├───────────┼────────────────┼─────────────────┤
│ BTCUSDT   │ btcusdt        │ btcusdt@ticker  │
│ ETHUSDT   │ ethusdt        │ ethusdt@ticker  │
│ BNBUSDT   │ bnbusdt        │ bnbusdt@ticker  │
│ SOLUSDT   │ solusdt        │ solusdt@ticker  │
│ ADAUSDT   │ adausdt        │ adausdt@ticker  │
└───────────┴────────────────┴─────────────────┘

Flow:
1. Connect to Binance combined stream WebSocket
2. Subscribe to mini ticker streams (@miniTicker)
3. On each message:
   a. Parse Binance ticker data (last price, volume, high, low)
   b. Map to local stock code
   c. Update local ticker snapshot
   d. Emit TickerUpdated event → WebSocket Hub → subscribers
4. On disconnect:
   a. Exponential backoff reconnect (1s, 2s, 4s, ... max 30s)
   b. Fall back to internal simulator while disconnected

Config:
BINANCE_FEED_ENABLED=false         # Enable/disable Binance feed
BINANCE_WS_URL=wss://stream.binance.com:9443
BINANCE_RECONNECT_MAX=30s
```

---

## 12. SUPPORTED STOCKS

### Simulated (Internal Simulator)
| Code | Name | Base Price | Tick Size |
|---|---|---|---|
| BBCA | Bank BCA | 9500 | 25 |
| BBRI | Bank BRI | 5000 | 25 |
| TLKM | Telkom | 3500 | 25 |
| ASII | Astra International | 6000 | 25 |
| GOTO | GoTo Group | 100 | 1 |

### Real Market Data (Binance Feed, when enabled)
| Code | Binance Pair | Approximate Price |
|---|---|---|
| BTCUSDT | BTCUSDT | ~60000 |
| ETHUSDT | ETHUSDT | ~3000 |
| BNBUSDT | BNBUSDT | ~600 |
| SOLUSDT | SOLUSDT | ~150 |
| ADAUSDT | ADAUSDT | ~0.5 |

Note: When Binance feed is enabled, both sets of stocks are available. Users can trade on simulated IDX stocks AND real-priced crypto stocks.

---

## 13. TESTING STRATEGY

### Unit Tests
- Matching engine: test match, partial fill, FIFO, no match
- Order service: test create, validate, list with filters
- WebSocket hub: test subscribe, unsubscribe, broadcast
- Event bus: test publish, subscribe, fan-out

### Integration Tests
- HTTP handlers: test full request/response cycle
- WebSocket: test connect, subscribe, receive data
- End-to-end: create order → match → trade → WS broadcast

### Testing Tools untuk WebSocket
- **wscat**: `npx wscat -c ws://localhost:8080/ws`
- **websocat**: `websocat ws://localhost:8080/ws`
- **Postman**: Built-in WebSocket client
- **curl** (HTTP endpoints only)

---

## 14. CONFIGURATION

```env
# Server
SERVER_PORT=8080
SERVER_READ_TIMEOUT=10s
SERVER_WRITE_TIMEOUT=10s
SERVER_SHUTDOWN_TIMEOUT=30s

# Storage
STORAGE_TYPE=memory          # memory | postgres
DATABASE_URL=postgres://user:pass@localhost:5432/miniexchange?sslmode=disable

# DB Worker (async persistence)
DB_WORKER_ENABLED=false
DB_WORKER_BATCH_SIZE=100
DB_WORKER_FLUSH_INTERVAL=100ms
DB_WORKER_CHANNEL_SIZE=5000
DB_WORKER_RETRY_MAX=3
DB_WORKER_RETRY_BACKOFF=1s

# Redis (bonus)
REDIS_ENABLED=false
REDIS_URL=redis://localhost:6379/0

# NATS (bonus)
NATS_ENABLED=false
NATS_URL=nats://localhost:4222

# Auth (bonus)
AUTH_ENABLED=false
JWT_SECRET=your-secret-key
JWT_EXPIRY=24h

# Rate Limiting (bonus)
RATE_LIMIT_ENABLED=true
RATE_LIMIT_RPS=100           # requests per second per IP
RATE_LIMIT_BURST=200         # burst capacity

# Simulator
SIMULATOR_ENABLED=true
SIMULATOR_INTERVAL_MIN=1s
SIMULATOR_INTERVAL_MAX=3s

# Binance External Feed (bonus)
BINANCE_FEED_ENABLED=false
BINANCE_WS_URL=wss://stream.binance.com:9443
BINANCE_RECONNECT_MAX=30s
BINANCE_STREAMS=btcusdt@miniTicker/ethusdt@miniTicker/bnbusdt@miniTicker

# WebSocket
WS_WRITE_TIMEOUT=10s
WS_PONG_TIMEOUT=60s
WS_PING_INTERVAL=54s
WS_MAX_MESSAGE_SIZE=4096
WS_SEND_BUFFER_SIZE=256
WS_MAX_CONNECTIONS=1000
WS_MAX_CONNECTIONS_PER_IP=10
WS_MAX_SUBSCRIPTIONS_PER_CLIENT=20

# Logging
LOG_LEVEL=info               # debug | info | warn | error
LOG_FORMAT=json              # json | text
```

---

## 15. DOCKER SETUP

### Dockerfile (Multi-stage)
```dockerfile
# Stage 1: Build
FROM golang:1.25-alpine AS builder
RUN apk --no-cache add ca-certificates tzdata git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# Stage 2: Runtime (alpine for healthcheck wget support)
FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata wget
COPY --from=builder /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
```

### Docker Compose
```yaml
services:
  app:
    build: .
    ports: ["8080:8080"]
    depends_on: [nats, redis, postgres]
    env_file: .env

  nats:
    image: nats:latest
    ports: ["4222:4222", "8222:8222"]

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  postgres:
    image: postgres:16-alpine
    ports: ["5432:5432"]
    environment:
      POSTGRES_DB: miniexchange
      POSTGRES_USER: miniexchange
      POSTGRES_PASSWORD: miniexchange
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./migrations:/docker-entrypoint-initdb.d

volumes:
  pgdata:
```

---

## 16. EVALUATION CRITERIA COVERAGE

| Criteria | Coverage |
|---|---|
| Arsitektur & design | Clean Architecture, dependency injection, clear layers |
| Pemahaman concurrency | Goroutine per stock, channels, RWMutex, atomic.Value, non-blocking patterns |
| WebSocket handling | Hub pattern, per-client goroutines, buffered channels, slow client handling |
| Code quality & readability | Clean separation, consistent naming, comprehensive error handling |
| Problem solving | Matching engine, price simulation, horizontal scaling design |
| Realtime handling | Event bus, NATS, Redis pub/sub, non-blocking broadcast |
| Clean architecture | Domain-driven, repository pattern, interface-based, swappable implementations |

### Bonus Coverage
| Bonus | Status |
|---|---|
| Message broker (NATS) | YES |
| Redis pub/sub | YES |
| Rate limiting | YES |
| Authentication (JWT) | YES |
| Logging & monitoring | YES (slog + prometheus) |
| Unit test | YES |
| Partial fill | YES |
| FIFO matching | YES |
| Order book channel | YES |
| Order update channel | YES |

---

## 17. MAKEFILE COMMANDS

```makefile
run:         # Run locally
build:       # Build binary
test:        # Run all tests
test-cover:  # Run tests with coverage
lint:        # Run golangci-lint
docker-up:   # Start all services with Docker Compose
docker-down: # Stop all services
migrate-up:  # Run database migrations
migrate-down: # Rollback migrations
```
