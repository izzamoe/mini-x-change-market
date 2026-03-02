// Package simulator provides price feed implementations that inject market data
// into the exchange via the event bus. Two implementations are available:
//
//   - Simulator: internal random-walk generator (always available)
//   - BinanceFeed: optional external real-time prices from Binance public WS
package simulator

import "context"

// PriceFeeder is the common interface for all price data sources.
type PriceFeeder interface {
	// Start begins streaming price updates until ctx is cancelled.
	// The call blocks until the feeder is fully stopped.
	Start(ctx context.Context) error

	// Stop signals the feeder to shut down and waits for completion.
	Stop()
}
