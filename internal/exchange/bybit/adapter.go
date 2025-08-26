package bybit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/infra/network"
)

type Adapter struct {
	cfg     config.Config
	http    *http.Client
	logger  log.Logger
	mu      sync.RWMutex
	steps   map[string]struct{ qtyStep, priceStep float64 }
	filters map[string]struct{ qtyStep, priceStep, minQty, minNotional float64 }
	// Live caches
	tickers map[string]common.Ticker
	books   map[string]struct{ bids, asks [][2]float64 }
	wsConn  *websocket.Conn
	lastWS  time.Time
}

func New(cfg config.Config, logger log.Logger) *Adapter {
	return &Adapter{cfg: cfg, http: network.NewHTTPClient(), logger: logger.With().Str("exchange", "bybit").Logger(), steps: make(map[string]struct{ qtyStep, priceStep float64 }), filters: make(map[string]struct{ qtyStep, priceStep, minQty, minNotional float64 }), tickers: make(map[string]common.Ticker), books: make(map[string]struct{ bids, asks [][2]float64 })}
}

func (a *Adapter) Name() string                    { return "bybit" }
func (a *Adapter) Start(ctx context.Context) error { return nil }
func (a *Adapter) Stop(ctx context.Context) error  { return nil }

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
	defer func() { _ = resp.Body.Close() }()
	var out = map[string]struct{}{}
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol string `json:"symbol"`
			} `json:"list"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit instruments-info response")
	if decErr != nil {
		return nil, decErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bybit instruments http %d", resp.StatusCode)
	}
	if r.RetCode != 0 {
		return nil, fmt.Errorf("bybit instruments error: %s", r.RetMsg)
	}
	for _, it := range r.Result.List {
		out[it.Symbol] = struct{}{}
	}
	return out, nil
}

// GetSymbolFilters returns steps and min filters (best-effort parsing from instruments-info)
func (a *Adapter) GetSymbolFilters(ctx context.Context, symbol string) (float64, float64, float64, float64, bool) {
	a.mu.RLock()
	if f, ok := a.filters[symbol]; ok {
		a.mu.RUnlock()
		return f.qtyStep, f.priceStep, f.minQty, f.minNotional, true
	}
	a.mu.RUnlock()
	u := fmt.Sprintf("%s/v5/market/instruments-info?category=spot&symbol=%s", a.cfg.Exchanges.Bybit.BaseURL, symbol)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Result struct {
			List []struct {
				LotSizeFilter struct {
					StepSize      string `json:"stepSize"`
					BasePrecision string `json:"basePrecision"`
					MinOrderQty   string `json:"minOrderQty"`
				} `json:"lotSizeFilter"`
				PriceFilter struct {
					TickSize    string `json:"tickSize"`
					MinOrderAmt string `json:"minOrderAmt"`
				} `json:"priceFilter"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, 0, 0, 0, false
	}
	if len(r.Result.List) == 0 {
		return 0, 0, 0, 0, false
	}
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	it := r.Result.List[0]
	qs := parse(it.LotSizeFilter.StepSize)
	if qs == 0 {
		qs = parse(it.LotSizeFilter.BasePrecision)
	}
	ps := parse(it.PriceFilter.TickSize)
	minQty := parse(it.LotSizeFilter.MinOrderQty)
	minNotional := parse(it.PriceFilter.MinOrderAmt)
	a.mu.Lock()
	a.filters[symbol] = struct{ qtyStep, priceStep, minQty, minNotional float64 }{qtyStep: qs, priceStep: ps, minQty: minQty, minNotional: minNotional}
	a.mu.Unlock()
	return qs, ps, minQty, minNotional, true
}

