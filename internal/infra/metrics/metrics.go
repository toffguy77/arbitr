package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

var (
	DecisionLatencyMs = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "decision_latency_ms", Help: "Decision latency", Buckets: prometheus.LinearBuckets(1, 10, 20)})
	OrderSubmitLatencyMs = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "order_submit_latency_ms", Help: "Order submit latency", Buckets: prometheus.LinearBuckets(1, 10, 20)})
	ArbOppsFound = prometheus.NewCounter(prometheus.CounterOpts{Name: "arbitrage_opportunities_found"})
	ArbOppsExecuted = prometheus.NewCounter(prometheus.CounterOpts{Name: "arbitrage_opportunities_executed"})
	TrianglesCheckedTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "triangles_checked_total", Help: "Total triangles evaluated"})
	TrianglesAttemptedTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "triangles_attempted_total", Help: "Total triangles attempted"})
	OrdersSubmittedTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "orders_submitted_total", Help: "Total orders submitted"})
	OrdersCancelledTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "orders_cancelled_total", Help: "Total orders cancelled"})
	OrdersFilledTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "orders_filled_total", Help: "Total orders filled (acknowledged)"})
	APIErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "api_errors_total", Help: "API errors by exchange and endpoint"}, []string{"exchange","endpoint"})
	TriangleGrossBps = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "triangle_gross_bps", Help: "Gross bps per triangle eval", Buckets: prometheus.LinearBuckets(-50, 5, 41)})
	TriangleNetBps = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "triangle_net_bps", Help: "Net bps per triangle eval", Buckets: prometheus.LinearBuckets(-50, 5, 41)})
	FillsSuccessRatio = prometheus.NewGauge(prometheus.GaugeOpts{Name: "fills_success_ratio"})
	PartialFillRatio = prometheus.NewGauge(prometheus.GaugeOpts{Name: "partial_fill_ratio"})
	NetProfitBps = prometheus.NewGauge(prometheus.GaugeOpts{Name: "net_profit_bps"})
	NetProfitUSD = prometheus.NewGauge(prometheus.GaugeOpts{Name: "net_profit_usd"})
	GrossSpreadBps = prometheus.NewGauge(prometheus.GaugeOpts{Name: "gross_spread_bps"})
	RejectedOrders = prometheus.NewCounter(prometheus.CounterOpts{Name: "rejected_orders"})
	WSReconnects = prometheus.NewCounter(prometheus.CounterOpts{Name: "ws_reconnects"})
	RiskBlocks = prometheus.NewCounter(prometheus.CounterOpts{Name: "risk_blocks"})
	BalanceDesyncEvents = prometheus.NewCounter(prometheus.CounterOpts{Name: "balance_desync_events"})
	SlippageRealizedBps = prometheus.NewGauge(prometheus.GaugeOpts{Name: "slippage_realized_bps"})
	VaR99Intraday = prometheus.NewGauge(prometheus.GaugeOpts{Name: "VaR_99_intraday"})
	DrawdownIntradayBps = prometheus.NewGauge(prometheus.GaugeOpts{Name: "drawdown_intraday_bps"})
	ComplianceBlocksTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "compliance_blocks_total"})
	RestrictedActionsTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "restricted_actions_total"})
	RTTWsMedianMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "rtt_ws_median_ms", Help: "Median WS RTT by exchange"}, []string{"exchange"})
	RTTRestMedianMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "rtt_rest_median_ms", Help: "Median REST RTT by exchange"}, []string{"exchange"})
TimeOffsetMs = prometheus.NewGauge(prometheus.GaugeOpts{Name: "time_offset_ms", Help: "NTP/clock drift in ms"})

// Triangle quality metrics
TrianglesOutcomeTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{Name: "triangles_outcome_total", Help: "Triangle execution outcomes by triangle and outcome"},
	[]string{"triangle","outcome"},
)
TrianglesFailReasonsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{Name: "triangles_fail_reasons_total", Help: "Fail reasons per triangle"},
	[]string{"triangle","reason"},
)
TrianglesNetBpsEma = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{Name: "triangles_net_bps_ema", Help: "EMA of net bps per triangle"},
	[]string{"triangle"},
)

// New metrics for lifecycle and data health
BookStalenessMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "book_staleness_ms", Help: "WS book staleness in ms by exchange"}, []string{"exchange"})
WSReconnectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ws_reconnects_total", Help: "WS reconnects by exchange and reason"}, []string{"exchange","reason"})
ExecutionLatencyMs = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "execution_latency_ms", Help: "Decision to submit latency", Buckets: prometheus.LinearBuckets(1, 5, 40)})
PartialFillsTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "partial_fills_total", Help: "Total partial fills observed"})
OrderTTLExpiredTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "order_ttl_expired_total", Help: "Orders that exceeded TTL"})
RealizedSlippageBps = prometheus.NewGauge(prometheus.GaugeOpts{Name: "realized_slippage_bps", Help: "Realized slippage bps from fills"})
RealizedFeeBps = prometheus.NewGauge(prometheus.GaugeOpts{Name: "realized_fee_bps", Help: "Realized fee bps"})
BookRebuildsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "book_rebuilds_total", Help: "Orderbook snapshot rebuilds by exchange and reason"}, []string{"exchange","reason"})
)

func Init(logger zerolog.Logger) *prometheus.Registry {
	reg := prometheus.NewRegistry()
toRegister := []prometheus.Collector{
		DecisionLatencyMs, OrderSubmitLatencyMs, ArbOppsFound, ArbOppsExecuted,
TrianglesCheckedTotal, TrianglesAttemptedTotal, OrdersSubmittedTotal, OrdersCancelledTotal, OrdersFilledTotal, APIErrorsTotal,
		TriangleGrossBps, TriangleNetBps,
		FillsSuccessRatio, PartialFillRatio, NetProfitBps, NetProfitUSD,
		GrossSpreadBps, RejectedOrders, WSReconnects, RiskBlocks,
		BalanceDesyncEvents, SlippageRealizedBps, VaR99Intraday, DrawdownIntradayBps,
		ComplianceBlocksTotal, RestrictedActionsTotal, RTTWsMedianMs, RTTRestMedianMs, TimeOffsetMs,
TrianglesOutcomeTotal, TrianglesFailReasonsTotal, TrianglesNetBpsEma,
BookStalenessMs, WSReconnectsTotal, ExecutionLatencyMs, PartialFillsTotal, OrderTTLExpiredTotal, RealizedSlippageBps, RealizedFeeBps, BookRebuildsTotal,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	}
	for _, c := range toRegister { _ = reg.Register(c) }
	logger.Info().Msg("Prometheus metrics initialized")
	return reg
}

func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
