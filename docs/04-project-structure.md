# 04. Project Structure

Struktur folder dan file yang jelas sesuai Clean Architecture.

---

## 📁 Root Structure

```
mini-exchange/
│
├── cmd/                          # Application entry points
│   ├── server/                   # Main trading server
│   │   ├── main.go              # DI wiring, graceful shutdown
│   │   └── main_test.go         # Integration tests
│   │
│   ├── loadtest/                # HTTP API load testing
│   │   └── main.go
│   │
│   ├── wsloadtest/              # WebSocket load testing (500 clients)
│   │   └── main.go
│   │
│   ├── matchtest/               # Matching engine verification
│   │   └── main.go
│   │
│   └── compliancetest/          # Compliance verification
│       └── main.go
│
├── config/                       # Configuration management
│   └── config.go                # Env var parsing, validation
│
├── internal/                     # Private application code
│   ├── domain/                  # Business logic (no external deps)
│   ├── app/                     # Application services (use cases)
│   ├── engine/                  # Core matching engine
│   ├── infra/                   # Infrastructure adapters
│   ├── transport/               # Input adapters (HTTP, WS)
│   ├── simulator/               # Price simulation
│   ├── partition/               # Horizontal scaling partition
│   └── worker/                  # Background workers
│
├── pkg/                          # Public packages
│   ├── response/                # HTTP response helpers
│   └── validator/               # Request validation
│
├── migrations/                   # SQL migration files
│   ├── 001_initial_schema.up.sql
│   ├── 001_initial_schema.down.sql
│   └── ...
│
├── docs/                         # Documentation
│   ├── README.md                # Index
│   ├── 01-cara-menjalankan.md
│   ├── 02-design-arsitektur.md
│   ├── 03-system-flow.md
│   ├── 04-project-structure.md
│   ├── 05-assumptions.md
│   ├── 06-race-condition.md
│   ├── 07-broadcast-strategy.md
│   ├── 08-bottlenecks.md
│   ├── 09-api-documentation.md
│   ├── 10-websocket-documentation.md
│   └── 11-horizontal-scaling.md
│
├── Dockerfile                    # Multi-stage build
├── docker-compose.yml           # Full stack deployment
├── docker-compose.scaled.yml    # Horizontal scaling setup
├── nginx.conf                   # Load balancer config
├── Makefile                     # Build automation
├── .env.example                 # Environment template
├── .gitignore
├── go.mod
├── go.sum
└── README.md                    # Main documentation
```

---

## 🔒 Internal Package Structure

### 1. Domain Layer (`internal/domain/`)

**Purpose:** Pure business logic, no external dependencies

```
domain/
├── entity/                      # Business entities
│   ├── order.go                # Order entity + methods
│   ├── trade.go                # Trade entity
│   ├── ticker.go               # Ticker/price entity
│   ├── user.go                 # User entity
│   ├── stock.go                # Stock definition
│   ├── orderbook.go            # OrderBook entity
│   └── side.go                 # BUY/SELL enum
│
├── event/                       # Event system
│   ├── event.go                # Event interface
│   ├── type.go                 # Event type constants
│   └── bus.go                  # EventBus interface
│
└── repository/                  # Repository interfaces
    ├── errors.go               # Repository errors
    ├── order.go                # OrderRepo interface
    ├── trade.go                # TradeRepo interface
    ├── user.go                 # UserRepo interface
    └── market.go               # MarketRepo interface
```

**Key Principle:** Domain layer tidak import package eksternal (no database, no HTTP).

---

### 2. Application Layer (`internal/app/`)

**Purpose:** Use case orchestration

```
app/
├── order/
│   ├── service.go              # Order use cases
│   └── dto.go                  # Request/response types
│
├── trade/
│   └── service.go              # Trade history queries
│
└── market/
    └── service.go              # Market data queries
```

**Responsibilities:**
- Validate input
- Call domain entities
- Coordinate repositories
- Publish events
- Return DTOs

---

### 3. Engine Layer (`internal/engine/`)

**Purpose:** Core matching logic

```
engine/
├── engine.go                   # Coordinator, partition routing
├── engine_test.go
├── matcher.go                  # Price-time matching logic
├── matcher_test.go
├── orderbook.go               # Order book structure
└── orderbook_test.go
```

**Design:**
- One Matcher per stock (goroutine)
- Lock-free within stock
- FIFO matching at same price

---

### 4. Infrastructure Layer (`internal/infra/`)

**Purpose:** External systems integration

```
infra/
├── auth/
│   ├── jwt.go                  # JWT implementation
│   └── jwt_test.go
│
├── broker/
│   ├── eventbus.go            # In-process event bus
│   ├── eventbus_test.go
│   └── natsbroker/            # NATS integration
│       ├── publisher.go
│       └── subscriber.go
│
└── storage/
    ├── memory/                # In-memory repositories
    │   ├── order_repo.go
    │   ├── trade_repo.go
    │   ├── user_repo.go
    │   └── market_repo.go
    │
    ├── redis/                 # Redis cache/pubsub
    │   ├── cache.go
    │   └── pubsub.go
    │
    └── postgres/              # PostgreSQL repositories
        ├── order_repo.go
        ├── trade_repo.go
        └── ...
```

