package risk

import (
	"math"
	"sync"
	"time"

	"arbitr/internal/exchange/common"
)

type Limits struct {
	MaxAssetPct        float64 // % of total portfolio in single asset
	MaxExchangePct     float64 // % of total portfolio on single exchange
	MaxNotionalPct     float64 // % of total portfolio in single position
	DailyVar99Pct      float64 // daily VaR limit as % of portfolio
	MaxInventoryUSD    float64 // max USD value per base asset
	SoftStopLossUSD    float64 // soft daily stop loss
	HardStopLossUSD    float64 // hard daily stop loss
}

type Position struct {
	Asset     string
	Exchange  string
	Qty       float64
	ValueUSD  float64
	Timestamp time.Time
}

type PnLTick struct {
	Timestamp time.Time
	RealizedUSD float64
	UnrealizedUSD float64
}

type Engine struct {
	mu                sync.RWMutex
	limits            Limits
	positions         map[string]Position // key: asset+exchange
	pnlHistory        []PnLTick
	dailyPnL          float64
	totalPortfolioUSD float64
	softStopTriggered bool
	hardStopTriggered bool
	lastResetDate     time.Time
}

type OrderRequest struct {
	Symbol    string
	Side      common.OrderSide
	Qty       float64
	Price     float64
	Exchange  string
	ValueUSD  float64
}

func NewEngine(limits Limits) *Engine {
	return &Engine{
		limits:        limits,
		positions:     make(map[string]Position),
		pnlHistory:    make([]PnLTick, 0, 1000),
		lastResetDate: time.Now().Truncate(24 * time.Hour),
	}
}

func (e *Engine) Allow(req OrderRequest) (bool, string, float64, float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if we need daily reset
	today := time.Now().Truncate(24 * time.Hour)
	if today.After(e.lastResetDate) {
		e.dailyPnL = 0
		e.softStopTriggered = false
		e.hardStopTriggered = false
		e.lastResetDate = today
	}

	// Hard stop check
	if e.hardStopTriggered {
		return false, "hard_daily_stop_triggered", 0, 0
	}
	// Soft stop check - reduce size but don't block completely
	adjQty := req.Qty
	adjValueUSD := req.ValueUSD
	if e.softStopTriggered {
		adjQty *= 0.3 // Reduce to 30% of original size
		adjValueUSD *= 0.3
	}

	// Daily PnL checks
	if e.limits.HardStopLossUSD > 0 && e.dailyPnL <= -e.limits.HardStopLossUSD {
		e.hardStopTriggered = true
		return false, "hard_daily_pnl_stop", 0, 0
	}
	if e.limits.SoftStopLossUSD > 0 && e.dailyPnL <= -e.limits.SoftStopLossUSD && !e.softStopTriggered {
		e.softStopTriggered = true
	}

	// Asset concentration check
	if e.limits.MaxAssetPct > 0 {
		asset := extractAsset(req.Symbol)
		currentAssetValue := e.getAssetExposureUSD(asset)
		newAssetValue := currentAssetValue + adjValueUSD
		if e.totalPortfolioUSD > 0 && newAssetValue/e.totalPortfolioUSD > e.limits.MaxAssetPct {
			return false, "asset_concentration_limit", 0, 0
		}
	}

	// Exchange concentration check
	if e.limits.MaxExchangePct > 0 {
		currentExchValue := e.getExchangeExposureUSD(req.Exchange)
		newExchValue := currentExchValue + adjValueUSD
		if e.totalPortfolioUSD > 0 && newExchValue/e.totalPortfolioUSD > e.limits.MaxExchangePct {
			return false, "exchange_concentration_limit", 0, 0
		}
	}

	// Position size check
	if e.limits.MaxNotionalPct > 0 && e.totalPortfolioUSD > 0 {
		if adjValueUSD/e.totalPortfolioUSD > e.limits.MaxNotionalPct {
			return false, "position_size_limit", 0, 0
		}
	}

	// Inventory limit check
	if e.limits.MaxInventoryUSD > 0 {
		asset := extractAsset(req.Symbol)
		currentValue := e.getAssetExposureUSD(asset)
		if currentValue+adjValueUSD > e.limits.MaxInventoryUSD {
			return false, "inventory_limit", 0, 0
		}
	}

	// VaR check
	if e.limits.DailyVar99Pct > 0 {
		var99 := e.calculateVaR99()
		if e.totalPortfolioUSD > 0 && var99/e.totalPortfolioUSD > e.limits.DailyVar99Pct {
			return false, "var_limit_exceeded", 0, 0
		}
	}

	return true, "", adjQty, adjValueUSD
}

