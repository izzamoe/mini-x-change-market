package ws

import (
	"context"
	"encoding/json"
	"log/slog"
)

// Connection limits enforced by the Hub.
const (
	MaxTotalConnections = 1000
	MaxConnPerIP        = 10
	MaxSubsPerClient    = 20
)

// subscribeReq is sent from a client's readPump to the Hub to add a subscription.
type subscribeReq struct {
	client *Client
	sub    Subscription
}

// unsubscribeReq is sent from a client's readPump to the Hub to remove a subscription.
type unsubscribeReq struct {
	client *Client
	sub    Subscription
}

// broadcastMsg carries a serialised JSON payload destined for all clients
// subscribed to the given Subscription.
type broadcastMsg struct {
	sub  Subscription
	data []byte
}

// Hub is the central coordinator for all WebSocket clients.
// A single goroutine (Run) owns all internal state — no mutexes needed.
type Hub struct {
	// inbound control channels
	register    chan *Client
	unregister  chan *Client
	subscribe   chan subscribeReq
	unsubscribe chan unsubscribeReq
	broadcast   chan broadcastMsg

	// state — ONLY accessed inside Run goroutine
	clients    map[*Client]bool                  // all connected clients
	subs       map[Subscription]map[*Client]bool // subscription → subscriber set
	clientSubs map[*Client]map[Subscription]bool // client → subscription set (fast lookup)
	ipCount    map[string]int                    // connections per IP address
}

// NewHub allocates and initialises a Hub ready to be started with Run.
func NewHub() *Hub {
	return &Hub{
		register:    make(chan *Client, 64),
		unregister:  make(chan *Client, 64),
		subscribe:   make(chan subscribeReq, 64),
		unsubscribe: make(chan unsubscribeReq, 64),
		broadcast:   make(chan broadcastMsg, 256),

		clients:    make(map[*Client]bool),
		subs:       make(map[Subscription]map[*Client]bool),
		clientSubs: make(map[*Client]map[Subscription]bool),
		ipCount:    make(map[string]int),
	}
}

// Run is the single goroutine that serialises all Hub state mutations.
// It runs until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			if len(h.clients) >= MaxTotalConnections {
				h.sendJSON(client, ServerMessage{Type: "error", Message: "max connections reached"})
				close(client.send)
				continue
			}
			if h.ipCount[client.ip] >= MaxConnPerIP {
				h.sendJSON(client, ServerMessage{Type: "error", Message: "max connections per IP reached"})
				close(client.send)
				continue
			}
			h.clients[client] = true
			h.clientSubs[client] = make(map[Subscription]bool)
			h.ipCount[client.ip]++
			slog.Info("ws client registered", "ip", client.ip, "total", len(h.clients))

		case client := <-h.unregister:
			h.removeClient(client)

		case req := <-h.subscribe:
			client := req.client
			if !h.clients[client] {
				continue
			}
			if len(h.clientSubs[client]) >= MaxSubsPerClient {
				h.sendJSON(client, ServerMessage{Type: "error", Message: "max subscriptions reached"})
				continue
			}
			sub := req.sub
			if h.clientSubs[client][sub] {
				continue // already subscribed — idempotent
			}
			if h.subs[sub] == nil {
				h.subs[sub] = make(map[*Client]bool)
			}
			h.subs[sub][client] = true
			h.clientSubs[client][sub] = true
			h.sendJSON(client, ServerMessage{
				Type:    "subscribed",
				Channel: sub.Channel,
				Stock:   sub.Stock,
			})
			slog.Debug("ws client subscribed", "ip", client.ip, "channel", sub.Channel, "stock", sub.Stock)

		case req := <-h.unsubscribe:
			client := req.client
			if !h.clients[client] {
				continue
			}
			sub := req.sub
			delete(h.subs[sub], client)
			delete(h.clientSubs[client], sub)
			h.sendJSON(client, ServerMessage{
				Type:    "unsubscribed",
				Channel: sub.Channel,
				Stock:   sub.Stock,
			})

		case msg := <-h.broadcast:
			// Iterate only the clients subscribed to this exact subscription.
			// Non-blocking send: a slow client is kicked immediately.
			// It is safe to delete from h.subs[msg.sub] while ranging over it
			// because removeClient deletes the *client* key, not the outer key.
			for client := range h.subs[msg.sub] {
				select {
				case client.send <- msg.data:
				default:
					slog.Warn("ws slow client kicked", "ip", client.ip)
					h.removeClient(client)
				}
			}

		case <-ctx.Done():
			for client := range h.clients {
				h.removeClient(client)
			}
			return
		}
	}
}

// removeClient cleans up all state for a client and closes its send channel.
// It is idempotent — calling it twice for the same client is safe.
func (h *Hub) removeClient(client *Client) {
	if !h.clients[client] {
		return
	}
	for sub := range h.clientSubs[client] {
		delete(h.subs[sub], client)
	}
	delete(h.clientSubs, client)
	delete(h.clients, client)
	h.ipCount[client.ip]--
	if h.ipCount[client.ip] <= 0 {
		delete(h.ipCount, client.ip)
	}
	close(client.send)
	slog.Info("ws client unregistered", "ip", client.ip, "total", len(h.clients))
}

// Broadcast serialises payload as JSON and fans it out to all clients subscribed
// to (channel, stock). The call is non-blocking; if the broadcast channel is
// full the message is dropped with a warning.
func (h *Hub) Broadcast(channel, stock string, payload interface{}) {
	data, err := json.Marshal(ServerMessage{
		Type:  channel,
		Stock: stock,
		Data:  payload,
	})
	if err != nil {
		slog.Error("ws broadcast marshal error", "err", err)
		return
	}
	select {
	case h.broadcast <- broadcastMsg{sub: Subscription{Channel: channel, Stock: stock}, data: data}:
	default:
		slog.Warn("ws broadcast channel full, dropping", "channel", channel, "stock", stock)
	}
}

// BroadcastToUser sends a message to all clients authenticated as userID.
// It reuses the standard subscription map by treating userID as the Stock key
// for the "order.update" channel.
func (h *Hub) BroadcastToUser(channel, userID string, payload interface{}) {
	h.Broadcast(channel, userID, payload)
}

// sendJSON marshals msg and does a non-blocking send to client.send.
func (h *Hub) sendJSON(client *Client, msg ServerMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
	}
}
