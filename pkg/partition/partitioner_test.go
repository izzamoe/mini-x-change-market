package partition_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/izzam/mini-exchange/pkg/partition"
)

// ─── New ─────────────────────────────────────────────────────────────────────

func TestNew_ValidInputs(t *testing.T) {
	tests := []struct {
		idx   int
		total int
	}{
		{0, 1},
		{0, 3},
		{1, 3},
		{2, 3},
		{0, 10},
		{9, 10},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("idx=%d total=%d", tc.idx, tc.total), func(t *testing.T) {
			p, err := partition.New(tc.idx, tc.total)
			require.NoError(t, err)
			assert.Equal(t, tc.idx, p.InstanceIndex())
			assert.Equal(t, tc.total, p.TotalInstances())
		})
	}
}

func TestNew_InvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		idx   int
		total int
	}{
		{"zero total", 0, 0},
		{"negative total", 0, -1},
		{"index equals total", 3, 3},
		{"index > total", 5, 3},
		{"negative index", -1, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := partition.New(tc.idx, tc.total)
			assert.Error(t, err)
		})
	}
}

// ─── OwnerIndex ───────────────────────────────────────────────────────────────

func TestOwnerIndex_Determinism(t *testing.T) {
	// Same stock + same total must always return the same index.
	stocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII"}
	for _, s := range stocks {
		first := partition.OwnerIndex(s, 3)
		for i := 0; i < 100; i++ {
			assert.Equal(t, first, partition.OwnerIndex(s, 3),
				"OwnerIndex must be deterministic for stock %s", s)
		}
	}
}

func TestOwnerIndex_InRange(t *testing.T) {
	total := 5
	stocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI", "UNVR", "CPIN", "ICBP", "KLBF"}
	for _, s := range stocks {
		idx := partition.OwnerIndex(s, total)
		assert.GreaterOrEqual(t, idx, 0, "OwnerIndex must be >= 0 for %s", s)
		assert.Less(t, idx, total, "OwnerIndex must be < total for %s", s)
	}
}

func TestOwnerIndex_TotalOne(t *testing.T) {
	// With a single instance, every stock must map to index 0.
	stocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII"}
	for _, s := range stocks {
		assert.Equal(t, 0, partition.OwnerIndex(s, 1),
			"single-instance: every stock must map to index 0")
	}
}

// ─── OwnsStock ────────────────────────────────────────────────────────────────

func TestOwnsStock_Exclusivity(t *testing.T) {
	// For any stock, exactly one instance out of N must claim ownership.
	total := 4
	stocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI"}

	for _, stock := range stocks {
		owners := 0
		for idx := 0; idx < total; idx++ {
			p, err := partition.New(idx, total)
			require.NoError(t, err)
			if p.OwnsStock(stock) {
				owners++
			}
		}
		assert.Equal(t, 1, owners,
			"stock %s must be owned by exactly 1 instance out of %d", stock, total)
	}
}

func TestOwnsStock_SingleInstance(t *testing.T) {
	p, err := partition.New(0, 1)
	require.NoError(t, err)

	stocks := []string{"BBCA", "GOTO", "TLKM", "BUMI"}
	for _, s := range stocks {
		assert.True(t, p.OwnsStock(s),
			"single instance must own every stock (got false for %s)", s)
	}
}

// ─── OwnedStocks ──────────────────────────────────────────────────────────────

func TestOwnedStocks_NoOverlap(t *testing.T) {
	total := 3
	allStocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI", "UNVR", "CPIN"}

	seen := make(map[string]int) // stock → owning instance index
	for idx := 0; idx < total; idx++ {
		p, err := partition.New(idx, total)
		require.NoError(t, err)
		for _, s := range p.OwnedStocks(allStocks) {
			if prev, exists := seen[s]; exists {
				t.Errorf("stock %s owned by both instance %d and %d", s, prev, idx)
			}
			seen[s] = idx
		}
	}
}

func TestOwnedStocks_FullCoverage(t *testing.T) {
	total := 3
	allStocks := []string{"BBCA", "GOTO", "TLKM", "BUMI", "ASII", "BMRI", "UNVR", "CPIN"}

	covered := make(map[string]bool)
	for idx := 0; idx < total; idx++ {
		p, err := partition.New(idx, total)
		require.NoError(t, err)
		for _, s := range p.OwnedStocks(allStocks) {
			covered[s] = true
		}
	}
	for _, s := range allStocks {
		assert.True(t, covered[s], "stock %s must be covered by at least one instance", s)
	}
}

func TestOwnedStocks_Sorted(t *testing.T) {
	p, err := partition.New(0, 3)
	require.NoError(t, err)

	allStocks := []string{"TLKM", "BBCA", "GOTO", "BUMI", "ASII", "BMRI", "UNVR", "CPIN", "ICBP", "KLBF"}
	owned := p.OwnedStocks(allStocks)

	for i := 1; i < len(owned); i++ {
		assert.LessOrEqual(t, owned[i-1], owned[i],
			"OwnedStocks must be sorted: %v", owned)
	}
}

func TestOwnedStocks_SingleInstance(t *testing.T) {
	p, err := partition.New(0, 1)
	require.NoError(t, err)

	allStocks := []string{"BBCA", "GOTO", "TLKM"}
	owned := p.OwnedStocks(allStocks)
	assert.Len(t, owned, len(allStocks),
		"single instance must own all stocks")
}

// ─── Distribution ─────────────────────────────────────────────────────────────

func TestOwnerIndex_ReasonableDistribution(t *testing.T) {
	// With a large stock list, no instance should get 0 stocks (severe imbalance).
	total := 3
	allStocks := []string{
		"BBCA", "BBNI", "BBRI", "BMRI", "BNGA",
		"TLKM", "EXCL", "ISAT", "FREN",
		"ASII", "AALI", "BSDE", "CPIN", "CTRA",
		"GOTO", "BUMI", "UNVR", "ICBP", "KLBF",
		"MNCN", "PGAS", "PTBA", "SMGR", "TBIG",
	}

	counts := make([]int, total)
	for _, s := range allStocks {
		counts[partition.OwnerIndex(s, total)]++
	}

	for idx, cnt := range counts {
		assert.Greater(t, cnt, 0,
			"instance %d has 0 stocks — distribution is too uneven", idx)
		t.Logf("instance %d owns %d stocks", idx, cnt)
	}
}