// GetTicker fetches best bid/ask from Bybit public API v5 spot tickers.
// Assumes spot category and symbols like BTCUSDT.
func (a *Adapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
	// prefer WS cache
	a.mu.RLock()
	if t, ok := a.tickers[symbol]; ok && t.Bid > 0 && t.Ask > 0 {
		a.mu.RUnlock()
		return t, nil
	}
	a.mu.RUnlock()
	// derive from WS orderbook top of book if available
	a.mu.RLock()
	if bk, ok := a.books[symbol]; ok && len(bk.bids) > 0 && len(bk.asks) > 0 {
		bid := bk.bids[0][0]
		ask := bk.asks[0][0]
		a.mu.RUnlock()
		if bid > 0 && ask > 0 {
			return common.Ticker{Bid: bid, Ask: ask}, nil
		}
	} else {
		a.mu.RUnlock()
	}
	u := fmt.Sprintf("%s/v5/market/tickers?category=spot&symbol=%s", a.cfg.Exchanges.Bybit.BaseURL, symbol)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Str("symbol", symbol).Msg("http request failed")
		return common.Ticker{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var t struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Bid1 string `json:"bid1Price"`
				Ask1 string `json:"ask1Price"`
			} `json:"list"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&t)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Str("symbol", symbol).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", t.RetCode).Str("retMsg", t.RetMsg).Msg("bybit tickers response")
	if decErr != nil {
		return common.Ticker{}, decErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return common.Ticker{}, fmt.Errorf("bybit tickers http %d", resp.StatusCode)
	}
	if t.RetCode != 0 {
		return common.Ticker{}, fmt.Errorf("bybit tickers error: %s", t.RetMsg)
	}
	if len(t.Result.List) == 0 {
		return common.Ticker{}, fmt.Errorf("no ticker data")
	}
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
	// Enforce filters/steps as last-mile safety
	roundDown := func(x, step float64) float64 {
		if step <= 0 {
			return x
		}
		return math.Floor(x/step) * step
	}
	if fp, ok := any(a).(common.SymbolFiltersProvider); ok {
		ctxS, cancelS := context.WithTimeout(ctx, 2*time.Second)
		if qs, ps, mq, mn, ok2 := fp.GetSymbolFilters(ctxS, ord.Symbol); ok2 {
			ord.Qty = roundDown(ord.Qty, qs)
			ord.Price = roundDown(ord.Price, ps)
			if ord.Qty <= 0 || ord.Price <= 0 {
				cancelS()
				return "", fmt.Errorf("qty/price invalid after rounding")
			}
			if mq > 0 && ord.Qty < mq {
				cancelS()
				return "", fmt.Errorf("qty below minQty")
			}
			if mn > 0 && ord.Qty*ord.Price < mn {
				cancelS()
				return "", fmt.Errorf("notional below minNotional")
			}
		}
		cancelS()
	} else if st, ok := any(a).(common.SymbolStepper); ok {
		ctxS, cancelS := context.WithTimeout(ctx, 2*time.Second)
		if qs, ps, ok2 := st.GetSymbolSteps(ctxS, ord.Symbol); ok2 {
			ord.Qty = roundDown(ord.Qty, qs)
			ord.Price = roundDown(ord.Price, ps)
		}
		cancelS()
	}
	// Bybit v5 private POST signing requires: sign(HMAC_SHA256(secret, timestamp + apiKey + recvWindow + body))
	endpoint := "/v5/order/create"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recv := "5000"
	side := strings.ToUpper(string(ord.Side)) // BUY/SELL
	// Build JSON body with string fields per Bybit expectations
	body := map[string]string{
		"category":  "spot",
		"symbol":    ord.Symbol,
		"side":      side,
		"orderType": "Limit",
		"qty":       fmt.Sprintf("%f", ord.Qty),
		"price":     fmt.Sprintf("%f", ord.Price),
		"timeInForce": func() string {
			if ord.TimeInForce != "" {
				return ord.TimeInForce
			}
			return "FOK"
		}(),
	}
	if ord.ID != "" {
		body["orderLinkId"] = ord.ID
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
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
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			OrderId string `json:"orderId"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodPost).Str("endpoint", endpoint).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Str("symbol", ord.Symbol).Str("side", side).Float64("qty", ord.Qty).Float64("price", ord.Price).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit order create response")
	if decErr != nil {
		return "", decErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("bybit place order http %d", resp.StatusCode)
	}
	if r.RetCode != 0 {
		return "", fmt.Errorf("bybit place order error: %s", r.RetMsg)
	}
	return r.Result.OrderId, nil
}

