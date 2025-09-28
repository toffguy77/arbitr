package okx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
	"arbitr/internal/infra/network"
)

type Adapter struct {
	cfg    config.Config
	http   *http.Client
	logger log.Logger
}

func New(cfg config.Config, logger log.Logger) *Adapter {
	return &Adapter{cfg: cfg, http: network.NewHTTPClient(), logger: logger.With().Str("exchange", "okx").Logger()}
}

func (a *Adapter) Name() string                    { return "okx" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error  { return nil }

func (a *Adapter) toInstID(sym string) string {
	s := strings.ToUpper(sym)
	if strings.Contains(s, "-") { return s }
	if strings.HasSuffix(s, "USDT") && len(s) > 4 {
		return s[:len(s)-4] + "-" + s[len(s)-4:]
	}
	if len(s) > 3 {
		return s[:len(s)-3] + "-" + s[len(s)-3:]
	}
	return s
}

// GetTicker fetches best bid/ask from OKX public API v5.
func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
	inst := a.toInstID(symbol)
	u := fmt.Sprintf("%s/api/v5/market/ticker?instId=%s", a.cfg.Exchanges.Okx.BaseURL, url.QueryEscape(inst))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil { return common.Ticker{}, err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct{
			Bid string `json:"bidPx"`
			Ask string `json:"askPx"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return common.Ticker{}, err }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.Code != "0" || len(r.Data) == 0 {
		return common.Ticker{}, fmt.Errorf("okx ticker http=%d code=%s msg=%s", resp.StatusCode, r.Code, r.Msg)
	}
	var bid, ask float64
	_, _ = fmt.Sscan(r.Data[0].Bid, &bid)
	_, _ = fmt.Sscan(r.Data[0].Ask, &ask)
	return common.Ticker{Bid: decimal.NewFromFloat(bid), Ask: decimal.NewFromFloat(ask)}, nil
}

// PlaceOrder submits spot IOC limit order on OKX.
func (a *Adapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	if a.cfg.Exchanges.Okx.APIKey == "" || a.cfg.Exchanges.Okx.Secret == "" || a.cfg.Exchanges.Okx.Passphrase == "" {
		return "", fmt.Errorf("okx api credentials not set")
	}
	inst := a.toInstID(ord.Symbol)
	endpoint := "/api/v5/trade/order"
	body := map[string]string{
		"instId": inst,
		"tdMode": "cash",
		"side":  strings.ToLower(string(ord.Side)), // buy/sell
		"ordType": "ioc",
	}
	qf, _ := ord.Qty.Float64()
	pf, _ := ord.Price.Float64()
	if qf <= 0 || pf <= 0 { return "", fmt.Errorf("qty/price must be > 0") }
	body["sz"] = trimFloat(qf)
	body["px"] = trimFloat(pf)
	b, _ := json.Marshal(body)
	// sign
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	prehash := ts + http.MethodPost + endpoint + string(b)
	signature := okxSign(a.cfg.Exchanges.Okx.Secret, prehash)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Exchanges.Okx.BaseURL+endpoint, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OK-ACCESS-KEY", a.cfg.Exchanges.Okx.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
	req.Header.Set("OK-ACCESS-PASSPHRASE", a.cfg.Exchanges.Okx.Passphrase)
	resp, err := a.http.Do(req)
	if err != nil { return "", err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct{
			OrdId string `json:"ordId"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return "", err }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.Code != "0" || len(r.Data) == 0 {
		return "", fmt.Errorf("okx order error http=%d code=%s msg=%s", resp.StatusCode, r.Code, r.Msg)
	}
	return r.Data[0].OrdId, nil
}

// Optional: Order status
func (a *Adapter) GetOrderStatus(ctx context.Context, orderID string) (string, float64, error) {
	endpoint := "/api/v5/trade/order"
	u := endpoint + "?ordId=" + url.QueryEscape(orderID)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	prehash := ts + http.MethodGet + u
	signature := okxSign(a.cfg.Exchanges.Okx.Secret, prehash)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Exchanges.Okx.BaseURL+u, nil)
	req.Header.Set("OK-ACCESS-KEY", a.cfg.Exchanges.Okx.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
	req.Header.Set("OK-ACCESS-PASSPHRASE", a.cfg.Exchanges.Okx.Passphrase)
	resp, err := a.http.Do(req)
	if err != nil { return "", 0, err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Code, Msg string; Data []struct{ State string `json:"state"`; AccFillSz string `json:"accFillSz"` } }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return "", 0, err }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.Code != "0" || len(r.Data) == 0 { return "", 0, fmt.Errorf("okx status http=%d code=%s msg=%s", resp.StatusCode, r.Code, r.Msg) }
	var f float64
	_, _ = fmt.Sscan(r.Data[0].AccFillSz, &f)
	return r.Data[0].State, f, nil
}

