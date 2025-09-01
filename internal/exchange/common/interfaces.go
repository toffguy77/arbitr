package common

import "context"

type Ticker struct{ Bid, Ask float64 }

type OrderSide string
const (
	Buy  OrderSide = "buy"
	Sell OrderSide = "sell"
)

type Order struct {
	ID          string // optional client order ID
	Symbol      string
	Side        OrderSide
	Qty         float64
	Price       float64
	TimeInForce string // optional: GTC, IOC, FOK
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

// Optional capability: order status querying
type OrderStatusQuerier interface {
	GetOrderStatus(ctx context.Context, orderID string) (status string, filledQty float64, err error)
}

// Optional capability: order fill info (average fill price and cumulative fee in quote currency)
// avgPrice is the average fill price; cumFee is the cumulative fee amount in the quote currency when available.
// filledQty is the total filled quantity of base asset.
type OrderFillInfoQuerier interface {
	GetOrderFillInfo(ctx context.Context, orderID string) (avgPrice float64, cumFee float64, filledQty float64, err error)
}

// Optional capability: order cancelation
type OrderCanceler interface {
	CancelOrder(ctx context.Context, orderID string) error
}

// Optional capability: symbol metadata (steps)
type SymbolStepper interface {
	GetSymbolSteps(ctx context.Context, symbol string) (qtyStep, priceStep float64, ok bool)
}

// Optional capability: prefetch steps for multiple symbols
type SymbolStepsPrefetcher interface {
	PrefetchSymbolSteps(ctx context.Context, symbols []string) error
}

// Optional capability: symbol filters including minQty and minNotional
type SymbolFiltersProvider interface {
	GetSymbolFilters(ctx context.Context, symbol string) (qtyStep, priceStep, minQty, minNotional float64, ok bool)
}

// Balancer is an optional capability for adapters that can report account balances.
// Adapters that implement this can be queried by the engine to log current balances.
type Balancer interface {
	GetBalances(ctx context.Context) ([]Balance, error)
}

// Optional capability: L2 orderbook provider for slippage estimation
// Implementations should return depth-limited aggregated L2 book for symbol.
// ok=false means not supported or unavailable.
type OrderbookProvider interface {
	GetOrderbookL2(ctx context.Context, symbol string, depth int) (bids [][2]float64, asks [][2]float64, ok bool)
}

// Optional capability: live market feeder (WS subscription)
type LiveMarketFeeder interface {
	SubscribeSymbols(ctx context.Context, symbols []string) error
}
