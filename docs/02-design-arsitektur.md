# 02. Design Arsitektur (Singkat)

## 🎯 Overview

Mini Exchange menggunakan **Clean Architecture** dengan separation of concerns yang jelas antara business logic dan infrastructure.

```
┌─────────────────────────────────────────┐
│  Clients (Browser, Mobile, Bot)         │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│  Transport Layer (Adapters)             │
│  ├── HTTP Handlers (REST API)          │
│  └── WebSocket Handler                 │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│  Application Layer (Use Cases)          │
│  ├── Order Service                     │
│  ├── Trade Service                     │
│  └── Market Service                    │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│  Domain Layer (Business Logic)          │
│  ├── Entities                          │
│  ├── Events                            │
│  └── Repository Interfaces             │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│  Infrastructure Layer                   │
│  ├── Storage (Memory/Redis/Postgres)   │
│  ├── Message Broker (NATS)             │
│  ├── Auth (JWT)                        │
│  └── Workers                           │
└─────────────────────────────────────────┘
```

---

## 🏗️ Layer Details

### 1. Transport Layer

**Responsibility:** Handle HTTP/WebSocket requests/responses

**Files:**
- `internal/transport/http/handler/` — REST API handlers
- `internal/transport/http/router.go` — Route definitions
- `internal/transport/http/middleware/` — Auth, rate limit, logging
- `internal/transport/ws/` — WebSocket hub dan client management

**Key Principle:** Layer ini hanya meng-handle protocol-specific concerns. Business logic di-delegate ke Application Layer.

---

### 2. Application Layer

**Responsibility:** Orchestrate use cases, coordinate domain objects

**Files:**
- `internal/app/order/service.go` — Create order, list orders
- `internal/app/trade/service.go` — Trade history queries
- `internal/app/market/service.go` — Market data queries

**Example:**
```go
func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
    // 1. Create entity
    order := entity.NewOrder(req)
    
    // 2. Save to repository
    s.orderRepo.Save(ctx, order)
    
    // 3. Submit to matching engine
    s.engine.SubmitOrder(order)
    
    // 4. Publish event
    s.eventBus.Publish(event.OrderCreated{Order: order})
    
    return order, nil
}
```

---

### 3. Domain Layer

**Responsibility:** Pure business logic, no external dependencies

**Files:**
- `internal/domain/entity/` — Order, Trade, Ticker, User, Stock
- `internal/domain/event/` — Event types dan EventBus interface
- `internal/domain/repository/` — Repository interfaces

**Key Characteristics:**
- No imports dari external packages (database, HTTP, etc)
- Pure Go structs dan interfaces
- Business rules di-encode di entity methods

---

### 4. Engine Layer

**Responsibility:** Core matching logic

**Files:**
- `internal/engine/engine.go` — Coordinator, partition routing
- `internal/engine/matcher.go` — Price-time matching
- `internal/engine/orderbook.go` — Order book structure

**Design:**
```
Per-Stock Goroutine Pattern:

┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ BBCA        │  │ BBRI        │  │ TLKM        │
│ Channel     │  │ Channel     │  │ Channel     │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │                │                │
       ▼                ▼                ▼
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ BBCA        │  │ BBRI        │  │ TLKM        │
│ Matcher     │  │ Matcher     │  │ Matcher     │
│ (1 goroutine)│ │ (1 goroutine)│ │ (1 goroutine)│
└─────────────┘  └─────────────┘  └─────────────┘
```

**Benefits:**
- No mutex needed (single writer per stock)
- Natural FIFO ordering
- Isolated failure domains

---

### 5. Infrastructure Layer

**Responsibility:** External systems integration

**Storage:**
- `internal/infra/storage/memory/` — In-memory repos (primary)
- `internal/infra/storage/redis/` — Cache layer
- `internal/infra/storage/postgres/` — Persistent storage

**Message Broker:**
- `internal/infra/broker/eventbus.go` — In-process event bus
- `internal/infra/broker/natsbroker/` — NATS integration

**Auth:**
- `internal/infra/auth/jwt.go` — JWT implementation

**Workers:**
- `internal/worker/dbworker.go` — Async DB persistence

---

## 🔄 Dependency Rule

**The Golden Rule:** Dependencies point INWARD only.

```
Transport ──▶ Application ──▶ Domain
                                ▲
Infrastructure ─────────────────┘
```

**Domain Layer** tidak boleh import dari layer lain.
**Infrastructure** implements interfaces defined in Domain.

---

## 📦 Project Structure

```
mini-exchange/
│
├── cmd/                    # Application entry points
│   ├── server/            # Main server
│   ├── loadtest/          # Load testing tools
│   └── ...
│
├── internal/              # Private application code
│   ├── domain/           # Business logic (no deps)
│   │   ├── entity/
│   │   ├── event/
│   │   └── repository/
│   │
│   ├── app/              # Use cases
│   │   ├── order/
│   │   ├── trade/
│   │   └── market/
│   │
│   ├── engine/           # Matching engine
│   │   ├── engine.go
│   │   ├── matcher.go
│   │   └── orderbook.go
│   │
│   ├── infra/            # External adapters
│   │   ├── auth/
│   │   ├── broker/
│   │   └── storage/
│   │
│   ├── transport/        # Input adapters
│   │   ├── http/
│   │   └── ws/
│   │
│   ├── simulator/        # Price simulation
│   └── worker/           # Background workers
│
├── pkg/                   # Public packages
│   ├── response/
│   └── validator/
│
└── docs/                  # Documentation
```

---

## 🎯 Key Design Decisions

### 1. Why Clean Architecture?

- **Testability:** Business logic can be tested without HTTP/DB
- **Flexibility:** Easy to swap storage (memory → postgres)
- **Clarity:** Clear separation of concerns

### 2. Why Per-Stock Goroutines?

- **No Mutex:** Single writer eliminates lock contention
- **FIFO:** Natural ordering within price level
- **Isolation:** One stock's load doesn't affect others

### 3. Why Event-Driven?

- **Decoupling:** Services don't know about each other
- **Extensibility:** Easy to add new consumers
- **Async:** Non-blocking event processing

### 4. Why NATS?

- **Simple:** Minimal configuration
- **Fast:** Sub-millisecond latency
- **Scalable:** Auto-discovery, queue groups

---

## 📊 Component Interaction

```
HTTP Request
    │
    ▼
Handler (Transport)
    │
    ▼
Service (Application)
    │
    ├──▶ Repository (Infrastructure) ──▶ Storage
    │
    ├──▶ Engine (Domain/Engine) ──▶ Match
    │
    └──▶ EventBus (Domain) ──▶ Publish Event
                                   │
                                   ├──▶ WS Hub (Transport)
                                   ├──▶ NATS (Infrastructure)
                                   └──▶ DB Worker (Infrastructure)
```

---

## 🔗 References

- [System Flow](03-system-flow.md) — Alur data detail
- [Project Structure](04-project-structure.md) — Struktur folder
- [Horizontal Scaling](11-horizontal-scaling.md) — Scaling dengan NATS
