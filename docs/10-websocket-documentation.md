# 10. WebSocket Documentation

Dokumentasi lengkap untuk WebSocket real-time streaming.

---

## 🔌 Connection

### Endpoint

```
ws://localhost:8080/ws
```

### Authenticated Connection (Optional)

```
ws://localhost:8080/ws?token=<jwt-token>
```

**Note:** Authentication required untuk channel `order.update`.

---

## 📤 Client → Server Messages

### 1. Subscribe

Subscribe ke channel untuk menerima updates.

**Format:**
```json
{
  "action": "subscribe",
  "channel": "market.ticker",
  "stock": "BBCA"
}
```

**Parameters:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action` | string | Yes | "subscribe" |
| `channel` | string | Yes | Channel name |
| `stock` | string | Yes* | Stock code (*kecuali order.update) |

**Available Channels:**

| Channel | Description | Requires Stock |
|---------|-------------|----------------|
| `market.ticker` | Price updates | Yes |
| `market.trade` | Trade executions | Yes |
| `market.orderbook` | Order book snapshots | Yes |
| `order.update` | Your order status changes | No |

**Examples:**

```json
// Subscribe to ticker
{"action":"subscribe","channel":"market.ticker","stock":"BBCA"}

// Subscribe to trades
{"action":"subscribe","channel":"market.trade","stock":"BBCA"}

// Subscribe to order book
{"action":"subscribe","channel":"market.orderbook","stock":"BBCA"}

