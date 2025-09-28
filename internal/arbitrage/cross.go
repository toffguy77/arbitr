package arbitrage

import (
	"context"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/risk"
)

// scanCrossTick evaluates cross-exchange opportunities across configured pairs
func (e *Engine) scanCrossTick(ctx context.Context) {
	// build exchange list
	names := make([]string, 0, len(e.adapters))
	for name := range e.adapters {
		names = append(names, name)
	}
	if len(names) < 2 { return }

	pairs := e.cfg.Trading.Pairs
	if len(pairs) == 0 { return }

	workers := 6
	sem := make(chan struct{}, workers)
	done := make(chan struct{})

	type job struct { sym, buyEx, sellEx string }
	jobs := make([]job, 0, len(pairs)*len(names)*len(names))
	for _, sym := range pairs {
		for i := range names {
			for j := range names {
				if i == j { continue }
				jobs = append(jobs, job{ sym: sym, buyEx: names[i], sellEx: names[j] })
			}
		}
	}

	for _, jb := range jobs {
		jb := jb
		sem <- struct{}{}
		go func(){
			defer func(){ <-sem; done <- struct{}{} }()
			buy := e.adapters[jb.buyEx]
			sell := e.adapters[jb.sellEx]
			if buy == nil || sell == nil { return }
			// fetch tickers with timeout
			ctxTO, cancel := context.WithTimeout(ctx, 2*time.Second)
			tBuy, err1 := buy.GetTicker(ctxTO, jb.sym)
			tSell, err2 := sell.GetTicker(ctxTO, jb.sym)
			cancel()
			if err1 != nil || err2 != nil { return }
			ask, _ := tBuy.Ask.Float64()
			bid, _ := tSell.Bid.Float64()
			if ask <= 0 || bid <= 0 { return }
			gross := (bid/ask - 1.0) * 10000.0
			fees := e.cfg.Trading.FeesBps[jb.buyEx] + e.cfg.Trading.FeesBps[jb.sellEx]
			slip := e.cfg.Trading.SlippageBps // fallback; per-book estimation could refine later
			net := gross - fees - slip - e.cfg.Trading.RiskReserveBps
			metrics.CrossGrossBps.Observe(gross)
			metrics.CrossNetBps.Observe(net)
			baseThreshold, _ := e.cfg.Trading.MinNetBps.Float64()
			if net < baseThreshold { return }
			metrics.CrossOppsFound.Inc()
			// log candidate before execution for visibility
			e.logger.Info().Str("pair", jb.sym).Str("buy_ex", buy.Name()).Str("sell_ex", sell.Name()).Float64("ask", ask).Float64("bid", bid).Float64("net_bps", net).Msg("cross opportunity")
			// execute
			if e.tryExecuteCross(ctx, buy, sell, jb.sym, ask, bid, net) {
				metrics.CrossOppsExecuted.Inc()
			}
		}()
	}
	// wait all
	for i := 0; i < len(jobs); i++ { <-done }
}

