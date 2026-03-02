package ws

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestServer spins up an httptest.Server backed by a live Hub and returns
// the server, the Hub and a cancel func to stop the Hub's Run goroutine.
func startTestServer(t *testing.T) (*httptest.Server, *Hub, context.CancelFunc) {
	t.Helper()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	handler := NewHandler(hub)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, hub, cancel
}

// wsURL converts an http:// URL to a ws:// URL.
func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

// dialWS dials the test server and registers cleanup.
func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), wsURL(srv.URL), nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close(websocket.StatusNormalClosure, "test done")
	})
	return conn
}

// sendMsg encodes msg as JSON and writes it to conn.
func sendMsg(t *testing.T, conn *websocket.Conn, msg ClientMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, data))
}

// recvMsg reads one JSON ServerMessage from conn with a 2 s timeout.
func recvMsg(t *testing.T, conn *websocket.Conn) ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var msg ServerMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestHandler_UpgradeAndSubscribe(t *testing.T) {
	srv, _, cancel := startTestServer(t)
	defer cancel()

	conn := dialWS(t, srv)

	sendMsg(t, conn, ClientMessage{
		Action:  "subscribe",
		Channel: "market.ticker",
		Stock:   "BBCA",
	})

	ack := recvMsg(t, conn)
	assert.Equal(t, "subscribed", ack.Type)
	assert.Equal(t, "market.ticker", ack.Channel)
	assert.Equal(t, "BBCA", ack.Stock)
}

func TestHandler_Ping(t *testing.T) {
	srv, _, cancel := startTestServer(t)
	defer cancel()

	conn := dialWS(t, srv)

	sendMsg(t, conn, ClientMessage{Action: "ping"})

	resp := recvMsg(t, conn)
	assert.Equal(t, "pong", resp.Type)
}

func TestHandler_UnknownAction(t *testing.T) {
	srv, _, cancel := startTestServer(t)
	defer cancel()

	conn := dialWS(t, srv)

	sendMsg(t, conn, ClientMessage{Action: "foobar"})

	resp := recvMsg(t, conn)
	assert.Equal(t, "error", resp.Type)
	assert.Contains(t, resp.Message, "unknown action")
}

func TestHandler_InvalidJSON(t *testing.T) {
	srv, _, cancel := startTestServer(t)
	defer cancel()

	conn := dialWS(t, srv)

	ctx, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{bad json`)))

	resp := recvMsg(t, conn)
	assert.Equal(t, "error", resp.Type)
}

func TestHandler_SubscribeAndReceiveBroadcast(t *testing.T) {
	srv, hub, cancel := startTestServer(t)
	defer cancel()

	conn := dialWS(t, srv)

	sendMsg(t, conn, ClientMessage{
		Action:  "subscribe",
		Channel: "market.ticker",
		Stock:   "TLKM",
	})
	ack := recvMsg(t, conn)
	require.Equal(t, "subscribed", ack.Type)

	// Broadcast from the server side.
	hub.Broadcast("market.ticker", "TLKM", map[string]interface{}{"last_price": 3800})

	pushed := recvMsg(t, conn)
	assert.Equal(t, "market.ticker", pushed.Type)
	assert.Equal(t, "TLKM", pushed.Stock)
	assert.NotNil(t, pushed.Data)
}

func TestHandler_Unsubscribe(t *testing.T) {
	srv, hub, cancel := startTestServer(t)
	defer cancel()

	conn := dialWS(t, srv)

	sendMsg(t, conn, ClientMessage{Action: "subscribe", Channel: "market.ticker", Stock: "ASII"})
	recvMsg(t, conn) // sub ack

	sendMsg(t, conn, ClientMessage{Action: "unsubscribe", Channel: "market.ticker", Stock: "ASII"})
	ack := recvMsg(t, conn)
	assert.Equal(t, "unsubscribed", ack.Type)

	// Broadcast must NOT reach the unsubscribed client.
	hub.Broadcast("market.ticker", "ASII", map[string]interface{}{"last_price": 6000})

	ctx, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	_, _, err := conn.Read(ctx)
	// expect a timeout / deadline exceeded error — not a data frame.
	assert.Error(t, err, "should not receive broadcast after unsubscribe")
}
