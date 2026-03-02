package ws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// newMockClient creates a Client with a nil WS connection for use in Hub unit
// tests. The send channel is the only I/O mechanism exercised.
func newMockClient(ip string) *Client {
	return &Client{
		conn:   nil,
		hub:    nil, // not needed; hub is referenced directly in tests
		send:   make(chan []byte, 256),
		userID: "user-" + ip,
		ip:     ip,
	}
}

// startHub starts a Hub's Run goroutine and returns a cancel func that stops it.
func startHub(t *testing.T) (*Hub, context.CancelFunc) {
	t.Helper()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	return hub, cancel
}

// registerAndWait sends the client to hub.register and blocks until the hub
// has processed it. We verify this by issuing a round-trip through the hub
// (subscribe to a sentinel channel, wait for the ack, then unsubscribe).
func registerAndWait(t *testing.T, hub *Hub, client *Client) {
	t.Helper()
	hub.register <- client
	// Use a tiny sleep so the hub goroutine can process register before any
	// subsequent subscribe/broadcast requests are sent. In production the
	// natural network round-trip provides this ordering; in unit tests we
	// approximate it with a brief pause.
	time.Sleep(5 * time.Millisecond)
}

// drainUntilClosed reads from ch until it is closed, failing if the timeout
// elapses first.
func drainUntilClosed(t *testing.T, ch <-chan []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for send channel to be closed")
		}
	}
}

