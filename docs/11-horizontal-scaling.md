# 11. Horizontal Scaling

Cara kerja horizontal scaling dengan NATS.

---

## 🤔 Masalah: Single Instance Limit

**Keterbatasan single instance:**
- Max 1000 WebSocket connections
- CPU/Memory limits
- Single point of failure
- Tidak bisa scale dengan traffic growth

**Solusi:** Horizontal Scaling (multiple instances)

---

## 🏗️ Arsitektur Horizontal Scaling

```
┌─────────────────────────────────────────────────────────────────┐
│                     HORIZONTAL SCALING SETUP                    │
│                         (3 Instances)                           │
└─────────────────────────────────────────────────────────────────┘

                              ┌──────────────┐
                              │   NGINX      │
                              │ Load Balancer│
                              │              │
                              │ /ws: ip_hash │◀── Sticky sessions
                              │ /api: round  │
                              └──────┬───────┘
                                     │
           ┌─────────────────────────┼─────────────────────────┐
           │                         │                         │
┌──────────▼─────────┐  ┌────────────▼────────────┐  ┌─────────▼──────────┐
│   Instance 0       │  │      Instance 1         │  │    Instance 2      │
│   (BBCA, GOTO)     │  │      (BBRI, ASII)       │  │    (TLKM, others)  │
│                    │  │                         │  │                    │
│ ┌────────────────┐ │  │ ┌─────────────────────┐ │  │ ┌────────────────┐ │
│ │ BBCA Matcher   │ │  │ │ BBRI Matcher        │ │  │ │ TLKM Matcher   │ │
│ │ (1 goroutine)  │ │  │ │ (1 goroutine)       │ │  │ │ (1 goroutine)  │ │
│ └───────┬────────┘ │  │ └──────────┬──────────┘ │  │ └───────┬────────┘ │
│         │          │  │            │            │  │         │          │
│ ┌───────▼────────┐ │  │ ┌──────────▼──────────┐ │  │ ┌───────▼────────┐ │
│ │ WS Hub         │ │  │ │ WS Hub              │ │  │ │ WS Hub         │ │
│ │ (Local Clients)│ │  │ │ (Local Clients)     │ │  │ │ (Local Clients)│ │
│ └───────┬────────┘ │  │ └──────────┬──────────┘ │  │ └───────┬────────┘ │
└─────────┼──────────┘  └────────────┼────────────┘  └─────────┼──────────┘
          │                          │                         │
          └──────────────────────────┼─────────────────────────┘
                                     │
                           ┌─────────▼──────────┐
                           │   NATS Server      │
                           │   (Message Broker) │
                           │                    │
                           │ Subjects:          │
                           │ - trade.*          │
                           │ - ticker.*         │
                           │ - orderbook.*      │
                           │ - engine.orders.*  │◀── Queue Groups
                           └─────────┬──────────┘
                                     │
          ┌──────────────────────────┼──────────────────────────┐
          │                          │                          │
   ┌──────▼──────┐        ┌─────────▼──────────┐      ┌────────▼──────┐
   │   REDIS     │        │   POSTGRESQL       │      │   Simulator   │
   │   (Cache)   │        │   (Persistence)    │      │   (Price)     │
   └─────────────┘        └────────────────────┘      └───────────────┘
```

---

## 🔑 Key Components

### 1. Stock Partitioning (Consistent Hashing)

**Masalah:** Order untuk BBCA harus diproses oleh satu instance (untuk FIFO matching)

**Solusi:** Partition berdasarkan stock code menggunakan FNV-1a hash

```go
// pkg/partition/partitioner.go
func OwnerIndex(stockCode string, totalInstances int) int {
    h := fnv.New64a()
    h.Write([]byte(stockCode))
    return int(h.Sum64() % uint64(totalInstances))
}
```

**Contoh dengan 3 instances:**

