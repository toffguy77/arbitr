package slippage

import "arbitr/internal/orderbook"

// Integral-based slippage over L2 depth. Returns slippage in bps relative to mid.
func IntegralBps(book orderbook.L2, qty float64, isBuy bool, mid float64) float64 {
	if qty <= 0 || mid <= 0 { return 0 }
	var cost float64
	var filled float64
	if isBuy {
		for _, lvl := range book.Asks {
			use := min(qty-filled, lvl.Qty)
			if use <= 0 { break }
			cost += use * lvl.Price
			filled += use
			if filled >= qty { break }
		}
	} else {
		for _, lvl := range book.Bids {
			use := min(qty-filled, lvl.Qty)
			if use <= 0 { break }
			cost += use * lvl.Price
			filled += use
			if filled >= qty { break }
		}
	}
	if filled < qty { return 1e9 } // effectively reject
	avg := cost / qty
	var diff float64
	if isBuy { diff = avg - mid } else { diff = mid - avg }
	return (diff / mid) * 10000.0 + 0.5 // add 0.5bps buffer
}

func min(a,b float64) float64 { if a<b {return a}; return b }
