package arbitrage

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/orderbook"
	"arbitr/internal/risk"
	"arbitr/internal/slippage"
)

// allowedForLive checks whether all legs of the triangle are permitted for live trading.
func (e *Engine) allowedForLive(tri config.Triangle) bool {
	if len(e.cfg.Trading.AllowedSymbols) == 0 {
		return true
	}
	ok := func(sym string) bool {
		for _, s := range e.cfg.Trading.AllowedSymbols {
			if s == sym {
				return true
			}
		}
		return false
	}
	return ok(tri.AB) && ok(tri.BC) && ok(tri.CA)
}

// splitSymbol splits a symbol like BTCUSDT into base and quote using common suffixes.
func splitSymbol(sym string) (string, string) {
	s := strings.ToUpper(sym)
	suffixes := []string{"USDT", "USD", "BTC", "ETH"}
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) && len(s) > len(suf) {
			return s[:len(s)-len(suf)], suf
		}
	}
	// fallback: split last 3 chars
	if len(s) > 3 {
		return s[:len(s)-3], s[len(s)-3:]
	}
	return s, ""
}

// usdPriceOfAsset returns the USD price of an asset using *USDT ticker bids.
func usdPriceOfAsset(ctx context.Context, by common.ExchangeAdapter, asset string) (float64, error) {
	a := strings.ToUpper(asset)
	if a == "USDT" || a == "USD" {
		return 1.0, nil
	}
	tick, err := by.GetTicker(ctx, a+"USDT")
	if err != nil {
		return 0, err
	}
	return tick.Bid, nil
}

