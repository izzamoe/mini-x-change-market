package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

const (
	writeWait  = 10 * time.Second // max time to write a message to the WS connection
	pongWait   = 60 * time.Second // max time to wait for the next read (acts as pong timeout)
	pingPeriod = 54 * time.Second // how often writePump sends a ping (must be < pongWait)
	maxMsgSize = 4096             // max bytes per client message
)

// Client represents a single WebSocket connection.
// Two goroutines are launched per client: readPump and writePump.
type Client struct {
	conn   *websocket.Conn
	hub    *Hub
	send   chan []byte // buffered outbound message queue; closed by Hub on disconnect
	userID string      // set from JWT (or "anonymous")
	ip     string      // remote address used for per-IP rate limiting
}

// NewClient creates a Client and registers it with the hub.
func NewClient(conn *websocket.Conn, hub *Hub, userID, ip string) *Client {
	return &Client{
		conn:   conn,
		hub:    hub,
		send:   make(chan []byte, 256),
		userID: userID,
		ip:     ip,
	}
}

// readPump reads inbound messages from the WebSocket connection.
// It runs in its own goroutine and drives subscription management.
// On return it triggers hub unregister so the Hub cleans up and closes send.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
	}()

	c.conn.SetReadLimit(maxMsgSize)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), pongWait)
		_, data, err := c.conn.Read(ctx)
		cancel()
		if err != nil {
			// Connection closed or timed out — exit cleanly.
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.hub.SendJSONSafe(c, ServerMessage{Type: "error", Message: "invalid JSON"})
			continue
		}
		c.handleMessage(msg)
	}
}

// writePump drains the send channel and forwards messages to the WebSocket
// connection. It also sends periodic pings to keep the connection alive.
// It runs in its own goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				// Hub closed the channel — perform WS close handshake and exit.
				_ = c.conn.Close(websocket.StatusNormalClosure, "server closed connection")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), writeWait)
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				slog.Debug("ws write error", "ip", c.ip, "err", err)
				c.conn.CloseNow() // force-close TCP so the client detects disconnection
				return
			}

		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), writeWait)
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				slog.Debug("ws ping failed", "ip", c.ip, "err", err)
				c.conn.CloseNow() // force-close TCP so the client detects disconnection
				return
			}
		}
	}
}

// handleMessage dispatches a parsed ClientMessage to the appropriate Hub operation.
func (c *Client) handleMessage(msg ClientMessage) {
	switch msg.Action {
	case "subscribe":
		stock := msg.Stock
		// For "order.update" we subscribe the client under their own userID so
		// BroadcastToUser can fan out using the same subscription map.
		if msg.Channel == "order.update" {
			stock = c.userID
		}
		c.hub.subscribe <- subscribeReq{
			client: c,
			sub:    Subscription{Channel: msg.Channel, Stock: stock},
		}

	case "unsubscribe":
		stock := msg.Stock
		if msg.Channel == "order.update" {
			stock = c.userID
		}
		c.hub.unsubscribe <- unsubscribeReq{
			client: c,
			sub:    Subscription{Channel: msg.Channel, Stock: stock},
		}

	case "ping":
		c.hub.SendJSONSafe(c, ServerMessage{Type: "pong"})

	default:
		c.hub.SendJSONSafe(c, ServerMessage{
			Type:    "error",
			Message: "unknown action: " + msg.Action,
		})
	}
}
