package entity

// Stock represents a tradeable instrument in the exchange.
type Stock struct {
	Code      string // e.g. "BBCA"
	Name      string // e.g. "Bank BCA"
	BasePrice int64  // reference / starting price (in smallest unit)
	TickSize  int64  // minimum price movement (e.g. 25 for IDX stocks)
}

// defaultStocks is the built-in registry of supported stocks.
var defaultStocks = []Stock{
	{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25},
	{Code: "BBRI", Name: "Bank BRI", BasePrice: 5000, TickSize: 25},
	{Code: "TLKM", Name: "Telkom Indonesia", BasePrice: 3500, TickSize: 25},
	{Code: "ASII", Name: "Astra International", BasePrice: 6000, TickSize: 25},
	{Code: "GOTO", Name: "GoTo Group", BasePrice: 100, TickSize: 1},
}

// binanceStocks are additional stocks fed from the Binance external feed.
var binanceStocks = []Stock{
	{Code: "BTCUSDT", Name: "Bitcoin / USDT", BasePrice: 60000, TickSize: 1},
	{Code: "ETHUSDT", Name: "Ethereum / USDT", BasePrice: 3000, TickSize: 1},
	{Code: "BNBUSDT", Name: "Binance Coin / USDT", BasePrice: 600, TickSize: 1},
	{Code: "SOLUSDT", Name: "Solana / USDT", BasePrice: 150, TickSize: 1},
	{Code: "ADAUSDT", Name: "Cardano / USDT", BasePrice: 1, TickSize: 1},
}

// DefaultStocks returns the list of internally simulated stocks.
func DefaultStocks() []Stock {
	result := make([]Stock, len(defaultStocks))
	copy(result, defaultStocks)
	return result
}

// BinanceStocks returns the list of stocks sourced from Binance.
func BinanceStocks() []Stock {
	result := make([]Stock, len(binanceStocks))
	copy(result, binanceStocks)
	return result
}

// AllStocks returns all supported stocks (internal + Binance).
func AllStocks() []Stock {
	all := make([]Stock, 0, len(defaultStocks)+len(binanceStocks))
	all = append(all, defaultStocks...)
	all = append(all, binanceStocks...)
	return all
}

// stockIndex is a lookup map for fast validation.
var stockIndex map[string]Stock

func init() {
	stockIndex = make(map[string]Stock, len(defaultStocks)+len(binanceStocks))
	for _, s := range defaultStocks {
		stockIndex[s.Code] = s
	}
	for _, s := range binanceStocks {
		stockIndex[s.Code] = s
	}
}

// FindStock returns the stock with the given code, and whether it was found.
func FindStock(code string) (Stock, bool) {
	s, ok := stockIndex[code]
	return s, ok
}

// IsValidStockCode returns true if the stock code is registered.
func IsValidStockCode(code string) bool {
	_, ok := stockIndex[code]
	return ok
}

// RoundToTick rounds a price down to the nearest valid tick size for a stock.
func RoundToTick(price, tickSize int64) int64 {
	if tickSize <= 0 {
		return price
	}
	return (price / tickSize) * tickSize
}
