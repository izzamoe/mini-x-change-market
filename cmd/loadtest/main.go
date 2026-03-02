// cmd/loadtest/main.go - Load test tool for mini-exchange
// Verifies the system can handle target throughput for orders.
//
// Usage:
//
//	go run ./cmd/loadtest [flags]
//
// Flags:
//
//	-url      Base URL of the exchange API (default: http://localhost:8080)
//	-rate     Target orders per minute (default: 1000)
//	-duration Duration of the test (default: 60s)
//	-workers  Number of concurrent goroutines (default: 20)
//	-burst    Run burst test to find max throughput (default: false)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ---- config ----------------------------------------------------------------

type config struct {
	baseURL  string
	rate     int           // target orders per minute
	duration time.Duration // test duration
	workers  int           // concurrent goroutines
	burst    bool          // run burst test
	flood    bool          // max-throughput flood test
}

// ---- result ----------------------------------------------------------------

type result struct {
	latency time.Duration
	status  int
	err     error
}

// ---- HTTP helpers ----------------------------------------------------------

type registerReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type registerResp struct {
	Success bool `json:"success"`
	Data    struct {
		Token string `json:"token"`
	} `json:"data"`
}

type orderReq struct {
	StockCode string  `json:"stock_code"`
	Side      string  `json:"side"`
	Price     float64 `json:"price"`
	Quantity  int     `json:"quantity"`
}

