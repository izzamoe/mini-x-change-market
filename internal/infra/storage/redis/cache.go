// Package redis provides Redis-backed caching for hot market data.
// Tickers and order books are written on every update so any server instance
// can serve read requests without hitting the in-memory store of another node.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	goredis "github.com/redis/go-redis/v9"
)

const (
	tickerTTL    = 60 * time.Second
	orderBookTTL = 10 * time.Second
)

// Cache provides get/set operations for market data against Redis.
type Cache struct {
	client *goredis.Client
}

// NewCache parses redisURL (e.g. "redis://localhost:6379/0") and returns a Cache.
func NewCache(redisURL string) (*Cache, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	client := goredis.NewClient(opts)
	return &Cache{client: client}, nil
}

// Ping verifies the Redis connection.
func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Close closes the Redis client.
func (c *Cache) Close() error {
	return c.client.Close()
}

// SetTicker stores the ticker for stock with a 60-second TTL.
func (c *Cache) SetTicker(ctx context.Context, stock string, ticker *entity.Ticker) error {
	data, err := json.Marshal(ticker)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, tickerKey(stock), data, tickerTTL).Err()
}

// GetTicker retrieves the cached ticker for stock.
// Returns (nil, nil) on cache miss.
func (c *Cache) GetTicker(ctx context.Context, stock string) (*entity.Ticker, error) {
	data, err := c.client.Get(ctx, tickerKey(stock)).Bytes()
	if err == goredis.Nil {
		return nil, nil // cache miss
	}
	if err != nil {
		return nil, err
	}
	var t entity.Ticker
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// SetOrderBook stores the order book snapshot for stock with a 10-second TTL.
func (c *Cache) SetOrderBook(ctx context.Context, stock string, book *entity.OrderBook) error {
	data, err := json.Marshal(book)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, orderBookKey(stock), data, orderBookTTL).Err()
}

// GetOrderBook retrieves the cached order book for stock.
// Returns (nil, nil) on cache miss.
func (c *Cache) GetOrderBook(ctx context.Context, stock string) (*entity.OrderBook, error) {
	data, err := c.client.Get(ctx, orderBookKey(stock)).Bytes()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var b entity.OrderBook
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func tickerKey(stock string) string    { return "ticker:" + stock }
func orderBookKey(stock string) string { return "orderbook:" + stock }
