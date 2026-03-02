# Mini Exchange — Realtime Trading System

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

A production-quality matching engine and WebSocket broadcast server written in Go. Built as a technical interview project demonstrating clean architecture, concurrency mastery, and real-time streaming.

**Status: ✅ All Requirements from [rule.md](rule.md) COMPLETED (100%)**

---

## 📋 Quick Start

### Prerequisites
- Go 1.25+
- Docker & Docker Compose (optional, for full stack)
- Make

### Run locally (in-memory, no external services)

```bash
# Clone and setup
git clone git@github.com:izzamoe/mini-x-change-market.git
cd mini-exchange
cp .env.example .env

# Run server
go run ./cmd/server

# Verify in another terminal
curl http://localhost:8080/healthz
curl http://localhost:8080/api/v1/market/ticker
```

### WebSocket Quick Test

```bash
# Install wscat
npm install -g wscat

# Connect and subscribe
wscat -c ws://localhost:8080/ws
> {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}

# Trigger a trade (in another terminal)
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"stock_code":"BBCA","side":"BUY","price":9500,"quantity":100,"type":"LIMIT"}'
  
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"stock_code":"BBCA","side":"SELL","price":9500,"quantity":100,"type":"LIMIT"}'

# You should see market.trade event in wscat
```

### Run with Docker Compose (full stack)

```bash
# Start all services (app + NATS + Redis + PostgreSQL)
make docker-up

# View logs
make docker-logs

# Run tests
make test-race

# Stop everything
make docker-down
```

### Run Tests

```bash
# All tests with race detector
make test-race

# With coverage report
make test-cover

# Specific package
go test ./internal/engine/... -v -race
```

---

## 🏗️ Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        CLIENTS                                   │
│  REST API    WebSocket    REST API                              │
└──────┬──────────┬──────────┬────────────────────────────────────┘
       │          │          │