type orderResp struct {
	Success bool `json:"success"`
	Data    struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func registerUser(client *http.Client, baseURL, username, password string) (string, error) {
	body, _ := json.Marshal(registerReq{Username: username, Password: password})
	resp, err := client.Post(baseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r registerResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("parse register response: %w", err)
	}
	if !r.Success || r.Data.Token == "" {
		return "", fmt.Errorf("register failed: %s", string(raw))
	}
	return r.Data.Token, nil
}

func placeOrder(client *http.Client, baseURL, token string, req orderReq) result {
	body, _ := json.Marshal(req)
	start := time.Now()
	httpReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/orders", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		return result{latency: latency, err: err}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return result{latency: latency, status: resp.StatusCode}
}

// ---- stats -----------------------------------------------------------------

type stats struct {
	total        int64
	success      int64
	errors       int64
	clientErrors int64 // 4xx
	latencies    []time.Duration
	statusCounts map[int]int64
	mu           sync.Mutex
	startTime    time.Time
	finishTime   time.Time
}

func newStats() *stats {
	return &stats{statusCounts: make(map[int]int64)}
}

func (s *stats) add(r result) {
	atomic.AddInt64(&s.total, 1)
	if r.err != nil || r.status >= 500 {
		atomic.AddInt64(&s.errors, 1)
	} else if r.status >= 200 && r.status < 300 {
		atomic.AddInt64(&s.success, 1)
	} else if r.status >= 400 && r.status < 500 {
		atomic.AddInt64(&s.clientErrors, 1)
	}
	s.mu.Lock()
	s.latencies = append(s.latencies, r.latency)
	s.statusCounts[r.status]++
	s.mu.Unlock()
}

func (s *stats) percentile(p float64) time.Duration {
	s.mu.Lock()
	cp := make([]time.Duration, len(s.latencies))
	copy(cp, s.latencies)
	s.mu.Unlock()
	if len(cp) == 0 {
		return 0
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(math.Ceil(p/100*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func (s *stats) print(label string) {
	elapsed := s.finishTime.Sub(s.startTime)
	total := atomic.LoadInt64(&s.total)
	success := atomic.LoadInt64(&s.success)
	errors := atomic.LoadInt64(&s.errors)
	clientErr := atomic.LoadInt64(&s.clientErrors)
	rps := float64(total) / elapsed.Seconds()
	rpm := rps * 60

	s.mu.Lock()
	codes := make(map[int]int64, len(s.statusCounts))
	for k, v := range s.statusCounts {
		codes[k] = v
	}
	s.mu.Unlock()

	fmt.Printf("\n=== %s ===\n", label)
	fmt.Printf("Duration       : %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Total requests : %d\n", total)
	fmt.Printf("Success (2xx)  : %d\n", success)
	fmt.Printf("Client err(4xx): %d\n", clientErr)
	fmt.Printf("Server err(5xx): %d\n", errors)
	fmt.Printf("Error rate     : %.2f%%\n", float64(errors)/float64(total)*100)
	fmt.Printf("Throughput     : %.1f req/s  (%.0f req/min)\n", rps, rpm)
	fmt.Printf("Latency p50    : %v\n", s.percentile(50).Round(time.Microsecond))
	fmt.Printf("Latency p95    : %v\n", s.percentile(95).Round(time.Microsecond))
	fmt.Printf("Latency p99    : %v\n", s.percentile(99).Round(time.Microsecond))
	fmt.Printf("Status codes   : ")
	for code, count := range codes {
		fmt.Printf("%d×%d ", code, count)
	}
	fmt.Println()
}

// ---- test runner -----------------------------------------------------------

var stocks = []string{"BBCA", "BBRI", "TLKM", "ASII", "GOTO"}
var prices = map[string]float64{
	"BBCA": 9500,
	"BBRI": 5000,
	"TLKM": 3500,
	"ASII": 6000,
	"GOTO": 100,
}

func randomOrder() orderReq {
	stock := stocks[rand.Intn(len(stocks))]
	side := "BUY"
	if rand.Intn(2) == 1 {
		side = "SELL"
	}
	basePrice := prices[stock]
	// price variation ±2%
	variation := basePrice * 0.02 * (rand.Float64()*2 - 1)
	price := math.Round((basePrice+variation)/25) * 25 // round to nearest 25
	if price <= 0 {
		price = basePrice
	}
	qty := (rand.Intn(10) + 1) * 100 // 100-1000 lots
	return orderReq{
		StockCode: stock,
		Side:      side,
		Price:     price,
		Quantity:  qty,
	}
}

func runFixedRate(cfg config, token string, client *http.Client) *stats {
	s := newStats()
	s.startTime = time.Now()
	interval := time.Duration(float64(time.Minute) / float64(cfg.rate))

	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.workers)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	done := time.After(cfg.duration)

	var sent int64
	for {
		select {
		case <-done:
			wg.Wait()
			s.finishTime = time.Now()
			return s
		case <-ticker.C:
			sem <- struct{}{}
			wg.Add(1)
			req := randomOrder()
			atomic.AddInt64(&sent, 1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				r := placeOrder(client, cfg.baseURL, token, req)
				s.add(r)
			}()
		}
	}
}

func runBurst(cfg config, token string, client *http.Client) *stats {
	s := newStats()
	s.startTime = time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.workers)

	// ramp: 200 → 400 → 800 → 1600 → 3200 workers, 5s each phase
	phases := []int{200, 400, 800, 1600, 3200}
	for _, count := range phases {
		phaseDone := make(chan struct{})
		go func(n int) {
			for i := 0; i < n; i++ {
				sem <- struct{}{}
				wg.Add(1)
				req := randomOrder()
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					r := placeOrder(client, cfg.baseURL, token, req)
					s.add(r)
				}()
			}
			close(phaseDone)
		}(count)
		<-phaseDone
		time.Sleep(2 * time.Second)

		total := atomic.LoadInt64(&s.total)
		elapsed := time.Since(s.startTime).Seconds()
		fmt.Printf("  phase %5d reqs | total so far: %d | throughput: %.0f req/s\n",
			count, total, float64(total)/elapsed)
	}
	wg.Wait()
	s.finishTime = time.Now()
	return s
}

// runFlood saturates the server: each worker fires requests back-to-back
// without any rate limiting. This reveals the true max throughput ceiling.
func runFlood(cfg config, token string, client *http.Client) *stats {
	s := newStats()
	s.startTime = time.Now()

	var wg sync.WaitGroup
	done := make(chan struct{})
	time.AfterFunc(cfg.duration, func() { close(done) })

	for i := 0; i < cfg.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					r := placeOrder(client, cfg.baseURL, token, randomOrder())
					s.add(r)
				}
			}
		}()
	}
	wg.Wait()
	s.finishTime = time.Now()
	return s
}

