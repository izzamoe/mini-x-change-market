# Plan 11 — Horizontal Scaling: Claims vs Reality

> Audit `ARCHITECTURE.md` Section 9 ("HORIZONTAL SCALING STRATEGY — IMPLEMENTED & DOCUMENTED")
> terhadap kode yang benar-benar ada di repo.
>
> **Tujuan:** membedakan secara jujur antara apa yang sudah berjalan, apa yang hanya
> dead-code, dan apa yang masih strategi di atas kertas.

---

## Status Legend

| Icon | Arti |
|------|------|
| ✅ | Benar-benar ada di code dan di-wire saat runtime |
| ⚠️ | Code ada tapi TIDAK di-wire / tidak aktif |
| 📄 | Hanya ada di dokumentasi / diagram, tidak ada kode |
| ❌ | Sama sekali belum ada |

---

## 1. Audit Per-Komponen

### 1.1 REST API Scaling

| Klaim (ARCHITECTURE.md §9.1) | Status | Bukti |
|------------------------------|--------|-------|
| Stateless — JWT self-contained, no server-side session | ✅ | `infra/auth/jwt.go` — token di-verify tanpa DB lookup, tidak ada session store |
| Load balancer round-robin | 📄 | Tidak ada nginx config, tidak ada `replicas` di `docker-compose.yml` |

**Kesimpulan:** Sisi aplikasi sudah stateless, tapi infrastruktur load balancer **tidak ada**.

---

### 1.2 Matching Engine Scaling (Consistent Hashing)

| Klaim (ARCHITECTURE.md §9.2) | Status | Bukti |
|------------------------------|--------|-------|
| Partition by stock code via consistent hashing | 📄 | Tidak ada consistent-hash library di `go.mod`, tidak ada routing logic |
| NATS routing order ke instance yang handle stock X | 📄 | `natsbroker/publisher.go` publish ke `trade.*`, `ticker.*`, `orderbook.*` — ini **broadcast**, bukan routing |
| Single writer per stock across instances | 📄 | Single goroutine per stock hanya berlaku **dalam 1 instance**; tidak ada cross-instance lock/partition |
| Failover: jika instance mati, NATS re-routes | 📄 | Tidak ada subscriber queue group, tidak ada health-based re-routing |

**Kesimpulan:** Matching engine scaling adalah **strategi murni di atas kertas**. Kode saat ini
menjalankan semua stock di semua instance tanpa partitioning. Jika dua instance hidup secara
bersamaan, **kedua matching engine akan proses order yang sama** — race condition antar instance.

---

### 1.3 WebSocket Scaling

| Klaim (ARCHITECTURE.md §9.3) | Status | Bukti |
|------------------------------|--------|-------|
| Sticky sessions di load balancer | 📄 | Tidak ada nginx config; tidak ada `ip_hash` / cookie-based stickiness |
| Cross-instance broadcast via Redis Pub/Sub | ⚠️ | `infra/storage/redis/pubsub.go` — code lengkap, tapi **tidak di-wire** di `main.go` |
| Cross-instance broadcast via NATS Pub/Sub | ✅ | `main.go:136-155` — `wireNATSSubscriber` di-wire; events di-publish ke `trade.*`, `ticker.*`, `orderbook.*`, `order.*` |

**Detail Redis PubSub:**
```
File: internal/infra/storage/redis/pubsub.go   ← code komplit (58 baris)
main.go                                         ← TIDAK ada NewPubSub(), tidak ada Subscribe()
```

**Detail NATS PubSub (benar-benar aktif):**
```
Publisher:  main.go:343-344  → natsPub.Publish("trade."+e.StockCode, ...)
            main.go:357      → natsPub.Publish("ticker."+e.StockCode, ...)
            main.go:372      → natsPub.Publish("orderbook."+e.StockCode, ...)
            main.go:400      → natsPub.Publish("order."+o.UserID, ...)

Subscriber: main.go:419-474 → wireNATSSubscriber() — decode & forward ke hub
```

**Kesimpulan:** Cross-instance WS broadcast via NATS **benar-benar jalan**. Redis PubSub adalah
dead code — aman untuk dihapus atau di-wire sebagai alternatif.

---

### 1.4 Storage Scaling

