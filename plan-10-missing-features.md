# Plan 10 — Missing & Incomplete Features

> Audit codebase vs `rule.md` requirements + plan-1 s/d plan-9.
> Tujuan: daftar fitur yang **belum diimplementasi** atau **belum lengkap** di code.

---

## Status Legend

| Icon | Arti |
|------|------|
| ✅ | Sudah implementasi & tested |
| ⚠️ | Partial / ada catatan |
| ❌ | Belum implementasi |

---

## 1. REST API

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| POST /orders | Wajib | ✅ | `handler/order.go:38`, `app/order/service.go:39` |
| GET /orders (filter stock, status) | Wajib | ✅ | `handler/order.go:65` |
| GET /orders/{id} | - | ✅ | `handler/order.go:102` |
| GET /trades | Wajib | ✅ | `handler/trade.go:24` |
| GET /market/ticker | Wajib | ✅ | `handler/market.go:24` |
| GET /market/ticker/{stock} | Wajib | ✅ | `handler/market.go:34` |
| GET /market/orderbook/{stock} | Wajib | ✅ | `handler/market.go:49` |
| GET /market/trades/{stock} | Opsional | ✅ | `handler/market.go:65` |
| **Cancel Order (DELETE/PATCH /orders/{id})** | Implied | ❌ | **BELUM ADA** — status `CANCELLED` defined di entity tapi tidak ada code path yang men-set nya |

### Detail: Cancel Order

**Apa yang sudah ada:**
- `entity/order.go:25` — `OrderStatusCancelled = "CANCELLED"` (status defined)
- `entity/order.go:31` — `IsValid()` recognize CANCELLED
- `engine/orderbook.go:62` — comment "removeOrder deletes a fully-filled or **cancelled** order"

**Apa yang belum ada:**
- ❌ `Cancel()` method di entity Order
- ❌ `CancelOrder()` method di `app/order/service.go`
- ❌ Handler `Cancel` di `handler/order.go`
- ❌ Route `DELETE /api/v1/orders/{id}` di `router.go`
- ❌ Engine method untuk remove order dari order book by ID
- ❌ Event `OrderCancelled` di domain event
- ❌ WebSocket broadcast ketika order di-cancel
- ❌ Unit test untuk cancel flow

**Implementasi yang diperlukan:**

```
Layer          | File                          | Yang perlu ditambah
---------------|-------------------------------|------------------------------------
Entity         | domain/entity/order.go        | Cancel() method
Repository     | domain/repository/order.go    | (sudah ada Save, bisa reuse)
Service        | app/order/service.go          | CancelOrder(ctx, orderID, userID)
Engine         | engine/engine.go              | CancelOrder(stockCode, orderID)
Engine         | engine/matcher.go             | handleCancel() — remove dari book
Handler        | handler/order.go              | Cancel(w, r)
Router         | router.go                     | DELETE /api/v1/orders/{id}
Event          | domain/event/event.go         | OrderCancelled event type
Test           | handler/order_test.go         | TestOrderHandler_Cancel_*
Test           | engine/matcher_test.go         | TestMatcher_CancelOrder_*
```

**Flow:**
```
DELETE /api/v1/orders/{id}
  → Handler: validate order exists, milik user, status OPEN/PARTIAL
  → Service: CancelOrder()
    → Engine: remove from order book
    → Repository: update status → CANCELLED
    → EventBus: publish OrderCancelled
    → WebSocket: broadcast order.update ke subscriber
```

---

## 2. Matching Engine

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| Simple matching (BUY ↔ SELL) | Wajib | ✅ | `engine/matcher.go:86-101` |
| Create trade on match | Wajib | ✅ | `engine/matcher.go:158-210` |
| Update order status | Wajib | ✅ | `engine/matcher.go:176` via `order.Fill(qty)` |
| Partial fill | Bonus | ✅ | Tested di `matcher_test.go:115-156` |
| FIFO matching | Bonus | ✅ | `sort.SliceStable` di `orderbook.go:50` |
| Race condition safe | Wajib | ✅ | Single goroutine per stock |

**Matching engine complete.** Tidak ada fitur missing.

---

## 3. WebSocket

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| Subscribe market.ticker | Wajib | ✅ | `ws/client.go:106-116` |
| Subscribe market.trade | Wajib | ✅ | Same mechanism |
| Subscribe market.orderbook | Bonus | ✅ | Same mechanism |
| Subscribe order.update | Bonus | ✅ | Uses userID as key `ws/client.go:110-112` |
| Unsubscribe | Wajib | ✅ | `ws/client.go:118-126` |
| Non-blocking broadcast | Wajib | ✅ | `ws/hub.go:131-143` select/default |
| Multiple client handling | Wajib | ✅ | Connection limits, per-IP limits |
| Slow client disconnect | Wajib | ✅ | `ws/hub.go:139-141` |

**WebSocket complete.** Tidak ada fitur missing.

---

## 4. Realtime Price Simulation

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| Harga berubah periodik | Wajib | ✅ | `simulator/price.go:161-198` |
| Harga berubah berdasarkan trade | Wajib | ✅ | `simulator/price.go:170-182` |
| Generate event realtime | Wajib | ✅ | Via EventBus |
| Broadcast ke subscriber | Wajib | ✅ | `simulator/price.go:153-158` |

