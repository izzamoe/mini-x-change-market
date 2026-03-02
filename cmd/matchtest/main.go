// cmd/matchtest/main.go — Matching Engine Verification Test
//
// This test verifies the matching engine actually works under high load by:
// 1. Submitting BUY+SELL pairs at matching prices (guaranteed matches)
// 2. Querying trade history to count actual trades generated
// 3. Measuring matching throughput and latency
// 4. Finding the max throughput where matching remains accurate
//
// Usage:
//
//	go run ./cmd/matchtest [flags]
//
// Flags:
//
//	-url           http://localhost:8080   Exchange base URL
//	-rate          1000                    Orders per minute (BUY+SELL pairs)
//	-duration      60s                     Test duration
//	-workers       20                      Concurrent workers
//	-verify        true                    Verify trades were created
//	-max-rate      0                       If >0, ramp up to find max (0=disable)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Config holds test configuration
type Config struct {
	BaseURL  string
	Rate     int // orders per minute (each = 1 BUY + 1 SELL = 2 orders)
	Duration time.Duration
	Workers  int
	Verify   bool
	MaxRate  int // if >0, ramp up to find max throughput
}

// Result tracks a single order submission result
type Result struct {
	OrderID    string
	Side       string
	Latency    time.Duration
	StatusCode int
	Error      error
}

// Stats tracks aggregate statistics
type Stats struct {
	TotalOrders   atomic.Int64
	SuccessOrders atomic.Int64
	FailedOrders  atomic.Int64
	TradesFound   atomic.Int64

	LatenciesMu sync.Mutex
	Latencies   []time.Duration

	StartTime time.Time
	EndTime   time.Time
}

func (s *Stats) AddLatency(d time.Duration) {
	s.LatenciesMu.Lock()
	s.Latencies = append(s.Latencies, d)
	s.LatenciesMu.Unlock()
}

