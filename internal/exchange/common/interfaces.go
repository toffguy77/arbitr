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

type ExchangeAdapter interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	GetTicker(ctx context.Context, symbol string) (Ticker, error)
	PlaceOrder(ctx context.Context, ord Order) (string, error)
}
