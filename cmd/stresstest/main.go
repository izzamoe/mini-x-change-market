// cmd/stresstest/main.go — mini-exchange stress test
//
// Finds the HTTP throughput ceiling and WS broadcast rate by ramping up
// concurrent workers phase by phase (default: 10→20→40→80→160→320→400).
// Each phase floods the server for -step-dur seconds, then cools down 3s
// before the next phase begins.
//
// Concurrently, -ws-clients WebSocket connections subscribe to all
// market channels and count every event received, giving a real-time
// picture of server-side broadcast capacity under HTTP load.
//
// Usage:
//
//	go run ./cmd/stresstest [flags]
//
// Flags:
//
//	-url           http://localhost:8081   Exchange base URL
//	-max-workers   400                     Max concurrent HTTP workers
//	-start-workers 10                      Workers in first phase (doubled each phase)
//	-step-dur      15s                     Duration of each ramp phase
//	-ws-clients    50                      WS clients for event counting
//	-error-thresh  5.0                     Error-rate % that marks the breaking point
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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

// ─── constants ───────────────────────────────────────────────────────────────

const maxConnPerIP = 10

var (
	stockList = []string{"BBCA", "BBRI", "TLKM", "ASII", "GOTO"}
	basePrice = map[string]int64{
		"BBCA": 9500, "BBRI": 5000, "TLKM": 3500, "ASII": 6000, "GOTO": 100,
	}
)

// ─── per-phase counters ───────────────────────────────────────────────────────

type phaseCounters struct {
	total  atomic.Int64
	ok2xx  atomic.Int64
	err4xx atomic.Int64
	err5xx atomic.Int64
	netErr atomic.Int64

	latMu sync.Mutex
	lats  []time.Duration
}

func (c *phaseCounters) addLat(d time.Duration) {
	c.latMu.Lock()
	c.lats = append(c.lats, d)
	c.latMu.Unlock()
}

// ─── phase result (immutable snapshot after phase) ───────────────────────────

type phaseResult struct {
	phaseNum    int
	workers     int
	duration    time.Duration
	total       int64
	ok2xx       int64
	err4xx      int64
	err5xx      int64
	netErr      int64
	lats        []time.Duration
	reqPerSec   float64
	reqPerMin   float64
	wsEvtTotal  int64 // raw WS events across all clients during phase
	wsEvtPerSec float64
}

func (p *phaseResult) errorRate() float64 {
	if p.total == 0 {
		return 0
	}
	return float64(p.err5xx+p.netErr) / float64(p.total) * 100
}

func pctOf(n, total int64) string {
	if total == 0 {
		return "n/a "
	}
	return fmt.Sprintf("%.1f%%", float64(n)/float64(total)*100)
}

// ─── percentile ──────────────────────────────────────────────────────────────