// executeTriangle places all three legs of the triangle in selected direction (F or R)
// with adaptive sizing, price skew, and basic risk gating. Due to limited adapter API,
// we assume aggressive limit orders near best prices to maximize fill probability.
func (e *Engine) executeTriangle(ctx context.Context, by common.ExchangeAdapter, tri config.Triangle, dir string, tAB, tBC, tCA common.Ticker, slipBps float64, netBps float64) error {
	startDecision := time.Now()
	// Determine base qty from config and balances
	qtyUSD := e.cfg.Trading.MaxNotionalUSD
	if qtyUSD <= 0 {
		qtyUSD = e.cfg.Trading.NotionalUSD
	}
	if qtyUSD <= 0 {
		qtyUSD = 50
	}

	// Adaptive sizing by edge: more edge -> more size (cap at MaxNotionalUSD)
	edgeBps := netBps
	mult := 1.0
	if edgeBps > 0 {
		mult = 1.0 + math.Min(edgeBps/10.0, 1.0)
	} // up to 2x
	// dynamic triangle quality factor
	triKey := tri.AB + "|" + tri.BC + "|" + tri.CA
	mult *= e.dynamicSizeFactor(triKey)
	qtyUSD = math.Min(qtyUSD*mult, e.cfg.Trading.MaxNotionalUSD)
	if qtyUSD <= 0 {
		qtyUSD = e.cfg.Trading.NotionalUSD
	}
	if qtyUSD <= 0 {
		qtyUSD = 50
	}

	// Side/price helpers
	applySkew := func(price float64, isBuy bool) float64 {
		baseSkewBps := e.cfg.Trading.PriceSkewBps
		// Add adaptive skew up to +2 bps based on netBps beyond threshold
		adapt := 0.0
		if netBps > e.cfg.Trading.MinNetBps {
			adapt = math.Min((netBps-e.cfg.Trading.MinNetBps)*0.5, 2.0) // 0.5x up to 2 bps
		}
		skew := (baseSkewBps + adapt) / 10000.0
		if isBuy {
			return price * (1 + skew)
		}
		return price * (1 - skew)
	}
	roundDown := func(x, step float64) float64 {
		if step <= 0 {
			return x
		}
		return math.Floor(x/step) * step
	}

	// Prepare filters per symbol
	getFilters := func(sym string) (qStep, pStep, minQty, minNotional float64) {
		qStep, pStep = 0.000001, 0.0001
		minQty, minNotional = 0, 0
		if fp, ok := by.(common.SymbolFiltersProvider); ok {
			ctxS, cancelS := context.WithTimeout(ctx, 2*time.Second)
			if qs, ps, mq, mn, ok2 := fp.GetSymbolFilters(ctxS, sym); ok2 {
				qStep, pStep, minQty, minNotional = qs, ps, mq, mn
			}
			cancelS()
		} else if st, ok := by.(common.SymbolStepper); ok {
			ctxS, cancelS := context.WithTimeout(ctx, 2*time.Second)
			if qs, ps, ok2 := st.GetSymbolSteps(ctxS, sym); ok2 {
				qStep, pStep = qs, ps
			}
			cancelS()
		}
		return
	}

	// Build the three orders depending on direction. We will approximate prices.
	type leg struct {
		sym  string
		side common.OrderSide
		px   float64
	}
	legs := make([]leg, 0, 3)
	switch dir {
	case "F":
		// sell A->B at AB bid, sell B->C at BC bid, sell C->A at CA bid (approx)
		legs = append(legs,
			leg{sym: tri.AB, side: common.Sell, px: applySkew(tAB.Bid, false)},
			leg{sym: tri.BC, side: common.Sell, px: applySkew(tBC.Bid, false)},
			leg{sym: tri.CA, side: common.Sell, px: applySkew(tCA.Bid, false)},
		)
	default:
		// Reverse: A->C buy CA (ask), C->B sell BC (bid), B->A buy AB (ask)
		legs = append(legs,
			leg{sym: tri.CA, side: common.Buy, px: applySkew(tCA.Ask, true)},
			leg{sym: tri.BC, side: common.Sell, px: applySkew(tBC.Bid, false)},
			leg{sym: tri.AB, side: common.Buy, px: applySkew(tAB.Ask, true)},
		)
	}

	// Determine qty in base units of first leg
	if len(legs) != 3 {
		return fmt.Errorf("invalid legs")
	}
	first := legs[0]
	if first.px <= 0 {
		return fmt.Errorf("invalid first leg price")
	}
	qty := qtyUSD / first.px
	// Log attempt before placing orders for better visibility
	e.logger.Info().Str("triangle", tri.AB+","+tri.BC+","+tri.CA).Str("dir", dir).Float64("net_bps", netBps).Float64("slip_bps", slipBps).Float64("qty_usd", qtyUSD).Msg("starting triangle execution")

	// Capacity cap from L2 books at our limit prices
	if ob, ok := by.(common.OrderbookProvider); ok {
		ctxTO, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		bAB, aAB, ok1 := ob.GetOrderbookL2(ctxTO, tri.AB, 20)
		bBC, aBC, ok2 := ob.GetOrderbookL2(ctxTO, tri.BC, 20)
		bCA, aCA, ok3 := ob.GetOrderbookL2(ctxTO, tri.CA, 20)
		if ok1 && ok2 && ok3 {
			build := func(bids [][2]float64, asks [][2]float64) orderbook.L2 {
				var l2 orderbook.L2
				for _, b := range bids {
					l2.Bids = append(l2.Bids, orderbook.Level{Price: b[0], Qty: b[1]})
				}
				for _, a := range asks {
					l2.Asks = append(l2.Asks, orderbook.Level{Price: a[0], Qty: a[1]})
				}
				return l2
			}
			cap1 := slippage.ExecutableQty(build(bAB, aAB), first.px, first.side == common.Buy)
			second := legs[1]
			cap2 := slippage.ExecutableQty(build(bBC, aBC), second.px, second.side == common.Buy)
			third := legs[2]
			cap3 := slippage.ExecutableQty(build(bCA, aCA), third.px, third.side == common.Buy)
			cap := cap1
			if cap2 < cap {
				cap = cap2
			}
			if cap3 < cap {
				cap = cap3
			}
			if cap > 0 && cap < qty {
				qty = cap
			}
		}
	}

	// Risk check for first leg
	if e.riskEng != nil {
		ok, reason, adjQty, _ := e.riskEng.Allow(risk.OrderRequest{Symbol: first.sym, Side: first.side, Qty: qty, Price: first.px, Exchange: by.Name(), ValueUSD: qty * first.px})
		if !ok {
			metrics.RiskBlocks.Inc()
			return fmt.Errorf("risk blocked first leg: %s", reason)
		}
		if adjQty > 0 {
			qty = adjQty
		}
	}

	// Round and enforce min filters
	qStep, pStep, minQty, minNotional := getFilters(first.sym)
	qty = roundDown(qty, qStep)
	first.px = roundDown(first.px, pStep)
	if qty <= 0 || first.px <= 0 {
		return fmt.Errorf("qty/price invalid after rounding")
	}
	if minQty > 0 && qty < minQty {
		return fmt.Errorf("first leg qty below minQty")
	}
	if minNotional > 0 && qty*first.px < minNotional {
		return fmt.Errorf("first leg notional below minNotional")
	}

	// Submit first leg
	ctx1, cancel1 := context.WithTimeout(ctx, 5*time.Second)
	id1, err := by.PlaceOrder(ctx1, common.Order{Symbol: first.sym, Side: first.side, Qty: qty, Price: first.px, TimeInForce: "IOC"})
	metrics.ExecutionLatencyMs.Observe(float64(time.Since(startDecision).Milliseconds()))
	cancel1()
	if err != nil || id1 == "" {
		return fmt.Errorf("first leg rejected: %w", err)
	}

	// Resolve first leg status and filled qty if supported
	filled1 := qty
	var avg1, fee1 float64
	if fi, ok := by.(common.OrderFillInfoQuerier); ok {
		if px, fee, f, err := fi.GetOrderFillInfo(ctx, id1); err == nil {
			avg1, fee1, filled1 = px, fee, f
		}
	}
	if qs, ok := by.(common.OrderStatusQuerier); ok {
		ctxS, cS := context.WithTimeout(ctx, 2*time.Second)
		st, f, err := qs.GetOrderStatus(ctxS, id1)
		cS()
		if err == nil {
			if f < qty {
				metrics.PartialFillsTotal.Inc()
			}
			filled1 = f
			if st != "Filled" && st != "PartiallyFilled" {
				// try cancel if not filled
				if canc, ok2 := by.(common.OrderCanceler); ok2 {
					_ = canc.CancelOrder(ctx, id1)
				}
				return fmt.Errorf("first leg not filled: %s", st)
			}
		}
	}

	// Second leg qty equals output of first; approximate as same qty for now
	second := legs[1]
	qStep2, pStep2, minQty2, minNot2 := getFilters(second.sym)
	second.px = roundDown(second.px, pStep2)
	qty2 := roundDown(filled1, qStep2)
	if minQty2 > 0 && qty2 < minQty2 {
		return fmt.Errorf("second leg qty below minQty")
	}
	if minNot2 > 0 && qty2*second.px < minNot2 {
		return fmt.Errorf("second leg notional below minNotional")
	}
	if e.riskEng != nil {
		if ok, reason, adjQty2, _ := e.riskEng.Allow(risk.OrderRequest{Symbol: second.sym, Side: second.side, Qty: qty2, Price: second.px, Exchange: by.Name(), ValueUSD: qty2 * second.px}); !ok {
			metrics.RiskBlocks.Inc()
			return fmt.Errorf("risk blocked second leg: %s", reason)
		} else {
			if adjQty2 > 0 {
				qty2 = adjQty2
			}
		}
	}
	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	id2, err := by.PlaceOrder(ctx2, common.Order{Symbol: second.sym, Side: second.side, Qty: qty2, Price: second.px, TimeInForce: "IOC"})
	cancel2()
	var avg2, fee2 float64
	if fi, ok := by.(common.OrderFillInfoQuerier); ok {
		if px, fee, f, err := fi.GetOrderFillInfo(ctx, id2); err == nil {
			avg2, fee2, qty2 = px, fee, f
		}
	}
	if err != nil || id2 == "" {
		// Unwind first leg if second failed with IOC
		opSide := common.Sell
		if first.side == common.Sell {
			opSide = common.Buy
		}
		e.unwindPair(ctx, by, first.sym, opSide, qty, first.px)
		return fmt.Errorf("second leg rejected: %w (unwind attempted)", err)
	}

	// Third leg: size by second leg fills if available
	third := legs[2]
	qStep3, pStep3, minQty3, minNot3 := getFilters(third.sym)
	third.px = roundDown(third.px, pStep3)
	qty3 := roundDown(qty2, qStep3)
	if minQty3 > 0 && qty3 < minQty3 {
		return fmt.Errorf("third leg qty below minQty")
	}
	if minNot3 > 0 && qty3*third.px < minNot3 {
		return fmt.Errorf("third leg notional below minNotional")
	}
	if qs, ok := by.(common.OrderStatusQuerier); ok {
		ctxS2, cS2 := context.WithTimeout(ctx, 2*time.Second)
		st2, f2, err2 := qs.GetOrderStatus(ctxS2, id2)
		cS2()
		if err2 == nil {
			if f2 < qty2 {
				metrics.PartialFillsTotal.Inc()
			}
			qty3 = roundDown(f2, qStep3)
			if st2 != "Filled" && st2 != "PartiallyFilled" {
				if canc, ok2 := by.(common.OrderCanceler); ok2 {
					_ = canc.CancelOrder(ctx, id2)
				}
				// unwind first
				op1 := common.Sell
				if first.side == common.Sell {
					op1 = common.Buy
				}
				e.unwindPair(ctx, by, first.sym, op1, filled1, first.px)
				return fmt.Errorf("second leg not filled: %s", st2)
			}
		}
	}
	if e.riskEng != nil {
		if ok, reason, adjQty3, _ := e.riskEng.Allow(risk.OrderRequest{Symbol: third.sym, Side: third.side, Qty: qty3, Price: third.px, Exchange: by.Name(), ValueUSD: qty3 * third.px}); !ok {
			metrics.RiskBlocks.Inc()
			// Unwind second then first using IOC and slippage budget
			e.unwindPair(ctx, by, second.sym, second.side, qty2, second.px)
			e.unwindPair(ctx, by, first.sym, func() common.OrderSide {
				if first.side == common.Sell {
					return common.Buy
				}
				return common.Sell
			}(), qty, first.px)
			return fmt.Errorf("risk blocked third leg: %s (unwind attempted)", reason)
		} else {
			if adjQty3 > 0 {
				qty3 = adjQty3
			}
		}
	}
	ctx3, cancel3 := context.WithTimeout(ctx, 5*time.Second)
	id3, err := by.PlaceOrder(ctx3, common.Order{Symbol: third.sym, Side: third.side, Qty: qty3, Price: third.px, TimeInForce: "IOC"})
	cancel3()
	var avg3, fee3 float64
	if fi, ok := by.(common.OrderFillInfoQuerier); ok {
		if px, fee, f, err := fi.GetOrderFillInfo(ctx, id3); err == nil {
			avg3, fee3, qty3 = px, fee, f
		}
	}
	if err != nil || id3 == "" {
		// Unwind second then first using IOC and slippage budget
		e.unwindPair(ctx, by, second.sym, second.side, qty2, second.px)
		e.unwindPair(ctx, by, first.sym, func() common.OrderSide {
			if first.side == common.Sell {
				return common.Buy
			}
			return common.Sell
		}(), qty, first.px)
		return fmt.Errorf("third leg rejected: %w (unwind attempted)", err)
	}

	// Realized PnL update based on actual fills (avgPrice and cumFee if available)
	if e.riskEng != nil {
		// Sum fees in USD using quote asset USD prices per leg
		feeUSD := 0.0
		// helper to convert a fee (denominated in quote) to USD
		convFee := func(fee float64, symbol string) float64 {
			_, quote := splitSymbol(symbol)
			usd, _ := usdPriceOfAsset(ctx, by, quote)
			return fee * usd
		}
		if fee1 > 0 {
			feeUSD += convFee(fee1, first.sym)
		}
		if fee2 > 0 {
			feeUSD += convFee(fee2, second.sym)
		}
		if fee3 > 0 {
			feeUSD += convFee(fee3, third.sym)
		}
		// Compute exact cycle return in A units using avg prices
		A0 := filled1
		var A2 float64
		if dir == "F" {
			// sells A->B->C->A: multiply by prices
			B1 := A0 * avg1
			C1 := B1 * avg2
			A2 = C1 * avg3
		} else {
			// reverse: buy C with A (CA), sell C->B (BC), buy A with B (AB)
			// Note avg1 = CA, avg2 = BC, avg3 = AB
			C1 := A0 / avg1
			B1 := C1 * avg2
			A2 = B1 / avg3
		}
		// Convert A PnL to USD
		baseA, _ := splitSymbol(tri.AB) // AB base is A
		usdA, _ := usdPriceOfAsset(ctx, by, baseA)
		pnlUSD := (A2-A0)*usdA - feeUSD
		e.riskEng.UpdatePnL(pnlUSD, 0)
		// Update positions on base assets using USD valuation
		base1, quote1 := splitSymbol(first.sym)
		base2, quote2 := splitSymbol(second.sym)
		base3, quote3 := splitSymbol(third.sym)
		usd1, _ := usdPriceOfAsset(ctx, by, quote1)
		usd2, _ := usdPriceOfAsset(ctx, by, quote2)
		usd3, _ := usdPriceOfAsset(ctx, by, quote3)
		// base USD prices
		b1USD := avg1
		if b1USD <= 0 {
			b1USD = first.px
		}
		b1USD = b1USD * usd1
		b2USD := avg2
		if b2USD <= 0 {
			b2USD = second.px
		}
		b2USD = b2USD * usd2
		b3USD := avg3
		if b3USD <= 0 {
			b3USD = third.px
		}
		b3USD = b3USD * usd3
		// leg1
		if first.side == common.Sell {
			e.riskEng.UpdatePosition(base1, by.Name(), -filled1, -filled1*b1USD)
		} else {
			e.riskEng.UpdatePosition(base1, by.Name(), +filled1, +filled1*b1USD)
		}
		// leg2
		if second.side == common.Sell {
			e.riskEng.UpdatePosition(base2, by.Name(), -qty2, -qty2*b2USD)
		} else {
			e.riskEng.UpdatePosition(base2, by.Name(), +qty2, +qty2*b2USD)
		}
		// leg3
		if third.side == common.Sell {
			e.riskEng.UpdatePosition(base3, by.Name(), -qty3, -qty3*b3USD)
		} else {
			e.riskEng.UpdatePosition(base3, by.Name(), +qty3, +qty3*b3USD)
		}
	}
	return nil
}

// unwindPair attempts IOC unwind with configured slippage and TTL
func (e *Engine) unwindPair(ctx context.Context, by common.ExchangeAdapter, symbol string, side common.OrderSide, qty float64, refPrice float64) {
	if qty <= 0 || refPrice <= 0 {
		return
	}
	// compute worst acceptable price based on max unwind slippage bps
	maxBps := e.cfg.Trading.MaxUnwindSlippageBps
	skew := maxBps / 10000.0
	price := refPrice
	if side == common.Buy {
		price = refPrice * (1 + skew)
	} else {
		price = refPrice * (1 - skew)
	}
	ttl := e.cfg.Trading.OrderTTLMs
	if ttl <= 0 {
		ttl = 1500
	}
	ctxU, cancelU := context.WithTimeout(ctx, time.Duration(ttl)*time.Millisecond)
	defer cancelU()
	_, _ = by.PlaceOrder(ctxU, common.Order{Symbol: symbol, Side: side, Qty: qty, Price: price, TimeInForce: "IOC"})
}
