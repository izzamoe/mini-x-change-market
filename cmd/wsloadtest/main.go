// Package main — WebSocket load test for mini-exchange.
//
// Three phases run sequentially:
//
//  1. Ramp-up: connect all -clients WebSocket clients over -ramp duration.
//     Slow clients (controlled by -slow-pct) read infrequently, allowing
//     the server hub's non-blocking broadcast to accumulate and kick them.
//
//  2. Steady-state (-duration):
//     • Normal clients receive market events; messages are counted per client.
//     • Slow clients stall; the server hub kicks them when their send buffer
//     fills (hub uses non-blocking channel send → immediate kick).
//     • An order submitter places BUY+SELL pairs at -order-rate/sec,
//     generating trade events and measuring end-to-end order→WS latency.
//
//  3. Report: connection stats, slow-client kick stats, order-flow stats,
//     and a pass/fail verdict for each scenario.
//
// Usage:
//
//	go run ./cmd/wsloadtest [flags]
//
// Flags:
//
//	-url           http://localhost:8080  Exchange base URL
//	-clients       500                   Total WS clients
//	-slow-pct      10                    Percent of clients that are slow (0-100)
//	-duration      60s                   Steady-state duration
//	-ramp          3s                    Ramp-up window (clients spread evenly)
//	-order-rate    30                    Matching order pairs per second
//	-order-workers 20                    Concurrent order goroutines
//	-slow-delay    2s                    Read delay for slow clients
//	-verbose                             Print per-client connection errors
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ─── constants ────────────────────────────────────────────────────────────────

// maxConnPerIP must mirror ws.MaxConnPerIP in the hub (hub.go).
// We spread load-test clients across synthetic IPs via X-Forwarded-For so
// every client is allowed in.
const maxConnPerIP = 10

// ─── types ────────────────────────────────────────────────────────────────────

type config struct {
	baseURL      string
	numClients   int
	slowPct      int
	duration     time.Duration
	ramp         time.Duration
	orderRate    int
	orderWorkers int
	slowDelay    time.Duration
	verbose      bool
}

