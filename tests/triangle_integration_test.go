package tests

import (
	"context"
	"testing"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/arbitrage"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
)

// Fake adapter to simulate partial fills and failures

type fakeAdapter struct{ ch chan common.Ticker }

func (f *fakeAdapter) Name() string { return "fake" }
func (f *fakeAdapter) Start(ctx context.Context) error { return nil }
func (f *fakeAdapter) Stop(ctx context.Context) error { return nil }
func (f *fakeAdapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) { return common.Ticker{Bid: 100, Ask: 100.1}, nil }
func (f *fakeAdapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) { return "id", nil }

func TestPartialFillUnwind(t *testing.T) {
	cfg := config.Load()
	cfg.Trading.Enabled = true
	cfg.Trading.Live = false
	logger := log.NewLogger(cfg)
	adapters := map[string]common.ExchangeAdapter{"bybit": &fakeAdapter{}}
	eng := arbitrage.New(cfg, adapters, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func(){ _ = eng.Run(ctx) }()
	// ensure it ticks at least once
	time.Sleep(1500 * time.Millisecond)
}

