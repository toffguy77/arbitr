package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

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
	u := fmt.Sprintf("%s/api/v3/ticker/bookTicker?symbol=%s", a.cfg.Exchanges.Binance.BaseURL, url.QueryEscape(strings.ToUpper(symbol)))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil { return common.Ticker{}, err }
	defer func() { _ = resp.Body.Close() }()
	var t struct{ BidPrice string `json:"bidPrice"`; AskPrice string `json:"askPrice"` }
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil { return common.Ticker{}, err }
	var bid, ask float64
	_, _ = fmt.Sscan(t.BidPrice, &bid)
	_, _ = fmt.Sscan(t.AskPrice, &ask)
	return common.Ticker{Bid: decimal.NewFromFloat(bid), Ask: decimal.NewFromFloat(ask)}, nil
}

func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	if a.cfg.Exchanges.Binance.APIKey == "" || a.cfg.Exchanges.Binance.Secret == "" {
		return "", fmt.Errorf("binance api credentials not set")
	}
	sym := strings.ToUpper(ord.Symbol)
	endpoint := "/api/v3/order"
	params := map[string]string{
		"symbol": sym,
		"side": strings.ToUpper(string(ord.Side)), // BUY/SELL
		"type": "LIMIT",
		"timeInForce": "IOC",
	}
	qf, _ := ord.Qty.Float64(); pf, _ := ord.Price.Float64()
	if qf <= 0 || pf <= 0 { return "", fmt.Errorf("qty/price must be > 0") }
	params["quantity"] = trimFloat(qf)
	params["price"] = trimFloat(pf)
	params["timestamp"] = strconv.FormatInt(time.Now().UnixMilli(), 10)
	qs := buildQuery(params)
	sig := sign(a.cfg.Exchanges.Binance.Secret, qs)
	u := a.cfg.Exchanges.Binance.BaseURL + endpoint + "?" + qs + "&signature=" + sig
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	req.Header.Set("X-MBX-APIKEY", a.cfg.Exchanges.Binance.APIKey)
	resp, err := a.http.Do(req)
	if err != nil { return "", err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ OrderId int64 `json:"orderId"`; Code int `json:"code"`; Msg string `json:"msg"` }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return "", err }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.OrderId == 0 {
		return "", fmt.Errorf("binance order error http=%d code=%d msg=%s", resp.StatusCode, r.Code, r.Msg)
	}
	return strconv.FormatInt(r.OrderId, 10), nil
}

// Optional capabilities
func (a *Adapter) GetOrderStatus(ctx context.Context, orderID string) (string, float64, error) {
	if a.cfg.Exchanges.Binance.APIKey == "" || a.cfg.Exchanges.Binance.Secret == "" { return "", 0, fmt.Errorf("binance creds not set") }
	endpoint := "/api/v3/order"
	params := map[string]string{ "orderId": orderID, "timestamp": strconv.FormatInt(time.Now().UnixMilli(), 10) }
	qs := buildQuery(params)
	sig := sign(a.cfg.Exchanges.Binance.Secret, qs)
	u := a.cfg.Exchanges.Binance.BaseURL + endpoint + "?" + qs + "&signature=" + sig
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-MBX-APIKEY", a.cfg.Exchanges.Binance.APIKey)
	resp, err := a.http.Do(req)
	if err != nil { return "", 0, err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Status string `json:"status"`; ExecutedQty string `json:"executedQty"` }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return "", 0, err }
	var f float64
	_, _ = fmt.Sscan(r.ExecutedQty, &f)
	return r.Status, f, nil
}

func (a *Adapter) GetOrderFillInfo(ctx context.Context, orderID string) (float64, float64, float64, error) {
	if a.cfg.Exchanges.Binance.APIKey == "" || a.cfg.Exchanges.Binance.Secret == "" { return 0,0,0, fmt.Errorf("binance creds not set") }
	endpoint := "/api/v3/order"
	params := map[string]string{ "orderId": orderID, "timestamp": strconv.FormatInt(time.Now().UnixMilli(), 10) }
	qs := buildQuery(params)
	sig := sign(a.cfg.Exchanges.Binance.Secret, qs)
	u := a.cfg.Exchanges.Binance.BaseURL + endpoint + "?" + qs + "&signature=" + sig
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-MBX-APIKEY", a.cfg.Exchanges.Binance.APIKey)
	resp, err := a.http.Do(req)
	if err != nil { return 0,0,0, err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Price string `json:"price"`; ExecutedQty string `json:"executedQty"` }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return 0,0,0, err }
	var avg, filled float64
	_, _ = fmt.Sscan(r.Price, &avg)
	_, _ = fmt.Sscan(r.ExecutedQty, &filled)
	return avg, 0, filled, nil
}

func (a *Adapter) GetOrderbookL2(ctx context.Context, symbol string, depth int) ([][2]float64, [][2]float64, bool) {
	if depth <= 0 { depth = 10 }
	u := fmt.Sprintf("%s/api/v3/depth?symbol=%s&limit=%d", a.cfg.Exchanges.Binance.BaseURL, url.QueryEscape(strings.ToUpper(symbol)), depth)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil { return nil, nil, false }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Bids [][]string `json:"bids"`; Asks [][]string `json:"asks"` }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return nil, nil, false }
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	b2 := make([][2]float64, 0, len(r.Bids))
	for _, lv := range r.Bids { if len(lv) >= 2 { b2 = append(b2, [2]float64{parse(lv[0]), parse(lv[1])}) } }
	a2 := make([][2]float64, 0, len(r.Asks))
	for _, lv := range r.Asks { if len(lv) >= 2 { a2 = append(a2, [2]float64{parse(lv[0]), parse(lv[1])}) } }
	return b2, a2, true
}

func (a *Adapter) GetSymbolFilters(ctx context.Context, symbol string) (float64, float64, float64, float64, bool) {
	u := fmt.Sprintf("%s/api/v3/exchangeInfo?symbol=%s", a.cfg.Exchanges.Binance.BaseURL, url.QueryEscape(strings.ToUpper(symbol)))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil { return 0,0,0,0,false }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Symbols []struct{ Filters []struct{ FilterType string `json:"filterType"`; StepSize string `json:"stepSize"`; TickSize string `json:"tickSize"`; MinQty string `json:"minQty"`; MinNotional string `json:"minNotional"` } `json:"filters"` } `json:"symbols"` }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return 0,0,0,0,false }
	if len(r.Symbols) == 0 { return 0,0,0,0,false }
	var qtyStep, priceStep, minQty, minNotional float64
	for _, f := range r.Symbols[0].Filters {
		switch strings.ToUpper(f.FilterType) {
		case "LOT_SIZE":
			_, _ = fmt.Sscan(f.StepSize, &qtyStep)
			_, _ = fmt.Sscan(f.MinQty, &minQty)
		case "PRICE_FILTER":
			_, _ = fmt.Sscan(f.TickSize, &priceStep)
		case "MIN_NOTIONAL":
			_, _ = fmt.Sscan(f.MinNotional, &minNotional)
		}
	}
	return qtyStep, priceStep, minQty, minNotional, true
}

// helpers
func sign(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}
func buildQuery(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m { keys = append(keys, k) }
	sort.Strings(keys)
	vals := make([]string, 0, len(keys))
	for _, k := range keys { vals = append(vals, k+"="+url.QueryEscape(m[k])) }
	return strings.Join(vals, "&")
}
func trimFloat(f float64) string { return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.12f", f), "0"), ".") }