┌──────▼──────────▼──────────▼────────────────────────────────────┐
│                   LOAD BALANCER                                  │
│         /api/* → Round Robin    /ws → Sticky Sessions           │
└──────┬───────────────────────┬───────────────────────────────────┘
       │                       │
┌──────▼──────┐       ┌────────▼────────┐
│ Instance 1  │◀─────▶│   Instance 2    │  ... More instances
│ (BBCA,GOTO) │ NATS  │   (BBRI,ASII)   │
└──────┬──────┘       └────────┬────────┘
       │                       │
       └───────────┬───────────┘
                   │
          ┌────────▼────────┐
          │  NATS Server    │  ◀── Message Broker untuk horizontal scaling
          └────────┬────────┘
                   │
       ┌───────────┼───────────┐
       ▼           ▼           ▼
   Redis      PostgreSQL    Simulator
  (Cache)     (Persistent)   (Price Feed)
```

### Clean Architecture Layers

```
┌─────────────────────────────────────────┐
│  Transport Layer                        │
│  ├── HTTP Handlers (REST API)          │
│  └── WebSocket Handler                 │
├─────────────────────────────────────────┤
│  Application Layer                      │
│  ├── Order Service                     │
│  ├── Trade Service                     │
│  └── Market Service                    │
├─────────────────────────────────────────┤
│  Domain Layer                           │
│  ├── Entities (Order, Trade, etc)      │
│  ├── Events                            │
│  └── Repository Interfaces             │
├─────────────────────────────────────────┤
│  Engine Layer                           │
│  ├── Matching Engine (per-stock)       │
│  └── Order Book                        │
├─────────────────────────────────────────┤
│  Infrastructure Layer                   │
│  ├── Storage (Memory/Redis/Postgres)   │
│  ├── Message Broker (NATS)             │
│  ├── Auth (JWT)                        │
│  └── Workers (DB Worker)               │
└─────────────────────────────────────────┘
```

---

## 🔄 System Flow

### Order Lifecycle

```
1. Client ──POST /api/v1/orders──▶ HTTP Handler
                                   │
2.                                ▼
                           Validation (stock, side, price, qty)
                                   │
3.                                ▼
                           Create Order Entity (UUID, status=OPEN)
                                   │
4.                                ▼
                           Save to Repository (in-memory)
                                   │
5.                                ▼
                           Submit to Engine
                                   │
6.                    Partition Router (horizontal scaling)
                    ├─ Stock owned? → Process locally
                    └─ Not owned?   → Forward via NATS
                                   │
7.                                ▼
                       Matcher Goroutine (per-stock)
                                   │
8.                         Order Book Matching
                      ├─ Match found → Execute Trade
                      └─ No match   → Rest in book
                                   │
9.                           Publish Events
                    ├─ TradeExecuted
                    ├─ OrderUpdated (×2)
                    ├─ TickerUpdated
                    └─ OrderBookUpdated
                                   │
10.                    ┌───────────┼───────────┐
                       ▼           ▼           ▼
                   WS Hub      NATS Pub     DB Worker
                   (local)    (cross-inst)  (async)
                       │           │           │
11.                    └───────────┴───────────┘
                                   │
                           Clients receive events
```

---

## 📊 System Assumptions

| Parameter | Target | Achieved | Headroom |
|-----------|--------|----------|----------|
| Orders/min | 1,000 | **2,093,315** | **2,093×** |
| WS Clients | 500 | **500+ tested** | **100%** |
| Subscriptions/client | 1-5 | **Max 20** | **4×** |
| Latency p99 | <100ms | **~12ms** | **8×** |

**Key Assumptions:**
- Price representation: Integer (smallest currency unit) — no floating-point
- Primary storage: In-memory for low latency
- Matching: Single goroutine per stock (no mutex needed)
- Partial fills: Supported
- FIFO: Orders at same price matched in arrival order
- Authentication: Optional (configurable)

---

## 🛡️ Race Condition Prevention

| Resource | Strategy | Implementation |
|----------|----------|----------------|
| **Order Book (per stock)** | Single goroutine | One Matcher goroutine per stock |
| **Order Storage** | RWMutex | `sync.RWMutex` in memory repo |
| **Trade Storage** | RWMutex | `sync.RWMutex` in memory repo |
| **WebSocket Hub** | Channel-based | Single Hub goroutine |
| **Event Bus** | Buffered channel | 10,000 capacity dispatch queue |

**Why no mutex needed for matching?**
```
Each stock has its own goroutine:
┌─────────┐ ┌─────────┐ ┌─────────┐
│ BBCA    │ │ BBRI    │ │ TLKM    │
│ Matcher │ │ Matcher │ │ Matcher │
│ (1 gor) │ │ (1 gor) │ │ (1 gor) │
└─────────┘ └─────────┘ └─────────┘
    │           │           │
No shared state between stocks!
```

---

## 📡 Non-Blocking Broadcast Strategy

### Three-Level Non-Blocking

**Level 1: EventBus Publish**
```go
select {
case b.ch <- event:  // Enqueue
default:               // Drop if full (backpressure)
    // Metric: event_dropped_total++
}
```

**Level 2: Hub Broadcast**
```go
select {
case h.broadcast <- msg:  // Queue
default:                    // Drop if full
    slog.Warn("broadcast full, dropping")
}
```

**Level 3: Client Send**
```go
select {
case client.send <- data:  // Send (buffered 256)
default:                    // Kick slow client!
    hub.removeClient(client)
}
```

**Why this works:**
- Fast clients receive messages immediately
- Slow clients get disconnected (not block others)
- System remains responsive under load

---

## ⚠️ Three Main Bottlenecks & Solutions

### 1. Matching Engine Throughput

**Problem:** Global lock on order book blocks all stocks.

**Solution:** Per-Stock Partitioning
```
Before:                    After:
┌────────────────┐         ┌────────┐ ┌────────┐ ┌────────┐
│ Global Lock    │    →    │ BBCA   │ │ BBRI   │ │ TLKM   │
│ (All stocks)   │         │Matcher │ │Matcher │ │Matcher │
└────────────────┘         └────────┘ └────────┘ └────────┘
```

**Code:** `internal/engine/engine.go`

---

### 2. WebSocket Fan-Out (N×M)

**Problem:** Broadcasting to 500 clients × 5 subs = 2,500 writes per event.

**Solution:** Indexed Subscriptions + Buffered Channels
```go
// O(1) lookup instead of O(N)
index := map[string][]*Client{
    "market.ticker:BBCA": [client1, client5, client9, ...],
}

// Non-blocking send
for _, client := range index[key] {
    select {
    case client.send <- msg:
    default:
        hub.unregister <- client  // Kick slow
    }
}
```

**Code:** `internal/transport/ws/hub.go`

---

### 3. Storage Write Amplification

**Problem:** Every fill = 5 writes (orders ×2, trade, ticker, orderbook).

**Solution:** Async DB Worker with Batching
```
Events ──▶ Buffered Channel (5000) ──▶ DB Worker
                                          │
                                          ├── Batch 100 ops
                                          ├── Flush every 100ms
                                          └── Retry with backoff
```

**Code:** `internal/worker/dbworker.go`

---

## 🚀 Horizontal Scaling

### How It Works

**1. Stock Partitioning (Consistent Hashing)**
```go
OwnerIndex(stockCode) = FNV-1a(stockCode) % TotalInstances
```

| Stock | Hash % 3 | Owner |
|-------|----------|-------|
| BBCA | 0 | Instance 0 |
| BBRI | 1 | Instance 1 |
| TLKM | 2 | Instance 2 |

**2. NATS Communication**
- **Queue Groups:** Route orders to owner instance (exactly-one delivery)
- **Pub/Sub:** Broadcast events to all instances (fan-out)

**3. WebSocket Sticky Sessions**
- Nginx `ip_hash` pins client to one instance
- Local subscriptions don't move

### Adding New Instance

1. **Partition auto-update:** Algorithm is stateless
2. **NATS rebalancing:** Queue groups redistribute load
3. **No config change:** Just add to upstream

**Code:** `internal/partition/`, `internal/infra/broker/natsbroker/`

---

## 📚 API Documentation

### Base URL
```
http://localhost:8080/api/v1
```

### Response Format
```json
// Success
{
  "success": true,
  "data": { ... },
  "meta": { "total": 100, "page": 1, "per_page": 20 }
}

// Error
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid stock code"
  }
}
```

### Endpoints

#### Orders

**Create Order**
```http
POST /api/v1/orders
Content-Type: application/json
Authorization: Bearer <token>

{
  "stock_code": "BBCA",
  "side": "BUY",           // "BUY" or "SELL"
  "price": 9500,           // integer
  "quantity": 100,
  "type": "LIMIT"
}

Response:
{
  "success": true,
  "data": {
    "id": "uuid",
    "stock_code": "BBCA",
    "side": "BUY",
    "price": 9500,
    "quantity": 100,
    "filled_quantity": 0,
    "status": "OPEN",
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
}
```

**List Orders**
```http
GET /api/v1/orders?stock=BBCA&status=OPEN&page=1&per_page=20

Response:
{
  "success": true,
  "data": [...],
  "meta": { "total": 42, "page": 1, "per_page": 20 }
}
```

**Get Order by ID**
```http
GET /api/v1/orders/{id}
```

#### Trades

**Get Trade History**
```http
GET /api/v1/trades?stock=BBCA&page=1&per_page=20

Response:
{
  "success": true,
  "data": [
    {
      "id": "uuid",
      "stock_code": "BBCA",
      "price": 9500,
      "quantity": 100,
      "buy_order_id": "uuid",
      "sell_order_id": "uuid",
      "executed_at": "2024-01-01T00:00:00Z"
    }
  ],
  "meta": { "total": 100, "page": 1 }
}
```

#### Market Data

**Get All Tickers**
```http
GET /api/v1/market/ticker

Response:
{
  "success": true,
  "data": [
    {
      "stock_code": "BBCA",
      "last_price": 9500,
      "open": 9400,
      "high": 9600,
      "low": 9350,
      "volume": 1500000,
      "change": 100,
      "change_percent": 1.06
    }
  ]
}
```

**Get Single Ticker**
```http
GET /api/v1/market/ticker/BBCA
```

**Get Order Book**
```http
GET /api/v1/market/orderbook/BBCA

Response:
{
  "success": true,
  "data": {
    "stock_code": "BBCA",
    "bids": [
      {"price": 9500, "quantity": 200},
      {"price": 9450, "quantity": 150}
    ],
    "asks": [
      {"price": 9510, "quantity": 100},
      {"price": 9550, "quantity": 250}
    ],
    "timestamp": "2024-01-01T00:00:00Z"
  }
}
```

**Get Recent Trades**
```http
GET /api/v1/market/trades/BBCA?limit=50
```

#### Authentication (Optional)

**Register**
```http
POST /api/v1/auth/register
Content-Type: application/json

{
  "username": "alice",
  "password": "secret123"
}
```

**Login**
```http
POST /api/v1/auth/login
Content-Type: application/json

{
  "username": "alice",
  "password": "secret123"
}

Response:
{
  "success": true,
  "data": {
    "token": "eyJhbGc...",
    "expires_at": "2024-01-02T00:00:00Z"
  }
}
```

#### Health & Metrics

```http
GET /healthz     # Health check
GET /metrics     # Prometheus metrics
```

---

## 🔌 WebSocket Documentation

### Connection

```
ws://localhost:8080/ws
ws://localhost:8080/ws?token=<jwt>  # Authenticated
```

### Client → Server Messages

| Action | Description | Example |
|--------|-------------|---------|
| `subscribe` | Subscribe to channel | `{"action":"subscribe","channel":"market.ticker","stock":"BBCA"}` |
| `unsubscribe` | Unsubscribe | `{"action":"unsubscribe","channel":"market.ticker","stock":"BBCA"}` |
| `ping` | Keepalive | `{"action":"ping"}` |

### Server → Client Messages

| Type | When | Payload |
|------|------|---------|
| `subscribed` | Subscription confirmed | `{"type":"subscribed","channel":"market.ticker","stock":"BBCA"}` |
| `market.ticker` | Price update | `{"type":"market.ticker","stock":"BBCA","data":{"last_price":9500,...}}` |
| `market.trade` | Trade executed | `{"type":"market.trade","stock":"BBCA","data":{"price":9500,"quantity":100,...}}` |
| `market.orderbook` | Book updated | `{"type":"market.orderbook","stock":"BBCA","data":{"bids":[...],"asks":[...]}}` |
| `order.update` | Your order status | `{"type":"order.update","data":{"order_id":"...","status":"FILLED"}}` |
| `pong` | Ping response | `{"type":"pong"}` |
| `error` | Protocol error | `{"type":"error","message":"..."}` |

### Limits

- Max connections: 1000 per instance
- Max connections per IP: 10
- Max subscriptions per client: 20
- Client send buffer: 256 messages
- Slow clients auto-disconnected

### Testing Tools

#### 1. wscat (Recommended)
```bash
npm install -g wscat

wscat -c ws://localhost:8080/ws
> {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}

# See real-time updates
```

#### 2. websocat (No Node.js)
```bash
# https://github.com/vi/websocat/releases
echo '{"action":"subscribe","channel":"market.trade","stock":"BBCA"}' \
  | websocat ws://localhost:8080/ws
```

#### 3. Browser DevTools
```javascript
const ws = new WebSocket('ws://localhost:8080/ws');
ws.onmessage = e => console.log(JSON.parse(e.data));
ws.onopen = () => ws.send(JSON.stringify({
  action: 'subscribe', channel: 'market.ticker', stock: 'BBCA'
}));
```

---

## 📁 Project Structure

```
mini-exchange/
├── cmd/
│   ├── server/          # Main application entry point
│   │   ├── main.go      # DI wiring + graceful shutdown
│   │   └── main_test.go # Integration tests
│   ├── loadtest/        # HTTP load test tool
│   ├── wsloadtest/      # WebSocket load test (500 clients)
│   ├── matchtest/       # Matching engine verification
│   └── compliancetest/  # Compliance tests
│
├── config/              # Configuration management
│
├── internal/
│   ├── domain/          # Business logic (no external deps)
│   │   ├── entity/      # Order, Trade, Ticker, User, Stock
│   │   ├── event/       # Event types and interfaces
│   │   └── repository/  # Repository interfaces
│   │
│   ├── app/             # Application services (use cases)
│   │   ├── order/       # Order creation, listing
│   │   ├── trade/       # Trade history
│   │   └── market/      # Market data queries
│   │
│   ├── engine/          # Core matching engine
│   │   ├── engine.go    # Fan-in/fan-out coordinator
│   │   ├── matcher.go   # Price-time matching logic
│   │   └── orderbook.go # Order book (bids/asks)
│   │
│   ├── infra/           # Infrastructure adapters
│   │   ├── auth/        # JWT implementation
│   │   ├── broker/      # EventBus, NATS broker
│   │   └── storage/     # Memory, Redis, PostgreSQL
│   │
│   ├── transport/       # Input adapters
│   │   ├── http/        # HTTP handlers, middleware
│   │   └── ws/          # WebSocket hub, client
│   │
│   ├── simulator/       # Price simulation
│   └── worker/          # Background workers (DB persistence)
│
├── pkg/                 # Shared packages
├── migrations/          # SQL migrations
├── docs/                # Documentation
├── docker-compose.yml   # Full stack deployment
└── Makefile            # Build automation
```

---

## 🎁 Bonus Features

- ✅ **NATS Message Broker** — Cross-instance communication
- ✅ **Redis Cache/PubSub** — Shared cache layer
- ✅ **JWT Authentication** — Optional auth
- ✅ **Rate Limiting** — Token bucket algorithm
- ✅ **DB Worker** — Async batch persistence
- ✅ **Prometheus Metrics** — Observability
- ✅ **Binance Feed** — External price data
- ✅ **Partial Fill** — Fill as much as available
- ✅ **FIFO Matching** — Time priority
- ✅ **Docker Compose** — One-command deployment
- ✅ **Horizontal Scaling** — Partition + NATS

---

## ⚙️ Configuration

Key environment variables (see `.env.example` for full list):

```bash
# Server
SERVER_PORT=8080

# Storage
STORAGE_TYPE=memory          # memory | postgres
DB_WORKER_ENABLED=false      # Enable async DB writes

# Message Broker
NATS_ENABLED=false
NATS_URL=nats://localhost:4222

# Cache
REDIS_ENABLED=false
REDIS_URL=redis://localhost:6379/0

# Auth
AUTH_ENABLED=false
JWT_SECRET=change-me-in-production

# Rate Limiting
RATE_LIMIT_ENABLED=true
RATE_LIMIT_RPS=100
RATE_LIMIT_BURST=200

# WebSocket
WS_MAX_CONNECTIONS=1000
WS_MAX_CONNECTIONS_PER_IP=10
WS_MAX_SUBSCRIPTIONS_PER_CLIENT=20
```

---

## 📊 Performance

Load test results (rate limiter disabled):

| Metric | Value |
|--------|-------|
| Max Throughput | **35,450 req/s** (2.1M orders/min) |
| WS Broadcast | **201,680 events/s** |
| Latency p50 | **1ms** |
| Latency p99 | **4ms** |
| Error Rate | **0%** |

**Test commands:**
```bash
# HTTP stress test
go run ./cmd/stresstest -url http://localhost:8080

# WS load test (500 clients)
go run ./cmd/wsloadtest -url http://localhost:8080 -clients 500

# Matching verification
go run ./cmd/matchtest -url http://localhost:8080
```

---

## 📖 Documentation

Complete documentation in `docs/` folder:

- [Getting Started](docs/01-cara-menjalankan.md)
- [Architecture](docs/02-design-arsitektur.md)
- [System Flow](docs/03-system-flow.md)
- [Project Structure](docs/04-project-structure.md)
- [Assumptions](docs/05-assumptions.md)
- [Race Condition Prevention](docs/06-race-condition.md)
- [Broadcast Strategy](docs/07-broadcast-strategy.md)
- [Bottlenecks](docs/08-bottlenecks.md)
- [API Docs](docs/09-api-documentation.md)
- [WebSocket Docs](docs/10-websocket-documentation.md)
- [Horizontal Scaling](docs/11-horizontal-scaling.md)

---

## ✅ Requirements Checklist

| Requirement | Status |
|------------|--------|
| **REST API** (Create Order, List, Trade History, Market Info) | ✅ Complete |
| **Matching Engine** (BUY-SELL match, Trade creation, Status update) | ✅ Complete |
| **Partial Fill** (Bonus) | ✅ Complete |
| **FIFO Matching** (Bonus) | ✅ Complete |
| **Race Condition Safe** | ✅ Complete |
| **WebSocket** (ticker, trade, orderbook, order.update) | ✅ Complete |
| **Non-blocking Broadcast** | ✅ Complete |
| **Price Simulation** | ✅ Complete |
| **Concurrency** (Goroutines, Channels) | ✅ Complete |
| **Bottleneck Documentation** | ✅ Complete |
| **Horizontal Scaling Strategy** | ✅ Complete |
| **Goroutine Leak Prevention** | ✅ Complete |
| **API Documentation** | ✅ Complete |
| **WebSocket Documentation** | ✅ Complete |
| **Clean Architecture** | ✅ Complete |
| **Unit Tests** | ✅ Complete |

**All 25+ requirements from rule.md completed! 🎉**

---

Built with ❤️ using Go, Goroutines, and Channels.