// Subscribe to your order updates (authenticated)
{"action":"subscribe","channel":"order.update","stock":""}
```

---

### 2. Unsubscribe

Berhenti menerima updates dari channel.

**Format:**
```json
{
  "action": "unsubscribe",
  "channel": "market.ticker",
  "stock": "BBCA"
}
```

**Example:**
```json
{"action":"unsubscribe","channel":"market.ticker","stock":"BBCA"}
```

---

### 3. Ping

Keepalive ping untuk menjaga connection.

**Format:**
```json
{"action":"ping"}
```

**Response:**
```json
{"type":"pong"}
```

---

## 📥 Server → Client Messages

### 1. Subscribed

Konfirmasi subscription berhasil.

**Format:**
```json
{
  "type": "subscribed",
  "channel": "market.ticker",
  "stock": "BBCA"
}
```

---

### 2. Unsubscribed

Konfirmasi unsubscription berhasil.

**Format:**
```json
{
  "type": "unsubscribed",
  "channel": "market.ticker",
  "stock": "BBCA"
}
```

---

### 3. Market Ticker

Price update untuk suatu saham.

**Trigger:** Price berubah (dari trade atau simulator)

**Format:**
```json
{
  "type": "market.ticker",
  "stock": "BBCA",
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

**Frequency:** Setiap ada perubahan (dari simulator setiap 1-3 detik)

---

### 4. Market Trade

Notifikasi trade yang baru terjadi.

**Trigger:** Order match berhasil

**Format:**
```json
{
  "type": "market.trade",
  "stock": "BBCA",
  "data": {
    "id": "trade-uuid",
    "stock_code": "BBCA",
    "price": 9500,
    "quantity": 100,
    "buy_order_id": "buy-order-uuid",
    "sell_order_id": "sell-order-uuid",
    "executed_at": "2024-01-01T12:01:00Z"
  }
}
```

**Frequency:** Real-time saat match terjadi

---

### 5. Market Order Book

Snapshot order book terbaru.

**Trigger:** Order book berubah (order baru, match, cancel)

**Format:**
```json
{
  "type": "market.orderbook",
  "stock": "BBCA",
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
    "timestamp": "2024-01-01T12:00:00Z"
  }
}
```

**Note:** Bids sorted DESC, asks sorted ASC.

---

### 6. Order Update

Status order Anda berubah.

**Trigger:** Order filled (partial atau full)

**Format:**
```json
{
  "type": "order.update",
  "data": {
    "order_id": "550e8400-e29b-41d4-a716-446655440000",
    "stock_code": "BBCA",
    "side": "BUY",
    "price": 9500,
    "quantity": 100,
    "filled_quantity": 100,
    "status": "FILLED",
    "updated_at": "2024-01-01T12:01:00Z"
  }
}
```

**Requires:** Authentication (JWT token)

---

### 7. Pong

Response dari ping.

**Format:**
```json
{"type":"pong"}
```

---

### 8. Error

Protocol error.

**Format:**
```json
{
  "type": "error",
  "message": "Unknown action: foobar"
}
```

**Common Errors:**
- `"unknown action"` - Action tidak valid
- `"invalid message format"` - JSON invalid
- `"subscription limit reached"` - Max 20 subscriptions
- `"authentication required"` - Perlu login untuk order.update

---

## ⚠️ Limits

| Limit | Value | Description |
|-------|-------|-------------|
| **Max connections** | 1000 per instance | Global limit |
| **Max connections per IP** | 10 | Per-IP limit |
| **Max subscriptions per client** | 20 | Channel + stock combinations |
| **Client send buffer** | 256 messages | Per-client buffer |
| **Slow client behavior** | Disconnect | Kick if buffer full |

---

## 🧪 Testing Tools

### 1. wscat (Recommended)

**Install:**
```bash
npm install -g wscat
```

**Basic Usage:**
```bash
# Connect
wscat -c ws://localhost:8080/ws

# Subscribe commands:
> {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}
> {"action":"subscribe","channel":"market.trade","stock":"BBCA"}
> {"action":"subscribe","channel":"market.orderbook","stock":"BBCA"}

# Unsubscribe:
> {"action":"unsubscribe","channel":"market.ticker","stock":"BBCA"}

# Keepalive:
> {"action":"ping"}

# Exit:
Ctrl+C
```

**Full Test Flow:**
```bash
# Terminal 1: WebSocket client
wscat -c ws://localhost:8080/ws

# Subscribe
> {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}
> {"action":"subscribe","channel":"market.trade","stock":"BBCA"}

# Wait for events...
```

```bash
# Terminal 2: Trigger trade via REST
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"stock_code":"BBCA","side":"BUY","price":9500,"quantity":100,"type":"LIMIT"}'

curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"stock_code":"BBCA","side":"SELL","price":9500,"quantity":100,"type":"LIMIT"}'
```

**Expected Result in Terminal 1:**
```json
{"type":"subscribed","channel":"market.ticker","stock":"BBCA"}
{"type":"subscribed","channel":"market.trade","stock":"BBCA"}
{"type":"market.ticker","stock":"BBCA","data":{"last_price":9500,...}}
{"type":"market.trade","stock":"BBCA","data":{"price":9500,"quantity":100,...}}
```

---

### 2. websocat

**Install:** https://github.com/vi/websocat/releases

**Basic Usage:**
```bash
# Interactive mode
websocat ws://localhost:8080/ws

# One-liner: Subscribe and print
echo '{"action":"subscribe","channel":"market.trade","stock":"BBCA"}' \
  | websocat -n1 ws://localhost:8080/ws

# Pipe to jq for pretty printing
echo '{"action":"subscribe","channel":"market.ticker","stock":"BBCA"}' \
  | websocat ws://localhost:8080/ws \
  | jq .
```

---

### 3. Postman

**Steps:**
1. Open Postman
2. Click **New** → **WebSocket Request**
3. Enter URL: `ws://localhost:8080/ws`
4. Click **Connect**
5. In **Message** tab:
   - Select **Raw** / **JSON**
   - Type message: `{"action":"subscribe","channel":"market.ticker","stock":"BBCA"}`
   - Click **Send**
6. Incoming messages appear in **Messages** panel

**Screenshot Guide:**
```
┌─────────────────────────────────────────┐
│  WebSocket Request                      │
│                                         │
│  URL: ws://localhost:8080/ws    [Connect]
│                                         │
│  ┌─────────────────────────────────┐   │
│  │ Messages  |  Message            │   │
│  ├─────────────────────────────────┤   │
│  │ ◀ {"type":"subscribed",...}     │   │
│  │ ◀ {"type":"market.ticker",...}  │   │
│  │                                 │   │
│  └─────────────────────────────────┘   │
│                                         │
│  [Raw ▼] [JSON ▼]                      │
│  ┌─────────────────────────────────┐   │
│  │ {"action":"subscribe",...}      │   │
│  └─────────────────────────────────┘   │
│  [Send]                                │
└─────────────────────────────────────────┘
```

---

### 4. Browser DevTools (No Install)

**Console:**
```javascript
// Open DevTools (F12) on any page
const ws = new WebSocket('ws://localhost:8080/ws');

// Connection opened
ws.onopen = () => {
  console.log('Connected!');
  
  // Subscribe
  ws.send(JSON.stringify({
    action: 'subscribe',
    channel: 'market.ticker',
    stock: 'BBCA'
  }));
};

// Receive messages
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  console.log('Received:', msg);
};

// Connection closed
ws.onclose = () => {
  console.log('Disconnected');
};

// Error
ws.onerror = (error) => {
  console.error('Error:', error);
};
```

**HTML Page Example:**
```html
<!DOCTYPE html>
<html>
<head>
  <title>WebSocket Test</title>
</head>
<body>
  <div id="output"></div>
  
  <script>
    const ws = new WebSocket('ws://localhost:8080/ws');
    const output = document.getElementById('output');
    
    ws.onopen = () => {
      output.innerHTML += '<p>Connected!</p>';
      ws.send(JSON.stringify({
        action: 'subscribe',
        channel: 'market.trade',
        stock: 'BBCA'
      }));
    };
    
    ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      output.innerHTML += `<pre>${JSON.stringify(msg, null, 2)}</pre>`;
    };
    
    ws.onclose = () => {
      output.innerHTML += '<p>Disconnected</p>';
    };
  </script>
</body>
</html>
```

---

### 5. Python (websocket-client)

**Install:**
```bash
pip install websocket-client
```

**Script:**
```python
import websocket
import json

def on_message(ws, message):
    data = json.loads(message)
    print(f"Received: {json.dumps(data, indent=2)}")

def on_open(ws):
    print("Connected!")
    
    # Subscribe
    ws.send(json.dumps({
        "action": "subscribe",
        "channel": "market.ticker",
        "stock": "BBCA"
    }))

ws = websocket.WebSocketApp("ws://localhost:8080/ws",
                            on_open=on_open,
                            on_message=on_message)

ws.run_forever()
```

---

## 📊 Message Flow Example

### Complete Trading Flow

```
┌────────────────────────────────────────────────────────────────┐
│                    WEBSOCKET MESSAGE FLOW                      │
└────────────────────────────────────────────────────────────────┘

Step 1: Connect
─────────────────────────────────────────────────────────────────
  Client ──WebSocket──▶ Server
  ws://localhost:8080/ws
                           │
                           ▼
                    Hub.register
                    Client created
                    Goroutines started

Step 2: Subscribe
─────────────────────────────────────────────────────────────────
  Client ──{"action":"subscribe","channel":"market.trade","stock":"BBCA"}──▶ Server
                                                                             │
                                                                             ▼
                                                                      Add to subs map
                                                                      Send response:
  Client ◀──{"type":"subscribed","channel":"market.trade","stock":"BBCA"}── Server

Step 3: Trigger Trade (via REST API)
─────────────────────────────────────────────────────────────────
  Client ──POST /api/v1/orders (BUY)──▶ Server
  Client ──POST /api/v1/orders (SELL)──▶ Server
                                              │
                                              ▼
                                       Matching Engine
                                       Trade executed
                                       Events published

Step 4: Receive Trade Event
─────────────────────────────────────────────────────────────────
                                            │
                                            ▼
                                      Hub.Broadcast
                                      Lookup subscribers
                                            │
  Client ◀──{"type":"market.trade",...}── Server
                                              │
                                              └──▶ Other subscribers

Step 5: Unsubscribe
─────────────────────────────────────────────────────────────────
  Client ──{"action":"unsubscribe","channel":"market.trade","stock":"BBCA"}──▶ Server
                                                                                  │
                                                                                  ▼
                                                                           Remove from subs
                                                                           Send response:
  Client ◀──{"type":"unsubscribed","channel":"market.trade","stock":"BBCA"}── Server

Step 6: Disconnect
─────────────────────────────────────────────────────────────────
  Client ──Close──▶ Server
                         │
                         ▼
                  Hub.unregister
                  Cleanup resources
                  Goroutines exit
```

---

## 📚 References

- **Protocol:** `internal/transport/ws/message.go`
- **Hub:** `internal/transport/ws/hub.go`
- **Client:** `internal/transport/ws/client.go`
- **Handler:** `internal/transport/ws/handler.go`
- **Tests:** `internal/transport/ws/hub_test.go`
