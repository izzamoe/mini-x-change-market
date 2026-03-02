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

func makeOrder(id, userID, stockCode string, side entity.Side, price, qty int64) *entity.Order {
	return &entity.Order{
		ID:        id,
		UserID:    userID,
		StockCode: stockCode,
		Side:      side,
		Price:     price,
		Quantity:  qty,
		Status:    entity.OrderStatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestOrderRepo_SaveAndFindByID(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	o := makeOrder("o1", "u1", "BBCA", entity.SideBuy, 10000, 100)
	require.NoError(t, r.Save(ctx, o))

	got, err := r.FindByID(ctx, "o1")
	require.NoError(t, err)
	assert.Equal(t, "o1", got.ID)
	assert.Equal(t, int64(10000), got.Price)
}

func TestOrderRepo_SaveDuplicate(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	o := makeOrder("o1", "u1", "BBCA", entity.SideBuy, 10000, 100)
	require.NoError(t, r.Save(ctx, o))
	err := r.Save(ctx, o)
	assert.ErrorIs(t, err, repository.ErrDuplicate)
}

func TestOrderRepo_FindByID_NotFound(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	_, err := r.FindByID(ctx, "nonexistent")
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestOrderRepo_Update(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	o := makeOrder("o1", "u1", "BBCA", entity.SideBuy, 10000, 100)
	require.NoError(t, r.Save(ctx, o))

	o.Fill(100)
	require.NoError(t, r.Update(ctx, o))

	got, err := r.FindByID(ctx, "o1")
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusFilled, got.Status)
	assert.Equal(t, int64(100), got.FilledQuantity)
}

func TestOrderRepo_Update_NotFound(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	o := makeOrder("missing", "u1", "BBCA", entity.SideBuy, 10000, 100)
	err := r.Update(ctx, o)
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestOrderRepo_FindAll_FilterByStock(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	require.NoError(t, r.Save(ctx, makeOrder("o1", "u1", "BBCA", entity.SideBuy, 10000, 100)))
	require.NoError(t, r.Save(ctx, makeOrder("o2", "u1", "BBRI", entity.SideBuy, 5000, 50)))
	require.NoError(t, r.Save(ctx, makeOrder("o3", "u2", "BBCA", entity.SideSell, 10100, 100)))

	orders, total, err := r.FindAll(ctx, entity.OrderFilter{StockCode: "BBCA", Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, orders, 2)
}

func TestOrderRepo_FindAll_FilterByUserID(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	require.NoError(t, r.Save(ctx, makeOrder("o1", "u1", "BBCA", entity.SideBuy, 10000, 100)))
	require.NoError(t, r.Save(ctx, makeOrder("o2", "u2", "BBCA", entity.SideBuy, 10000, 100)))
	require.NoError(t, r.Save(ctx, makeOrder("o3", "u1", "BBRI", entity.SideBuy, 5000, 50)))

	orders, total, err := r.FindAll(ctx, entity.OrderFilter{UserID: "u1", Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, orders, 2)
}

func TestOrderRepo_FindAll_Pagination(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("o%d", i)
		require.NoError(t, r.Save(ctx, makeOrder(id, "u1", "BBCA", entity.SideBuy, 10000, 100)))
	}

	// Page 1, 3 per page => 3 results, total 10
	orders, total, err := r.FindAll(ctx, entity.OrderFilter{Page: 1, PerPage: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 10, total)
	assert.Len(t, orders, 3)

	// Page 4 (last), 3 per page => 1 result
	orders, total, err = r.FindAll(ctx, entity.OrderFilter{Page: 4, PerPage: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 10, total)
	assert.Len(t, orders, 1)

	// Page beyond end => empty
	orders, total, err = r.FindAll(ctx, entity.OrderFilter{Page: 10, PerPage: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 10, total)
	assert.Empty(t, orders)
}

func TestOrderRepo_IsolatesCallerMutations(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	o := makeOrder("o1", "u1", "BBCA", entity.SideBuy, 10000, 100)
	require.NoError(t, r.Save(ctx, o))

	// Mutate the original after saving — should not affect stored copy.
	o.Price = 99999

	got, err := r.FindByID(ctx, "o1")
	require.NoError(t, err)
	assert.Equal(t, int64(10000), got.Price)
}

func TestOrderRepo_ConcurrentAccess(t *testing.T) {
	r := NewOrderRepo()
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("o%d", i)
			o := makeOrder(id, "u1", "BBCA", entity.SideBuy, int64(i*100), int64(i+1))
			_ = r.Save(ctx, o)
		}(i)
	}
	wg.Wait()

	// Add readers concurrently with writers.
	var wg2 sync.WaitGroup
	wg2.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg2.Done()
			id := fmt.Sprintf("o%d", i)
			_, _ = r.FindByID(ctx, id)
		}(i)
		go func() {
			defer wg2.Done()
			_, _, _ = r.FindAll(ctx, entity.OrderFilter{Page: 1, PerPage: 10})
		}()
	}
	wg2.Wait()
}
