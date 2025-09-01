package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/network"
)

type Adapter struct{ cfg config.Config; http *http.Client }

func New(cfg config.Config) *Adapter { return &Adapter{cfg: cfg, http: network.NewHTTPClient()} }

func (a *Adapter) Name() string { return "binance" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error { return nil }

func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
	url := fmt.Sprintf("%s/api/v3/ticker/bookTicker?symbol=%s", a.cfg.Exchanges.Binance.BaseURL, symbol)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := a.http.Do(req)
	if err != nil { return common.Ticker{}, err }
	defer func() { _ = resp.Body.Close() }()
	var t struct{ BidPrice string `json:"bidPrice"`; AskPrice string `json:"askPrice"` }
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil { return common.Ticker{}, err }
	var bid, ask float64
	_, _ = fmt.Sscan(t.BidPrice, &bid)
	_, _ = fmt.Sscan(t.AskPrice, &ask)
	return common.Ticker{Bid: bid, Ask: ask}, nil
}

func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	// Live trading not enabled in Phase 1; will implement signed REST when TRADING_ENABLED is true and requested.
	return "", fmt.Errorf("place order not implemented in Phase 1")
}