func percentile(d []time.Duration, p float64) time.Duration {
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

// ─── HTTP flood worker ───────────────────────────────────────────────────────

func randomOrder() (stock, side string, price, qty int64) {
	stock = stockList[rand.Intn(len(stockList))]
	if rand.Intn(2) == 0 {
		side = "BUY"
	} else {
		side = "SELL"
	}
	price = basePrice[stock]
	qty = int64((rand.Intn(5) + 1) * 100)
	return
}

func newHTTPClient(workers int) *http.Client {
	n := workers * 2
	if n < 64 {
		n = 64
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        n,
			MaxIdleConnsPerHost: n,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

func floodWorker(ctx context.Context, baseURL string, client *http.Client, c *phaseCounters) {
	for {
		if ctx.Err() != nil {
			return
		}
		stock, side, price, qty := randomOrder()
		body, _ := json.Marshal(map[string]interface{}{
			"stock_code": stock,
			"side":       side,
			"price":      price,
			"quantity":   qty,
			"type":       "LIMIT",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/orders", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		start := time.Now()
		resp, err := client.Do(req)
		lat := time.Since(start)

		if err != nil {
			if ctx.Err() != nil {
				return // phase ended mid-request — don't count
			}
			c.netErr.Add(1)
			c.total.Add(1)
			c.addLat(lat)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		c.total.Add(1)
		c.addLat(lat)
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			c.ok2xx.Add(1)
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			c.err4xx.Add(1)
		default:
			c.err5xx.Add(1)
		}
	}
}

// ─── WS event counter ────────────────────────────────────────────────────────

type wsMsg struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Stock   string          `json:"stock,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func fakeIP(i int) string { return fmt.Sprintf("10.0.%d.1", i/maxConnPerIP) }

func makeWSURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasPrefix(base, "https://") {
		return "wss://" + base[8:] + "/ws"
	}
	return "ws://" + strings.TrimPrefix(base, "http://") + "/ws"
}

// wsListenerClient connects, subscribes to every stock × every market channel,
// then counts incoming events into counter until ctx is cancelled.
func wsListenerClient(ctx context.Context, id int, wurl string, counter *atomic.Int64, connected *atomic.Int64) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := websocket.Dial(dialCtx, wurl, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Forwarded-For": {fakeIP(id)}},
	})
	cancel()
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	connected.Add(1)

	// Subscribe to 5 stocks × 3 channels = 15 subs (< MaxSubsPerClient=20)
	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	for _, s := range stockList {
		for _, ch := range []string{"market.trade", "market.ticker", "market.orderbook"} {
			_ = wsjson.Write(subCtx, conn, map[string]string{
				"action": "subscribe", "channel": ch, "stock": s,
			})
		}
	}
	subCancel()

	for {
		readCtx, rcancel := context.WithTimeout(ctx, 65*time.Second)
		var msg wsMsg
		err := wsjson.Read(readCtx, conn, &msg)
		rcancel()
		if err != nil {
			return
		}
		if msg.Type != "subscribed" && msg.Type != "" {
			counter.Add(1)
		}
	}
}

// ─── progress ticker ─────────────────────────────────────────────────────────

// progressTicker prints rolling req/s and ws evt/s every interval during a phase.
func progressTicker(ctx context.Context, c *phaseCounters, wsCounter *atomic.Int64, wsSnap int64, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var prevTotal int64
	var prevWS = wsSnap
	prevTime := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			curTotal := c.total.Load()
			curWS := wsCounter.Load()
			dt := now.Sub(prevTime).Seconds()
			rps := float64(curTotal-prevTotal) / dt
			wps := float64(curWS-prevWS) / dt
			errR := float64(c.netErr.Load()+c.err5xx.Load()) / float64(max64(c.total.Load(), 1)) * 100
			fmt.Printf("         → req/s=%-8.0f  ws evt/s=%-8.0f  2xx=%d  4xx=%d  5xx+net=%d  err=%.1f%%\n",
				rps, wps, c.ok2xx.Load(), c.err4xx.Load(), c.err5xx.Load()+c.netErr.Load(), errR)
			prevTotal = curTotal
			prevWS = curWS
			prevTime = now
		}
	}
}

// ─── phase runner ─────────────────────────────────────────────────────────────

func runPhase(phaseNum, workers int, dur time.Duration, baseURL string, wsCounter *atomic.Int64) phaseResult {
	client := newHTTPClient(workers)
	c := &phaseCounters{}

	wsSnap := wsCounter.Load()
	phaseCtx, phaseCancel := context.WithTimeout(context.Background(), dur+2*time.Second)
	defer phaseCancel()

	// Progress ticker: print every 5s during phase
	tickCtx, tickCancel := context.WithCancel(phaseCtx)
	go progressTicker(tickCtx, c, wsCounter, wsSnap, 5*time.Second)

	var wg sync.WaitGroup
	start := time.Now()

	// Cancel workers exactly at dur
	workerCtx, workerCancel := context.WithTimeout(phaseCtx, dur)
	defer workerCancel()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			floodWorker(workerCtx, baseURL, client, c)
		}()
	}
	wg.Wait()
	tickCancel()

	elapsed := time.Since(start)
	wsEnd := wsCounter.Load()

	c.latMu.Lock()
	lats := make([]time.Duration, len(c.lats))
	copy(lats, c.lats)
	c.latMu.Unlock()

	total := c.total.Load()
	rps := float64(total) / elapsed.Seconds()
	wsEvtTotal := wsEnd - wsSnap

	return phaseResult{
		phaseNum:    phaseNum,
		workers:     workers,
		duration:    elapsed,
		total:       total,
		ok2xx:       c.ok2xx.Load(),
		err4xx:      c.err4xx.Load(),
		err5xx:      c.err5xx.Load(),
		netErr:      c.netErr.Load(),
		lats:        lats,
		reqPerSec:   rps,
		reqPerMin:   rps * 60,
		wsEvtTotal:  wsEvtTotal,
		wsEvtPerSec: float64(wsEvtTotal) / elapsed.Seconds(),
	}
}

// ─── report ──────────────────────────────────────────────────────────────────

func printReport(phases []phaseResult, breakingPoint int, baseURL string, wsClients int) {
	wide := strings.Repeat("═", 96)
	sep := strings.Repeat("─", 96)

	fmt.Printf("\n%s\n", wide)
	fmt.Printf("  STRESS TEST REPORT  —  %s\n", baseURL)
	fmt.Printf("  Generated : %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("  WS clients: %d (subscribed to 5 stocks × 3 channels; event counts are fan-out totals)\n", wsClients)
	fmt.Printf("%s\n\n", wide)

	// ── header ────────────────────────────────────────────────────────────
	fmt.Printf("%-7s %-8s %-10s %-12s %-8s %-7s %-7s %-7s %-8s %-8s %-8s %-12s\n",
		"Phase", "Workers", "Req/s", "Req/min", "Total", "2xx%", "4xx%", "5xx%", "p50", "p95", "p99", "WS evt/s")
	fmt.Println(sep)

	var peakPhaseIdx int
	var peakRPS float64
	var peakWSEvtS float64

	for i, p := range phases {
		mark := "  "
		if i+1 == breakingPoint {
			mark = "★ "
		}

		p50 := percentile(p.lats, 50).Round(time.Millisecond)
		p95 := percentile(p.lats, 95).Round(time.Millisecond)
		p99 := percentile(p.lats, 99).Round(time.Millisecond)

		fmt.Printf("%s%-5d  %-8d %-10.0f %-12.0f %-8d %-7s %-7s %-7s %-8v %-8v %-8v %-12.0f\n",
			mark, i+1, p.workers,
			p.reqPerSec, p.reqPerMin, p.total,
			pctOf(p.ok2xx, p.total),
			pctOf(p.err4xx, p.total),
			pctOf(p.err5xx+p.netErr, p.total),
			p50, p95, p99,
			p.wsEvtPerSec,
		)

		if p.reqPerSec > peakRPS {
			peakRPS = p.reqPerSec
			peakPhaseIdx = i
		}
		if p.wsEvtPerSec > peakWSEvtS {
			peakWSEvtS = p.wsEvtPerSec
		}
	}

	fmt.Println(sep)
	fmt.Println()

	// ── summary ───────────────────────────────────────────────────────────
	peak := phases[peakPhaseIdx]
	p50 := percentile(peak.lats, 50).Round(time.Millisecond)
	p95 := percentile(peak.lats, 95).Round(time.Millisecond)
	p99 := percentile(peak.lats, 99).Round(time.Millisecond)

	fmt.Printf("  SUMMARY\n")
	fmt.Println(sep)
	fmt.Printf("  Peak HTTP throughput  : %.0f req/s  (%.0f req/min)  [phase %d, %d workers]\n",
		peak.reqPerSec, peak.reqPerMin, peakPhaseIdx+1, peak.workers)
	fmt.Printf("  Latency at peak       : p50=%-6v  p95=%-6v  p99=%v\n", p50, p95, p99)

	if breakingPoint > 0 && breakingPoint <= len(phases) {
		bp := phases[breakingPoint-1]
		fmt.Printf("  ★ Breaking point      : phase %d (%d workers) — error rate %.1f%%\n",
			breakingPoint, bp.workers, bp.errorRate())
	} else {
		fmt.Printf("  Breaking point        : not reached — server held through all phases\n")
	}

	if wsClients > 0 {
		perClient := 0.0
		if wsClients > 0 {
			perClient = peakWSEvtS / float64(wsClients)
		}
		fmt.Printf("  Peak WS events/s      : %.0f total  (%.1f evt/s per client, %.0f evt/min total)\n",
			peakWSEvtS, perClient, peakWSEvtS*60)
	}

	fmt.Printf("\n%s\n\n", wide)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	var (
		baseURL      string
		maxWorkers   int
		startWorkers int
		stepDur      time.Duration
		wsClients    int
		errorThresh  float64
	)
	flag.StringVar(&baseURL, "url", "http://localhost:8081", "Exchange base URL")
	flag.IntVar(&maxWorkers, "max-workers", 400, "Max concurrent HTTP workers")
	flag.IntVar(&startWorkers, "start-workers", 10, "Workers in first phase (doubled each phase)")
	flag.DurationVar(&stepDur, "step-dur", 15*time.Second, "Duration of each ramp phase")
	flag.IntVar(&wsClients, "ws-clients", 50, "WS clients for event counting")
	flag.Float64Var(&errorThresh, "error-thresh", 5.0, "5xx+net error rate % that marks breaking point")
	flag.Parse()

	if startWorkers <= 0 {
		startWorkers = 10
	}

	// Build phase worker list: start, start×2, start×4, … up to maxWorkers
	var workerCounts []int
	for w := startWorkers; w < maxWorkers; w *= 2 {
		workerCounts = append(workerCounts, w)
	}
	workerCounts = append(workerCounts, maxWorkers) // always include max

	wurl := makeWSURL(baseURL)
	totalDur := time.Duration(len(workerCounts)) * stepDur

	fmt.Printf("\n%s\n", strings.Repeat("═", 60))
	fmt.Printf("  mini-exchange STRESS TEST\n")
	fmt.Printf("%s\n", strings.Repeat("═", 60))
	fmt.Printf("  Target      : %s\n", baseURL)
	fmt.Printf("  Phases      : %v\n", workerCounts)
	fmt.Printf("  Step dur    : %v each  (~%v total)\n", stepDur, totalDur)
	fmt.Printf("  WS clients  : %d\n", wsClients)
	fmt.Printf("  Break thresh: %.1f%% error rate\n", errorThresh)
	fmt.Printf("%s\n", strings.Repeat("═", 60))

	// ── connect WS listener clients ───────────────────────────────────────
	var wsCounter atomic.Int64
	var wsConnected atomic.Int64

	wsCtx, wsCancel := context.WithCancel(context.Background())
	defer wsCancel()

	if wsClients > 0 {
		fmt.Printf("\n[WS] Connecting %d listener clients to %s ...\n", wsClients, wurl)
		for i := 0; i < wsClients; i++ {
			go wsListenerClient(wsCtx, i, wurl, &wsCounter, &wsConnected)
		}
		// Wait up to 5s for connections to settle
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if wsConnected.Load() >= int64(wsClients) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Printf("[WS] %d/%d clients connected\n", wsConnected.Load(), wsClients)
	}

	time.Sleep(500 * time.Millisecond) // brief stabilisation

	// ── run phases ────────────────────────────────────────────────────────
	var phases []phaseResult
	breakingPoint := 0

	for i, workers := range workerCounts {
		fmt.Printf("\n[phase %d/%d] %d workers × %v\n", i+1, len(workerCounts), workers, stepDur)

		pr := runPhase(i+1, workers, stepDur, baseURL, &wsCounter)
		phases = append(phases, pr)

		p50 := percentile(pr.lats, 50).Round(time.Millisecond)
		p99 := percentile(pr.lats, 99).Round(time.Millisecond)
		fmt.Printf("         PHASE %d DONE: req/s=%.0f  req/min=%.0f  total=%d  2xx=%d  4xx=%d  5xx+net=%d  p50=%v  p99=%v  ws_evt/s=%.0f  errRate=%.1f%%\n",
			i+1, pr.reqPerSec, pr.reqPerMin, pr.total,
			pr.ok2xx, pr.err4xx, pr.err5xx+pr.netErr,
			p50, p99, pr.wsEvtPerSec, pr.errorRate())

		if breakingPoint == 0 && pr.errorRate() >= errorThresh {
			breakingPoint = i + 1
			fmt.Printf("         ★ BREAKING POINT — error rate %.1f%% ≥ threshold %.1f%%\n",
				pr.errorRate(), errorThresh)
		}

		if i < len(workerCounts)-1 {
			fmt.Printf("         [cooldown 3s ...]\n")
			time.Sleep(3 * time.Second)
		}
	}

	wsCancel()
	printReport(phases, breakingPoint, baseURL, int(wsConnected.Load()))
}