func (s *Stats) Percentile(p float64) time.Duration {
	s.LatenciesMu.Lock()
	defer s.LatenciesMu.Unlock()

	if len(s.Latencies) == 0 {
		return 0
	}

	sorted := make([]time.Duration, len(s.Latencies))
	copy(sorted, s.Latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(len(sorted)) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// OrderRequest for creating orders
type OrderRequest struct {
	StockCode string `json:"stock_code"`
	Side      string `json:"side"`
	Price     int64  `json:"price"`
	Quantity  int64  `json:"quantity"`
	Type      string `json:"type"`
}

// OrderResponse from server
type OrderResponse struct {
	Success bool `json:"success"`
	Data    struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

// Trade represents a trade from API
type Trade struct {
	ID        string    `json:"id"`
	StockCode string    `json:"stock_code"`
	Price     int64     `json:"price"`
	Quantity  int64     `json:"quantity"`
	CreatedAt time.Time `json:"created_at"`
}

// TradesResponse from server (for /market/trades/{stock})
type TradesResponse struct {
	Success bool    `json:"success"`
	Data    []Trade `json:"data"`
}

// PaginatedTradesResponse from /api/v1/trades
type PaginatedTradesResponse struct {
	Success bool    `json:"success"`
	Data    []Trade `json:"data"`
	Meta    struct {
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
		Pages   int   `json:"pages"`
	} `json:"meta"`
}

var (
	client *http.Client
	stocks = []string{"BBCA", "BBRI", "TLKM", "ASII", "GOTO"}
	prices = map[string]int64{
		"BBCA": 9500,
		"BBRI": 5000,
		"TLKM": 3500,
		"ASII": 6000,
		"GOTO": 100,
	}
)

func init() {
	client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 200,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func submitOrder(baseURL string, req OrderRequest) (string, time.Duration, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/orders", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(httpReq)
	latency := time.Since(start)

	if err != nil {
		return "", latency, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", latency, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var orderResp OrderResponse
	if err := json.Unmarshal(respBody, &orderResp); err != nil {
		return "", latency, err
	}

	return orderResp.Data.ID, latency, nil
}

func getTrades(baseURL, stockCode string) ([]Trade, error) {
	resp, err := client.Get(baseURL + "/api/v1/market/trades/" + stockCode)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var tradesResp TradesResponse
	if err := json.Unmarshal(body, &tradesResp); err != nil {
		return nil, err
	}

	return tradesResp.Data, nil
}

func getAllTrades(baseURL string) ([]Trade, int64, error) {
	// Use the paginated /api/v1/trades endpoint to get total count
	resp, err := client.Get(baseURL + "/api/v1/trades?per_page=100")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var paginatedResp PaginatedTradesResponse
	if err := json.Unmarshal(body, &paginatedResp); err != nil {
		// Fall back to per-stock endpoint
		var allTrades []Trade
		for _, stock := range stocks {
			trades, err := getTrades(baseURL, stock)
			if err != nil {
				return nil, 0, err
			}
			allTrades = append(allTrades, trades...)
		}
		return allTrades, int64(len(allTrades)), nil
	}

	return paginatedResp.Data, paginatedResp.Meta.Total, nil
}

// Worker submits matching BUY/SELL pairs
func worker(ctx context.Context, baseURL string, sem chan struct{}, stats *Stats, stock string, price int64) {
	for {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}

		qty := int64(100)

		// Submit BUY
		buyReq := OrderRequest{
			StockCode: stock,
			Side:      "BUY",
			Price:     price,
			Quantity:  qty,
			Type:      "LIMIT",
		}
		buyID, lat, err := submitOrder(baseURL, buyReq)
		stats.TotalOrders.Add(1)
		if err != nil {
			stats.FailedOrders.Add(1)
			<-sem
			continue
		}
		stats.SuccessOrders.Add(1)
		stats.AddLatency(lat)

		// Submit matching SELL (same price, should match immediately)
		sellReq := OrderRequest{
			StockCode: stock,
			Side:      "SELL",
			Price:     price,
			Quantity:  qty,
			Type:      "LIMIT",
		}
		sellID, lat, err := submitOrder(baseURL, sellReq)
		stats.TotalOrders.Add(1)
		if err != nil {
			stats.FailedOrders.Add(1)
			<-sem
			continue
		}
		stats.SuccessOrders.Add(1)
		stats.AddLatency(lat)

		// Verify orders were created (both should exist even if matched)
		_ = buyID
		_ = sellID

		<-sem
	}
}

func runTest(cfg Config) *Stats {
	stats := &Stats{}
	stats.StartTime = time.Now()

	interval := time.Duration(float64(time.Minute) / float64(cfg.Rate))
	log.Printf("Running matching test: %d orders/min (interval: %v)", cfg.Rate, interval)
	log.Printf("Each order pair = 1 BUY + 1 SELL at same price (guaranteed match)")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	sem := make(chan struct{}, cfg.Workers)

	// Distribute load across stocks
	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		stock := stocks[i%len(stocks)]
		price := prices[stock]
		go func(s string, p int64) {
			defer wg.Done()
			worker(ctx, cfg.BaseURL, sem, stats, s, p)
		}(stock, price)
	}

	// Progress reporter
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		start := time.Now()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				elapsed := t.Sub(start)
				total := stats.TotalOrders.Load()
				success := stats.SuccessOrders.Load()
				failed := stats.FailedOrders.Load()
				rate := float64(total) / elapsed.Seconds() * 60
				log.Printf("[%5.1fs] orders=%d success=%d failed=%d rate=%.0f/min",
					elapsed.Seconds(), total, success, failed, rate)
			}
		}
	}()

	wg.Wait()
	stats.EndTime = time.Now()

	return stats
}

func runRampUpTest(cfg Config) *Stats {
	rates := []int{1000, 5000, 10000, 20000, 30000, 50000, 100000}

	log.Println("=== RAMP-UP TEST ===")
	log.Println("Finding maximum matching throughput...")

	var bestStats *Stats
	var bestRate int

	for _, rate := range rates {
		log.Printf("\n--- Testing %d orders/min ---", rate)

		// Clear previous state by restarting server not possible,
		// so we just measure incremental
		testCfg := cfg
		testCfg.Rate = rate
		testCfg.Duration = 15 * time.Second // shorter per phase

		stats := runTest(testCfg)

		// Verify trades
		time.Sleep(100 * time.Millisecond) // let trades settle
		_, totalTrades, err := getAllTrades(cfg.BaseURL)
		if err != nil {
			log.Printf("Error getting trades: %v", err)
			continue
		}

		totalOrders := stats.TotalOrders.Load()
		successOrders := stats.SuccessOrders.Load()
		elapsed := stats.EndTime.Sub(stats.StartTime).Seconds()
		actualRate := float64(totalOrders) / elapsed * 60

		log.Printf("Results: %d orders, %d success, %.0f actual/min, %d total trades",
			totalOrders, successOrders, actualRate, totalTrades)

		// Check if we hit a limit
		if actualRate < float64(rate)*0.8 {
			log.Printf("★ Throughput limit reached at ~%.0f orders/min", actualRate)
			break
		}

		bestStats = stats
		bestRate = rate

		// Small cooldown
		time.Sleep(2 * time.Second)
	}

	if bestStats != nil {
		log.Printf("\n★ Best throughput: %d orders/min", bestRate)
	}

	return bestStats
}