// Optional: Fill info
func (a *Adapter) GetOrderFillInfo(ctx context.Context, orderID string) (float64, float64, float64, error) {
	endpoint := "/api/v5/trade/order"
	u := endpoint + "?ordId=" + url.QueryEscape(orderID)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	prehash := ts + http.MethodGet + u
	signature := okxSign(a.cfg.Exchanges.Okx.Secret, prehash)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Exchanges.Okx.BaseURL+u, nil)
	req.Header.Set("OK-ACCESS-KEY", a.cfg.Exchanges.Okx.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
	req.Header.Set("OK-ACCESS-PASSPHRASE", a.cfg.Exchanges.Okx.Passphrase)
	resp, err := a.http.Do(req)
	if err != nil { return 0, 0, 0, err }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Code, Msg string; Data []struct{ AvgPx string `json:"avgPx"`; AccFillSz string `json:"accFillSz"`; Fee string `json:"fee"` } }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return 0, 0, 0, err }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.Code != "0" || len(r.Data) == 0 { return 0, 0, 0, fmt.Errorf("okx fill http=%d code=%s msg=%s", resp.StatusCode, r.Code, r.Msg) }
	var avg, fee, f float64
	_, _ = fmt.Sscan(r.Data[0].AvgPx, &avg)
	_, _ = fmt.Sscan(r.Data[0].AccFillSz, &f)
	_, _ = fmt.Sscan(r.Data[0].Fee, &fee)
	fee = -fee // OKX returns negative fee for paid
	return avg, fee, f, nil
}

// Orderbook (L2)
func (a *Adapter) GetOrderbookL2(ctx context.Context, symbol string, depth int) ([][2]float64, [][2]float64, bool) {
	if depth <= 0 { depth = 10 }
	inst := a.toInstID(symbol)
	u := fmt.Sprintf("%s/api/v5/market/books?instId=%s&sz=%d", a.cfg.Exchanges.Okx.BaseURL, url.QueryEscape(inst), depth)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil { return nil, nil, false }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct{ Asks [][]string `json:"asks"`; Bids [][]string `json:"bids"` } `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return nil, nil, false }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.Code != "0" || len(r.Data) == 0 { return nil, nil, false }
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	b2 := make([][2]float64, 0, len(r.Data[0].Bids))
	for _, lv := range r.Data[0].Bids {
		if len(lv) >= 2 { b2 = append(b2, [2]float64{parse(lv[0]), parse(lv[1])}) }
	}
	a2 := make([][2]float64, 0, len(r.Data[0].Asks))
	for _, lv := range r.Data[0].Asks {
		if len(lv) >= 2 { a2 = append(a2, [2]float64{parse(lv[0]), parse(lv[1])}) }
	}
	return b2, a2, true
}

// Symbol filters best-effort from instruments endpoint
func (a *Adapter) GetSymbolFilters(ctx context.Context, symbol string) (float64, float64, float64, float64, bool) {
	inst := a.toInstID(symbol)
	u := fmt.Sprintf("%s/api/v5/public/instruments?instType=SPOT&instId=%s", a.cfg.Exchanges.Okx.BaseURL, url.QueryEscape(inst))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil { return 0,0,0,0,false }
	defer func(){ _ = resp.Body.Close() }()
	var r struct{ Code, Msg string; Data []struct{ LotSz string `json:"lotSz"`; TickSz string `json:"tickSz"` } }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return 0,0,0,0,false }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.Code != "0" || len(r.Data) == 0 { return 0,0,0,0,false }
	var lot, tick float64
	_, _ = fmt.Sscan(r.Data[0].LotSz, &lot)
	_, _ = fmt.Sscan(r.Data[0].TickSz, &tick)
	return lot, tick, 0, 0, true
}

// helpers
func okxSign(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.12f", f), "0"), ".")
}
