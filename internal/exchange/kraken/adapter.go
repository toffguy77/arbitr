package kraken

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/network"
)

type Adapter struct{ cfg config.Config; http *http.Client }

func New(cfg config.Config) *Adapter { return &Adapter{cfg: cfg, http: network.NewHTTPClient()} }

func (a *Adapter) Name() string { return "kraken" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error { return nil }

func mapSymbol(symbol string) string {
	// Minimal mapping: BTCUSDT -> XBTUSDT; pass-through others
	if strings.EqualFold(symbol, "BTCUSDT") { return "XBTUSDT" }
	return symbol
}

func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
	pair := mapSymbol(symbol)
	url := fmt.Sprintf("%s/0/public/Ticker?pair=%s", a.cfg.Exchanges.Kraken.BaseURL, pair)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := a.http.Do(req)
	if err != nil { return common.Ticker{}, err }
	defer resp.Body.Close()
	var t struct{ Result map[string]struct{ B []string `json:"b"`; A []string `json:"a"` } `json:"result"` }
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil { return common.Ticker{}, err }
	for _, v := range t.Result {
		var bid, ask float64
		if len(v.B) > 0 { _, _ = fmt.Sscan(v.B[0], &bid) }
		if len(v.A) > 0 { _, _ = fmt.Sscan(v.A[0], &ask) }
		return common.Ticker{Bid: bid, Ask: ask}, nil
	}
	return common.Ticker{}, fmt.Errorf("no ticker data")
}

func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	return "", fmt.Errorf("place order not implemented in Phase 1")
}
