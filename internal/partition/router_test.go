package partition_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	ipartition "github.com/izzam/mini-exchange/internal/partition"
	pkgpartition "github.com/izzam/mini-exchange/pkg/partition"
)

// ─── Mocks ───────────────────────────────────────────────────────────────────

// mockSubmitter records which orders were submitted locally.
type mockSubmitter struct {
	submitted []*entity.Order
	err       error
}

func (m *mockSubmitter) SubmitOrder(o *entity.Order) error {
	if m.err != nil {
		return m.err
	}
	m.submitted = append(m.submitted, o)
	return nil
}

// mockPublisher records which subjects and payloads were published.
type mockPublisher struct {
	calls []publishCall
	err   error
}

type publishCall struct {
	subject string
	payload interface{}
}

func (m *mockPublisher) Publish(subject string, payload interface{}) error {
	if m.err != nil {
		return m.err
	}
	m.calls = append(m.calls, publishCall{subject: subject, payload: payload})
	return nil
}

// ─── Router.SubmitOrder ───────────────────────────────────────────────────────

func TestRouter_SubmitOrder_LocalWhenOwned(t *testing.T) {
	// Instance 0 out of 1 owns every stock.
	p, err := pkgpartition.New(0, 1)
	require.NoError(t, err)

	local := &mockSubmitter{}
	pub := &mockPublisher{}
	router := ipartition.NewRouter(local, pub, p)

	order := &entity.Order{StockCode: "BBCA", ID: "order-1"}
	err = router.SubmitOrder(order)

	require.NoError(t, err)
	assert.Len(t, local.submitted, 1, "owned order must go to local engine")
	assert.Len(t, pub.calls, 0, "no NATS publish expected for owned stock")
}

func TestRouter_SubmitOrder_RemoteWhenNotOwned(t *testing.T) {
	// Use 2 instances; instance 0 owns some stocks, instance 1 owns others.
	// We'll pick a stock that instance 0 does NOT own.
	total := 2

	// Find a stock owned by instance 1 (not instance 0).
	var remoteStock string
	for _, s := range []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI"} {
		if pkgpartition.OwnerIndex(s, total) == 1 {
			remoteStock = s
			break
		}
	}
	require.NotEmpty(t, remoteStock, "need at least one stock owned by instance 1")

	// Create router for instance 0.
	p, err := pkgpartition.New(0, total)
	require.NoError(t, err)

	local := &mockSubmitter{}
	pub := &mockPublisher{}
	router := ipartition.NewRouter(local, pub, p)

	order := &entity.Order{StockCode: remoteStock, ID: "order-2"}
	err = router.SubmitOrder(order)

	require.NoError(t, err)
	assert.Len(t, local.submitted, 0, "remote order must NOT go to local engine")
	require.Len(t, pub.calls, 1, "remote order must be published to NATS")
	assert.Equal(t, "engine.orders."+remoteStock, pub.calls[0].subject)
	assert.Equal(t, order, pub.calls[0].payload)
}

func TestRouter_SubmitOrder_LocalEngineErrorPropagated(t *testing.T) {
	p, err := pkgpartition.New(0, 1)
	require.NoError(t, err)

	wantErr := errors.New("engine full")
	local := &mockSubmitter{err: wantErr}
	pub := &mockPublisher{}
	router := ipartition.NewRouter(local, pub, p)

	err = router.SubmitOrder(&entity.Order{StockCode: "BBCA"})
	assert.ErrorIs(t, err, wantErr)
}

func TestRouter_SubmitOrder_PublisherErrorWrapped(t *testing.T) {
	total := 2
	// Find stock NOT owned by instance 0.
	var remoteStock string
	for _, s := range []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI"} {
		if pkgpartition.OwnerIndex(s, total) == 1 {
			remoteStock = s
			break
		}
	}
	require.NotEmpty(t, remoteStock)

	p, err := pkgpartition.New(0, total)
	require.NoError(t, err)

	wantErr := errors.New("nats down")
	local := &mockSubmitter{}
	pub := &mockPublisher{err: wantErr}
	router := ipartition.NewRouter(local, pub, p)

	err = router.SubmitOrder(&entity.Order{StockCode: remoteStock})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nats down")
}

func TestRouter_OwnsStock_Passthrough(t *testing.T) {
	p, err := pkgpartition.New(0, 1)
	require.NoError(t, err)

	router := ipartition.NewRouter(&mockSubmitter{}, &mockPublisher{}, p)
	assert.True(t, router.OwnsStock("BBCA"),
		"single-instance router must own every stock")
}

// ─── Routing correctness for all stocks across 3 instances ───────────────────

func TestRouter_AllStocksRoutedCorrectly(t *testing.T) {
	total := 3
	allStocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI", "UNVR", "CPIN"}

	for _, stock := range allStocks {
		ownerIdx := pkgpartition.OwnerIndex(stock, total)
		order := &entity.Order{StockCode: stock, ID: "o-" + stock}

		for idx := 0; idx < total; idx++ {
			p, err := pkgpartition.New(idx, total)
			require.NoError(t, err)

			local := &mockSubmitter{}
			pub := &mockPublisher{}
			router := ipartition.NewRouter(local, pub, p)

			require.NoError(t, router.SubmitOrder(order))

			if idx == ownerIdx {
				assert.Len(t, local.submitted, 1,
					"instance %d should process stock %s locally", idx, stock)
				assert.Len(t, pub.calls, 0)
			} else {
				assert.Len(t, local.submitted, 0,
					"instance %d should NOT process stock %s locally", idx, stock)
				assert.Len(t, pub.calls, 1,
					"instance %d should forward stock %s via NATS", idx, stock)
			}
		}
	}
}