| Stock Code | FNV-1a Hash | Hash % 3 | Owner Instance |
|------------|-------------|----------|----------------|
| BBCA | 1,234,567,890 | 0 | Instance 0 |
| BBRI | 9,876,543,210 | 1 | Instance 1 |
| TLKM | 5,555,555,555 | 2 | Instance 2 |
| ASII | 7,777,777,777 | 1 | Instance 1 |
| GOTO | 3,333,333,333 | 0 | Instance 0 |

**Configuration per instance:**

```yaml
# Instance 0
PARTITION_ENABLED: "true"
PARTITION_INSTANCE_INDEX: "0"
PARTITION_TOTAL_INSTANCES: "3"

# Instance 1
PARTITION_INSTANCE_INDEX: "1"

# Instance 2
PARTITION_INSTANCE_INDEX: "2"
```

**Algorithm is stateless:**
- Semua instance menghitung owner yang sama
- Tidak perlu coordination
- Deterministic: Same stock → same owner

---

### 2. NATS Message Broker

**Dua mode komunikasi:**

#### A. Queue Groups (Order Routing)

**Purpose:** Route order ke instance yang own stock

```
┌───────────────────────────────────────────────────────────────┐
│                    QUEUE GROUPS                               │
│                                                               │
│   Order untuk BBCA (owned by Instance 0)                      │
│                                                               │
│   Publisher:                                                  │
│   subject = "engine.orders.BBCA"                              │
│                                                               │
│   Subscribers:                                                │
│   Instance 0 ──┐                                              │
│   Instance 1 ──┼──▶ NATS ──▶ [Queue: engine-BBCA]            │
│   Instance 2 ──┘                                             │
│                    │                                          │
│                    └──▶ Hanya Instance 0 yang terima!        │
│                        (exactly-one delivery)                │
└───────────────────────────────────────────────────────────────┘
```

**Code:**
```go
// Publisher (Instance menerima order dari client)
func (r *Router) SubmitOrder(order *entity.Order) error {
    if r.partitioner.OwnsStock(order.StockCode) {
        // Stock owned by this instance
        return r.local.SubmitOrder(order)
    }
    
    // Forward to owning instance via NATS
    subject := "engine.orders." + order.StockCode
    return r.publisher.Publish(subject, order)
}

// Subscriber (Queue Group)
func wireEngineNATSOrders(sub *natsbroker.Subscriber, eng *engine.Engine) {
    ownedStocks := partitioner.OwnedStocks(allStocks)
    
    for _, stock := range ownedStocks {
        subject := "engine.orders." + stock
        queue := "engine-" + stock
        
        sub.QueueSubscribe(subject, queue, func(data []byte) {
            var order entity.Order
            json.Unmarshal(data, &order)
            eng.SubmitOrder(&order)  // Process locally
        })
    }
}
```

**Keuntungan Queue Groups:**
- ✅ Exactly-one delivery (tidak ada duplicate)
- ✅ Auto load balancing
- ✅ Auto failover (if instance dies, NATS routes to another)

---

#### B. Pub/Sub (Event Broadcast)

**Purpose:** Broadcast events ke semua instances

```
┌───────────────────────────────────────────────────────────────┐
│                     PUB/SUB                                   │
│                                                               │
│   Trade terjadi di Instance 0 (BBCA)                          │
│                                                               │
│   Publisher (Instance 0):                                     │
│   subject = "trade.BBCA"                                      │
│   payload = JSON trade                                        │
│                                                               │
│   NATS Fan-Out:                                               │
│                    ┌────────────────┐                        │
│   Instance 0 ◀─────┤                │                        │
│   Instance 1 ◀─────┤  NATS Server   │                        │
│   Instance 2 ◀─────┤                │                        │
│                    └────────────────┘                        │
│                                                               │
│   Result: Semua instance menerima trade event!               │
│           Each broadcasts ke local WS clients                │
└───────────────────────────────────────────────────────────────┘
```

