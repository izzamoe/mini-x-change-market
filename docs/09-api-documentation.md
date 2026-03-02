# 09. API Documentation

Dokumentasi lengkap REST API endpoints.

---

## 📍 Base URL

```
http://localhost:8080/api/v1
```

---

## 📋 Response Format

### Success Response

```json
{
  "success": true,
  "data": { ... },
  "meta": {
    "total": 100,
    "page": 1,
    "per_page": 20,
    "pages": 5
  }
}
```

### Error Response

```json
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Invalid stock code: XYZ"
  }
}
```

---

## 🔐 Authentication

Authentication menggunakan JWT (JSON Web Token).

**Header:**
```http
Authorization: Bearer <token>
```

**Note:** Authentication dapat di-disable untuk development (`AUTH_ENABLED=false`).

---

### POST /auth/register

Register user baru.

**Request:**
```http
POST /api/v1/auth/register
Content-Type: application/json

{
  "username": "alice",
  "password": "secret123"
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "user": {
      "id": "uuid",
      "username": "alice",
      "created_at": "2024-01-01T00:00:00Z"
    },
    "token": "eyJhbGciOiJIUzI1NiIs...",
    "expires_at": "2024-01-02T00:00:00Z"
  }
}
```

**Error Codes:**
- `400` - Bad Request (invalid JSON)
- `409` - Conflict (username exists)

---

### POST /auth/login

Login dan dapatkan token.

**Request:**
```http
POST /api/v1/auth/login
Content-Type: application/json

{
  "username": "alice",
  "password": "secret123"
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIs...",
    "expires_at": "2024-01-02T00:00:00Z"
  }
}
```

**Error Codes:**
- `401` - Unauthorized (invalid credentials)

---

## 📦 Orders

### POST /orders

Membuat order baru (BUY atau SELL).

**Request:**
```http
POST /api/v1/orders
Content-Type: application/json
Authorization: Bearer <token>

{
  "stock_code": "BBCA",
  "side": "BUY",
  "price": 9500,
  "quantity": 100,
  "type": "LIMIT"
}
```

**Parameters:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `stock_code` | string | Yes | Kode saham (BBCA, BBRI, TLKM, ASII, GOTO) |
| `side` | string | Yes | "BUY" atau "SELL" |
| `price` | integer | Yes | Harga dalam integer (e.g., 9500 = Rp 9,500) |
| `quantity` | integer | Yes | Jumlah lot (1 - 1,000,000) |
| `type` | string | Yes | "LIMIT" (market order belum support) |

**Response:**
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
    "created_at": "2024-01-01T12:00:00Z",
    "updated_at": "2024-01-01T12:00:00Z"
  }
}
```

**Status Values:**
- `OPEN` - Order aktif, menunggu match
- `PARTIAL` - Partially filled, masih menunggu
- `FILLED` - Fully filled, selesai
- `CANCELLED` - Dibatalkan

**Error Codes:**
- `400` - Validation error (invalid params)
- `401` - Unauthorized (invalid token)
- `429` - Rate limited (too many requests)
- `503` - Backpressure (engine queue full)

**Cara Penggunaan:**

```bash
# Create BUY order
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{
    "stock_code": "BBCA",
    "side": "BUY",
    "price": 9500,
    "quantity": 100,
    "type": "LIMIT"
  }'

# Create SELL order (matching)
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{
    "stock_code": "BBCA",
    "side": "SELL",
    "price": 9500,
    "quantity": 100,
    "type": "LIMIT"
  }'