---

### 5. Transport Layer (`internal/transport/`)

**Purpose:** Input adapters (HTTP, WebSocket)

```
transport/
├── http/
│   ├── router.go              # Route definitions
│   ├── handler/               # HTTP handlers
│   │   ├── order.go          # Order handlers
│   │   ├── trade.go          # Trade handlers
│   │   ├── market.go         # Market data handlers
│   │   ├── auth.go           # Auth handlers
│   │   └── *_test.go
│   │
│   └── middleware/            # HTTP middleware
│       ├── logger.go
│       ├── recovery.go
│       ├── cors.go
│       ├── auth.go
│       ├── ratelimit.go
│       └── metrics.go
│
└── ws/
    ├── hub.go                # WebSocket hub (coordinator)
    ├── hub_test.go
    ├── client.go             # Per-connection client
    ├── handler.go            # WS upgrade handler
    └── message.go            # Protocol messages
```

---

### 6. Supporting Packages

```
internal/
├── simulator/                 # Price simulation
│   ├── simulator.go
│   ├── price.go
│   ├── binance.go           # External feed
│   └── *_test.go
│
├── partition/                # Horizontal scaling
│   ├── router.go            # Partition router
│   └── partitioner.go       # Consistent hashing
│
└── worker/                   # Background workers
    └── dbworker.go          # Async DB persistence
```

---

## 📦 Public Packages (`pkg/`)

**Purpose:** Shared utilities, can be imported by external packages

```
pkg/
├── response/                 # HTTP response helpers
│   └── response.go
│
└── validator/               # Request validation
    └── validator.go
```

---

## 🧪 Test Files

**Pattern:** `*_test.go` alongside source files

```
engine/
├── engine.go
├── engine_test.go          # Unit tests
├── matcher.go
├── matcher_test.go
└── orderbook.go
    └── orderbook_test.go
```

**Test Commands:**
```bash
# All tests
go test ./...

# With race detector
go test -race ./...

# Specific package
go test ./internal/engine/... -v

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## 🔧 Configuration Files

### Environment Configuration

```
.env                    # Local development (gitignored)
.env.example           # Template (committed)
.env.docker            # Docker environment
```

### Docker Configuration

```
Dockerfile              # Multi-stage build
docker-compose.yml     # Full stack
docker-compose.scaled.yml  # Horizontal scaling
nginx.conf            # Load balancer
```

### Build Configuration

```
Makefile               # Build automation
go.mod                # Go module definition
go.sum                # Dependency checksums
```

---

## 📊 Code Statistics

**Approximate breakdown:**

| Category | Files | Lines of Code | Purpose |
|----------|-------|---------------|---------|
| Domain | 15 | ~800 | Business logic |
| Application | 6 | ~600 | Use cases |
| Engine | 6 | ~1,200 | Matching logic |
| Infrastructure | 20 | ~2,500 | External adapters |
| Transport | 15 | ~1,800 | HTTP/WS |
| Tests | 20 | ~2,000 | Test coverage |
| Tools | 4 | ~1,500 | Load testing |
| **Total** | **~86** | **~10,400** | **Complete system** |

---

## 🎯 Clean Architecture Compliance

### Dependency Direction

```
Transport ──▶ Application ──▶ Domain
                                ▲
Infrastructure ─────────────────┘
```

✅ **Domain** has no external dependencies  
✅ **Application** depends only on Domain  
✅ **Infrastructure** implements Domain interfaces  
✅ **Transport** depends on Application & Domain  

### Import Rules

**Allowed:**
```go
// Application can import Domain
import "github.com/.../internal/domain/entity"

// Infrastructure can import Domain
import "github.com/.../internal/domain/repository"
```

**Forbidden:**
```go
// Domain CANNOT import Infrastructure
// Domain CANNOT import Application
// Domain CANNOT import Transport
```

---

## 📝 Naming Conventions

### Files
- `service.go` — Application service
- `handler.go` — HTTP handler
- `repo.go` — Repository implementation
- `*_test.go` — Test file

### Interfaces
- `Repository` suffix for repos: `OrderRepository`
- `Service` suffix for services: `OrderService`
- `Handler` suffix for handlers: `OrderHandler`

### Implementations
- `orderRepo` — Struct implementing `OrderRepository`
- `orderService` — Struct implementing `OrderService`
- `orderHandler` — Struct implementing HTTP handler

---

## 🚀 Build Commands

```bash
# Development
go run ./cmd/server

# Build binary
go build -o bin/server ./cmd/server

# With Makefile
make build
make run
make test-race
make docker-up
```
