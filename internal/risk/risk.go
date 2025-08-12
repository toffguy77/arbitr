package risk

type Limits struct {
	MaxAssetPct float64
	MaxExchangePct float64
	MaxNotionalPct float64
	DailyVar99Pct float64
}

type Engine interface {
	Allow(order any) bool
}