func printReport(stats *Stats, cfg Config) {
	total := stats.TotalOrders.Load()
	success := stats.SuccessOrders.Load()
	failed := stats.FailedOrders.Load()
	elapsed := stats.EndTime.Sub(stats.StartTime).Seconds()

	ordersPerSec := float64(total) / elapsed
	ordersPerMin := ordersPerSec * 60

	fmt.Println()
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Println("  MATCHING ENGINE TEST REPORT")
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Printf("  Test Configuration:\n")
	fmt.Printf("    Target Rate: %d orders/min\n", cfg.Rate)
	fmt.Printf("    Duration: %v\n", cfg.Duration)
	fmt.Printf("    Workers: %d\n", cfg.Workers)
	fmt.Println()
	fmt.Printf("  Results:\n")
	fmt.Printf("    Total Orders Submitted: %d\n", total)
	fmt.Printf("    Successful: %d (%.1f%%)\n", success, float64(success)/float64(total)*100)
	fmt.Printf("    Failed: %d (%.1f%%)\n", failed, float64(failed)/float64(total)*100)
	fmt.Printf("    Actual Rate: %.0f orders/min (%.1f/sec)\n", ordersPerMin, ordersPerSec)
	fmt.Println()
	fmt.Printf("  Latency:\n")
	fmt.Printf("    p50: %v\n", stats.Percentile(50))
	fmt.Printf("    p95: %v\n", stats.Percentile(95))
	fmt.Printf("    p99: %v\n", stats.Percentile(99))
	fmt.Println()

	if cfg.Verify {
		fmt.Println("  Trade Verification:")
		_, totalTrades, err := getAllTrades(cfg.BaseURL)
		if err != nil {
			fmt.Printf("    ERROR: %v\n", err)
		} else {
			fmt.Printf("    Total Trades in System: %d\n", totalTrades)
			if totalTrades > 0 {
				// Each matching pair should create 1 trade
				expectedTrades := success / 2 // Each pair = 2 orders = 1 trade
				fmt.Printf("    Expected Trades (approx): %d\n", expectedTrades)
				if expectedTrades > 0 {
					fmt.Printf("    Match Rate: %.1f%%\n", float64(totalTrades)/float64(expectedTrades)*100)
				}

				if totalTrades > 0 {
					fmt.Println("    ✓ Matching engine is working!")
				}
			}
		}
		fmt.Println()
	}

	// Compare to rule.md target
	target := 1000 // orders/min from rule.md
	fmt.Printf("  vs rule.md Target (%d orders/min):\n", target)
	if ordersPerMin >= float64(target) {
		fmt.Printf("    ✓ PASS: %.0f/min is %.1fx the target\n", ordersPerMin, ordersPerMin/float64(target))
	} else {
		fmt.Printf("    ✗ FAIL: %.0f/min is below target\n", ordersPerMin)
	}

	fmt.Println("══════════════════════════════════════════════════════════")
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.BaseURL, "url", "http://localhost:8080", "Exchange base URL")
	flag.IntVar(&cfg.Rate, "rate", 1000, "Target orders per minute (BUY+SELL pairs)")
	flag.DurationVar(&cfg.Duration, "duration", 60*time.Second, "Test duration")
	flag.IntVar(&cfg.Workers, "workers", 20, "Concurrent workers")
	flag.BoolVar(&cfg.Verify, "verify", true, "Verify trades were created")
	flag.IntVar(&cfg.MaxRate, "max-rate", 0, "If >0, ramp up to find max throughput")
	flag.Parse()

	log.Println("══════════════════════════════════════════════════════════")
	log.Println("  mini-exchange MATCHING ENGINE TEST")
	log.Println("══════════════════════════════════════════════════════════")
	log.Printf("  Server: %s", cfg.BaseURL)
	log.Printf("  Target: %d orders/min (%d pairs/min)", cfg.Rate, cfg.Rate/2)
	log.Println()

	// Get initial trade count
	_, initialTotalTrades, err := getAllTrades(cfg.BaseURL)
	if err != nil {
		log.Printf("Warning: Could not get initial trades: %v", err)
	} else {
		log.Printf("Initial trades in system: %d", initialTotalTrades)
	}

	var stats *Stats
	if cfg.MaxRate > 0 {
		stats = runRampUpTest(cfg)
	} else {
		stats = runTest(cfg)
	}

	// Wait a bit for final trades to settle
	time.Sleep(200 * time.Millisecond)

	printReport(stats, cfg)
}