| Klaim (ARCHITECTURE.md §9.4) | Status | Bukti |
|------------------------------|--------|-------|
| PostgreSQL shared across instances | ✅ | Connection via `DATABASE_URL` env; semua instance bisa connect ke DB yang sama |
| Redis shared cache | ✅ | `redistore.NewCache()` di `main.go:160`; key-space bersifat global |
| DB Worker per instance (async batch persist) | ✅ | `worker/dbworker.go` — batching, 5000-buffer channel, flush 100ms |
| PostgreSQL read replicas | 📄 | Tidak ada `pgxpool` dengan read/write separation; tidak ada replica URL config |

**Kesimpulan:** Storage per-instance sudah correct. Read replicas adalah ide, bukan implementasi.

---

### 1.5 Concurrency Guarantees Across Instances

| Klaim (ARCHITECTURE.md §9.5) | Status | Bukti |
|------------------------------|--------|-------|
| Hanya 1 instance proses orders per stock | 📄 | Lihat §1.2 — tidak ada partitioning |
| WS eventual consistency via Redis/NATS | ✅ | NATS benar-benar aktif (lihat §1.3) |
| UUID untuk uniqueness across instances | ✅ | `entity/order.go` — `uuid.New().String()` |
| Trade hanya dari matching engine owner | 📄 | Tidak ada ownership enforcement; semua instance punya engine untuk semua stock |

---

## 2. Ringkasan Gap

### Yang Benar-Benar Implemented & Running

| Komponen | File / Lokasi |
|----------|---------------|
| Stateless JWT auth | `infra/auth/jwt.go` |
| NATS publisher (cross-instance event fan-out) | `main.go:333-405` `wireEvents()` |
| NATS subscriber (cross-instance WS broadcast) | `main.go:408-475` `wireNATSSubscriber()` |
| Redis cache-aside (ticker + orderbook) | `main.go:187-190`, `redistore.Cache` |
| DB Worker async batch persist | `worker/dbworker.go` |
| UUID uniqueness per order | `domain/entity/order.go` |
| PostgreSQL shareable across instances | `infra/storage/postgres/` |

### Yang Ada Sebagai Dead Code (⚠️)

| Komponen | File | Issue |
|----------|------|-------|
| Redis PubSub | `infra/storage/redis/pubsub.go` | Tidak di-wire di `main.go`; NATS sudah cover |

### Yang Hanya Ada di Dokumentasi / Diagram (📄)

| Komponen | Catatan |
|----------|---------|
| Nginx / Load Balancer | Tidak ada `nginx.conf`, `docker-compose.yml` hanya 1 `app` service tanpa `replicas` |
| Consistent hashing untuk matching engine | Tidak ada library, tidak ada routing logic |
| NATS queue groups (per-stock ownership) | Publisher broadcast, bukan point-to-point routing |
| Sticky sessions WebSocket | Membutuhkan nginx `ip_hash` atau cookie-based LB config |
| PostgreSQL read replicas | Tidak ada `READ_REPLICA_URL`, tidak ada pgx read/write split |
| Multi-instance docker-compose | Hanya ada 1 `app` service, tidak ada `replicas: N` |

---

## 3. Risiko: Menjalankan 2 Instance Sekarang

Jika `docker-compose up --scale app=2` dijalankan **sekarang**:

| Masalah | Dampak |
|---------|--------|
| Kedua instance menjalankan semua matching engines | **Race condition** — order yang sama bisa di-match 2x, trade duplikat |
| Tidak ada sticky session | WebSocket client bisa terputus ketika request berikutnya di-route ke instance lain |
| NATS subscriber mendengar events dari kedua instance | WS client menerima event duplikat (2x trade, 2x ticker) |
| DB Worker di tiap instance menulis ke PostgreSQL bersamaan | Potential duplicate order/trade rows |

**Kesimpulan: Saat ini aplikasi belum aman untuk di-scale horizontal.**

---

## 4. Apa Yang Perlu Dilakukan Untuk Benar-Benar Horizontal Scalable

### Step 1 — Matching Engine Partitioning (Critical)

**Problem:** Semua instance menjalankan matching engine untuk semua stock.

**Solusi:**
```
A. Consistent hash: tentukan instance mana yang "owns" stock X
   - Library: rendezvous hashing atau jump consistent hash
   - Config: INSTANCE_ID env var + total instances count

B. Order routing via NATS:
   - Sebelum submit ke engine, resolve owning instance
   - Publish ke NATS subject "engine.<stockCode>.order"
   - Hanya instance yang "own" stock tersebut yang subscribe & process

C. NATS Queue Groups:
   - subscriber.QueueSubscribe("engine.BBCA.order", "engine-BBCA", handler)
   - Garantees hanya 1 instance menerima tiap order
```

