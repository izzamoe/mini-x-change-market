# Codebase Architecture Summary

## Project Overview

This is a **Mini Exchange — Realtime Trading System** built in Go, implementing a production-quality matching engine with WebSocket streaming capabilities.

---

## Current Architecture Analysis

### 1. Layered Architecture (Clean Architecture)

```
┌─────────────────────────────────────────────────────────────────┐
│                        Transport Layer                           │
│  HTTP (Chi Router)  │  WebSocket (coder/websocket)  │  Middleware│
├─────────────────────────────────────────────────────────────────┤
│                      Application Layer                           │
│   order.Service  │  trade.Service  │  market.Service            │
├─────────────────────────────────────────────────────────────────┤
│                      Domain Layer                                │
│  entity (Order, Trade, Ticker)  │  event  │  repository (interface)│
├─────────────────────────────────────────────────────────────────┤
│                      Engine Layer                                │
│  Matching Engine (per-stock goroutine)  │  FIFO + Partial Fill    │
├─────────────────────────────────────────────────────────────────┤
│                    Infrastructure Layer                          │
│  Memory Storage  │  PostgreSQL  │  Redis  │  NATS  │  JWT Auth   │
└─────────────────────────────────────────────────────────────────┘
```

### 2. Key Components

#### **Domain Layer** (`internal/domain/`)
- **entity/**: Core business types
  - `Order`: BUY/SELL orders with status tracking (OPEN, PARTIAL, FILLED, CANCELLED)
  - `Trade`: Executed match between buy and sell orders
  - `Ticker`: Market snapshot with price, volume, change
  - `OrderBook`: Bid/ask depth levels
  - `Stock`: Stock configuration with base price and tick size
  - `User`: User entity for authentication

- **event/**: Event-driven architecture
  - Event types: OrderCreated, OrderUpdated, TradeExecuted, TickerUpdated, OrderBookUpdated
  - Bus interface: Publish/Subscribe pattern for decoupled communication

- **repository/**: Repository interfaces (abstraction for storage)

#### **Engine Layer** (`internal/engine/`)
- **engine.go**: Coordinates per-stock matchers, manages lifecycle (Start/Stop)
- **matcher.go**: Core matching logic per stock
  - Single goroutine per stock (no mutex contention)
  - FIFO matching at same price levels
  - Partial fill support
  - Price-time priority algorithm
- **orderbook.go**: In-memory order book
  - Sorted bids (descending) and asks (ascending)
  - Stable sort preserves insertion order (FIFO)

#### **Application Layer** (`internal/app/`)
- **order/service.go**: Order creation, validation, submission to engine
- **trade/service.go**: Trade history queries
- **market/service.go**: Market data queries with Redis cache-aside

#### **Transport Layer** (`internal/transport/`)
- **http/**: REST API handlers
  - Order CRUD operations
  - Trade history
  - Market data (ticker, orderbook)
  - Authentication (JWT)
- **ws/**: WebSocket server
  - Hub manages subscriptions and fan-out
  - Non-blocking broadcast with backpressure handling
  - Per-client goroutines (readPump + writePump)

#### **Infrastructure Layer** (`internal/infra/`)
- **storage/memory/**: Thread-safe in-memory repositories with RWMutex
- **storage/postgres/**: PostgreSQL implementations (optional)
- **storage/redis/**: Cache and Pub/Sub support (optional)
- **broker/**: Event bus implementations
  - In-process event bus (`eventbus.go`)
  - NATS publisher/subscriber for horizontal scaling (`natsbroker/`)
- **auth/**: JWT authentication service

#### **Workers** (`internal/worker/`)
- **dbworker.go**: Async batch PostgreSQL writer
  - Batches 100 ops or flushes every 100ms
  - Retry with exponential backoff
  - Decouples hot path from DB latency

#### **Simulator** (`internal/simulator/`)
- **price.go**: Random-walk price simulator with mean reversion
- **binance.go**: Binance WebSocket feed integration (external data)

---

## Requirement Mapping (from rule.md)

### ✅ REST API (HTTP)

| Requirement | Implementation | File |
|------------|----------------|------|
| Create Order (BUY/SELL) | ✅ `POST /api/v1/orders` | `handler/order.go:38` |
| Concurrent order safety | ✅ Single goroutine per stock + channel-based submission | `engine/engine.go:69` |
| Get Order List | ✅ `GET /api/v1/orders` with filters | `handler/order.go:65` |
| Filter by stock/status | ✅ Query params supported | `handler/order.go:84` |
| Get Trade History | ✅ `GET /api/v1/trades` | `handler/trade.go` |
| Market Snapshot (Ticker) | ✅ `GET /api/v1/market/ticker/{stock}` | `handler/market.go` |
| Market Snapshot (OrderBook) | ✅ `GET /api/v1/market/orderbook/{stock}` | `handler/market.go` |
| Recent Trades | ✅ `GET /api/v1/market/trades/{stock}` | `handler/market.go` |

### ✅ Matching Engine (Core Logic)

| Requirement | Implementation | File |
|------------|----------------|------|
| BUY matches SELL | ✅ Price-overlap matching | `engine/matcher.go:86` |
| Create trade on match | ✅ Trade entity created | `engine/matcher.go:158` |
| Update order status | ✅ Order.Fill() method | `entity/order.go:62` |
| Partial fill | ✅ Supported | `entity/order.go:65` |
| FIFO matching | ✅ Slice-based stable sort | `engine/orderbook.go:48` |
| Race condition safety | ✅ Single goroutine per stock | `engine/matcher.go:27` |

### ✅ WebSocket (Realtime)

| Requirement | Implementation | File |
|------------|----------------|------|
| Subscribe price/ticker | ✅ `market.ticker` channel | `ws/hub.go:176` |
| Subscribe trade | ✅ `market.trade` channel | `ws/hub.go:176` |
| Subscribe order update | ✅ `order.update` channel (user-scoped) | `ws/hub.go:196` |
| Subscribe orderbook | ✅ `market.orderbook` channel | `ws/hub.go:176` |
| Non-blocking broadcast | ✅ Buffered channels + select/default | `ws/hub.go:137` |
| Handle slow clients | ✅ Disconnect on blocked send | `ws/hub.go:141` |
| Scalable concurrency | ✅ Per-client goroutines, hub as coordinator | `ws/hub.go:36` |

### ✅ Realtime Price Simulation

| Requirement | Implementation | File |
|------------|----------------|------|
| Price changes periodically | ✅ Random walk 1-3s intervals | `simulator/price.go:162` |
| Based on trade events | ✅ Trade fills update immediately | `simulator/price.go:170` |
| Realtime event generation | ✅ EventBus publish | `simulator/price.go:153` |
| Broadcast to subscribers | ✅ Hub.Broadcast() | `ws/hub.go:176` |

### ✅ Concurrency & Performance

| Requirement | Implementation | File |
|------------|----------------|------|
| Goroutine usage | ✅ Extensive use throughout | All files |
| Channel usage | ✅ For communication between components | `engine/`, `ws/`, `worker/` |
| No race condition | ✅ Single goroutine per stock, RWMutex for repos | `engine/`, `infra/storage/` |
| Non-blocking | ✅ Buffered channels with select/default | `ws/hub.go`, `broker/eventbus.go` |
| Multiple request handling | ✅ HTTP server with configurable timeouts | `cmd/server/main.go:269` |

### ✅ Data Storage

| Requirement | Implementation | File |
|------------|----------------|------|
| In-memory | ✅ Default storage type | `infra/storage/memory/` |
| Redis (optional) | ✅ Cache-aside implementation | `infra/storage/redis/` |
| Database (optional) | ✅ PostgreSQL with migrations | `infra/storage/postgres/` |

### ✅ Bonus Features

| Feature | Status | Implementation |
|---------|--------|----------------|
| Message Broker (NATS) | ✅ | `infra/broker/natsbroker/` |
| Redis Pub/Sub | ✅ | `infra/storage/redis/pubsub.go` |
| Rate Limiting | ✅ | `middleware/ratelimit.go` |
| JWT Authentication | ✅ | `infra/auth/jwt.go` |
| Logging & Monitoring | ✅ | Prometheus metrics, structured logging |
| Unit Tests | ✅ | Race detector, coverage reporting |

---

## Design Decisions & Patterns

### 1. Concurrency Model

**Per-Stock Single Goroutine Pattern**
- Each stock has ONE matcher goroutine
- Orders submitted via buffered channel (capacity: 1000)
- No mutexes needed in hot path (engine is single-threaded per stock)
- Eliminates cross-order locking entirely

**WebSocket Hub Pattern**
- Single coordinator goroutine owns all state
- Channels for register/unregister/subscribe/unsubscribe/broadcast
- No mutexes in hub - serialized through channel communication

### 2. Event-Driven Architecture

```
Domain Events:
├── OrderCreated (from order.Service)
├── OrderUpdated (from matcher after fill)
├── TradeExecuted (from matcher after match)
├── TickerUpdated (from simulator or matcher)
└── OrderBookUpdated (from matcher after change)

Subscribers:
├── WebSocket Hub (broadcast to clients)
├── NATS Publisher (cross-instance fan-out)
├── DB Worker (async persistence)
├── Redis Cache (write-through)
└── Prometheus Metrics
```

### 3. Storage Strategy

**In-Memory (Default)**
- Primary storage for speed
- Thread-safe with sync.RWMutex
- Copy-on-read to prevent external mutation

**PostgreSQL (Optional)**
- Async writes via DB Worker
- Batch operations with pgx.Batch
- 100ms flush interval or 100 ops batch

**Redis (Optional)**
- Cache-aside for market data
- Write-through on ticker/orderbook updates
- 60s TTL for tickers, 10s for orderbooks

### 4. Horizontal Scaling

| Component | Strategy |
|-----------|----------|
| REST API | Stateless replicas behind load balancer |
| Matching Engine | Partition by stock code (consistent hash) |
| WebSocket | Sticky sessions + NATS cross-instance broadcast |
| Market Data | Redis cache reduces load |
| Database | Read replicas for queries |

---

## Suggestions for rule.md Requirements

Based on the current codebase analysis, here are suggestions to enhance the requirements document:

### 1. **Missing: Connection Limits Documentation**
**Current**: Code implements limits but not explicitly required
**Suggestion**: Add requirement for:
```
Connection Limits:
- Max simultaneous connections per instance: 1000
- Max connections per IP: 10
- Max subscriptions per client: 20
```

### 2. **Missing: Graceful Shutdown Requirements**
**Current**: Implemented but not required
**Suggestion**: Add:
```
Graceful Shutdown:
- Handle SIGINT/SIGTERM
- Stop accepting new orders
- Drain pending operations
- Close WebSocket connections cleanly
- Flush DB worker queue
```

### 3. **Missing: Rate Limiting Requirements**
**Current**: Implemented (100 RPS default) but not required
**Suggestion**: Add:
```
Rate Limiting:
- Token bucket algorithm
- Per-IP limiting
- Configurable RPS and burst
- Return 429 status when exceeded
```

### 4. **Missing: Metrics/Observability Requirements**
**Current**: Prometheus metrics implemented
**Suggestion**: Add:
```
Observability:
- HTTP request latency histograms
- Order/trade counters
- WebSocket connection gauges
- DB worker queue size
- Health check endpoint
```

### 5. **Clarification Needed: Price Representation**
**Current**: Uses int64 (smallest currency unit) to avoid floating-point
**Suggestion**: Explicitly state in requirements:
```
Price Representation:
- Use integer (e.g., IDR cents) not float64
- Prevents floating-point precision issues
- Example: Rp 9,500.00 stored as 950000
```

### 6. **Missing: External Data Feed Support**
**Current**: Binance WebSocket feed implemented
**Suggestion**: Add as optional requirement:
```
External Data Feed (Optional):
- Connect to public WebSocket (Binance, etc.)
- Map external symbols to internal stocks
- Exponential backoff on disconnect
```

### 7. **Missing: Authentication Scope**
**Current**: JWT auth optional with fallback to anonymous
**Suggestion**: Clarify:
```
Authentication:
- JWT-based (optional for development)
- Anonymous access allowed when disabled
- Order.update channel requires authentication
```

### 8. **Performance Expectations**
**Current**: Code achieves ~14,700 req/s (880k orders/min)
**Suggestion**: Specify acceptable ranges:
```
Performance Targets:
- Order submission: p99 < 10ms
- WebSocket latency: p99 < 50ms
- Throughput: minimum 1000 orders/min
- Scale headroom: 10x target minimum
```

### 9. **Missing: Data Retention Requirements**
**Current**: In-memory storage limited by RAM
**Suggestion**: Add:
```
Data Retention:
- In-memory: unbounded (testing only)
- PostgreSQL: persistent storage
- Trade history: configurable retention policy
```

### 10. **Missing: Error Handling Requirements**
**Current**: Comprehensive error handling implemented
**Suggestion**: Specify:
```
Error Handling:
- Consistent JSON error format
- Error codes for client handling
- Structured logging with context
- Retry with exponential backoff for transient failures
```

---

## Strengths of Current Implementation

1. **Clean Architecture**: Clear separation of concerns across layers
2. **Concurrency Safety**: Single goroutine per stock eliminates race conditions
3. **Non-Blocking Design**: Buffered channels with select/default everywhere
4. **Horizontal Scaling Ready**: NATS for cross-instance communication
5. **Feature Flags**: All optional features configurable via environment
6. **Production Ready**: Rate limiting, metrics, graceful shutdown
7. **Test Coverage**: Unit tests with race detector
8. **Documentation**: Comprehensive README with examples

## Areas for Potential Enhancement

1. **Order Cancellation**: Not implemented (would require new endpoint)
2. **Order Types**: Only LIMIT orders (MARKET orders could be added)
3. **WebSocket Reconnection**: Client-side reconnection logic
4. **Circuit Breaker**: For external feeds (Binance)
5. **Data Validation**: More strict validation on price/quantity ranges
6. **Audit Logging**: Separate audit trail for compliance
7. **Schema Migrations**: More robust migration tooling
8. **Load Testing**: Formal load test suite

---

## Conclusion

The current codebase **fully satisfies** all requirements from rule.md and includes numerous bonus features. The architecture demonstrates:

- **Clean separation** of domain, application, and infrastructure concerns
- **Excellent concurrency design** with single-goroutine-per-stock pattern
- **Production readiness** with metrics, auth, rate limiting, and graceful shutdown
- **Scalability** through horizontal partitioning and message broker integration

The implementation goes beyond basic requirements with sophisticated features like partial fills, FIFO matching, WebSocket fan-out optimization, async database writes, and external data feed integration.