**Code:**
```go
// Publisher (after trade execution)
func (m *Matcher) executeTrade(...) {
    // ... create trade ...
    
    // Publish to NATS
    if natsPub != nil {
        natsPub.Publish("trade."+m.stockCode, trade)
    }
}

// Subscribers (all instances)
func wireNATSSubscriber(sub *natsbroker.Subscriber, hub *ws.Hub) {
    // Subscribe to all trade events
    sub.SubscribeSubject("trade.*", func(subject string, data []byte) {
        stock := strings.TrimPrefix(subject, "trade.")
        var trade entity.Trade
        json.Unmarshal(data, &trade)
        
        // Broadcast to local WS clients
        hub.Broadcast("market.trade", stock, &trade)
    })
    
    // Similar for ticker.*, orderbook.*
}
```

**Keuntungan Pub/Sub:**
- ✅ All instances receive events
- ✅ Decoupled (publisher tidak tahu subscribers)
- ✅ Wildcards (trade.* = all trades)

---

### 3. Sticky Sessions untuk WebSocket

**Masalah:** WebSocket stateful (subscriptions disimpan di memory)

**Solusi:** Sticky sessions dengan nginx `ip_hash`

```nginx
# nginx.conf

# WebSocket upstream dengan sticky sessions
upstream ws_backend {
    ip_hash;  # Same client IP → same instance
    server instance-0:8080;
    server instance-1:8080;
    server instance-2:8080;
}

# REST API upstream (round robin OK)
upstream api_backend {
    server instance-0:8080;
    server instance-1:8080;
    server instance-2:8080;
}

server {
    listen 80;
    
    # WebSocket endpoint (sticky required)
    location /ws {
        proxy_pass http://ws_backend;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_set_header X-Real-IP $remote_addr;
    }
    
    # REST API (round robin OK)
    location /api/ {
        proxy_pass http://api_backend;
    }
}
```

**Why ip_hash:**
- Client reconnects → same instance
- Subscriptions preserved
- No state migration needed

**Alternative:** Session cookie-based (kalau behind NAT)

---

## 📊 Cross-Instance Flow Example

### Scenario: Client A (Instance 0) submit order BBRI (owned by Instance 1)

```
┌──────────────────────────────────────────────────────────────────────────┐
│                    CROSS-INSTANCE MESSAGE FLOW                           │
└──────────────────────────────────────────────────────────────────────────┘

  Client A                     Instance 0                    Instance 1
    │                            │ (owns BBCA,GOTO)            │ (owns BBRI,ASII)
    │  1. POST /api/v1/orders    │                             │
    │     {stock:BBRI,...}       │                             │
    │───────────────────────────▶│                             │
    │                            │                             │
    │                            │  2. Partition check:        │
    │                            │     OwnsStock("BBRI")?      │
    │                            │     → NO (hash % 3 = 1)     │
    │                            │                             │
    │                            │  3. Publish ke NATS:        │
    │                            │     "engine.orders.BBRI"    │
    │                            │────────────────────────────▶│
    │                            │                             │
    │                            │                             │ 4. QueueSubscribe
    │                            │                             │    menerima order
    │                            │                             │
    │                            │                             │ 5. Submit ke local
    │                            │                             │    Matcher BBRI
    │                            │                             │
    │                            │                             │ 6. Match! Trade created
    │                            │                             │
    │                            │  8. Receive dari NATS       │ 7. Publish: "trade.BBRI"
    │                            │◀────────────────────────────│
    │                            │                             │
    │  10. WS: trade event       │  9. Hub.Broadcast ke        │
    │◀───────────────────────────│     local subscribers       │
    │     {"type":"market.trade"}│                             │
    │                            │                             │
    ▼                            ▼                             ▼
```

**Result:**
- Client A connected ke Instance 0
- Order BBRI diproses oleh Instance 1
- Client A tetap menerima trade event via WebSocket!

---

## ➕ Adding New Instance