**File yang perlu diubah:**
- `engine/engine.go` — tambah `OwnedStocks(instanceID, totalInstances) []string`
- `main.go` — kondisional start engine hanya untuk owned stocks
- `natsbroker/subscriber.go` — tambah `QueueSubscribe(subject, queue, handler)`
- `app/order/service.go` — route order ke engine owner via NATS jika tidak local

---

### Step 2 — Nginx + Sticky Sessions (untuk WebSocket)

**Problem:** WebSocket memerlukan persistent connection ke instance yang sama.

**Solusi `nginx.conf`:**
```nginx
upstream app_servers {
    ip_hash;                    # sticky: same client IP → same instance
    server app1:8080;
    server app2:8080;
    server app3:8080;
}

server {
    listen 80;
    location /ws {
        proxy_pass http://app_servers;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
    location / {
        proxy_pass http://app_servers;
    }
}
```

**docker-compose tambahan:**
```yaml
services:
  nginx:
    image: nginx:alpine
    ports:
      - "80:80"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
    depends_on: [app]

  app:
    deploy:
      replicas: 3
    ports: []   # remove direct port, traffic goes through nginx
```

---

### Step 3 — Wire Redis PubSub atau Hapus Dead Code

**Opsi A (hapus dead code):** NATS sudah handle cross-instance broadcast, Redis PubSub tidak
diperlukan. Hapus `infra/storage/redis/pubsub.go`.

**Opsi B (aktifkan sebagai alternative):** Wire di `main.go` sebagai fallback ketika NATS tidak
tersedia:
```go
// main.go — setelah Redis cache init
var redisPubSub *redistore.PubSub
if cfg.Redis.Enabled && !cfg.NATS.Enabled {
    ps, err := redistore.NewPubSub(cfg.Redis.URL)
    if err == nil {
        redisPubSub = ps
        wireRedisPubSubSubscriber(redisPubSub, hub)
        slog.Info("redis pubsub subscriber wired (fallback)")
    }
}
```

---

### Step 4 — Fix NATS Subscriber Deduplication

**Problem:** Saat ini setiap instance subscribe ke `trade.*`, `ticker.*` dan juga publish ke
subject yang sama. Sehingga instance A publish trade event → NATS fan-out → instance A dan B
keduanya subscribe → instance A akan broadcast ke hub-nya **2x** (sekali dari local eventbus,
sekali dari NATS subscriber).

**Solusi:** Di `wireNATSSubscriber`, hanya forward event yang **berasal dari instance lain**:
```go
// Option 1: add INSTANCE_ID header to NATS message; skip if same instance
// Option 2: use NATS Queue Groups — NATS delivers to only 1 member of group
//           (not applicable here since we WANT all instances to receive for WS broadcast)
// Option 3 (current workaround): accept the duplicate, hub deduplicates
```

Sebenarnya untuk WS broadcast ke klien, duplikasi lebih baik daripada missed events.
Ini bukan bug kritis tapi perlu didokumentasikan.

---

## 5. Prioritas Implementasi

| # | Item | Effort | Needed For Scale? |
|---|------|--------|-------------------|
| 1 | NATS Queue Groups untuk matching engine partitioning | High | **CRITICAL** — tanpa ini, multi-instance = corrupt data |
| 2 | nginx.conf + sticky sessions config | Medium | High — WebSocket tidak stable tanpa sticky |
| 3 | docker-compose replicas + nginx service | Low | High — deploy support |
| 4 | Hapus/wire Redis PubSub dead code | Trivial | Low — cleanup only |
| 5 | PostgreSQL read replica config | Medium | Low — optimisation only |

---

## 6. Kesimpulan

`ARCHITECTURE.md` Section 9 mengklaim **"IMPLEMENTED & DOCUMENTED"** tapi realitanya:

- **"IMPLEMENTED"** hanya berlaku untuk: NATS pub/sub fan-out, Redis cache-aside, stateless JWT
- **"DOCUMENTED"** (a.k.a. belum kode) berlaku untuk: nginx, consistent hashing, sticky sessions, multi-instance docker-compose, read replicas

Aplikasi ini **scalable secara vertikal** (1 instance bisa handle ribuan koneksi), tapi belum
**safe untuk di-scale horizontal** karena matching engine belum dipartisi.

Untuk production horizontal scaling, prioritas utama adalah **Step 1 (matching engine
partitioning via NATS Queue Groups)** karena tanpa itu, data integrity tidak terjamin.