// GetOrderStatus implements common.OrderStatusQuerier (optional)
func (a *Adapter) GetOrderStatus(ctx context.Context, orderID string) (string, float64, error) {
	endpoint := "/v5/order/realtime"
	q := url.Values{}
	q.Set("category", "spot")
	q.Set("orderId", orderID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Exchanges.Bybit.BaseURL+endpoint+"?"+q.Encode(), nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("endpoint", endpoint).Msg("http request failed")
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				OrderStatus string `json:"orderStatus"`
				CumExecQty  string `json:"cumExecQty"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", 0, err
	}
	a.logger.Debug().Str("method", http.MethodGet).Str("endpoint", endpoint).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Msg("bybit order realtime response")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.RetCode != 0 || len(r.Result.List) == 0 {
		return "", 0, fmt.Errorf("status http=%d ret=%d", resp.StatusCode, r.RetCode)
	}
	st := r.Result.List[0].OrderStatus
	var filled float64
	_, _ = fmt.Sscan(r.Result.List[0].CumExecQty, &filled)
	return st, filled, nil
}

// GetOrderFillInfo implements common.OrderFillInfoQuerier (optional)
func (a *Adapter) GetOrderFillInfo(ctx context.Context, orderID string) (avgPrice float64, cumFee float64, filledQty float64, err error) {
	endpoint := "/v5/order/realtime"
	q := url.Values{}
	q.Set("category", "spot")
	q.Set("orderId", orderID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Exchanges.Bybit.BaseURL+endpoint+"?"+q.Encode(), nil)
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				AvgPrice   string `json:"avgPrice"`
				CumExecFee string `json:"cumExecFee"`
				CumExecQty string `json:"cumExecQty"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, 0, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.RetCode != 0 || len(r.Result.List) == 0 {
		return 0, 0, 0, fmt.Errorf("fillinfo http=%d ret=%d", resp.StatusCode, r.RetCode)
	}
	_, _ = fmt.Sscan(r.Result.List[0].AvgPrice, &avgPrice)
	_, _ = fmt.Sscan(r.Result.List[0].CumExecFee, &cumFee)
	_, _ = fmt.Sscan(r.Result.List[0].CumExecQty, &filledQty)
	return avgPrice, cumFee, filledQty, nil
}

// CancelOrder implements common.OrderCanceler (optional)
func (a *Adapter) CancelOrder(ctx context.Context, orderID string) error {
	if a.cfg.Exchanges.Bybit.APIKey == "" || a.cfg.Exchanges.Bybit.Secret == "" {
		return fmt.Errorf("bybit api credentials not set")
	}
	endpoint := "/v5/order/cancel"
	recv := "5000"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	body := map[string]string{
		"category": "spot",
		"orderId":  orderID,
	}
	b, _ := json.Marshal(body)
	signature := sign(a.cfg.Exchanges.Bybit.Secret, ts+a.cfg.Exchanges.Bybit.APIKey+recv+string(b))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Exchanges.Bybit.BaseURL+endpoint, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BAPI-API-KEY", a.cfg.Exchanges.Bybit.APIKey)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recv)
	req.Header.Set("X-BAPI-SIGN", signature)
	req.Header.Set("X-BAPI-SIGN-TYPE", "2")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || r.RetCode != 0 {
		return fmt.Errorf("cancel failed http=%d ret=%d msg=%s", resp.StatusCode, r.RetCode, r.RetMsg)
	}
	return nil
}

// GetOrderbookL2 implements common.OrderbookProvider for Bybit spot L2 orderbook
func (a *Adapter) GetOrderbookL2(ctx context.Context, symbol string, depth int) ([][2]float64, [][2]float64, bool) {
	if depth <= 0 {
		depth = 10
	}
	// prefer WS cache
	a.mu.RLock()
	if b, ok2 := a.books[symbol]; ok2 && len(b.bids) > 0 && len(b.asks) > 0 {
		a.mu.RUnlock()
		return b.bids, b.asks, true
	}
	a.mu.RUnlock()
	u := fmt.Sprintf("%s/v5/market/orderbook?category=spot&symbol=%s&limit=%d", a.cfg.Exchanges.Bybit.BaseURL, symbol, depth)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Str("symbol", symbol).Msg("http request failed")
		return nil, nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Bids [][]string `json:"b"`
			Asks [][]string `json:"a"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Str("symbol", symbol).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Int("bids", len(r.Result.Bids)).Int("asks", len(r.Result.Asks)).Msg("bybit orderbook response")
	if decErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || r.RetCode != 0 {
		return nil, nil, false
	}
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	b2 := make([][2]float64, 0, len(r.Result.Bids))
	for _, lv := range r.Result.Bids {
		if len(lv) >= 2 {
			b2 = append(b2, [2]float64{parse(lv[0]), parse(lv[1])})
		}
	}
	a2 := make([][2]float64, 0, len(r.Result.Asks))
	for _, lv := range r.Result.Asks {
		if len(lv) >= 2 {
			a2 = append(a2, [2]float64{parse(lv[0]), parse(lv[1])})
		}
	}
	return b2, a2, true
}

// SubscribeSymbols connects to Bybit public WS and subscribes tickers+orderbook for given symbols.
func (a *Adapter) SubscribeSymbols(ctx context.Context, symbols []string) error {
	wsURL := "wss://stream.bybit.com/v5/public/spot"
	if strings.Contains(strings.ToLower(a.cfg.Exchanges.Bybit.BaseURL), "test") {
		wsURL = "wss://stream-testnet.bybit.com/v5/public/spot"
	}
	dial := func(ctx context.Context) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	}
	run := func(ctx context.Context, topics []string) error {
		c, _, err := dial(ctx)
		if err != nil {
			return err
		}
		a.wsConn = c
		c.SetPongHandler(func(string) error { a.mu.Lock(); a.lastWS = time.Now(); a.mu.Unlock(); return nil })
		// build subscribe message
		sub := map[string]any{"op": "subscribe", "args": topics}
		if err := c.WriteJSON(sub); err != nil {
			c.Close()
			return err
		}
		// ping ticker
		ping := time.NewTicker(15 * time.Second)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ping.C:
					_ = c.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(2*time.Second))
				}
			}
		}()
		// staleness updater
		go func() {
			t := time.NewTicker(1 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					a.mu.RLock()
					last := a.lastWS
					a.mu.RUnlock()
					if !last.IsZero() {
						metrics.BookStalenessMs.WithLabelValues("bybit").Set(float64(time.Since(last).Milliseconds()))
					}
				}
			}
		}()
		// reader
		go func() {
			defer c.Close()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					var msg struct {
						Topic string          `json:"topic"`
						Type  string          `json:"type"`
						Data  json.RawMessage `json:"data"`
					}
					if err := c.ReadJSON(&msg); err != nil {
						return
					}
					if strings.HasPrefix(msg.Topic, "tickers.") {
						// Bybit WS can send data as an array of objects or a single object.
						type tick struct {
							Symbol string `json:"symbol"`
							Bid1   string `json:"bid1Price"`
							Ask1   string `json:"ask1Price"`
						}
						var (
							t        tick
							arr      []tick
							bid, ask float64
						)
						sym := strings.TrimPrefix(msg.Topic, "tickers.")
						if err := json.Unmarshal(msg.Data, &t); err == nil && (t.Bid1 != "" || t.Ask1 != "") {
							_, _ = fmt.Sscan(t.Bid1, &bid)
							_, _ = fmt.Sscan(t.Ask1, &ask)
							if t.Symbol != "" {
								sym = t.Symbol
							}
						} else if err := json.Unmarshal(msg.Data, &arr); err == nil && len(arr) > 0 {
							_, _ = fmt.Sscan(arr[0].Bid1, &bid)
							_, _ = fmt.Sscan(arr[0].Ask1, &ask)
							if arr[0].Symbol != "" {
								sym = arr[0].Symbol
							}
						}
						if bid > 0 && ask > 0 {
							a.mu.Lock()
							a.tickers[sym] = common.Ticker{Bid: bid, Ask: ask}
							a.lastWS = time.Now()
							a.mu.Unlock()
						}
					} else if strings.HasPrefix(msg.Topic, "orderbook.1.") {
						var ob struct {
							Bids [][]string `json:"b"`
							Asks [][]string `json:"a"`
						}
						_ = json.Unmarshal(msg.Data, &ob)
						parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
						bids := make([][2]float64, 0, len(ob.Bids))
						for _, lv := range ob.Bids {
							if len(lv) >= 2 {
								bids = append(bids, [2]float64{parse(lv[0]), parse(lv[1])})
							}
						}
						asks := make([][2]float64, 0, len(ob.Asks))
						for _, lv := range ob.Asks {
							if len(lv) >= 2 {
								asks = append(asks, [2]float64{parse(lv[0]), parse(lv[1])})
							}
						}
						sym := strings.TrimPrefix(msg.Topic, "orderbook.1.")
						// basic checksum/sanity: top bid must be less than top ask
						if len(bids) > 0 && len(asks) > 0 && bids[0][0] >= asks[0][0] {
							// attempt snapshot rebuild via REST
							metrics.BookRebuildsTotal.WithLabelValues("bybit", "crossed").Inc()
							ctxSnap, cancelSnap := context.WithTimeout(ctx, 2*time.Second)
							b2, a2, ok := a.GetOrderbookL2(ctxSnap, sym, 50)
							cancelSnap()
							if ok {
								bids, asks = b2, a2
							}
						}
						a.mu.Lock()
						a.books[sym] = struct{ bids, asks [][2]float64 }{bids: bids, asks: asks}
						a.lastWS = time.Now()
						a.mu.Unlock()
					}
				}
			}
		}()
		return nil
	}
	// build topics
	topics := []string{}
	for _, s := range symbols {
		if s == "" {
			continue
		}
		topics = append(topics, fmt.Sprintf("tickers.%s", s))
		topics = append(topics, fmt.Sprintf("orderbook.1.%s", s))
	}
	if err := run(ctx, topics); err != nil {
		metrics.WSReconnectsTotal.WithLabelValues("bybit", "dial_error").Inc()
	}
	// supervise and reconnect loop
	go func() {
		backoff := time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// probe staleness: if no WS updates for 5s, reconnect
			a.mu.RLock()
			stale := time.Since(a.lastWS) > 5*time.Second
			a.mu.RUnlock()
			if stale {
				metrics.WSReconnectsTotal.WithLabelValues("bybit", "stale").Inc()
				_ = run(ctx, topics)
			}
			time.Sleep(backoff)
			if backoff < 8*time.Second {
				backoff *= 2
			} else {
				backoff = 8 * time.Second
			}
		}
	}()
	return nil
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
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol        string `json:"symbol"`
				LotSizeFilter struct {
					BasePrecision string `json:"basePrecision"`
					StepSize      string `json:"stepSize"`
				} `json:"lotSizeFilter"`
				PriceFilter struct {
					TickSize string `json:"tickSize"`
				} `json:"priceFilter"`
			} `json:"list"`
		} `json:"result"`
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
	if qtyStep == 0 {
		qtyStep = parse(it.LotSizeFilter.BasePrecision)
	}
	priceStep := parse(it.PriceFilter.TickSize)
	if priceStep == 0 {
		return 0, 0, false
	}
	a.mu.Lock()
	a.steps[symbol] = struct{ qtyStep, priceStep float64 }{qtyStep: qtyStep, priceStep: priceStep}
	a.mu.Unlock()
	return qtyStep, priceStep, true
}

// PrefetchSymbolSteps fetches steps for many symbols in one call and caches them.
func (a *Adapter) PrefetchSymbolSteps(ctx context.Context, symbols []string) error {
	want := map[string]struct{}{}
	for _, s := range symbols {
		if s != "" {
			want[s] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil
	}
	u := fmt.Sprintf("%s/v5/market/instruments-info?category=spot", a.cfg.Exchanges.Bybit.BaseURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("url", u).Msg("http request failed")
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol        string `json:"symbol"`
				LotSizeFilter struct {
					StepSize      string `json:"stepSize"`
					BasePrecision string `json:"basePrecision"`
				} `json:"lotSizeFilter"`
				PriceFilter struct {
					TickSize string `json:"tickSize"`
				} `json:"priceFilter"`
			} `json:"list"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("url", u).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit instruments-info response")
	if decErr != nil {
		return decErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bybit instruments http %d", resp.StatusCode)
	}
	if r.RetCode != 0 {
		return fmt.Errorf("bybit instruments error: %s", r.RetMsg)
	}
	parse := func(s string) float64 { var f float64; _, _ = fmt.Sscan(s, &f); return f }
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, it := range r.Result.List {
		if _, ok := want[it.Symbol]; !ok {
			continue
		}
		q := parse(it.LotSizeFilter.StepSize)
		if q == 0 {
			q = parse(it.LotSizeFilter.BasePrecision)
		}
		p := parse(it.PriceFilter.TickSize)
		if p == 0 {
			continue
		}
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
	if err != nil {
		a.logger.Debug().Err(err).Str("method", http.MethodGet).Str("endpoint", endpoint).Msg("http request failed")
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				// Bybit v5 wallet-balance returns list entries that contain a nested array under key "coin".
				Coin []struct {
					Coin                string `json:"coin"`
					WalletBalance       string `json:"walletBalance"`
					AvailableToWithdraw string `json:"availableToWithdraw"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&r)
	a.logger.Debug().Str("method", http.MethodGet).Str("endpoint", endpoint).Int("status", resp.StatusCode).Dur("latency", time.Since(start)).Int("retCode", r.RetCode).Str("retMsg", r.RetMsg).Msg("bybit wallet-balance response")
	if decErr != nil {
		return nil, decErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bybit get balances http %d", resp.StatusCode)
	}
	if r.RetCode != 0 {
		return nil, fmt.Errorf("bybit get balances error: %s", r.RetMsg)
	}
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
