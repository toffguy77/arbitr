package arbitrage

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
)

// allowedForLive checks whether all legs of the triangle are permitted for live trading.
func (e *Engine) allowedForLive(tri config.Triangle) bool {
	if len(e.cfg.Trading.AllowedSymbols) == 0 { return true }
	ok := func(sym string) bool {
		for _, s := range e.cfg.Trading.AllowedSymbols {
			if s == sym { return true }
		}
		return false
	}
	return ok(tri.AB) && ok(tri.BC) && ok(tri.CA)
}

// placeFirstLeg submits a small limit order on the AB leg as a safe testnet action.
func (e *Engine) placeFirstLeg(ctx context.Context, by common.ExchangeAdapter, tri config.Triangle, tAB, tBC, tCA common.Ticker) (string, error) {
	_ = tBC; _ = tCA
	if tri.AB == "" { return "", fmt.Errorf("empty symbol AB") }
	price := tAB.Bid
	if price <= 0 { return "", fmt.Errorf("invalid price") }
	qty := e.cfg.Trading.MaxNotionalUSD / price
	if qty <= 0 { return "", fmt.Errorf("invalid qty") }

	// Fetch balances (will also be logged below) to decide side/qty
	var balances []common.Balance
	pre := map[string]float64{}
	if b, ok := by.(common.Balancer); ok {
		ctxB, cancelB := context.WithTimeout(ctx, 4*time.Second)
		bls, err := b.GetBalances(ctxB)
		cancelB()
		if err == nil {
			balances = bls
			for _, bl := range bls {
				pre[strings.ToUpper(bl.Asset)] = bl.Free
				e.logger.Info().Str("exchange", by.Name()).Str("asset", bl.Asset).Float64("free", bl.Free).Msg("balance before order")
			}
		} else {
			e.logger.Debug().Err(err).Str("exchange", by.Name()).Msg("failed to fetch balances before order")
		}
	}

	// Derive base/quote for symbols like BTCUSDT, ETHUSDT, SOLUSDT
	base := tri.AB
	quote := ""
	if strings.HasSuffix(tri.AB, "USDT") {
		base = strings.TrimSuffix(tri.AB, "USDT")
		quote = "USDT"
	}

	// Build balance map for quick lookup
	bal := map[string]float64{}
	for _, b := range balances { bal[strings.ToUpper(b.Asset)] = b.Free }

	// Decide side: SELL if enough base balance; otherwise BUY
	side := common.OrderSide("sell")
	if bal[strings.ToUpper(base)] < qty*1.0001 {
		side = common.OrderSide("buy")
	}

	// Choose price: for BUY use Ask, for SELL use Bid to improve fill probability
	if side == common.Buy {
		if tAB.Ask > 0 { price = tAB.Ask }
	} else {
		if tAB.Bid > 0 { price = tAB.Bid }
	}
	// Apply configurable micro skew to improve execution probability
	skew := e.cfg.Trading.PriceSkewBps / 10000.0
	if skew > 0 {
		if side == common.Buy { price = price * (1 + skew) } else { price = price * (1 - skew) }
	}

	// Apply fee buffer when sizing to avoid insufficient balance due to fees
	feeBps := e.cfg.Trading.FeesBps["bybit"]
	feeFactor := 1.0 - (feeBps/10000.0) - 0.002 // include a slightly larger safety margin of 0.2%
	if feeFactor < 0.0 { feeFactor = 0.0 }

	// Cap qty to available balance depending on side, including fee buffer
	if side == common.Sell {
		if free := bal[strings.ToUpper(base)]; free > 0 {
			maxQty := free * feeFactor
			if qty > maxQty { qty = maxQty }
		}
	} else { // buy
		// ensure we have quote currency; cap by USDT balance if known
		if quote == "USDT" {
			if free := bal["USDT"]; free > 0 && price > 0 {
				maxQty := (free / price) * feeFactor
				if qty > maxQty { qty = maxQty }
			}
		}
	}

	// Also respect MaxNotionalUSD cap with the potentially adjusted price
	maxByNotional := e.cfg.Trading.MaxNotionalUSD / price
	if maxByNotional > 0 && qty > maxByNotional { qty = maxByNotional }

	if qty <= 0 { return "", fmt.Errorf("qty after balance/fee/notional checks is zero") }

	// Round price/qty using exchange-provided steps when available; fallback to conservative defaults
	qtyStep := 0.000001 // fallback 1e-6
	priceStep := 0.1    // fallback for USDT-quoted majors
	if st, ok := by.(common.SymbolStepper); ok {
		ctxS, cancelS := context.WithTimeout(ctx, 3*time.Second)
		if qStep, pStep, ok2 := st.GetSymbolSteps(ctxS, tri.AB); ok2 {
			qtyStep = qStep
			priceStep = pStep
		}
		cancelS()
	} else if quote == "" {
		priceStep = 0.0001
	}
	roundDown := func(x, step float64) float64 {
		if step <= 0 { return x }
		return math.Floor(x/step) * step
	}
	qty = roundDown(qty, qtyStep)
	price = roundDown(price, priceStep)
	if qty <= 0 || price <= 0 { return "", fmt.Errorf("qty/price became zero after rounding") }

	ctxTO, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ord := common.Order{ Symbol: tri.AB, Side: side, Qty: qty, Price: price }
	id, err := by.PlaceOrder(ctxTO, ord)

	// Log balances after order if supported
	if b, ok := by.(common.Balancer); ok {
		ctxA, cancelA := context.WithTimeout(ctx, 4*time.Second)
		bls, err2 := b.GetBalances(ctxA)
		cancelA()
		if err2 == nil {
			post := map[string]float64{}
			for _, bl := range bls {
				post[strings.ToUpper(bl.Asset)] = bl.Free
				e.logger.Info().Str("exchange", by.Name()).Str("asset", bl.Asset).Float64("free", bl.Free).Msg("balance after order")
			}
			// If order succeeded, compute deltas and estimated PnL
			if err == nil && id != "" {
				baseU := strings.ToUpper(base)
				quoteU := strings.ToUpper(quote)
				db := post[baseU] - pre[baseU]
				dq := post[quoteU] - pre[quoteU]
				mid := price
				if tAB.Bid > 0 && tAB.Ask > 0 { mid = (tAB.Bid + tAB.Ask) / 2 }
				pnlQuote := dq + db*mid
				e.logger.Info().
					Str("exchange", by.Name()).
					Str("symbol", tri.AB).
					Str("side", string(side)).
					Float64("qty", qty).
					Float64("price", price).
					Float64("pnl_quote", pnlQuote).
					Str("base", baseU).Float64("base_delta", db).Float64("base_after", post[baseU]).
					Str("quote", quoteU).Float64("quote_delta", dq).Float64("quote_after", post[quoteU]).
					Msg("order executed: balances updated and pnl estimated")
			}
		} else {
			e.logger.Debug().Err(err2).Str("exchange", by.Name()).Msg("failed to fetch balances after order")
		}
	}
	return id, err
}

