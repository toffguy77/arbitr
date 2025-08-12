package bybit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
	"arbitr/internal/infra/network"
)

type Adapter struct{
	cfg    config.Config
	http   *http.Client
	logger log.Logger
	mu     sync.RWMutex
	steps  map[string]struct{ qtyStep, priceStep float64 }
}

func New(cfg config.Config, logger log.Logger) *Adapter {
	return &Adapter{cfg: cfg, http: network.NewHTTPClient(), logger: logger.With().Str("exchange", "bybit").Logger(), steps: make(map[string]struct{ qtyStep, priceStep float64 })}
}

func (a *Adapter) Name() string { return "bybit" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error { return nil }

// ListSpotSymbols returns a set of available spot symbols on Bybit.
func (a *Adapter) ListSpotSymbols(ctx context.Context) (map[string]struct{}, error) {
	u := fmt.Sprintf("%s/v5/market/instruments-info?category=spot", a.cfg.Exchanges.Bybit.BaseURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Msg("http request failed")
		return nil, err
	}
	defer resp.Body.Close()
	var out = map[string]struct{}{}
	var r struct{
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct{ List []struct{ Symbol string `json:"symbol"` } `json:"list"` } `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit instruments-info response")
	if decErr != nil { return nil, decErr }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, fmt.Errorf("bybit instruments http %d", resp.StatusCode) }
	if r.RetCode != 0 { return nil, fmt.Errorf("bybit instruments error: %s", r.RetMsg) }
	for _, it := range r.Result.List { out[it.Symbol] = struct{}{} }
	return out, nil
}

// GetTicker fetches best bid/ask from Bybit public API v5 spot tickers.
// Assumes spot category and symbols like BTCUSDT.
func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
	u := fmt.Sprintf("%s/v5/market/tickers?category=spot&symbol=%s", a.cfg.Exchanges.Bybit.BaseURL, symbol)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil { a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Str("symbol", symbol).Msg("http request failed"); return common.Ticker{}, err }
	defer resp.Body.Close()
	var t struct{
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct{ List []struct{ Bid1 string `json:"bid1Price"`; Ask1 string `json:"ask1Price"` } `json:"list"` } `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&t)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Str("symbol", symbol).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", t.RetCode).Str("retMsg", t.RetMsg).Msg("bybit tickers response")
	if decErr != nil { return common.Ticker{}, decErr }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return common.Ticker{}, fmt.Errorf("bybit tickers http %d", resp.StatusCode) }
	if t.RetCode != 0 { return common.Ticker{}, fmt.Errorf("bybit tickers error: %s", t.RetMsg) }
	if len(t.Result.List) == 0 { return common.Ticker{}, fmt.Errorf("no ticker data") }
	var bid, ask float64
	_, _ = fmt.Sscan(t.Result.List[0].Bid1, &bid)
	_, _ = fmt.Sscan(t.Result.List[0].Ask1, &ask)
	return common.Ticker{Bid: bid, Ask: ask}, nil
}

func sign(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	if a.cfg.Exchanges.Bybit.APIKey == "" || a.cfg.Exchanges.Bybit.Secret == "" {
		return "", fmt.Errorf("bybit api credentials not set")
	}
	// Bybit v5 private POST signing requires: sign(HMAC_SHA256(secret, timestamp + apiKey + recvWindow + body))
	endpoint := "/v5/order/create"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recv := "5000"
	side := strings.ToUpper(string(ord.Side)) // BUY/SELL
	// Build JSON body with string fields per Bybit expectations
	body := map[string]string{
		"category":    "spot",
		"symbol":      ord.Symbol,
		"side":        side,
		"orderType":   "Limit",
		"qty":         fmt.Sprintf("%f", ord.Qty),
		"price":       fmt.Sprintf("%f", ord.Price),
		"timeInForce": "GTC",
	}
	b, err := json.Marshal(body)
	if err != nil { return "", err }
	payload := ts + a.cfg.Exchanges.Bybit.APIKey + recv + string(b)
	signature := sign(a.cfg.Exchanges.Bybit.Secret, payload)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Exchanges.Bybit.BaseURL+endpoint, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BAPI-API-KEY", a.cfg.Exchanges.Bybit.APIKey)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recv)
	req.Header.Set("X-BAPI-SIGN", signature)
	// Explicitly set sign type to HMAC-SHA256 (2)
	req.Header.Set("X-BAPI-SIGN-TYPE", "2")

	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodPost).Str("endpoint", endpoint).Str("symbol", ord.Symbol).Str("side", side).Float64("qty", ord.Qty).Float64("price", ord.Price).Msg("http request failed")
		return "", err
	}
	defer resp.Body.Close()
	var r struct{ RetCode int `json:"retCode"`; RetMsg string `json:"retMsg"`; Result struct{ OrderId string `json:"orderId"` } `json:"result"` }
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodPost).Str("endpoint", endpoint).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Str("symbol", ord.Symbol).Str("side", side).Float64("qty", ord.Qty).Float64("price", ord.Price).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit order create response")
	if decErr != nil { return "", decErr }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return "", fmt.Errorf("bybit place order http %d", resp.StatusCode) }
	if r.RetCode != 0 { return "", fmt.Errorf("bybit place order error: %s", r.RetMsg) }
return r.Result.OrderId, nil
}