// ---- main ------------------------------------------------------------------

func main() {
	cfg := config{}
	flag.StringVar(&cfg.baseURL, "url", "http://localhost:8080", "Base URL of the exchange API")
	flag.IntVar(&cfg.rate, "rate", 1000, "Target orders per minute")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "Test duration")
	flag.IntVar(&cfg.workers, "workers", 50, "Max concurrent goroutines")
	flag.BoolVar(&cfg.burst, "burst", false, "Run burst test to find max throughput")
	flag.BoolVar(&cfg.flood, "flood", false, "Max-throughput flood: workers fire back-to-back with no rate limit")
	flag.Parse()

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.workers * 2,
			MaxIdleConnsPerHost: cfg.workers * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Register a dedicated test user
	username := fmt.Sprintf("loadtest_%d", time.Now().UnixNano())
	log.Printf("Registering test user: %s", username)
	token, err := registerUser(client, cfg.baseURL, username, "loadtest123")
	if err != nil {
		log.Fatalf("Failed to register test user: %v", err)
	}
	log.Printf("Test user registered, token obtained")

	if cfg.flood {
		fmt.Printf("\nRunning FLOOD test: %d workers firing back-to-back for %v\n",
			cfg.workers, cfg.duration)
		s := runFlood(cfg, token, client)
		s.print(fmt.Sprintf("Flood Test (%d workers, %v)", cfg.workers, cfg.duration))
	} else if cfg.burst {
		fmt.Printf("\nRunning BURST test (ramp-up to find ceiling)...\n")
		s := runBurst(cfg, token, client)
		s.print("Burst Test Results")
	} else {
		fmt.Printf("\nRunning FIXED-RATE test: %d orders/min for %v with %d workers\n",
			cfg.rate, cfg.duration, cfg.workers)

		// progress ticker
		go func() {
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			start := time.Now()
			for t := range tick.C {
				elapsed := t.Sub(start)
				if elapsed > cfg.duration {
					return
				}
				fmt.Printf("  [%5.1fs] running...\n", elapsed.Seconds())
			}
		}()

		s := runFixedRate(cfg, token, client)
		s.print(fmt.Sprintf("Fixed-Rate Test (%d orders/min, %v)", cfg.rate, cfg.duration))

		// verdict
		total := atomic.LoadInt64(&s.total)
		errors := atomic.LoadInt64(&s.errors)
		clientErr := atomic.LoadInt64(&s.clientErrors)
		elapsed := s.finishTime.Sub(s.startTime)
		actualRPM := float64(total) / elapsed.Minutes()
		targetRPM := float64(cfg.rate)

		fmt.Printf("Target  : %.0f orders/min\n", targetRPM)
		fmt.Printf("Actual  : %.0f orders/min\n", actualRPM)
		totalErrRate := float64(errors+clientErr) / float64(total) * 100
		errRate := float64(errors) / float64(total) * 100

		if actualRPM >= targetRPM*0.95 && totalErrRate < 1.0 {
			fmt.Printf("PASS: System handled %.0f orders/min with %.2f%% total error rate (5xx=%.2f%%)\n",
				actualRPM, totalErrRate, errRate)
		} else {
			fmt.Printf("FAIL: actual=%.0f/min (target=%.0f/min), total_error_rate=%.2f%% (5xx=%.2f%%)\n",
				actualRPM, targetRPM, totalErrRate, errRate)
		}
	}
}
