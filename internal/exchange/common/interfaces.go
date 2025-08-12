package common

import "context"

type Ticker struct{ Bid, Ask float64 }

type OrderSide string
const (
	Buy  OrderSide = "buy"
	Sell OrderSide = "sell"
)

type Order struct {
	ID     string
	Symbol string
	Side   OrderSide
	Qty    float64
	Price  float64
}

// Balance represents a simple asset balance on an exchange account (free funds available for trading)
type Balance struct {
	Asset string
	Free  float64
}

type ExchangeAdapter interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	GetTicker(ctx context.Context, symbol string) (Ticker, error)
	PlaceOrder(ctx context.Context, ord Order) (string, error)
}

// Optional capability: symbol metadata (steps)
type SymbolStepper interface {
	GetSymbolSteps(ctx context.Context, symbol string) (qtyStep, priceStep float64, ok bool)
}

// Optional capability: prefetch steps for multiple symbols
type SymbolStepsPrefetcher interface {
	PrefetchSymbolSteps(ctx context.Context, symbols []string) error
}

// Balancer is an optional capability for adapters that can report account balances.
// Adapters that implement this can be queried by the engine to log current balances.
type Balancer interface {
	GetBalances(ctx context.Context) ([]Balance, error)
}