// GetSymbolSteps returns lot size (qty) and price tick steps for a spot symbol, cached.
func (a *Adapter) GetSymbolSteps(ctx context.Context, symbol string) (float64, float64, bool) {
	a.mu.RLock()
	if s, ok := a.steps[symbol]; ok {
		a.mu.RUnlock()
		return s.qtyStep, s.priceStep, true
	}
	a.mu.RUnlock()
	// fetch from instruments-info for specific symbol
	u := fmt.Sprintf("%s/v5/market/instruments-info?category=spot&symbol=%s", a.cfg.Exchanges.Bybit.BaseURL, symbol)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Msg("http request failed")
		return 0, 0, false
	}
	defer resp.Body.Close()
	var r struct{
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct{ List []struct{
			Symbol string `json:"symbol"`
			LotSizeFilter struct{
				BasePrecision string `json:"basePrecision"`
				StepSize      string `json:"stepSize"`
			} `json:"lotSizeFilter"`
			PriceFilter struct{
				TickSize string `json:"tickSize"`
			} `json:"priceFilter"`
		} `json:"list"` } `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit instruments-info response")
	if decErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || r.RetCode != 0 || len(r.Result.List) == 0 {
		return 0, 0, false
	}
	it := r.Result.List[0]
	// parse steps from strings
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	qtyStep := parse(it.LotSizeFilter.StepSize)
	if qtyStep == 0 { qtyStep = parse(it.LotSizeFilter.BasePrecision) }
	priceStep := parse(it.PriceFilter.TickSize)
	if priceStep == 0 { return 0, 0, false }
	a.mu.Lock()
	a.steps[symbol] = struct{ qtyStep, priceStep float64 }{qtyStep: qtyStep, priceStep: priceStep}
	a.mu.Unlock()
	return qtyStep, priceStep, true
}

// PrefetchSymbolSteps fetches steps for many symbols in one call and caches them.
func (a *Adapter) PrefetchSymbolSteps(ctx context.Context, symbols []string) error {
	want := map[string]struct{}{}
	for _, s := range symbols { if s != "" { want[s] = struct{}{} } }
	if len(want) == 0 { return nil }
	u := fmt.Sprintf("%s/v5/market/instruments-info?category=spot", a.cfg.Exchanges.Bybit.BaseURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Msg("http request failed")
		return err
	}
	defer resp.Body.Close()
	var r struct{
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct{ List []struct{
			Symbol string `json:"symbol"`
			LotSizeFilter struct{ StepSize string `json:"stepSize"`; BasePrecision string `json:"basePrecision"` } `json:"lotSizeFilter"`
			PriceFilter   struct{ TickSize string `json:"tickSize"` } `json:"priceFilter"`
		} `json:"list"` } `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit instruments-info response")
	if decErr != nil { return decErr }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return fmt.Errorf("bybit instruments http %d", resp.StatusCode) }
	if r.RetCode != 0 { return fmt.Errorf("bybit instruments error: %s", r.RetMsg) }
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, it := range r.Result.List {
		if _, ok := want[it.Symbol]; !ok { continue }
		q := parse(it.LotSizeFilter.StepSize)
		if q == 0 { q = parse(it.LotSizeFilter.BasePrecision) }
		p := parse(it.PriceFilter.TickSize)
		if p == 0 { continue }
		a.steps[it.Symbol] = struct{ qtyStep, priceStep float64 }{qtyStep: q, priceStep: p}
	}
	return nil
}

// GetBalances implements common.Balancer for Bybit unified account (spot).
// It signs a GET request per v5 private REST rules: sign(timestamp + apiKey + recvWindow + queryString).
func (a *Adapter) GetBalances(ctx context.Context) ([]common.Balance, error) {
	if a.cfg.Exchanges.Bybit.APIKey == "" || a.cfg.Exchanges.Bybit.Secret == "" {
		return nil, fmt.Errorf("bybit api credentials not set")
	}
	endpoint := "/v5/account/wallet-balance"
	recv := "5000"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	q := url.Values{}
	q.Set("accountType", "UNIFIED")
	qs := q.Encode()
	payload := ts + a.cfg.Exchanges.Bybit.APIKey + recv + qs
	signature := sign(a.cfg.Exchanges.Bybit.Secret, payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Exchanges.Bybit.BaseURL+endpoint+"?"+qs, nil)
	req.Header.Set("X-BAPI-API-KEY", a.cfg.Exchanges.Bybit.APIKey)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recv)
	req.Header.Set("X-BAPI-SIGN", signature)
	req.Header.Set("X-BAPI-SIGN-TYPE", "2")
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil { a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("endpoint", endpoint).Msg("http request failed"); return nil, err }
	defer resp.Body.Close()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct{
			List []struct{
				// Bybit v5 wallet-balance returns list entries that contain a nested array under key "coin".
				Coin []struct{
					Coin                 string `json:"coin"`
					WalletBalance        string `json:"walletBalance"`
					AvailableToWithdraw  string `json:"availableToWithdraw"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("endpoint", endpoint).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit wallet-balance response")
	if decErr != nil { return nil, decErr }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, fmt.Errorf("bybit get balances http %d", resp.StatusCode) }
	if r.RetCode != 0 { return nil, fmt.Errorf("bybit get balances error: %s", r.RetMsg) }
	out := make([]common.Balance, 0)
	for _, acc := range r.Result.List {
		for _, it := range acc.Coin {
			var free float64
			// prefer AvailableToWithdraw; fallback to WalletBalance
			if it.AvailableToWithdraw != "" {
				_, _ = fmt.Sscan(it.AvailableToWithdraw, &free)
			} else if it.WalletBalance != "" {
				_, _ = fmt.Sscan(it.WalletBalance, &free)
			}
			out = append(out, common.Balance{Asset: strings.ToUpper(it.Coin), Free: free})
		}
	}
	return out, nil
}
