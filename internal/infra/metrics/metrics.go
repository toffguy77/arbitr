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
)

func Init(logger zerolog.Logger) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	toRegister := []prometheus.Collector{
		DecisionLatencyMs, OrderSubmitLatencyMs, ArbOppsFound, ArbOppsExecuted,
		TrianglesCheckedTotal, TriangleGrossBps, TriangleNetBps,
		FillsSuccessRatio, PartialFillRatio, NetProfitBps, NetProfitUSD,
		GrossSpreadBps, RejectedOrders, WSReconnects, RiskBlocks,
		BalanceDesyncEvents, SlippageRealizedBps, VaR99Intraday, DrawdownIntradayBps,
		ComplianceBlocksTotal, RestrictedActionsTotal, RTTWsMedianMs, RTTRestMedianMs, TimeOffsetMs,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	}
	for _, c := range toRegister { _ = reg.Register(c) }
	logger.Info().Msg("Prometheus metrics initialized")
	return reg
}

func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