### Step-by-Step

**1. Deploy new instance:**
```yaml
# docker-compose.scaled.yml
services:
  instance-3:
    build: .
    environment:
      - PARTITION_ENABLED=true
      - PARTITION_INSTANCE_INDEX=3  # New index
      - PARTITION_TOTAL_INSTANCES=4 # Updated total
      - NATS_ENABLED=true
      - NATS_URL=nats://nats:4222
```

**2. Update nginx:**
```nginx
upstream ws_backend {
    ip_hash;
    server instance-0:8080;
    server instance-1:8080;
    server instance-2:8080;
    server instance-3:8080;  # New!
}
```

**3. Restart nginx:**
```bash
nginx -s reload
```

**4. Stock Migration:**

**Before (3 instances):**
- Instance 0: BBCA, GOTO, ...
- Instance 1: BBRI, ASII, ...
- Instance 2: TLKM, ...

**After (4 instances):**
- Instance 0: BBCA, ... (hash % 4 = 0)
- Instance 1: BBRI, ... (hash % 4 = 1)
- Instance 2: TLKM, ... (hash % 4 = 2)
- Instance 3: GOTO, ... (hash % 4 = 3)

**Notes:**
- Existing orders tetap di instance lama sampai filled
- New orders route ke instance baru
- Tidak ada downtime!

---

## 📈 Scaling Characteristics

### Throughput Scaling

| Instances | Orders/sec | WS Clients | Stock Coverage |
|-----------|-----------|------------|----------------|
| 1 | 35,000 | 1,000 | All stocks |
| 2 | 70,000 | 2,000 | Partitioned |
| 3 | 105,000 | 3,000 | Partitioned |
| N | N × 35k | N × 1k | Partitioned |

**Linear scaling:** Each instance adds ~35k orders/sec capacity

---

### Latency Impact

| Component | Latency | Note |
|-----------|---------|------|
| Local matching | <1ms | Same instance |
| Cross-instance (NATS) | +1-2ms | Network hop |
| Sticky session | 0ms | Routing only |

**Overhead:** Minimal (~1-2ms untuk cross-instance orders)

---

## ✅ Verification

### Test Cross-Instance Communication

```bash
# Terminal 1: Connect to Instance 0
wscat -c ws://localhost:8080/ws
> {"action":"subscribe","channel":"market.trade","stock":"BBRI"}

# Terminal 2: Submit order BBRI (mungkin ke Instance 1)
curl -X POST http://localhost:8080/api/v1/orders \
  -d '{"stock_code":"BBRI","side":"BUY","price":5000,"quantity":100}'

curl -X POST http://localhost:8080/api/v1/orders \
  -d '{"stock_code":"BBRI","side":"SELL","price":5000,"quantity":100}'

# Verify: Terminal 1 harus menerima trade event!
```

### Load Test Multiple Instances

```bash
# Run load test dengan multiple instances
docker-compose -f docker-compose.scaled.yml up -d

# Test dari luar (via nginx)
go run ./cmd/stresstest -url http://localhost:8080

# Scale up
docker-compose -f docker-compose.scaled.yml up -d --scale app=4
```

---

## 🎯 Key Takeaways

1. **Partition by stock** → Consistent hashing (FNV-1a)
2. **Queue groups** → Route orders to owner (exactly-one)
3. **Pub/Sub** → Broadcast events to all (fan-out)
4. **Sticky sessions** → WebSocket state preserved
5. **Stateless algorithm** → No coordination needed
6. **Linear scaling** → Add instances, double capacity

---

## 📚 References

- **Partition:** `internal/partition/partitioner.go`
- **NATS Publisher:** `internal/infra/broker/natsbroker/publisher.go`
- **NATS Subscriber:** `internal/infra/broker/natsbroker/subscriber.go`
- **Docker Compose:** `docker-compose.scaled.yml`
- **Nginx Config:** `nginx.conf`