// serverMsg mirrors ws.ServerMessage for JSON decoding.
type serverMsg struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Stock   string          `json:"stock,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Message string          `json:"message,omitempty"`
}

// clientResult records outcomes for one WebSocket client.
type clientResult struct {
	id          int
	isSlow      bool
	connected   bool
	connLatency time.Duration
	subAcked    bool          // received at least one "subscribed" ack
	msgsRcvd    int64         // market events received (trade + ticker + orderbook)
	kicked      bool          // server closed connection unexpectedly (slow-client kick)
	kickLatency time.Duration // duration from connect to server-initiated close
	closeReason string
}

// tradeNotif is sent through a channel whenever a WS client receives a trade event.
type tradeNotif struct {
	stock      string
	receivedAt time.Time
}

// orderResult captures one order-pair attempt.
type orderResult struct {
	stock  string
	sentAt time.Time // time SELL was submitted (triggers match)
	status int
	err    error
}

// ─── stock metadata ───────────────────────────────────────────────────────────

var (
	stockList = []string{"BBCA", "BBRI", "TLKM", "ASII", "GOTO"}
	basePrice = map[string]int64{"BBCA": 9500, "BBRI": 5000, "TLKM": 3500, "ASII": 6000, "GOTO": 100}
	tickSize  = map[string]int64{"BBCA": 25, "BBRI": 25, "TLKM": 25, "ASII": 25, "GOTO": 1}
)

// ─── network helpers ──────────────────────────────────────────────────────────

// fakeIP returns a unique synthetic client IP for client index i.
// Every group of maxConnPerIP clients shares one IP, exactly filling the
// per-IP connection limit without exceeding it.
//
//	client 0-9   → 10.0.0.1
//	client 10-19 → 10.0.1.1   etc.
func fakeIP(i int) string { return fmt.Sprintf("10.0.%d.1", i/maxConnPerIP) }

// makeWSURL converts an HTTP(S) base URL to a WS(S) /ws endpoint.
func makeWSURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasPrefix(base, "https://") {
		return "wss://" + base[8:] + "/ws"
	}
	return "ws://" + strings.TrimPrefix(base, "http://") + "/ws"
}

// dialWS opens a WebSocket connection, spoofing X-Forwarded-For so the hub
// treats each logical client group as coming from a distinct IP.
func dialWS(ctx context.Context, wurl, ip string) (*websocket.Conn, error) {
	conn, _, err := websocket.Dial(ctx, wurl, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Forwarded-For": {ip}},
	})
	return conn, err
}

// subscribeWS sends a subscribe action for channel+stock.
func subscribeWS(ctx context.Context, conn *websocket.Conn, channel, stock string) error {
	return wsjson.Write(ctx, conn, map[string]string{
		"action": "subscribe", "channel": channel, "stock": stock,
	})
}

// ─── normal client ────────────────────────────────────────────────────────────

// runNormalClient connects to the WS, subscribes to market.ticker and
// market.trade for its assigned stock, then reads messages continuously until
// ctx is cancelled or the server closes the connection.
func runNormalClient(
	ctx context.Context,
	id int,
	wurl string,
	tradeCh chan<- tradeNotif,
	resultCh chan<- clientResult,
) {
	res := clientResult{id: id, isSlow: false}

	start := time.Now()
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := dialWS(dialCtx, wurl, fakeIP(id))
	dialCancel()
	if err != nil {
		res.closeReason = err.Error()
		resultCh <- res
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	res.connected = true
	res.connLatency = time.Since(start)

	// Each client subscribes to one stock (round-robin) so event load is spread.
	stock := stockList[id%len(stockList)]
	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	_ = subscribeWS(subCtx, conn, "market.ticker", stock)
	_ = subscribeWS(subCtx, conn, "market.trade", stock)
	subCancel()

	// Read loop — runs until server close or ctx cancellation.
	for {
		readCtx, cancel := context.WithTimeout(ctx, 65*time.Second)
		var msg serverMsg
		err := wsjson.Read(readCtx, conn, &msg)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				// Server unexpectedly closed — normal clients should NOT be kicked.
				res.kicked = true
				res.closeReason = err.Error()
			}
			break
		}
		switch msg.Type {
		case "subscribed":
			res.subAcked = true
		case "market.trade":
			res.msgsRcvd++
			if tradeCh != nil && msg.Stock != "" {
				select {
				case tradeCh <- tradeNotif{stock: msg.Stock, receivedAt: time.Now()}:
				default: // drop if channel full — non-blocking
				}
			}
		case "market.ticker", "market.orderbook":
			res.msgsRcvd++
		}
	}
	resultCh <- res
}

// ─── slow client ──────────────────────────────────────────────────────────────

// runSlowClient connects, subscribes to ALL stocks on ALL market channels
// (maximising incoming event pressure), then reads only once every readDelay.
//
// With sufficient event throughput the server's per-client send channel
// (buffered 256) fills faster than writePump can drain it, triggering the
// hub's non-blocking send → immediate kick path:
//
//	select {
//	case client.send <- msg.data:
//	default:
//	    slog.Warn("ws slow client kicked", …)
//	    h.removeClient(client)
//	}
func runSlowClient(
	ctx context.Context,
	id int,
	wurl string,
	readDelay time.Duration,
	resultCh chan<- clientResult,
) {
	res := clientResult{id: id, isSlow: true}

	start := time.Now()
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := dialWS(dialCtx, wurl, fakeIP(id))
	dialCancel()
	if err != nil {
		res.closeReason = err.Error()
		resultCh <- res
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	res.connected = true
	res.connLatency = time.Since(start)

	// Subscribe to every stock × every channel to maximise event volume.
	// 5 stocks × 3 channels = 15 subscriptions (< MaxSubsPerClient=20).
	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	for _, s := range stockList {
		_ = subscribeWS(subCtx, conn, "market.ticker", s)
		_ = subscribeWS(subCtx, conn, "market.trade", s)
		_ = subscribeWS(subCtx, conn, "market.orderbook", s)
	}
	subCancel()
	res.subAcked = true
	connectAt := time.Now()

	// Slow-read loop: sleep between reads so the event queue accumulates.
	for {
		select {
		case <-ctx.Done():
			resultCh <- res
			return
		case <-time.After(readDelay):
		}

		readCtx, cancel := context.WithTimeout(ctx, readDelay+5*time.Second)
		var msg serverMsg
		err := wsjson.Read(readCtx, conn, &msg)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				// Server kicked this client — expected for a slow consumer.
				res.kicked = true
				res.kickLatency = time.Since(connectAt)
				res.closeReason = err.Error()
			}
			resultCh <- res
			return
		}
		res.msgsRcvd++
	}
}

// ─── order submitter ──────────────────────────────────────────────────────────

// runOrderSubmitter fires BUY+SELL pairs at a fixed rate to generate matching
// trade events. It records the SELL submission time per stock so the WS
// trade-event listener can compute end-to-end latency.
func runOrderSubmitter(
	ctx context.Context,
	baseURL string,
	rate int,
	workers int,
	submitted *atomic.Int64,
	errors *atomic.Int64,
	lastSellTime *sync.Map, // stock → time.Time of most-recent SELL
) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: workers * 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	interval := time.Duration(float64(time.Second) / float64(max(rate, 1)))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
			stock := stockList[rand.Intn(len(stockList))]
			price := alignTick(basePrice[stock], tickSize[stock])

			sem <- struct{}{}
			wg.Add(1)
			go func(s string, p int64) {
				defer wg.Done()
				defer func() { <-sem }()

				if status, err := placeOrder(client, baseURL, s, "BUY", p, 100); err != nil || status >= 400 {
					errors.Add(1)
					return
				}
				// Record SELL time: this triggers the match and trade event.
				sellAt := time.Now()
				if status, err := placeOrder(client, baseURL, s, "SELL", p, 100); err != nil || status >= 400 {
					errors.Add(1)
					return
				}
				submitted.Add(1)
				lastSellTime.Store(s, sellAt)
			}(stock, price)
		}
	}
}

// placeOrder submits a single LIMIT order via the REST API.
func placeOrder(client *http.Client, baseURL, stock, side string, price, qty int64) (int, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"stock_code": stock,
		"side":       side,
		"price":      price,
		"quantity":   qty,
		"type":       "LIMIT",
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/orders", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// alignTick rounds price down to the nearest tick boundary.
func alignTick(price, tick int64) int64 {
	if tick <= 0 {
		return price
	}
	return (price / tick) * tick
}

// ─── stats helpers ────────────────────────────────────────────────────────────

// pctile returns the p-th percentile of a duration slice (0–100).
func pctile(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := make([]time.Duration, len(d))
	copy(s, d)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := int(math.Ceil(p/100*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

func pct(n, total int) string {
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(n)/float64(total)*100)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ─── report ───────────────────────────────────────────────────────────────────

func printReport(
	results []clientResult,
	submitted, orderErrors int64,
	tradeLatencies []time.Duration,
	testDur time.Duration,
	cfg config,
) {
	// Partition results.
	var (
		normalTotal, normalConnected, normalSubAcked, normalKicked int
		slowTotal, slowConnected, slowKicked                       int
		totalMsgs                                                  int64
		connLats, kickLats                                         []time.Duration
		normalMsgCounts                                            []int64
	)
	for _, r := range results {
		if r.connected {
			connLats = append(connLats, r.connLatency)
		}
		if r.isSlow {
			slowTotal++
			if r.connected {
				slowConnected++
			}
			if r.kicked {
				slowKicked++
				kickLats = append(kickLats, r.kickLatency)
			}
		} else {
			normalTotal++
			if r.connected {
				normalConnected++
				normalMsgCounts = append(normalMsgCounts, r.msgsRcvd)
				totalMsgs += r.msgsRcvd
			}
			if r.subAcked {
				normalSubAcked++
			}
			if r.kicked {
				normalKicked++
			}
		}
	}

	sep := strings.Repeat("─", 58)
	fmt.Printf("\n%s\n", strings.Repeat("═", 58))
	fmt.Printf("  WS LOAD TEST REPORT\n")
	fmt.Printf("%s\n", strings.Repeat("═", 58))
	fmt.Printf("  Test duration   : %v\n", testDur.Round(time.Millisecond))
	fmt.Printf("  Config          : %d clients (%d normal / %d slow), %d order-pairs/s\n",
		cfg.numClients, normalTotal, slowTotal, cfg.orderRate)
	fmt.Println()

	// ── Connections ────────────────────────────────────────────────────────
	fmt.Println(sep)
	fmt.Println("  CONNECTIONS")
	fmt.Println(sep)
	totalConnected := normalConnected + slowConnected
	fmt.Printf("  Attempted             : %d\n", cfg.numClients)
	fmt.Printf("  Connected             : %d  (%s)\n", totalConnected, pct(totalConnected, cfg.numClients))
	if len(connLats) > 0 {
		fmt.Printf("  Connect latency p50   : %v\n", pctile(connLats, 50).Round(time.Millisecond))
		fmt.Printf("  Connect latency p95   : %v\n", pctile(connLats, 95).Round(time.Millisecond))
		fmt.Printf("  Connect latency p99   : %v\n", pctile(connLats, 99).Round(time.Millisecond))
	}
	fmt.Println()

	// ── Normal clients ─────────────────────────────────────────────────────
	fmt.Println(sep)
	fmt.Println("  NORMAL CLIENTS  (continuous readers)")
	fmt.Println(sep)
	fmt.Printf("  Clients               : %d\n", normalTotal)
	fmt.Printf("  Connected             : %d  (%s)\n", normalConnected, pct(normalConnected, normalTotal))
	fmt.Printf("  Subscription acked    : %d  (%s)\n", normalSubAcked, pct(normalSubAcked, normalTotal))
	fmt.Printf("  Total msgs received   : %d\n", totalMsgs)
	if normalConnected > 0 {
		fmt.Printf("  Avg msgs / client     : %.1f\n", float64(totalMsgs)/float64(normalConnected))
	}
	if len(normalMsgCounts) > 0 {
		sort.Slice(normalMsgCounts, func(i, j int) bool { return normalMsgCounts[i] < normalMsgCounts[j] })
		fmt.Printf("  Min / Max msgs        : %d / %d\n",
			normalMsgCounts[0], normalMsgCounts[len(normalMsgCounts)-1])
	}
	fmt.Printf("  Wrongly kicked        : %d  (expected 0)\n", normalKicked)
	fmt.Println()

	// ── Slow clients ───────────────────────────────────────────────────────
	fmt.Println(sep)
	fmt.Println("  SLOW CLIENTS  (stalled readers — should be kicked)")
	fmt.Println(sep)
	fmt.Printf("  Clients               : %d\n", slowTotal)
	fmt.Printf("  Connected             : %d  (%s)\n", slowConnected, pct(slowConnected, slowTotal))
	fmt.Printf("  Kicked by server      : %d  (%s of connected)\n", slowKicked, pct(slowKicked, max(slowConnected, 1)))
	if len(kickLats) > 0 {
		fmt.Printf("  Time-to-kick p50      : %v\n", pctile(kickLats, 50).Round(time.Millisecond))
		fmt.Printf("  Time-to-kick p95      : %v\n", pctile(kickLats, 95).Round(time.Millisecond))
		fmt.Printf("  Time-to-kick max      : %v\n", pctile(kickLats, 100).Round(time.Millisecond))
	} else if slowConnected > 0 {
		fmt.Printf("  NOTE: no kicks observed — increase -duration or -order-rate\n")
	}
	fmt.Println()

	// ── Order flow ─────────────────────────────────────────────────────────
	fmt.Println(sep)
	fmt.Println("  ORDER FLOW  (BUY+SELL pairs → WS trade events)")
	fmt.Println(sep)
	fmt.Printf("  Pairs submitted       : %d\n", submitted)
	fmt.Printf("  Pair errors           : %d\n", orderErrors)
	fmt.Printf("  Trade events via WS   : %d\n", len(tradeLatencies))
	if len(tradeLatencies) > 0 {
		fmt.Printf("  Order→WS latency p50  : %v\n", pctile(tradeLatencies, 50).Round(time.Millisecond))
		fmt.Printf("  Order→WS latency p95  : %v\n", pctile(tradeLatencies, 95).Round(time.Millisecond))
		fmt.Printf("  Order→WS latency p99  : %v\n", pctile(tradeLatencies, 99).Round(time.Millisecond))
	}
	fmt.Println()

	// ── Verdict ────────────────────────────────────────────────────────────
	fmt.Println(sep)
	fmt.Println("  VERDICT")
	fmt.Println(sep)

	connRate := 0.0
	if cfg.numClients > 0 {
		connRate = float64(totalConnected) / float64(cfg.numClients) * 100
	}
	slowKickRate := 0.0
	if slowConnected > 0 {
		slowKickRate = float64(slowKicked) / float64(slowConnected) * 100
	}
	orderOKRate := 0.0
	total := submitted + orderErrors
	if total > 0 {
		orderOKRate = float64(submitted) / float64(total) * 100
	}

	verdicts := []struct {
		label  string
		pass   bool
		detail string
	}{
		{
			"WS mass connect (≥95% success)",
			connRate >= 95.0,
			fmt.Sprintf("%.1f%% of %d clients connected", connRate, cfg.numClients),
		},
		{
			"Normal clients unaffected (0 wrongly kicked)",
			normalKicked == 0,
			func() string {
				if normalKicked == 0 {
					return "no normal clients were kicked"
				}
				return fmt.Sprintf("%d normal clients wrongly kicked", normalKicked)
			}(),
		},
		{
			"Slow clients kicked by server (≥50%)",
			slowKickRate >= 50.0 || slowTotal == 0,
			func() string {
				if slowTotal == 0 {
					return "no slow clients configured (use -slow-pct)"
				}
				return fmt.Sprintf("%.1f%% of slow clients kicked (%d/%d)", slowKickRate, slowKicked, slowConnected)
			}(),
		},
		{
			"Orders processed (≥90% success)",
			orderOKRate >= 90.0 || total == 0,
			func() string {
				if total == 0 {
					return "no orders submitted"
				}
				return fmt.Sprintf("%.1f%% success (%d pairs, %d errors)", orderOKRate, submitted, orderErrors)
			}(),
		},
		{
			"Trade events received via WS",
			len(tradeLatencies) > 0 || submitted == 0,
			func() string {
				if submitted == 0 {
					return "no orders submitted"
				}
				return fmt.Sprintf("%d trade events received for %d order pairs", len(tradeLatencies), submitted)
			}(),
		},
	}

	allPass := true
	for _, v := range verdicts {
		mark := "PASS"
		if !v.pass {
			mark = "FAIL"
			allPass = false
		}
		fmt.Printf("  [%s] %s\n        %s\n", mark, v.label, v.detail)
	}

	fmt.Println()
	if allPass {
		fmt.Println("  ✓ OVERALL: PASS")
	} else {
		fmt.Println("  ✗ OVERALL: FAIL")
	}
	fmt.Printf("%s\n\n", strings.Repeat("═", 58))
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	cfg := config{}
	flag.StringVar(&cfg.baseURL, "url", "http://localhost:8080", "Exchange base URL")
	flag.IntVar(&cfg.numClients, "clients", 500, "Total WS clients to connect")
	flag.IntVar(&cfg.slowPct, "slow-pct", 10, "Percentage of clients that are slow (0–100)")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "Steady-state test duration")
	flag.DurationVar(&cfg.ramp, "ramp", 3*time.Second, "Ramp-up window: clients spread evenly over this period")
	flag.IntVar(&cfg.orderRate, "order-rate", 30, "Matching order pairs per second")
	flag.IntVar(&cfg.orderWorkers, "order-workers", 20, "Concurrent order goroutines")
	flag.DurationVar(&cfg.slowDelay, "slow-delay", 2*time.Second, "Read delay for slow clients")
	flag.BoolVar(&cfg.verbose, "verbose", false, "Print per-client connection errors")
	flag.Parse()

	if cfg.slowPct < 0 || cfg.slowPct > 100 {
		log.Fatalf("-slow-pct must be between 0 and 100, got %d", cfg.slowPct)
	}
	if cfg.numClients <= 0 {
		log.Fatalf("-clients must be > 0")
	}

	numSlow := cfg.numClients * cfg.slowPct / 100
	numNormal := cfg.numClients - numSlow
	wurl := makeWSURL(cfg.baseURL)

	// Total context lifetime: ramp + steady + 15s drain margin.
	totalTimeout := cfg.ramp + cfg.duration + 15*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()

	// Channels sized to never block a client goroutine.
	tradeCh := make(chan tradeNotif, 4096)
	resultCh := make(chan clientResult, cfg.numClients+64)

	// Order tracking.
	var submitted, orderErrors atomic.Int64
	var lastSellTime sync.Map // stock → time.Time

	log.Printf("═══════════════════════════════════════════════════")
	log.Printf("  mini-exchange  WS Load Test")
	log.Printf("═══════════════════════════════════════════════════")
	log.Printf("  Server URL      : %s", cfg.baseURL)
	log.Printf("  WS endpoint     : %s", wurl)
	log.Printf("  Total clients   : %d  (%d normal, %d slow @ %v delay)",
		cfg.numClients, numNormal, numSlow, cfg.slowDelay)
	log.Printf("  Ramp-up         : %v", cfg.ramp)
	log.Printf("  Steady-state    : %v", cfg.duration)
	log.Printf("  Order rate      : %d pairs/sec  (%d workers)", cfg.orderRate, cfg.orderWorkers)
	log.Printf("═══════════════════════════════════════════════════")

	// ── Phase 1: ramp-up — connect all clients ────────────────────────────
	log.Printf("[phase 1] ramping up %d clients over %v ...", cfg.numClients, cfg.ramp)

	rampInterval := time.Duration(0)
	if cfg.numClients > 1 && cfg.ramp > 0 {
		rampInterval = cfg.ramp / time.Duration(cfg.numClients)
	}

	for i := 0; i < cfg.numClients; i++ {
		if rampInterval > 0 {
			time.Sleep(rampInterval)
		}
		if i < numSlow {
			go runSlowClient(ctx, i, wurl, cfg.slowDelay, resultCh)
		} else {
			go runNormalClient(ctx, i, wurl, tradeCh, resultCh)
		}
	}
	log.Printf("[phase 1] all %d client goroutines spawned", cfg.numClients)

	// Brief stabilisation pause before we start measuring.
	time.Sleep(500 * time.Millisecond)

	// ── Phase 2: start order submitter ────────────────────────────────────
	log.Printf("[phase 2] starting order submitter (%d pairs/sec) ...", cfg.orderRate)
	orderCtx, orderCancel := context.WithCancel(ctx)
	go runOrderSubmitter(orderCtx, cfg.baseURL, cfg.orderRate, cfg.orderWorkers,
		&submitted, &orderErrors, &lastSellTime)

	// ── Phase 3: collect trade-event latencies ────────────────────────────
	var (
		tradeLatencies []time.Duration
		tradeMu        sync.Mutex
	)
	go func() {
		for notif := range tradeCh {
			v, ok := lastSellTime.Load(notif.stock)
			if !ok {
				continue
			}
			sellAt, ok := v.(time.Time)
			if !ok {
				continue
			}
			lat := notif.receivedAt.Sub(sellAt)
			// Only record plausible latencies (positive, < 10s).
			if lat >= 0 && lat < 10*time.Second {
				tradeMu.Lock()
				tradeLatencies = append(tradeLatencies, lat)
				tradeMu.Unlock()
			}
		}
	}()

	// ── Progress ticker ───────────────────────────────────────────────────
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		phaseStart := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-tick.C:
				tradeMu.Lock()
				nTrade := len(tradeLatencies)
				tradeMu.Unlock()
				log.Printf("[progress %5.0fs] orders=%d  errors=%d  trade_events_via_ws=%d",
					t.Sub(phaseStart).Seconds(),
					submitted.Load(), orderErrors.Load(), nTrade)
			}
		}
	}()

	// ── Wait for steady-state to complete ─────────────────────────────────
	log.Printf("[phase 3] steady-state for %v ...", cfg.duration)
	testStart := time.Now()

	select {
	case <-time.After(cfg.duration):
		log.Printf("[phase 3] steady-state complete")
	case <-ctx.Done():
		log.Printf("[phase 3] context cancelled early")
	}

	// Stop the order submitter cleanly.
	orderCancel()

	// Cancel all WS client goroutines.
	cancel()

	// Close trade notification channel so the latency goroutine exits.
	close(tradeCh)

	// ── Collect results ───────────────────────────────────────────────────
	log.Printf("collecting results (%d clients) ...", cfg.numClients)

	var results []clientResult
	collectTimeout := time.NewTimer(10 * time.Second)
	collectDone := false
	for len(results) < cfg.numClients && !collectDone {
		select {
		case r := <-resultCh:
			if cfg.verbose && !r.connected && r.closeReason != "" {
				log.Printf("  client %d: connect error: %s", r.id, r.closeReason)
			}
			results = append(results, r)
		case <-collectTimeout.C:
			log.Printf("  collection timeout — received %d/%d results", len(results), cfg.numClients)
			collectDone = true
		}
	}
	collectTimeout.Stop()

	// Give latency goroutine a moment to drain any last notifications.
	time.Sleep(200 * time.Millisecond)

	tradeMu.Lock()
	finalLats := make([]time.Duration, len(tradeLatencies))
	copy(finalLats, tradeLatencies)
	tradeMu.Unlock()

	testDur := time.Since(testStart)
	printReport(results, submitted.Load(), orderErrors.Load(), finalLats, testDur, cfg)
}