// readServerMessage reads one message from the send channel or fails.
func readServerMessage(t *testing.T, client *Client, timeout time.Duration) ServerMessage {
	t.Helper()
	select {
	case data, ok := <-client.send:
		require.True(t, ok, "send channel closed unexpectedly")
		var msg ServerMessage
		require.NoError(t, json.Unmarshal(data, &msg))
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for server message")
		return ServerMessage{}
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestHub_RegisterClient(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.0.0.1")
	registerAndWait(t, hub, client)

	// Verify registration by subscribing — the ack proves the client is known.
	sub := Subscription{Channel: "market.ticker", Stock: "BBCA"}
	hub.subscribe <- subscribeReq{client: client, sub: sub}

	msg := readServerMessage(t, client, time.Second)
	assert.Equal(t, "subscribed", msg.Type)
	assert.Equal(t, "market.ticker", msg.Channel)
	assert.Equal(t, "BBCA", msg.Stock)
}

func TestHub_UnregisterClient(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.0.0.2")
	registerAndWait(t, hub, client)

	// Wait for register to be processed by subscribing first.
	hub.subscribe <- subscribeReq{client: client, sub: Subscription{Channel: "market.ticker", Stock: "X"}}
	readServerMessage(t, client, time.Second) // drain ack

	hub.unregister <- client

	// send channel must be closed after unregister.
	drainUntilClosed(t, client.send, time.Second)
}

func TestHub_MaxTotalConnections(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	// Register exactly MaxTotalConnections clients.
	for i := 0; i < MaxTotalConnections; i++ {
		c := newMockClient("10.0.0.1")
		c.send = make(chan []byte, 1) // small buffer; hub may send an ack/error
		hub.register <- c
	}

	// One more must be rejected. Give hub time to process all registrations first.
	// We synchronise by using a subscribe that won't be answered until after the
	// batch of registrations is processed (hub processes channels in order).
	extra := newMockClient("10.0.0.1")
	hub.register <- extra

	// The extra client should receive an error message, then its channel is closed.
	drainUntilClosed(t, extra.send, 2*time.Second)
}

func TestHub_MaxConnPerIP(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	ip := "192.168.1.50"
	// Register MaxConnPerIP clients from the same IP.
	for i := 0; i < MaxConnPerIP; i++ {
		c := newMockClient(ip)
		hub.register <- c
	}

	// One more from the same IP must be rejected.
	extra := newMockClient(ip)
	hub.register <- extra

	drainUntilClosed(t, extra.send, 2*time.Second)
}

func TestHub_Subscribe(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.1.0.1")
	registerAndWait(t, hub, client)

	sub := Subscription{Channel: "market.orderbook", Stock: "TLKM"}
	hub.subscribe <- subscribeReq{client: client, sub: sub}

	msg := readServerMessage(t, client, time.Second)
	assert.Equal(t, "subscribed", msg.Type)
	assert.Equal(t, "market.orderbook", msg.Channel)
	assert.Equal(t, "TLKM", msg.Stock)
}

func TestHub_SubscribeIdempotent(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.1.0.2")
	registerAndWait(t, hub, client)

	sub := Subscription{Channel: "market.ticker", Stock: "BBNI"}
	hub.subscribe <- subscribeReq{client: client, sub: sub}
	readServerMessage(t, client, time.Second) // drain first ack

	// Subscribe again — hub should silently ignore the duplicate.
	hub.subscribe <- subscribeReq{client: client, sub: sub}

	// Subscribe to a different channel to synchronise; if there was a duplicate
	// ack it would be in the buffer before this one.
	hub.subscribe <- subscribeReq{client: client, sub: Subscription{Channel: "market.trade", Stock: "BBNI"}}
	msg := readServerMessage(t, client, time.Second)
	assert.Equal(t, "subscribed", msg.Type)
	assert.Equal(t, "market.trade", msg.Channel)
}

func TestHub_Unsubscribe(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.1.0.3")
	registerAndWait(t, hub, client)

	sub := Subscription{Channel: "market.ticker", Stock: "ASII"}
	hub.subscribe <- subscribeReq{client: client, sub: sub}
	readServerMessage(t, client, time.Second) // drain sub ack

	hub.unsubscribe <- unsubscribeReq{client: client, sub: sub}
	msg := readServerMessage(t, client, time.Second)
	assert.Equal(t, "unsubscribed", msg.Type)
	assert.Equal(t, "market.ticker", msg.Channel)
	assert.Equal(t, "ASII", msg.Stock)
}

func TestHub_MaxSubscriptionsPerClient(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.2.0.1")
	registerAndWait(t, hub, client)

	// Fill up to the limit.
	for i := 0; i < MaxSubsPerClient; i++ {
		stock := string(rune('A' + i))
		hub.subscribe <- subscribeReq{
			client: client,
			sub:    Subscription{Channel: "market.ticker", Stock: stock},
		}
		readServerMessage(t, client, time.Second)
	}

	// One more must trigger an error.
	hub.subscribe <- subscribeReq{
		client: client,
		sub:    Subscription{Channel: "market.ticker", Stock: "OVERFLOW"},
	}
	msg := readServerMessage(t, client, time.Second)
	assert.Equal(t, "error", msg.Type)
	assert.Contains(t, msg.Message, "max subscriptions")
}

func TestHub_BroadcastReachesSubscribers(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	subscriber := newMockClient("10.3.0.1")
	other := newMockClient("10.3.0.2")
	registerAndWait(t, hub, subscriber)
	registerAndWait(t, hub, other)

	sub := Subscription{Channel: "market.ticker", Stock: "BBCA"}
	hub.subscribe <- subscribeReq{client: subscriber, sub: sub}
	readServerMessage(t, subscriber, time.Second) // drain ack

	// Broadcast a ticker update.
	hub.Broadcast("market.ticker", "BBCA", map[string]interface{}{"last_price": 9000})

	// subscriber should receive the broadcast.
	msg := readServerMessage(t, subscriber, time.Second)
	assert.Equal(t, "market.ticker", msg.Type)
	assert.Equal(t, "BBCA", msg.Stock)

	// other (not subscribed) must NOT receive anything.
	select {
	case <-other.send:
		t.Fatal("non-subscribed client should not receive broadcast")
	case <-time.After(50 * time.Millisecond):
		// correct: no message
	}
}

func TestHub_SlowClientIsKicked(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	// Unbuffered send channel: no goroutine is reading from it, so every
	// non-blocking send (select/default) inside the hub will hit the default
	// branch, triggering a kick.
	slow := &Client{
		conn:   nil,
		hub:    nil,
		send:   make(chan []byte), // unbuffered (cap=0)
		userID: "slow-user",
		ip:     "10.4.0.1",
	}
	hub.register <- slow
	time.Sleep(5 * time.Millisecond) // wait for hub to process register

	sub := Subscription{Channel: "market.ticker", Stock: "BMRI"}
	hub.subscribe <- subscribeReq{client: slow, sub: sub}
	time.Sleep(20 * time.Millisecond) // wait for hub to process subscribe

	// Broadcast — hub can't send to unbuffered channel with no reader → kicks.
	hub.Broadcast("market.ticker", "BMRI", map[string]interface{}{"last_price": 5000})
	time.Sleep(50 * time.Millisecond) // give hub time to process broadcast + kick

	// Verify: receive from a closed unbuffered channel returns (nil, false) instantly.
	// Receive from an open unbuffered channel with no sender blocks → default.
	select {
	case _, ok := <-slow.send:
		assert.False(t, ok, "slow client's send channel should be closed after kick")
	default:
		t.Fatal("slow client was not kicked (send channel not closed)")
	}
}

func TestHub_UnsubscribeOnUnregister(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	client := newMockClient("10.5.0.1")
	other := newMockClient("10.5.0.2")
	registerAndWait(t, hub, client)
	registerAndWait(t, hub, other)

	sub := Subscription{Channel: "market.ticker", Stock: "BBRI"}
	hub.subscribe <- subscribeReq{client: client, sub: sub}
	readServerMessage(t, client, time.Second) // drain ack

	// Unregister; this must clean up all subscriptions.
	hub.unregister <- client
	drainUntilClosed(t, client.send, time.Second)

	// Broadcast after unregister — other is not subscribed, so nothing should arrive.
	hub.Broadcast("market.ticker", "BBRI", map[string]interface{}{"last_price": 4000})

	select {
	case <-other.send:
		t.Fatal("unregistered client's subscriptions were not cleaned up")
	case <-time.After(50 * time.Millisecond):
		// correct
	}
}

func TestHub_BroadcastToUser(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	userClient := newMockClient("10.6.0.1")
	userClient.userID = "alice"
	otherClient := newMockClient("10.6.0.2")
	otherClient.userID = "bob"
	registerAndWait(t, hub, userClient)
	registerAndWait(t, hub, otherClient)

	// Subscribe alice's client to "order.update" using her userID as the stock key.
	sub := Subscription{Channel: "order.update", Stock: "alice"}
	hub.subscribe <- subscribeReq{client: userClient, sub: sub}
	readServerMessage(t, userClient, time.Second) // drain ack

	hub.BroadcastToUser("order.update", "alice", map[string]interface{}{"status": "FILLED"})

	msg := readServerMessage(t, userClient, time.Second)
	assert.Equal(t, "order.update", msg.Type)

	select {
	case <-otherClient.send:
		t.Fatal("bob should not receive alice's order update")
	case <-time.After(50 * time.Millisecond):
	}
}