```

---

### GET /orders

List semua orders dengan filter.

**Request:**
```http
GET /api/v1/orders?stock=BBCA&status=OPEN&page=1&per_page=20
Authorization: Bearer <token>
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `stock` | string | - | Filter by stock code |
| `status` | string | - | Filter by status (OPEN, PARTIAL, FILLED) |
| `side` | string | - | Filter by side (BUY, SELL) |
| `page` | integer | 1 | Page number |
| `per_page` | integer | 20 | Items per page (max 100) |

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": "uuid",
      "stock_code": "BBCA",
      "side": "BUY",
      "price": 9500,
      "quantity": 100,
      "filled_quantity": 50,
      "status": "PARTIAL",
      "created_at": "2024-01-01T12:00:00Z",
      "updated_at": "2024-01-01T12:05:00Z"
    }
  ],
  "meta": {
    "total": 42,
    "page": 1,
    "per_page": 20,
    "pages": 3
  }
}
```

---

### GET /orders/{id}

Get order by ID.

**Request:**
```http
GET /api/v1/orders/550e8400-e29b-41d4-a716-446655440000
Authorization: Bearer <token>
```

**Response:**
```json
{
  "success": true,
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "stock_code": "BBCA",
    "side": "BUY",
    "price": 9500,
    "quantity": 100,
    "filled_quantity": 100,
    "status": "FILLED",
    "created_at": "2024-01-01T12:00:00Z",
    "updated_at": "2024-01-01T12:01:00Z"
  }
}
```

**Error Codes:**
- `404` - Order not found

---

## 💹 Trades

### GET /trades

Get trade history.

**Request:**
```http
GET /api/v1/trades?stock=BBCA&page=1&per_page=20
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `stock` | string | - | Filter by stock code |
| `page` | integer | 1 | Page number |
| `per_page` | integer | 20 | Items per page |

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": "trade-uuid",
      "stock_code": "BBCA",
      "price": 9500,
      "quantity": 100,
      "buy_order_id": "buy-order-uuid",
      "sell_order_id": "sell-order-uuid",
      "buyer_user_id": "buyer-uuid",
      "seller_user_id": "seller-uuid",
      "executed_at": "2024-01-01T12:01:00Z"
    }
  ],
  "meta": {
    "total": 150,
    "page": 1,
    "per_page": 20,
    "pages": 8
  }
}
```

---

## 📈 Market Data

### GET /market/ticker

Get all stock tickers.

**Request:**
```http
GET /api/v1/market/ticker
```

**Response:**
```json
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
      "change_percent": 1.06,
      "updated_at": "2024-01-01T12:00:00Z"
    },
    {
      "stock_code": "BBRI",
      "last_price": 5000,
      "open": 4950,
      "high": 5100,
      "low": 4900,
      "volume": 2000000,
      "change": 50,
      "change_percent": 1.01,
      "updated_at": "2024-01-01T12:00:00Z"
    }
  ]
}
```

**Field Descriptions:**

| Field | Description |
|-------|-------------|
| `last_price` | Harga terakhir transaksi |
| `open` | Harga pembukaan |
| `high` | Harga tertinggi hari ini |
| `low` | Harga terendah hari ini |
| `volume` | Total volume transaksi |
| `change` | Perubahan dari open (absolute) |
| `change_percent` | Perubahan dari open (percentage) |

---

### GET /market/ticker/{stock}

Get ticker untuk satu saham.

**Request:**
```http
GET /api/v1/market/ticker/BBCA
```

**Response:**
```json
{
  "success": true,
  "data": {
    "stock_code": "BBCA",
    "last_price": 9500,
    "open": 9400,
    "high": 9600,
    "low": 9350,
    "volume": 1500000,
    "change": 100,
    "change_percent": 1.06,
    "updated_at": "2024-01-01T12:00:00Z"
  }
}
```

**Error Codes:**
- `404` - Stock not found

---

### GET /market/orderbook/{stock}

Get order book (bid/ask depth).

**Request:**
```http
GET /api/v1/market/orderbook/BBCA
```

**Response:**
```json
{
  "success": true,
  "data": {
    "stock_code": "BBCA",
    "bids": [
      {"price": 9500, "quantity": 200},
      {"price": 9450, "quantity": 150},
      {"price": 9400, "quantity": 300}
    ],
    "asks": [
      {"price": 9510, "quantity": 100},
      {"price": 9550, "quantity": 250},
      {"price": 9600, "quantity": 150}
    ],
    "timestamp": "2024-01-01T12:00:00Z"
  }
}
```

**Note:** 
- Bids sorted DESC by price (highest first)
- Asks sorted ASC by price (lowest first)
- Quantity = total aggregated per price level

---

### GET /market/trades/{stock}

Get recent trades untuk satu saham.

**Request:**
```http
GET /api/v1/market/trades/BBCA?limit=50
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | integer | 20 | Max trades to return (max 100) |

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": "trade-uuid",
      "stock_code": "BBCA",
      "price": 9500,
      "quantity": 100,
      "executed_at": "2024-01-01T12:01:00Z"
    }
  ]
}
```

---

## 🏥 Health & Metrics

### GET /healthz

Health check endpoint.

**Request:**
```http
GET /healthz
```

**Response:**
```json
{
  "status": "ok",
  "uptime": "42s",
  "goroutines": 24,
  "timestamp": "2024-01-01T12:00:00Z"
}
```

---

### GET /metrics

Prometheus metrics endpoint.

**Request:**
```http
GET /metrics
```

**Response:**
```
# HELP http_requests_total Total HTTP requests
# TYPE http_requests_total counter
http_requests_total{method="POST",endpoint="/api/v1/orders",status="200"} 1000

# HELP http_request_duration_seconds HTTP request duration
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{le="0.005"} 900
http_request_duration_seconds_bucket{le="0.01"} 950

# HELP ws_connections_active Active WebSocket connections
# TYPE ws_connections_active gauge
ws_connections_active 500

# HELP trades_executed_total Total trades executed
# TYPE trades_executed_total counter
trades_executed_total{stock="BBCA"} 5000
```

---

## 📊 Summary Table

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/auth/register` | POST | No | Register user |
| `/auth/login` | POST | No | Login |
| `/orders` | POST | Yes | Create order |
| `/orders` | GET | Yes | List orders |
| `/orders/{id}` | GET | Yes | Get order |
| `/trades` | GET | Optional | Trade history |
| `/market/ticker` | GET | No | All tickers |
| `/market/ticker/{stock}` | GET | No | Single ticker |
| `/market/orderbook/{stock}` | GET | No | Order book |
| `/market/trades/{stock}` | GET | No | Recent trades |
| `/healthz` | GET | No | Health check |
| `/metrics` | GET | No | Prometheus metrics |

---

## 🔧 Testing dengan curl

```bash
#!/bin/bash

BASE_URL="http://localhost:8080/api/v1"
TOKEN="your-jwt-token"

# 1. Create BUY order
echo "Creating BUY order..."
curl -s -X POST "$BASE_URL/orders" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "stock_code": "BBCA",
    "side": "BUY",
    "price": 9500,
    "quantity": 100,
    "type": "LIMIT"
  }' | jq .

# 2. Create SELL order (matching)
echo "Creating SELL order..."
curl -s -X POST "$BASE_URL/orders" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "stock_code": "BBCA",
    "side": "SELL",
    "price": 9500,
    "quantity": 100,
    "type": "LIMIT"
  }' | jq .

# 3. Get ticker
echo "Getting ticker..."
curl -s "$BASE_URL/market/ticker/BBCA" | jq .

# 4. Get order book
echo "Getting order book..."
curl -s "$BASE_URL/market/orderbook/BBCA" | jq .

# 5. Get recent trades
echo "Getting recent trades..."
curl -s "$BASE_URL/market/trades/BBCA?limit=10" | jq .
```

---

## 📚 References

- **Routes:** `internal/transport/http/router.go`
- **Handlers:** `internal/transport/http/handler/`
- **Middleware:** `internal/transport/http/middleware/`
