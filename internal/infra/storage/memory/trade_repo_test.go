package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTrade(id, stock, buyOrderID, sellOrderID, buyerID, sellerID string, price, qty int64) *entity.Trade {
	return &entity.Trade{
		ID:           id,
		StockCode:    stock,
		BuyOrderID:   buyOrderID,
		SellOrderID:  sellOrderID,
		BuyerUserID:  buyerID,
		SellerUserID: sellerID,
		Price:        price,
		Quantity:     qty,
		ExecutedAt:   time.Now(),
	}
}

func TestTradeRepo_SaveAndFindByStock(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	require.NoError(t, r.Save(ctx, makeTrade("t1", "BBCA", "o1", "o2", "u1", "u2", 10000, 100)))
	require.NoError(t, r.Save(ctx, makeTrade("t2", "BBCA", "o3", "o4", "u3", "u4", 10100, 50)))
	require.NoError(t, r.Save(ctx, makeTrade("t3", "BBRI", "o5", "o6", "u1", "u5", 5000, 200)))

	trades, err := r.FindByStockCode(ctx, "BBCA", 10)
	require.NoError(t, err)
	assert.Len(t, trades, 2)

	// Newest first: t2 should be first.
	assert.Equal(t, "t2", trades[0].ID)
	assert.Equal(t, "t1", trades[1].ID)
}

func TestTradeRepo_FindByStockCode_Limit(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("t%d", i)
		require.NoError(t, r.Save(ctx, makeTrade(id, "BBCA", "o1", "o2", "u1", "u2", int64(i*100), 10)))
	}

	trades, err := r.FindByStockCode(ctx, "BBCA", 3)
	require.NoError(t, err)
	assert.Len(t, trades, 3)
	// Newest first: t9 → t8 → t7
	assert.Equal(t, "t9", trades[0].ID)
}

func TestTradeRepo_FindByStockCode_Empty(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	trades, err := r.FindByStockCode(ctx, "NONEXISTENT", 10)
	require.NoError(t, err)
	assert.Empty(t, trades)
}

func TestTradeRepo_SaveDuplicate(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	trade := makeTrade("t1", "BBCA", "o1", "o2", "u1", "u2", 10000, 100)
	require.NoError(t, r.Save(ctx, trade))
	assert.ErrorIs(t, r.Save(ctx, trade), repository.ErrDuplicate)
}

func TestTradeRepo_FindAll_FilterByStock(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	require.NoError(t, r.Save(ctx, makeTrade("t1", "BBCA", "o1", "o2", "u1", "u2", 10000, 100)))
	require.NoError(t, r.Save(ctx, makeTrade("t2", "BBRI", "o3", "o4", "u1", "u3", 5000, 50)))
	require.NoError(t, r.Save(ctx, makeTrade("t3", "BBCA", "o5", "o6", "u4", "u5", 10100, 200)))

	trades, total, err := r.FindAll(ctx, entity.TradeFilter{StockCode: "BBCA", Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, trades, 2)
}

func TestTradeRepo_FindAll_FilterByUserID(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	require.NoError(t, r.Save(ctx, makeTrade("t1", "BBCA", "o1", "o2", "u1", "u2", 10000, 100)))
	require.NoError(t, r.Save(ctx, makeTrade("t2", "BBRI", "o3", "o4", "u3", "u4", 5000, 50)))
	require.NoError(t, r.Save(ctx, makeTrade("t3", "BBCA", "o5", "o6", "u5", "u1", 10100, 200))) // u1 as seller

	// u1 appears as buyer in t1 and seller in t3.
	trades, total, err := r.FindAll(ctx, entity.TradeFilter{UserID: "u1", Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, trades, 2)
}

func TestTradeRepo_FindAll_Pagination(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("t%d", i)
		require.NoError(t, r.Save(ctx, makeTrade(id, "BBCA", "o1", "o2", "u1", "u2", 10000, 10)))
	}

	trades, total, err := r.FindAll(ctx, entity.TradeFilter{Page: 1, PerPage: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 7, total)
	assert.Len(t, trades, 3)

	trades, total, err = r.FindAll(ctx, entity.TradeFilter{Page: 3, PerPage: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 7, total)
	assert.Len(t, trades, 1)
}

func TestTradeRepo_ConcurrentAccess(t *testing.T) {
	r := NewTradeRepo()
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("t%d", i)
			_ = r.Save(ctx, makeTrade(id, "BBCA", "o1", "o2", "u1", "u2", int64(i*100), 10))
		}(i)
	}
	wg.Wait()

	var wg2 sync.WaitGroup
	wg2.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg2.Done()
			_, _ = r.FindByStockCode(ctx, "BBCA", 5)
		}()
		go func() {
			defer wg2.Done()
			_, _, _ = r.FindAll(ctx, entity.TradeFilter{Page: 1, PerPage: 10})
		}()
	}
	wg2.Wait()
}
