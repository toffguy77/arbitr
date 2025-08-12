package bybit

import (
	"context"
	"arbitr/internal/exchange/common"
)

type Adapter struct{}

func (a *Adapter) Name() string { return "bybit" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error { return nil }
func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) { return common.Ticker{Bid:0, Ask:0}, nil }
func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) { return "", nil }
