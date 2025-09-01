package arbitrage

import (
	"context"
	"sync"
	"testing"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
)

type fakeExecAdapter struct {
	mu      sync.Mutex
	nextID  int
	orders  map[string]common.Order
	status  map[string]struct{ st string; filled float64 }
	fills   map[string]struct{ avg, fee, filled float64 }
	booksOK bool

	// test controls
	placeCount           int
	placeStatusOverrides []string // optional per-order status override: e.g., "Filled", "New"
}

func newFakeExecAdapter() *fakeExecAdapter {
	return &fakeExecAdapter{orders: map[string]common.Order{}, status: map[string]struct{ st string; filled float64 }{}, fills: map[string]struct{ avg, fee, filled float64 }{}, booksOK: true}
}

func (f *fakeExecAdapter) Name() string { return "fake" }
func (f *fakeExecAdapter) Start(ctx context.Context) error { return nil }
func (f *fakeExecAdapter) Stop(ctx context.Context) error { return nil }
func (f *fakeExecAdapter) GetTicker(ctx context.Context, symbol string) (common.Ticker, error) {
	// choose large bids to drive positive forward path in tests
	switch symbol {
	case "BTCUSDT":
		return common.Ticker{Bid: 100.0, Ask: 100.1}, nil
	case "ETHUSDT":
		return common.Ticker{Bid: 0.01, Ask: 0.0101}, nil
	case "ETHBTC":
		return common.Ticker{Bid: 10.0, Ask: 10.1}, nil
	default:
		// price asset in USD for fee conversion
		return common.Ticker{Bid: 1.0, Ask: 1.0}, nil
	}
}
func (f *fakeExecAdapter) PlaceOrder(ctx context.Context, ord common.Order) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := "id" + string(rune('a'+f.nextID))
	f.orders[id] = ord
	// default to filled
	status := "Filled"
	filled := ord.Qty
	// apply override if provided
	f.placeCount++
	idx := f.placeCount - 1
	if idx >= 0 && idx < len(f.placeStatusOverrides) {
		ov := f.placeStatusOverrides[idx]
		if ov == "New" || ov == "PartiallyFilled" || ov == "Cancelled" {
			status = ov
			if ov == "New" || ov == "Cancelled" {
				filled = 0
			}
		}
	}
	f.status[id] = struct{ st string; filled float64 }{status, filled}
	f.fills[id] = struct{ avg, fee, filled float64 }{avg: ord.Price, fee: 0.001, filled: filled}
	return id, nil
}

// OrderStatusQuerier
func (f *fakeExecAdapter) GetOrderStatus(ctx context.Context, orderID string) (string, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.status[orderID]
	return s.st, s.filled, nil
}

// OrderFillInfoQuerier
func (f *fakeExecAdapter) GetOrderFillInfo(ctx context.Context, orderID string) (float64, float64, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fi := f.fills[orderID]
	return fi.avg, fi.fee, fi.filled, nil
}

// OrderCanceler
func (f *fakeExecAdapter) CancelOrder(ctx context.Context, orderID string) error { return nil }

// OrderbookProvider
func (f *fakeExecAdapter) GetOrderbookL2(ctx context.Context, symbol string, depth int) ([][2]float64, [][2]float64, bool) {
	if !f.booksOK { return nil, nil, false }
bids := make([][2]float64, 0, 3)
	asks := make([][2]float64, 0, 3)
	// simple tight book around ticker bid/ask
	t, _ := f.GetTicker(ctx, symbol)
	bids = append(bids, [2]float64{t.Bid, 100})
bids = append(bids, [2]float64{t.Bid * 0.999, 100})
	asks = append(asks, [2]float64{t.Ask, 100})
	asks = append(asks, [2]float64{t.Ask * 1.001, 100})
	return bids, asks, true
}

func TestExecuteTriangle_PartialSecond_UnwindFirst(t *testing.T) {
	cfg := config.Load()
	cfg.Trading.Enabled = true
	cfg.Trading.Live = true
	cfg.Trading.MinNetBps = 1.0
	logger := log.NewLogger(cfg)
	ad := newFakeExecAdapter()
	// Force first order filled, second order unfilled (New), third won't be reached
	ad.placeStatusOverrides = []string{"Filled", "New"}
	eng := New(cfg, map[string]common.ExchangeAdapter{"bybit": ad}, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tri := config.Triangle{AB: "BTCUSDT", BC: "ETHUSDT", CA: "ETHBTC"}
	// Manually drive executeTriangle instead of Run loop
	tAB, _ := ad.GetTicker(ctx, tri.AB)
	tBC, _ := ad.GetTicker(ctx, tri.BC)
	tCA, _ := ad.GetTicker(ctx, tri.CA)
	err := eng.executeTriangle(ctx, ad, tri, "F", tAB, tBC, tCA, 0.5, 10.0)
	if err == nil {
		t.Fatalf("expected error due to second leg not filled, got nil")
	}
}

func TestExecuteTriangle_Success_UpdatesPnL(t *testing.T) {
	cfg := config.Load()
	cfg.Trading.Enabled = true
	cfg.Trading.Live = true
	cfg.Trading.MinNetBps = 1.0
	logger := log.NewLogger(cfg)
	ad := newFakeExecAdapter()
	eng := New(cfg, map[string]common.ExchangeAdapter{"bybit": ad}, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tri := config.Triangle{AB: "BTCUSDT", BC: "ETHUSDT", CA: "ETHBTC"}
	tAB, _ := ad.GetTicker(ctx, tri.AB)
	tBC, _ := ad.GetTicker(ctx, tri.BC)
	tCA, _ := ad.GetTicker(ctx, tri.CA)
	err := eng.executeTriangle(ctx, ad, tri, "F", tAB, tBC, tCA, 0.0, 20.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pnl := eng.riskEng.GetDailyPnL()
	if pnl == 0 {
		t.Fatalf("expected non-zero PnL after successful triangle")
	}
}

