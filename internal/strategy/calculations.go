package strategy

// Example net spread calculation (bps)
func NetSpreadBps(grossSpreadBps, feesBps, slippageBps, riskReserveBps float64) float64 {
	return grossSpreadBps - feesBps - slippageBps - riskReserveBps
}

func Reject(netSpreadBps float64) bool { return netSpreadBps < 4.0 }
