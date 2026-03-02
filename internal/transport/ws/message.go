// Package ws implements the WebSocket server with Hub pattern,
// per-client goroutines, channel-based subscriptions, and non-blocking broadcast.
package ws

// ClientMessage is a message sent from the client to the server.
type ClientMessage struct {
	Action  string `json:"action"`  // "subscribe" | "unsubscribe" | "ping"
	Channel string `json:"channel"` // "market.ticker" | "market.trade" | "market.orderbook" | "order.update"
	Stock   string `json:"stock"`   // stock code; empty for "order.update" (server uses userID)
}

// ServerMessage is a message sent from the server to the client.
type ServerMessage struct {
	Type    string      `json:"type"`              // channel name, "subscribed", "unsubscribed", "pong", or "error"
	Channel string      `json:"channel,omitempty"` // echo on subscribed/unsubscribed ack
	Stock   string      `json:"stock,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Message string      `json:"message,omitempty"` // human-readable detail for errors
}

// Subscription is the composite key that uniquely identifies a (channel, stock) pair.
// For "order.update", Stock holds the userID so the standard subscription map can be reused.
type Subscription struct {
	Channel string
	Stock   string
}
