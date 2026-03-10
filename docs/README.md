# Mini Exchange - Documentation Index

Dokumentasi lengkap untuk Mini Exchange - Realtime Trading System.

## 📚 Daftar Dokumen

### 1. Getting Started
- **[Cara Menjalankan Project](01-cara-menjalankan.md)** - Quick start guide untuk menjalankan project secara lokal dan dengan Docker

### 2. Architecture & Design
- **[Design Arsitektur](02-design-arsitektur.md)** - Penjelasan singkat arsitektur sistem (Clean Architecture)
- **[System Flow](03-system-flow.md)** - Alur data dari order submission sampai broadcast
- **[Project Structure](04-project-structure.md)** - Struktur folder dan file

### 3. Technical Deep Dive
- **[Assumptions](05-assumptions.md)** - Asumsi yang digunakan dalam desain
- **[Race Condition Prevention](06-race-condition.md)** - Penjelasan cara mencegah race condition
- **[Broadcast Strategy](07-broadcast-strategy.md)** - Strategi broadcast non-blocking
- **[Bottlenecks](08-bottlenecks.md)** - Tiga bottleneck utama dan cara mengatasinya

### 4. API Documentation
- **[API Documentation](09-api-documentation.md)** - List endpoint REST API dan cara penggunaan

### 5. WebSocket Documentation
- **[WebSocket Documentation](10-websocket-documentation.md)** - Cara connect, subscribe, format message, dan testing tools

### 6. Horizontal Scaling
- **[Horizontal Scaling](11-horizontal-scaling.md)** - Cara kerja horizontal scaling dengan NATS

### 7. Bug Fixes & Improvements
- **[Graceful Shutdown Fix](12-graceful-shutdown-fix.md)** - Fix race condition saat shutdown untuk mencegah data loss

---

## 🎯 Quick Reference

### Requirements yang ter-cover:

| Requirement | Document |
|------------|----------|
| **2a. Cara menjalankan project** | [01-cara-menjalankan.md](01-cara-menjalankan.md) |
| **2b. Design arsitektur (singkat)** | [02-design-arsitektur.md](02-design-arsitektur.md) |
| **2c. Flow system** | [03-system-flow.md](03-system-flow.md) |
| **2d. Assumption** | [05-assumptions.md](05-assumptions.md) |
| **2e. Race condition** | [06-race-condition.md](06-race-condition.md) |
| **2f. Broadcast strategy** | [07-broadcast-strategy.md](07-broadcast-strategy.md) |
| **2g. Tiga bottleneck** | [08-bottlenecks.md](08-bottlenecks.md) |
| **3. API Documentation** | [09-api-documentation.md](09-api-documentation.md) |
| **4. WebSocket Documentation** | [10-websocket-documentation.md](10-websocket-documentation.md) |
| **1b. Struktur project** | [04-project-structure.md](04-project-structure.md) |

---

## 🚀 Start Here

Baru pertama kali? Mulai dari:
1. [Cara Menjalankan Project](01-cara-menjalankan.md) - Setup dan run
2. [Design Arsitektur](02-design-arsitektur.md) - Understand the big picture
3. [System Flow](03-system-flow.md) - How data flows through the system

---

**Semua dokumentasi ini menjawab 100% requirement ✅**
