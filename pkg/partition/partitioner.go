// Package partition implements consistent-hash partitioning for horizontal scaling.
//
// The algorithm uses FNV-1a (64-bit) to deterministically map each stock code
// to one of N instances.  The mapping is stable: as long as totalInstances does
// not change, every stock always maps to the same instance index.
//
// Usage:
//
//	p, _ := partition.New(0, 3)      // I am instance 0 out of 3
//	if p.OwnsStock("BBCA") { ... }   // run the matching engine for BBCA
package partition

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// Partitioner determines which instance owns (i.e. runs the matching engine
// for) each stock code.
type Partitioner struct {
	instanceIndex  int // 0-based index of THIS instance
	totalInstances int
}

// New creates a Partitioner for this instance.
//   - instanceIndex: 0-based position of this instance in the cluster (0 … N-1)
//   - totalInstances: total number of running instances
func New(instanceIndex, totalInstances int) (*Partitioner, error) {
	if totalInstances < 1 {
		return nil, fmt.Errorf("partition: totalInstances must be >= 1, got %d", totalInstances)
	}
	if instanceIndex < 0 || instanceIndex >= totalInstances {
		return nil, fmt.Errorf("partition: instanceIndex %d out of range [0, %d)", instanceIndex, totalInstances)
	}
	return &Partitioner{
		instanceIndex:  instanceIndex,
		totalInstances: totalInstances,
	}, nil
}

// OwnsStock returns true if THIS instance is responsible for stockCode.
func (p *Partitioner) OwnsStock(stockCode string) bool {
	return OwnerIndex(stockCode, p.totalInstances) == p.instanceIndex
}

// OwnerIndex returns the 0-based index of the instance that owns stockCode.
// This is a pure, stateless function useful for routing decisions.
func OwnerIndex(stockCode string, totalInstances int) int {
	if totalInstances <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(stockCode))
	return int(h.Sum64() % uint64(totalInstances))
}

// OwnedStocks filters allStocks and returns the subset owned by this instance.
// The result is sorted for determinism.
func (p *Partitioner) OwnedStocks(allStocks []string) []string {
	owned := make([]string, 0, len(allStocks)/p.totalInstances+1)
	for _, s := range allStocks {
		if p.OwnsStock(s) {
			owned = append(owned, s)
		}
	}
	sort.Strings(owned)
	return owned
}

// InstanceIndex returns the 0-based index of this instance.
func (p *Partitioner) InstanceIndex() int { return p.instanceIndex }

// TotalInstances returns the total number of instances in the cluster.
func (p *Partitioner) TotalInstances() int { return p.totalInstances }
