# Plan 8: Bonus Features - JWT Auth, NATS, Redis, DB Worker
# Estimated: ~60 minutes
# Dependencies: Plan 1-7 (core system must be working)

## Objective
Implement all bonus features: JWT authentication, NATS message broker, Redis pub/sub + cache, DB Worker for async PostgreSQL persistence, and Prometheus monitoring.

---

## Tasks

### 8.1 JWT Authentication (internal/infra/auth/jwt.go) — BONUS

#### Auth Service
```go
type AuthService struct {
    secret []byte
    expiry time.Duration
    users  map[string]*entity.User  // in-memory user store
    mu     sync.RWMutex
}

func (a *AuthService) Register(username, password string) (*entity.User, error)
    // Check username uniqueness
    // Hash password (bcrypt)
    // Create User entity
    // Store in map

func (a *AuthService) Login(username, password string) (string, error)
    // Find user by username
    // Verify password (bcrypt.CompareHashAndPassword)
    // Generate JWT token with claims: {user_id, username, exp}
    // Return token string

func (a *AuthService) ValidateToken(tokenString string) (*Claims, error)
    // Parse and validate JWT
    // Return claims (user_id, username)
```

#### Auth Handler (internal/transport/http/handler/auth.go)
```go
POST /api/v1/auth/register → {username, password} → {user, token}
POST /api/v1/auth/login    → {username, password} → {token, expires_at}
```

#### Auth Middleware (internal/transport/http/middleware/auth.go)
```go
func AuthMiddleware(authSvc *auth.AuthService, required bool) func(http.Handler) http.Handler
    // Extract token from Authorization: Bearer <token>
    // Validate token
    // If valid: set user_id in request context
    // If invalid AND required: return 401
    // If invalid AND NOT required: continue (anonymous)
```

#### WS Auth
- Token passed via query param: `ws://localhost:8080/ws?token=xxx`
- Parsed in WS handler, user_id stored in Client
- `order.update` channel only broadcasts to matching user_id

### 8.2 NATS Message Broker (internal/infra/broker/nats/) — BONUS

#### NATS Publisher (nats/publisher.go)
```go
type NATSPublisher struct {
    conn *nats.Conn
}

func (p *NATSPublisher) Publish(subject string, data []byte) error
    // Publish to NATS subject
    // Subjects: "trade.{stock}", "ticker.{stock}", "orderbook.{stock}", "order.{userID}"
```

#### NATS Subscriber (nats/subscriber.go)
```go
type NATSSubscriber struct {
    conn *nats.Conn
}

func (s *NATSSubscriber) Subscribe(subject string, handler func([]byte)) (*nats.Subscription, error)
    // Subscribe to NATS subject
    // Call handler on each message
```

#### Integration with Event Bus
- EventBus publishes to BOTH local subscribers AND NATS
- NATS subscriber on each instance forwards events to local WS Hub
- This enables cross-instance WebSocket broadcasting

### 8.3 Redis Pub/Sub + Cache (internal/infra/storage/redis/) — BONUS

#### Redis Cache (redis/cache.go)
```go
type RedisCache struct {
    client *redis.Client
}

func (c *RedisCache) SetTicker(ctx, stock string, ticker *entity.Ticker) error
    // SET ticker:{stock} {json} EX 60

func (c *RedisCache) GetTicker(ctx, stock string) (*entity.Ticker, error)
    // GET ticker:{stock}

func (c *RedisCache) SetOrderBook(ctx, stock string, book *entity.OrderBook) error
func (c *RedisCache) GetOrderBook(ctx, stock string) (*entity.OrderBook, error)
```

#### Redis Pub/Sub (redis/pubsub.go)
```go
type RedisPubSub struct {
    client *redis.Client
}

func (p *RedisPubSub) Publish(ctx, channel string, data []byte) error
    // PUBLISH channel data

func (p *RedisPubSub) Subscribe(ctx, channel string, handler func([]byte)) error
    // SUBSCRIBE channel
    // Route messages to handler
```

### 8.4 DB Worker (internal/worker/dbworker.go) — BONUS

```go
type WriteOp struct {
    Type    string      // "order_save", "order_update", "trade_save", "ticker_update"
    Payload interface{} // actual entity
}

type DBWorker struct {
    writeCh   chan WriteOp  // buffered, 5000
    pgPool    *pgxpool.Pool
    batchSize int           // 100
    flushInterval time.Duration // 100ms
    retryMax  int           // 3
}

func (w *DBWorker) Start(ctx context.Context) {
    batch := make([]WriteOp, 0, w.batchSize)
    ticker := time.NewTicker(w.flushInterval)
    
    for {
        select {
        case op := <-w.writeCh:
            batch = append(batch, op)
            if len(batch) >= w.batchSize {
                w.flush(ctx, batch)
                batch = batch[:0]
            }
        
        case <-ticker.C:
            if len(batch) > 0 {
                w.flush(ctx, batch)
                batch = batch[:0]
            }
        
        case <-ctx.Done():
            // Drain remaining
            close(w.writeCh)
            for op := range w.writeCh {
                batch = append(batch, op)
            }
            if len(batch) > 0 {
                w.flush(context.Background(), batch)
            }
            return
        }
    }
}

func (w *DBWorker) flush(ctx context.Context, ops []WriteOp) {
    // Group by type
    // Use pgx.Batch for efficient bulk operations
    // Retry with exponential backoff on failure
}

func (w *DBWorker) Enqueue(op WriteOp) error {
    select {
    case w.writeCh <- op:
        return nil
    default:
        return errors.New("db worker queue full")
    }
}
```

