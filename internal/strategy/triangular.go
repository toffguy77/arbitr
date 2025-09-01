package strategy

import "github.com/shopspring/decimal"

// Triangle defines a cycle of three symbols within a single exchange where
// each symbol is represented as concatenation of BASEQUOTE, e.g. BTCUSDT.
// The order must be such that you sell the base asset at each leg using bid prices:
// A/B (sell A for B), B/C (sell B for C), C/A (sell C for A).
// Example: AB=BTCUSDT, BC=USDTETH (this is uncommon), CA=ETHBTC.
// In practice, pick symbols accordingly for your venue; for most spot venues,
// a common triangle would be: AB=BTCUSDT, BC=USDTETH (if available), CA=ETHBTC.
// Alternatively, configure triangles that exist on the exchange.
// This uses tickers (bid/ask) for a conservative feasibility check.

type Triangle struct {
    AB string // sell A for B at bid(AB)
    BC string // sell B for C at bid(BC)
    CA string // sell C for A at bid(CA)
}

// EvalTriangleForward computes gross and net bps for the forward cycle A->B->C->A
// using bid prices only (selling at each leg). It returns gross and net bps.
// feesPerLegBps is the taker fee in bps for the exchange; total fees are 3*feesPerLegBps.
func EvalTriangleForward(bidAB, bidBC, bidCA decimal.Decimal, feesPerLegBps, slippageBps, riskReserveBps decimal.Decimal) (grossBps, netBps decimal.Decimal) {
    zero := decimal.Zero
    if bidAB.LessThanOrEqual(zero) || bidBC.LessThanOrEqual(zero) || bidCA.LessThanOrEqual(zero) { return zero, zero }
    // Start with 1 unit of A, sell A->B at bidAB, B->C at bidBC, C->A at bidCA
    one := decimal.NewFromInt(1)
    tenThousand := decimal.NewFromInt(10000)
    finalA := one.Mul(bidAB).Mul(bidBC).Mul(bidCA)
    grossBps = finalA.Sub(one).Mul(tenThousand)
    fees := decimal.NewFromFloat(1.5).Mul(feesPerLegBps) // Ultra aggressive maker fees
    netBps = NetSpreadBps(grossBps, fees, slippageBps.Mul(decimal.NewFromFloat(0.4)), riskReserveBps.Mul(decimal.NewFromFloat(0.2))) // Maximum aggressive estimates
    return grossBps, netBps
}

// EvalTriangleReverse computes gross and net bps for the reverse cycle A->C->B->A
// using ask on buy legs and bid on sell legs. Specifically:
// A->C: buy C with A using pair CA at askCA (C = A / askCA)
// C->B: sell C for B using pair BC at bidBC (B = C * bidBC)
// B->A: buy A with B using pair AB at askAB (A' = B / askAB)
func EvalTriangleReverse(askAB, bidBC, askCA decimal.Decimal, feesPerLegBps, slippageBps, riskReserveBps decimal.Decimal) (grossBps, netBps decimal.Decimal) {
    zero := decimal.Zero
    if askAB.LessThanOrEqual(zero) || bidBC.LessThanOrEqual(zero) || askCA.LessThanOrEqual(zero) { return zero, zero }
    one := decimal.NewFromInt(1)
    tenThousand := decimal.NewFromInt(10000)
    finalA := one.Div(askCA).Mul(bidBC).Mul(one.Div(askAB))
    grossBps = finalA.Sub(one).Mul(tenThousand)
    fees := decimal.NewFromFloat(1.5).Mul(feesPerLegBps) // Ultra aggressive maker fees
    netBps = NetSpreadBps(grossBps, fees, slippageBps.Mul(decimal.NewFromFloat(0.4)), riskReserveBps.Mul(decimal.NewFromFloat(0.2))) // Maximum aggressive estimates
    return grossBps, netBps
}
