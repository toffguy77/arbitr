package strategy

import "github.com/shopspring/decimal"

// NetSpreadBps computes net spread after fees, slippage, and risk reserves using decimal precision
func NetSpreadBps(grossSpreadBps, feesBps, slippageBps, riskReserveBps decimal.Decimal) decimal.Decimal {
	return grossSpreadBps.Sub(feesBps).Sub(slippageBps).Sub(riskReserveBps)
}

// Reject opportunities below 0.5 bps for aggressive trading
func Reject(netSpreadBps decimal.Decimal) bool { 
	return netSpreadBps.LessThan(decimal.NewFromFloat(0.5)) 
}

// PositionSizeUSD computes aggressive position size in USD
func PositionSizeUSD(maxNotionalUSD, netBps decimal.Decimal) decimal.Decimal {
	zero := decimal.Zero
	if netBps.LessThanOrEqual(zero) {
		return zero
	}
	// Aggressive scaling - use higher multiplier for better spreads
	fifty := decimal.NewFromInt(50)
	two := decimal.NewFromInt(2)
	spreadFactor := netBps.Div(fifty)
	if spreadFactor.GreaterThan(two) {
		spreadFactor = two
	}
	// Boost position size by 2x for aggressive trading
	boost := decimal.NewFromFloat(2.0)
	return maxNotionalUSD.Mul(spreadFactor).Mul(boost)
}
