package engine

import (
	"testing"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/stretchr/testify/assert"
)

func bid(id string, price, qty int64) *entity.Order {
	return &entity.Order{
		ID:       id,
		Side:     entity.SideBuy,
		Price:    price,
		Quantity: qty,
		Status:   entity.OrderStatusOpen,
	}
}

func ask(id string, price, qty int64) *entity.Order {
	return &entity.Order{
		ID:       id,
		Side:     entity.SideSell,
		Price:    price,
		Quantity: qty,
		Status:   entity.OrderStatusOpen,
	}
}

func TestOrderBook_AddBid_SortedDescending(t *testing.T) {
	ob := newOrderBook("BBCA")
	ob.addOrder(bid("b1", 9500, 100))
	ob.addOrder(bid("b2", 9700, 50))
	ob.addOrder(bid("b3", 9600, 75))

	assert.Equal(t, int64(9700), ob.bids[0].order.Price)
	assert.Equal(t, int64(9600), ob.bids[1].order.Price)
	assert.Equal(t, int64(9500), ob.bids[2].order.Price)
}

func TestOrderBook_AddAsk_SortedAscending(t *testing.T) {
	ob := newOrderBook("BBCA")
	ob.addOrder(ask("a1", 9600, 100))
	ob.addOrder(ask("a2", 9400, 50))
	ob.addOrder(ask("a3", 9500, 75))

	assert.Equal(t, int64(9400), ob.asks[0].order.Price)
	assert.Equal(t, int64(9500), ob.asks[1].order.Price)
	assert.Equal(t, int64(9600), ob.asks[2].order.Price)
}

func TestOrderBook_FIFO_SamePrice(t *testing.T) {
	ob := newOrderBook("BBCA")
	// Three bids at the same price — insertion order must be preserved.
	ob.addOrder(bid("b1", 9500, 100))
	ob.addOrder(bid("b2", 9500, 200))
	ob.addOrder(bid("b3", 9500, 300))

	assert.Equal(t, "b1", ob.bids[0].order.ID, "first inserted must be first matched (FIFO)")
	assert.Equal(t, "b2", ob.bids[1].order.ID)
	assert.Equal(t, "b3", ob.bids[2].order.ID)
}

func TestOrderBook_BestBid_BestAsk(t *testing.T) {
	ob := newOrderBook("BBCA")
	assert.Nil(t, ob.bestBid(), "empty book should return nil")
	assert.Nil(t, ob.bestAsk(), "empty book should return nil")

	ob.addOrder(bid("b1", 9500, 100))
	ob.addOrder(bid("b2", 9600, 50))
	ob.addOrder(ask("a1", 9700, 80))
	ob.addOrder(ask("a2", 9650, 40))

	assert.Equal(t, int64(9600), ob.bestBid().order.Price)
	assert.Equal(t, int64(9650), ob.bestAsk().order.Price)
}

func TestOrderBook_RemoveOrder(t *testing.T) {
	ob := newOrderBook("BBCA")
	ob.addOrder(bid("b1", 9500, 100))
	ob.addOrder(bid("b2", 9600, 50))
	ob.addOrder(ask("a1", 9700, 80))

	ob.removeOrder("b1")
	assert.Len(t, ob.bids, 1)
	assert.Equal(t, "b2", ob.bids[0].order.ID)

	ob.removeOrder("a1")
	assert.Len(t, ob.asks, 0)
}

func TestOrderBook_GetDepth(t *testing.T) {
	ob := newOrderBook("BBCA")
	// Two bids at same price, one at lower price.
	ob.addOrder(bid("b1", 9500, 100))
	ob.addOrder(bid("b2", 9500, 200))
	ob.addOrder(bid("b3", 9400, 150))
	// Two asks at same price.
	ob.addOrder(ask("a1", 9600, 80))
	ob.addOrder(ask("a2", 9600, 120))
	ob.addOrder(ask("a3", 9700, 200))

	depth := ob.getDepth(5)

	// Bids: 9500 (300 total), 9400 (150)
	assert.Len(t, depth.Bids, 2)
	assert.Equal(t, int64(9500), depth.Bids[0].Price)
	assert.Equal(t, int64(300), depth.Bids[0].Quantity)
	assert.Equal(t, 2, depth.Bids[0].Count)
	assert.Equal(t, int64(9400), depth.Bids[1].Price)

	// Asks: 9600 (200 total), 9700 (200)
	assert.Len(t, depth.Asks, 2)
	assert.Equal(t, int64(9600), depth.Asks[0].Price)
	assert.Equal(t, int64(200), depth.Asks[0].Quantity)
	assert.Equal(t, 2, depth.Asks[0].Count)
}

func TestOrderBook_GetDepth_LevelCap(t *testing.T) {
	ob := newOrderBook("BBCA")
	for i := int64(0); i < 10; i++ {
		ob.addOrder(bid("b"+string(rune('0'+i)), 9500-i*25, 100))
	}

	depth := ob.getDepth(3)
	assert.Len(t, depth.Bids, 3, "depth should be capped at 3 levels")
}