func (e *Engine) UpdatePosition(asset, exchange string, qtyDelta, valueDelta float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := asset + "|" + exchange
	pos := e.positions[key]
	pos.Asset = asset
	pos.Exchange = exchange
	pos.Qty += qtyDelta
	pos.ValueUSD += valueDelta
	pos.Timestamp = time.Now()
	e.positions[key] = pos

	// Remove zero positions
	if math.Abs(pos.Qty) < 1e-8 && math.Abs(pos.ValueUSD) < 0.01 {
		delete(e.positions, key)
	}
}

func (e *Engine) UpdatePnL(realizedUSD, unrealizedUSD float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.dailyPnL += realizedUSD
	tick := PnLTick{
		Timestamp: time.Now(),
		RealizedUSD: realizedUSD,
		UnrealizedUSD: unrealizedUSD,
	}
	e.pnlHistory = append(e.pnlHistory, tick)

	// Keep only recent history for VaR calculation
	if len(e.pnlHistory) > 1000 {
		e.pnlHistory = e.pnlHistory[len(e.pnlHistory)-1000:]
	}
}

func (e *Engine) UpdatePortfolioValue(totalUSD float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.totalPortfolioUSD = totalUSD
}

func (e *Engine) GetDailyPnL() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dailyPnL
}

func (e *Engine) IsSoftStopped() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.softStopTriggered
}

func (e *Engine) IsHardStopped() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.hardStopTriggered
}

func (e *Engine) getAssetExposureUSD(asset string) float64 {
	var total float64
	for _, pos := range e.positions {
		if pos.Asset == asset {
			total += math.Abs(pos.ValueUSD)
		}
	}
	return total
}

func (e *Engine) getExchangeExposureUSD(exchange string) float64 {
	var total float64
	for _, pos := range e.positions {
		if pos.Exchange == exchange {
			total += math.Abs(pos.ValueUSD)
		}
	}
	return total
}

func (e *Engine) calculateVaR99() float64 {
	if len(e.pnlHistory) < 20 {
		return 0
	}

	// Simple VaR calculation using historical simulation
	pnls := make([]float64, len(e.pnlHistory))
	for i, tick := range e.pnlHistory {
		pnls[i] = tick.RealizedUSD + tick.UnrealizedUSD
	}

	// Sort and take 1st percentile (99% VaR)
	for i := 0; i < len(pnls)-1; i++ {
		for j := 0; j < len(pnls)-i-1; j++ {
			if pnls[j] > pnls[j+1] {
				pnls[j], pnls[j+1] = pnls[j+1], pnls[j]
			}
		}
	}

	percentileIdx := int(float64(len(pnls)) * 0.01)
	if percentileIdx >= len(pnls) {
		percentileIdx = len(pnls) - 1
	}
	return math.Abs(pnls[percentileIdx])
}

func extractAsset(symbol string) string {
	// Extract base asset from symbol like BTCUSDT -> BTC
	if len(symbol) > 4 && symbol[len(symbol)-4:] == "USDT" {
		return symbol[:len(symbol)-4]
	}
	if len(symbol) > 3 && symbol[len(symbol)-3:] == "BTC" {
		return symbol[:len(symbol)-3]
	}
	if len(symbol) > 3 && symbol[len(symbol)-3:] == "ETH" {
		return symbol[:len(symbol)-3]
	}
	return symbol
}