**Simulator complete.** Tidak ada fitur missing.

---

## 5. Concurrency & Performance

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| Goroutine usage | Wajib | ✅ | Per-stock engine, WS hub, read/writePump |
| Channel usage | Wajib | ✅ | Engine channels, hub channels, eventbus |
| Tidak race condition | Wajib | ✅ | Single goroutine ownership, RWMutex |
| Tidak blocking | Wajib | ✅ | Non-blocking publish dengan select/default |

**Concurrency complete.** Tidak ada fitur missing.

---

## 6. Data Storage

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| In-memory | Wajib | ✅ | `infra/storage/memory/` — order, trade, market repos |
| Redis | Bonus | ⚠️ | Cache: ✅ wired. PubSub: ✅ code ada tapi **TIDAK wired di main.go** |
| Database (PostgreSQL) | Bonus | ✅ | `infra/storage/postgres/` + DB Worker batching |

### Detail: Redis PubSub Not Wired

**Apa yang sudah ada:**
- `infra/storage/redis/cache.go` — ✅ Wired di `main.go`
- `infra/storage/redis/pubsub.go` — ✅ Code lengkap

**Apa yang belum ada:**
- ❌ `main.go` tidak membuat instance `redis.PubSub`
- ❌ Tidak ada wiring Redis PubSub ke WebSocket Hub atau EventBus
- NATS dipakai sebagai pengganti untuk cross-instance fan-out, jadi ini bukan blocker

**Impact:** Rendah. NATS sudah handle cross-instance broadcast. Redis PubSub code ada tapi jadi dead code.

---

## 7. Documentation (README.md)

| Requirement | rule.md | Status | Catatan |
|-------------|---------|--------|---------|
| Cara menjalankan project | Wajib | ✅ | Quick Start section |
| Design arsitektur | Wajib | ✅ | Architecture section + diagram |
| Flow system | Wajib | ✅ | Order Matching, WS Subscription, Price Simulation |
| Assumption yang digunakan | Wajib | ✅ | Assumptions section |
| Penjelasan potensi race condition | Wajib | ✅ | Race Condition Prevention table |
| Strategi broadcast non blocking | Wajib | ✅ | Dedicated section |
| Tiga bottleneck utama | Wajib | ✅ | Three Main Bottlenecks section |
| API Documentation (endpoints + usage) | Wajib | ✅ | Full API docs + curl examples |
| WS Documentation (connect, subscribe, format) | Wajib | ✅ | Complete |
| WS testing tools recommendation | Wajib | ✅ | wscat, websocat, Postman, Bruno, Browser |

**Documentation complete.** Tidak ada yang missing.

---

## 8. Bonus Points (Nice to Have)

| Fitur | rule.md | Status | Catatan |
|-------|---------|--------|---------|
| Message broker (NATS) | Bonus | ✅ | `infra/broker/natsbroker/` publisher + subscriber |
| Redis pub/sub | Bonus | ⚠️ | Code ada, TIDAK wired (lihat section 6) |
| Rate limiting | Bonus | ✅ | `middleware/ratelimit.go` token bucket per IP |
| Authentication (JWT) | Bonus | ✅ | `infra/auth/jwt.go` + `middleware/auth.go` + `handler/auth.go` |
| Logging & monitoring | Bonus | ✅ | slog logging + Prometheus metrics |
| Unit test | Bonus | ✅ | 17+ test files across all layers |

---

## 9. Infrastructure & Misc

| Item | Status | Catatan |
|------|--------|---------|
| Docker | ✅ | `Dockerfile` + `docker-compose.yml` |
| Makefile | ✅ | Build, test, run, docker commands |
| Migrations | ✅ | `migrations/001-003` SQL files |
| Graceful shutdown | ✅ | `main.go` SIGINT/SIGTERM handling |
| Health check | ✅ | `GET /healthz` |
| Prometheus metrics | ✅ | `GET /metrics` |
| **Empty `infra/broker/nats/` directory** | ⚠️ | Leftover directory, actual code di `natsbroker/` |

---

## Ringkasan: Fitur Yang Perlu Dibuat

### Priority: HIGH (Fitur yang implied/expected)

| # | Fitur | Effort | Keterangan |
|---|-------|--------|------------|
| 1 | **Cancel Order** (full flow) | Medium | Entity method, service, engine remove, handler, router, event, WS broadcast, tests |

### Priority: LOW (Nice to have / cleanup)

| # | Fitur | Effort | Keterangan |
|---|-------|--------|------------|
| 2 | Wire Redis PubSub di main.go (atau hapus dead code) | Low | Code sudah ada tapi tidak di-wired; NATS sudah handle |
| 3 | Hapus empty `infra/broker/nats/` directory | Trivial | Leftover directory |

---

## Kesimpulan

Dari seluruh **rule.md requirements**:
- **Wajib**: 100% implemented ✅
- **Bonus**: 100% implemented ✅ (Redis PubSub partial tapi NATS sudah cover)
- **Documentation**: 100% complete ✅

**Satu-satunya fitur yang belum di-implementasi**: `Cancel Order` — status CANCELLED sudah defined di entity tapi tidak ada code path (endpoint, service, engine) yang menggunakan nya.

Secara keseluruhan codebase sudah sangat complete terhadap rule.md.
