package strategy

// Pseudocode for triangular arbitrage (within one exchange):
// 1) Choose base asset A, quote B, cross C.
// 2) Paths: A->B, B->C, C->A (or reverse depending on orderbook sides).
// 3) Use L2 books to compute executable size given depth and fees/slippage.
// 4) Compute grossSpreadBps = (product of prices roundtrip - 1)*10000.
// 5) Compute slippage via SlippageModel.Integral(book, qty).
// 6) netSpreadBps = grossSpreadBps - feesBps - slippageBps - riskReserveBps.
// 7) If netSpreadBps >= 4 bps, construct ArbPlan and submit.