#### Integration
- Memory storage wraps DB Worker: after memory write, enqueue write op to DB Worker
- DB Worker persists to PostgreSQL asynchronously
- On startup: optionally load data from PostgreSQL to populate memory

### 8.5 PostgreSQL Storage (internal/infra/storage/postgres/)

#### Schema Migrations (migrations/)
```sql
-- 001_create_orders.up.sql
CREATE TABLE orders (
    id UUID PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    stock_code VARCHAR(10) NOT NULL,
    side VARCHAR(4) NOT NULL,
    price BIGINT NOT NULL,
    quantity BIGINT NOT NULL,
    filled_quantity BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'OPEN',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_orders_stock_code ON orders(stock_code);
CREATE INDEX idx_orders_status ON orders(status);
CREATE INDEX idx_orders_user_id ON orders(user_id);

-- 002_create_trades.up.sql
CREATE TABLE trades (
    id UUID PRIMARY KEY,
    stock_code VARCHAR(10) NOT NULL,
    buy_order_id UUID NOT NULL REFERENCES orders(id),
    sell_order_id UUID NOT NULL REFERENCES orders(id),
    price BIGINT NOT NULL,
    quantity BIGINT NOT NULL,
    buyer_user_id VARCHAR(255) NOT NULL,
    seller_user_id VARCHAR(255) NOT NULL,
    executed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_trades_stock_code ON trades(stock_code);
CREATE INDEX idx_trades_executed_at ON trades(executed_at DESC);

-- 003_create_users.up.sql
CREATE TABLE users (
    id UUID PRIMARY KEY,
    username VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 8.6 Prometheus Metrics (internal/transport/http/middleware/metrics.go) — BONUS
```go
var (
    httpRequestsTotal = prometheus.NewCounterVec(...)     // HTTP requests by method, path, status
    httpRequestDuration = prometheus.NewHistogramVec(...)  // Request latency
    wsConnectionsGauge = prometheus.NewGauge(...)          // Active WS connections
    wsSubscriptionsGauge = prometheus.NewGauge(...)        // Active subscriptions
    ordersCreatedTotal = prometheus.NewCounterVec(...)     // Orders by stock, side
    tradesExecutedTotal = prometheus.NewCounterVec(...)    // Trades by stock
    matchingEngineLatency = prometheus.NewHistogram(...)   // Matching engine processing time
    dbWorkerQueueSize = prometheus.NewGauge(...)           // DB worker queue depth
)
```
- Exposed at `GET /metrics` for Prometheus scraping

### 8.7 Unit Tests
- **auth/jwt_test.go**: Test register, login, token validation, expired token
- **broker/nats/publisher_test.go**: Test publish (mock NATS)
- **worker/dbworker_test.go**: Test batching, flush interval, retry, graceful drain
- **redis/cache_test.go**: Test set/get (mock Redis)

---

## Files Created
```
internal/infra/auth/jwt.go
internal/infra/auth/jwt_test.go
internal/transport/http/handler/auth.go
internal/transport/http/middleware/auth.go
internal/infra/broker/nats/publisher.go
internal/infra/broker/nats/subscriber.go
internal/infra/storage/redis/cache.go
internal/infra/storage/redis/pubsub.go
internal/worker/dbworker.go
internal/worker/dbworker_test.go
internal/infra/storage/postgres/order_repo.go
internal/infra/storage/postgres/trade_repo.go
migrations/001_create_orders.up.sql
migrations/001_create_orders.down.sql
migrations/002_create_trades.up.sql
migrations/002_create_trades.down.sql
migrations/003_create_users.up.sql
migrations/003_create_users.down.sql
internal/transport/http/middleware/metrics.go
```

## Verification
```bash
go test ./internal/infra/auth/... -v
go test ./internal/worker/... -v -race
# With Docker:
docker-compose up -d nats redis postgres
go run ./cmd/server  # with NATS_ENABLED=true REDIS_ENABLED=true
```

## Exit Criteria
- JWT auth works (register → login → use token on protected endpoints)
- NATS pub/sub works (events published to NATS, received by subscribers)
- Redis cache stores/retrieves tickers and orderbooks
- DB Worker batches writes to PostgreSQL correctly
- Prometheus metrics available at /metrics
- All tests pass
