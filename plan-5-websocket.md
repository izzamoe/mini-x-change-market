# Plan 5: WebSocket Server
# Estimated: ~60 minutes
# Dependencies: Plan 1 (domain), Plan 2 (event bus), Plan 4 (HTTP router)

## Objective
Implement the WebSocket server with Hub pattern, per-client goroutines, channel-based subscriptions, non-blocking broadcast, and slow client handling. This enables realtime data streaming to clients.

---

## Tasks

### 5.1 WebSocket Message Types (internal/transport/ws/message.go)
```go
// Client → Server
type ClientMessage struct {
    Action  string `json:"action"`   // "subscribe" | "unsubscribe" | "ping"
    Channel string `json:"channel"`  // "market.ticker" | "market.trade" | "market.orderbook" | "order.update"
    Stock   string `json:"stock"`    // "BBCA" (optional for order.update)
}

// Server → Client
type ServerMessage struct {
    Type    string      `json:"type"`    // channel name or "subscribed"/"unsubscribed"/"pong"/"error"
    Stock   string      `json:"stock,omitempty"`
    Data    interface{} `json:"data,omitempty"`
    Message string      `json:"message,omitempty"` // for errors
}

// Subscription key
type Subscription struct {
    Channel string // "market.ticker"
    Stock   string // "BBCA"
}
```

### 5.2 WebSocket Client (internal/transport/ws/client.go)
- Represents a single WebSocket connection
- Two goroutines per client: readPump + writePump
- Fields:
  - `conn *websocket.Conn`
  - `hub *Hub`
  - `send chan []byte` (buffered, 256 cap)
  - `subscriptions map[Subscription]bool`
  - `userID string` (optional, from JWT)
  - `ip string`
- **readPump goroutine**:
  - Reads messages from WS connection
  - Parses ClientMessage
  - Handles: subscribe, unsubscribe, ping
  - On subscribe: register subscription in hub
  - On disconnect: unregister from hub
  - Enforces max message size (4096 bytes)
  - Enforces pong deadline (60s)
- **writePump goroutine**:
  - Reads from `send` channel
  - Writes to WS connection
  - Sends ping every 54s
  - Enforces write deadline (10s)
  - Exits when send channel closed

### 5.3 WebSocket Hub (internal/transport/ws/hub.go)
- Central coordinator for all WS clients
- Single goroutine manages all state (NO mutex needed)
- Channels:
  - `register chan *Client`
  - `unregister chan *Client`
  - `subscribe chan subscribeRequest`
  - `unsubscribe chan unsubscribeRequest`
  - `broadcast chan broadcastMessage`
- Internal state:
  - `clients map[*Client]bool` — all connected clients
  - `subscriptions map[Subscription]map[*Client]bool` — channel→clients mapping
  - `clientCount int` — total connections
  - `ipCount map[string]int` — connections per IP
- **Run goroutine** (main loop):
  ```go
  for {
      select {
      case client := <-h.register:
          // Check connection limits (max total, max per IP)
          // If ok: add to clients map, increment counters
          // If exceeded: close client connection with error

      case client := <-h.unregister:
          // Remove from all subscriptions
          // Remove from clients map
          // Close send channel
          // Decrement counters

      case req := <-h.subscribe:
          // Check max subscriptions per client (20)
          // Add client to subscription map
          // Send confirmation message

      case req := <-h.unsubscribe:
          // Remove client from subscription map
          // Send confirmation message

      case msg := <-h.broadcast:
          // Find all clients subscribed to msg.Subscription
          // For each client: non-blocking send
          //   select {
          //     case client.send <- data: // ok
          //     default: // slow client, kick
          //       h.unregister <- client
          //   }

      case <-ctx.Done():
          // Shutdown: close all client connections
          return
      }
  }
  ```

### 5.4 WebSocket Handler (internal/transport/ws/handler.go)
- HTTP upgrade handler using `coder/websocket`
- Accepts WS connection at `/ws`
- Optional: Extract JWT from query param `?token=xxx`
- Creates Client, registers with Hub, starts read/write pumps
```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
        InsecureSkipVerify: true, // Allow all origins in dev
    })
    // Extract user ID from JWT token (optional)
    // Create Client
    // Register with Hub
    // Start readPump and writePump in goroutines
}
```

### 5.5 Event Bus → Hub Integration
- Subscribe to relevant events from EventBus
- Transform domain events to WS messages
- Push to Hub's broadcast channel
```go
// In server startup:
eventBus.Subscribe(event.TradeExecuted, func(e event.Event) {
    trade := e.Payload.(*entity.Trade)
    hub.Broadcast("market.trade", trade.StockCode, trade)
})

eventBus.Subscribe(event.TickerUpdated, func(e event.Event) {
    ticker := e.Payload.(*entity.Ticker)
    hub.Broadcast("market.ticker", ticker.StockCode, ticker)
})

eventBus.Subscribe(event.OrderBookUpdated, func(e event.Event) {
    book := e.Payload.(*entity.OrderBook)
    hub.Broadcast("market.orderbook", book.StockCode, book)
})

eventBus.Subscribe(event.OrderUpdated, func(e event.Event) {
    order := e.Payload.(*entity.Order)
    hub.BroadcastToUser("order.update", order.UserID, order)
})
```

### 5.6 Non-Blocking Broadcast Verification
- Broadcast MUST NOT block even if:
  - Client is slow (send channel full → kick client)
  - No clients subscribed (no-op)
  - 500 clients connected (iterate only subscribed clients)
- Use `select/default` pattern for every send

### 5.7 Unit Tests
- **ws/hub_test.go**:
  - Test register/unregister clients
  - Test subscribe/unsubscribe channels
  - Test broadcast reaches only subscribed clients
  - Test slow client gets disconnected
  - Test connection limits (max total, max per IP)
  - Test max subscriptions per client
- **ws/client_test.go**:
  - Test message parsing
  - Test ping/pong keepalive
- **ws/handler_test.go**:
  - Test WS upgrade
  - Test subscribe flow end-to-end

---

## Files Created
```
internal/transport/ws/message.go
internal/transport/ws/client.go
internal/transport/ws/hub.go
internal/transport/ws/handler.go
internal/transport/ws/hub_test.go
internal/transport/ws/client_test.go
```

## Verification
```bash
go test ./internal/transport/ws/... -v -race
# Manual test with wscat:
# npx wscat -c ws://localhost:8080/ws
# > {"action":"subscribe","channel":"market.ticker","stock":"BBCA"}
# < {"type":"subscribed","channel":"market.ticker","stock":"BBCA"}
# < {"type":"market.ticker","stock":"BBCA","data":{...}}
```

## Exit Criteria
- WebSocket connections work with subscribe/unsubscribe
- Broadcast reaches only subscribed clients
- Slow clients are automatically disconnected (non-blocking)
- Connection and subscription limits are enforced
- Ping/pong keepalive works
- Tests pass with `-race`