func (e *Engine) tryExecuteCross(ctx context.Context, buy common.ExchangeAdapter, sell common.ExchangeAdapter, symbol string, ask float64, bid float64, netBps float64) bool {
	// token bucket: need 2 tokens (two orders)
	e.mu.Lock()
	allow := e.tokens >= 2
	if allow { e.tokens -= 2 }
	e.mu.Unlock()
	if !allow { return false }

	// sizing
	qtyUSDFloat, _ := e.cfg.Trading.MaxNotionalUSD.Float64()
	if qtyUSDFloat <= 0 {
		qtyUSDFloat, _ = e.cfg.Trading.NotionalUSD.Float64()
	}
	if qtyUSDFloat <= 0 { qtyUSDFloat = 50 }
	qty := qtyUSDFloat / ask
	if qty <= 0 { return false }

	// price skew (same as triangles)
	skew := (e.cfg.Trading.PriceSkewBps) / 10000.0
	buyPx := ask * (1 + skew)
	sellPx := bid * (1 - skew)

	// enforce filters
	roundDown := func(x, step float64) float64 { if step <= 0 { return x }; return math.Floor(x/step)*step }
	getFilters := func(ad common.ExchangeAdapter) (qStep, pStep, minQty, minNot float64) {
		if fp, ok := ad.(common.SymbolFiltersProvider); ok {
			ctxS, cancelS := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancelS()
			if qs, ps, mq, mn, ok2 := fp.GetSymbolFilters(ctxS, symbol); ok2 { return qs, ps, mq, mn }
		}
		return 0,0,0,0
	}
	qs1, ps1, mq1, mn1 := getFilters(buy)
	qs2, ps2, mq2, mn2 := getFilters(sell)
	qStep := math.Max(qs1, qs2)
	pStep := math.Max(ps1, ps2)
	minQty := math.Max(mq1, mq2)
	minNot := math.Max(mn1, mn2)
	qty = roundDown(qty, qStep)
	buyPx = roundDown(buyPx, pStep)
	sellPx = roundDown(sellPx, pStep)
	if qty <= 0 || buyPx <= 0 || sellPx <= 0 { return false }
	if minQty > 0 && qty < minQty { return false }
	if minNot > 0 && qty*buyPx < minNot { return false }

	// risk check (on buy leg value)
	if e.riskEng != nil {
		ok, reason, adjQty, _ := e.riskEng.Allow(riskOrderReq(symbol, common.Buy, qty, buyPx, buy.Name()))
		if !ok { metrics.RiskBlocks.Inc(); e.logger.Debug().Str("reason", reason).Msg("risk blocked cross"); return false }
		if adjQty > 0 { qty = adjQty }
	}
	if qty <= 0 { return false }

	// log attempt
	e.logger.Info().Str("pair", symbol).Str("buy_ex", buy.Name()).Str("sell_ex", sell.Name()).Float64("ask", ask).Float64("bid", bid).Float64("net_bps", netBps).Float64("qty", qty).Msg("starting cross execution")
	// place buy IOC
	ctx1, c1 := context.WithTimeout(ctx, time.Duration(max(800, e.cfg.Trading.OrderTTLMs/2))*time.Millisecond)
	idBuy, err := buy.PlaceOrder(ctx1, common.Order{Symbol: symbol, Side: common.Buy, Qty: decimal.NewFromFloat(qty), Price: decimal.NewFromFloat(buyPx), TimeInForce: "IOC"})
	c1()
	if err != nil || idBuy == "" { e.logger.Debug().Err(err).Msg("buy leg rejected"); return false }
	filled := qty
	avgBuy := buyPx
	if fi, ok := buy.(common.OrderFillInfoQuerier); ok {
		if px, _, f, err := fi.GetOrderFillInfo(ctx, idBuy); err == nil && f > 0 { avgBuy, filled = px, f }
	}
	if qs, ok := buy.(common.OrderStatusQuerier); ok {
		ctxS, cS := context.WithTimeout(ctx, 1500*time.Millisecond)
		st, f, err := qs.GetOrderStatus(ctxS, idBuy)
		cS()
		if err == nil && st != "Filled" && st != "PartiallyFilled" { return false }
		if f > 0 { filled = f }
	}
	filled = math.Min(filled, qty)
	if filled <= 0 { return false }
	// place sell IOC for filled
	ctx2, c2 := context.WithTimeout(ctx, time.Duration(max(800, e.cfg.Trading.OrderTTLMs/2))*time.Millisecond)
	idSell, err2 := sell.PlaceOrder(ctx2, common.Order{Symbol: symbol, Side: common.Sell, Qty: decimal.NewFromFloat(filled), Price: decimal.NewFromFloat(sellPx), TimeInForce: "IOC"})
	c2()
	avgSell := sellPx
	if fi, ok := sell.(common.OrderFillInfoQuerier); ok {
		if px, _, f, err := fi.GetOrderFillInfo(ctx, idSell); err == nil && f > 0 { avgSell = px }
	}
	if err2 != nil || idSell == "" {
		// unwind buy on buy exchange
		e.unwindPair(ctx, buy, symbol, common.Sell, filled, avgBuy)
		return false
	}
	// Update PnL best-effort (assume USDT quote ~ USD)
	if e.riskEng != nil {
		pnlUSD := (avgSell-avgBuy) * filled
		e.riskEng.UpdatePnL(pnlUSD, 0)
	}
	return true
}

func riskOrderReq(symbol string, side common.OrderSide, qty float64, px float64, exch string) risk.OrderRequest {
	return risk.OrderRequest{ Symbol: symbol, Side: side, Qty: qty, Price: px, Exchange: exch, ValueUSD: qty * px }
}
