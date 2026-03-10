# Plan 9: Docker, Makefile & Final Documentation
# Estimated: ~40 minutes
# Dependencies: Plan 1-8 (all features implemented)

## Objective
Create Docker setup (Dockerfile + docker-compose), Makefile for common commands, complete README.md with all required documentation (architecture, API docs, WS docs, assumptions, bottlenecks).

---

## Tasks

### 9.1 Dockerfile (Multi-stage Build)
```dockerfile
# Stage 1: Build
FROM golang:1.26-alpine AS builder
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# Stage 2: Runtime (minimal image)
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
```

### 9.2 Docker Compose (docker-compose.yml)
```yaml
services:
  app:
    build: .
    ports:
      - "8080:8080"
    depends_on:
      nats:
        condition: service_started
      redis:
        condition: service_healthy
      postgres:
        condition: service_healthy
    env_file: .env
    restart: unless-stopped

  nats:
    image: nats:latest
    ports:
      - "4222:4222"
      - "8222:8222"   # monitoring
    command: ["--js"]  # enable JetStream

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 3

  postgres:
    image: postgres:16-alpine
    ports:
      - "5432:5432"
    environment:
      POSTGRES_DB: miniexchange
      POSTGRES_USER: miniexchange
      POSTGRES_PASSWORD: miniexchange
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U miniexchange"]
      interval: 5s
      timeout: 3s
      retries: 3

volumes:
  pgdata:
```

### 9.3 Makefile
```makefile
.PHONY: run build test test-race test-cover lint docker-up docker-down migrate-up migrate-down clean

# Development
run:
	go run ./cmd/server

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/server ./cmd/server

# Testing
test:
	go test ./... -v

test-race:
	go test ./... -v -race

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Linting
lint:
	golangci-lint run ./...

# Docker
docker-up:
	docker-compose up -d --build

docker-down:
	docker-compose down -v

docker-logs:
	docker-compose logs -f app

# Database
migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down

# Cleanup
clean:
	rm -rf bin/ coverage.out coverage.html
```

### 9.4 .env File
```env
# Minimal config for local development
SERVER_PORT=8080
STORAGE_TYPE=memory
SIMULATOR_ENABLED=true
LOG_LEVEL=debug
LOG_FORMAT=text
AUTH_ENABLED=false
RATE_LIMIT_ENABLED=true
RATE_LIMIT_RPS=100
BINANCE_FEED_ENABLED=false
NATS_ENABLED=false
REDIS_ENABLED=false
DB_WORKER_ENABLED=false
```

### 9.5 .env.docker File (for docker-compose)
```env
SERVER_PORT=8080
STORAGE_TYPE=memory
SIMULATOR_ENABLED=true
LOG_LEVEL=info
LOG_FORMAT=json
AUTH_ENABLED=true
JWT_SECRET=super-secret-key-change-in-production
RATE_LIMIT_ENABLED=true
RATE_LIMIT_RPS=100
BINANCE_FEED_ENABLED=false
NATS_ENABLED=true
NATS_URL=nats://nats:4222
REDIS_ENABLED=true
REDIS_URL=redis://redis:6379/0
DB_WORKER_ENABLED=true
DATABASE_URL=postgres://miniexchange:miniexchange@postgres:5432/miniexchange?sslmode=disable
```

### 9.6 Complete README.md
The README must contain ALL required sections from the test requirements:

#### Structure:
```markdown
# Mini Exchange - Realtime Trading System

## Quick Start
- How to run locally (go run)
- How to run with Docker (docker-compose up)
- How to run tests

## Architecture
- System overview diagram
- Clean architecture layers
- Tech stack with justification

## System Flow
- Order creation → matching → trade → broadcast
- WebSocket subscription flow
- Price simulation flow

## API Documentation
### REST Endpoints
- POST /api/v1/orders (with request/response examples)
- GET /api/v1/orders
- GET /api/v1/orders/:id
- GET /api/v1/trades
- GET /api/v1/market/ticker
- GET /api/v1/market/ticker/:stock
- GET /api/v1/market/orderbook/:stock
- GET /api/v1/market/trades/:stock
- POST /api/v1/auth/register
- POST /api/v1/auth/login

### WebSocket Documentation
- How to connect: ws://localhost:8080/ws
- How to subscribe: {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}
- Message format (all message types with examples)
- Testing with wscat/websocat/Postman

## Assumptions
- 1000 orders/min, 500 WS clients, 1-5 subscriptions each
- In-memory as primary storage
- Matching engine serialized per stock
- Prices in smallest unit (integer, no floating point)

## Race Condition Prevention
- Per-stock goroutine for matching (no mutex needed)
- RWMutex for storage
- Channel-based WS Hub
- atomic.Value for tickers

## Non-Blocking Broadcast Strategy
- Buffered channels per client
- select/default pattern
- Slow client auto-disconnect

## Three Main Bottlenecks
1. Matching Engine Throughput → batch processing, per-stock isolation
2. WebSocket Fan-out → buffered channels, subscription-indexed map
3. Storage Write Amplification → in-memory primary, async DB Worker

## Horizontal Scaling Strategy
- REST API: stateless, round-robin
- Matching Engine: partition by stock
- WebSocket: sticky sessions + Redis/NATS cross-instance broadcast
- Storage: shared PostgreSQL + Redis cache

## Bonus Features
- NATS message broker
- Redis pub/sub + cache
- JWT authentication
- Rate limiting
- Prometheus monitoring
- Unit tests
- DB Worker (async persistence)
- Binance external feed
- Partial fill + FIFO matching
- Order book + order update WS channels

## Project Structure
(full directory tree)

## Configuration
(all env vars with descriptions)
```

---

## Files Created
```
Dockerfile
docker-compose.yml
Makefile
.env
.env.example
.env.docker
.gitignore
README.md
```

## Verification
```bash
# Build and run
make build
make run
make test-race
make docker-up
# Check all services healthy
docker-compose ps
curl http://localhost:8080/healthz
make docker-down
```

## Exit Criteria
- `docker-compose up --build` starts all services successfully
- App connects to NATS, Redis, PostgreSQL in Docker mode
- `make test-race` passes all tests
- README.md covers ALL required documentation sections
- .gitignore excludes binaries, .env, coverage files
- Project is ready for git push
