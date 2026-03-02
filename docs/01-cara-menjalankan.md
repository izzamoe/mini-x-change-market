# 01. Cara Menjalankan Project

## 🚀 Quick Start

### Prerequisites
- Go 1.25 atau lebih baru
- Docker & Docker Compose (opsional, untuk full stack)
- Make (opsional, untuk convenience commands)

## 📦 Instalasi

### 1. Clone Repository

```bash
git clone git@github.com:izzamoe/mini-x-change-market.git
cd mini-exchange
```

### 2. Setup Environment

```bash
# Copy environment template
cp .env.example .env

# Review konfigurasi (opsional untuk development)
cat .env
```

**Default `.env` untuk development:**
```bash
SERVER_PORT=8080
STORAGE_TYPE=memory              # In-memory storage
SIMULATOR_ENABLED=true           # Aktifkan price simulator
AUTH_ENABLED=false               # Nonaktifkan auth (dev mode)
RATE_LIMIT_ENABLED=false         # Nonaktifkan rate limiter (dev mode)
NATS_ENABLED=false               # Nonaktifkan NATS (single instance)
REDIS_ENABLED=false              # Nonaktifkan Redis
DB_WORKER_ENABLED=false          # Nonaktifkan DB persistence
```

---

## ▶️ Menjalankan Secara Lokal

### Mode 1: In-Memory Only (Development)

Cepat, tidak perlu services eksternal:

```bash
go run ./cmd/server
```

**Verifikasi:**
```bash
# Terminal 2
curl http://localhost:8080/healthz
# Output: {"status":"ok","uptime":"5s","goroutines":24}

curl http://localhost:8080/api/v1/market/ticker
# Output: {"success":true,"data":[...]}
```

---

## 🐳 Menjalankan dengan Docker Compose (Production Mode)

### Full Stack (App + NATS + Redis + PostgreSQL)

```bash
# Build dan start semua services
make docker-up

# Atau manual:
docker-compose up -d --build
```

**Services yang berjalan:**
- **app**: Mini Exchange server (port 8080)
- **nats**: Message broker (port 4222)
- **redis**: Cache (port 6379)
- **postgres**: Database (port 5432)

**Verifikasi:**
```bash
# Cek status containers
docker-compose ps

# View logs
make docker-logs
# atau
docker-compose logs -f app

# Test endpoint
curl http://localhost:8080/healthz
```

### Stop Services

```bash
make docker-down
# atau
docker-compose down -v
```

---

## 🧪 Menjalankan Tests

### Semua Tests

```bash
# Dengan race detector
make test-race

# Dengan coverage
make test-cover
# Lihat hasil: open coverage.html
```

### Tests Spesifik

```bash
# Engine tests
go test ./internal/engine/... -v -race

# WebSocket tests
go test ./internal/transport/ws/... -v -race

# Integration tests
go test ./cmd/server/... -v -race
```

---

## 🔄 Development Workflow

### 1. Hot Reload (dengin Air)

```bash
# Install Air
go install github.com/cosmtrek/air@latest

# Jalankan dengan hot reload
air
```

### 2. Format & Lint

```bash
# Format code
go fmt ./...

# Vet
go vet ./...

# Lint (golangci-lint)
golangci-lint run ./...
```

---

## 🌐 Testing WebSocket

### 1. Install wscat

```bash
npm install -g wscat
```

### 2. Connect & Subscribe

```bash
# Terminal 1: WebSocket client
wscat -c ws://localhost:8080/ws

# Subscribe ke ticker
> {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}
```

### 3. Trigger Trade

```bash
# Terminal 2: Submit orders
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"stock_code":"BBCA","side":"BUY","price":9500,"quantity":100,"type":"LIMIT"}'

curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"stock_code":"BBCA","side":"SELL","price":9500,"quantity":100,"type":"LIMIT"}'
```

### 4. Lihat Events

Di Terminal 1 (wscat), Anda akan melihat:
```json
{"type":"market.trade","stock":"BBCA","data":{"price":9500,"quantity":100,...}}
```

---

## 📊 Load Testing

### HTTP Stress Test

```bash
go run ./cmd/stresstest \
  -url http://localhost:8080 \
  -max-workers 400 \
  -ws-clients 50
```

### WebSocket Load Test

```bash
go run ./cmd/wsloadtest \
  -url http://localhost:8080 \
  -clients 500 \
  -duration 60s
```

### Matching Engine Test

```bash
go run ./cmd/matchtest \
  -url http://localhost:8080 \
  -rate 100000 \
  -duration 30s
```

---

## 🔧 Troubleshooting

### Port Already in Use

```bash
# Cek port 8080
lsof -i :8080

# Kill process atau ubah port
echo "SERVER_PORT=8081" >> .env
```

### Docker Compose Issues

```bash
# Reset semua
docker-compose down -v
docker-compose up -d --build

# Cek logs
docker-compose logs -f app
```

### Race Condition Warnings

```bash
# Pastikan semua tests pass dengan race detector
make test-race

# Jika ada race, cek:
# 1. Semua repo access pakai mutex
# 2. Tidak ada shared state tanpa proteksi
```

---

## 📚 Next Steps

Setelah berhasil menjalankan:
1. [Design Arsitektur](02-design-arsitektur.md) — Pahami struktur sistem
2. [System Flow](03-system-flow.md) — Alur data lengkap
3. [API Documentation](09-api-documentation.md) — Semua endpoints
