package bybit

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

func (a *Adapter) Name() string { return "bybit" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error { return nil }

// ListSpotSymbols returns a set of available spot symbols on Bybit.
func (a *Adapter) ListSpotSymbols(ctx context.Context) (map[string]struct{}, error) {
	url := fmt.Sprintf("%s/v5/market/instruments-info?category=spot", a.cfg.Exchanges.Bybit.BaseURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := a.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var out = map[string]struct{}{}
	var r struct{
		RetCode int `json:"retCode"`
		Result struct{ List []struct{ Symbol string `json:"symbol"` } `json:"list"` } `json:"result"`
	}
if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return nil, err }
	for _, it := range r.Result.List { out[it.Symbol] = struct{}{} }
	return out, nil
}

// GetTicker fetches best bid/ask from Bybit public API v5 spot tickers.
// Assumes spot category and symbols like BTCUSDT.
func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
url := fmt.Sprintf("%s/v5/market/tickers?category=spot&symbol=%s", a.cfg.Exchanges.Bybit.BaseURL, symbol)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := a.http.Do(req)
	if err != nil { return common.Ticker{}, err }
	defer resp.Body.Close()
	var t struct{
		RetCode int `json:"retCode"`
		Result struct{ List []struct{ Bid1 string `json:"bid1Price"`; Ask1 string `json:"ask1Price"` } `json:"list"` } `json:"result"`
	}
if err := json.NewDecoder(resp.Body).Decode(&t); err != nil { return common.Ticker{}, err }
	if len(t.Result.List) == 0 { return common.Ticker{}, fmt.Errorf("no ticker data") }
	var bid, ask float64
_, _ = fmt.Sscan(t.Result.List[0].Bid1, &bid)
	_, _ = fmt.Sscan(t.Result.List[0].Ask1, &ask)
	return common.Ticker{Bid: bid, Ask: ask}, nil
}

func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	return "", fmt.Errorf("place order not implemented in Phase 1")
}
