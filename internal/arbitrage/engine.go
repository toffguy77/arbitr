package arbitrage

import (
	"context"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/strategy"
)

type Engine struct {
	cfg config.Config
	adapters map[string]common.ExchangeAdapter
	logger log.Logger
	triangles []struct{ AB, BC, CA string }
	bestCh chan spread
}

type spread struct {
	Tri struct{ AB, BC, CA string }
	Dir string // F or R
	Gross float64
	Net float64
	Ts time.Time
}

func New(cfg config.Config, adapters map[string]common.ExchangeAdapter, logger log.Logger) *Engine {
return &Engine{cfg: cfg, adapters: adapters, logger: logger, bestCh: make(chan spread, 1024)}
}

func (e *Engine) Run(ctx context.Context) error {
	// one-time: filter triangles by available markets if possible
	by := e.adapters["bybit"]
	if by != nil {
		// optional symbol lister
		type symbolLister interface{ ListSpotSymbols(context.Context) (map[string]struct{}, error) }
		if lister, ok := by.(symbolLister); ok {
			ctxTO, cancel := context.WithTimeout(ctx, 5*time.Second)
			syms, err := lister.ListSpotSymbols(ctxTO)
			cancel()
			if err == nil {
				for _, tri := range e.cfg.Trading.Triangles {
					if _, ok1 := syms[tri.AB]; !ok1 { continue }
					if _, ok2 := syms[tri.BC]; !ok2 { continue }
					if _, ok3 := syms[tri.CA]; !ok3 { continue }
					e.triangles = append(e.triangles, tri)
				}
			}
		}
	}
	if len(e.triangles) == 0 {
		e.triangles = e.cfg.Trading.Triangles
		e.logger.Info().Int("configured", len(e.cfg.Trading.Triangles)).Int("available", len(e.triangles)).Msg("triangle filtering skipped (symbols list unavailable)")
	} else {
		// Log filtering outcome
		rejected := len(e.cfg.Trading.Triangles) - len(e.triangles)
		e.logger.Info().Int("configured", len(e.cfg.Trading.Triangles)).Int("available", len(e.triangles)).Int("rejected", rejected).Msg("triangles filtered by Bybit instruments list")
		if rejected > 0 {
			// Build details for debug
			missing := []string{}
			// For simplicity, re-evaluate and log what was missing
			present := map[string]struct{}{}
			for _, tri := range e.triangles { present[tri.AB] = struct{}{}; present[tri.BC] = struct{}{}; present[tri.CA] = struct{}{} }
			for _, tri := range e.cfg.Trading.Triangles {
				count := 0
				if _, ok := present[tri.AB]; ok { count++ }
				if _, ok := present[tri.BC]; ok { count++ }
				if _, ok := present[tri.CA]; ok { count++ }
				if count < 3 {
					missing = append(missing, tri.AB+","+tri.BC+","+tri.CA)
				}
			}
			e.logger.Debug().Strs("rejected_triangles", missing).Msg("triangles missing one or more markets")
		}
	}

	// periodic logger of top spreads
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				// drain channel and keep top 10 by net
				buf := make([]spread, 0, len(e.bestCh))
				for {
					select {
					case s := <-e.bestCh:
						buf = append(buf, s)
					default:
						goto RANK
					}
				}
			RANK:
				if len(buf) == 0 { continue }
				// simple selection for top 10
				n := 10
				if len(buf) < n { n = len(buf) }
				for i := 0; i < n; i++ {
					maxIdx := i
					for j := i + 1; j < len(buf); j++ { if buf[j].Net > buf[maxIdx].Net { maxIdx = j } }
					buf[i], buf[maxIdx] = buf[maxIdx], buf[i]
				}
				for i := 0; i < n; i++ {
					_ = i // Here we would log using a logger; for now expose via metrics gauges if needed or integrate logger into Engine later.
				}
			}
		}
	}()

	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			// Intra-exchange (Bybit) triangular scan
			by := e.adapters["bybit"]
			if by == nil { continue }
			for _, tri := range e.triangles {
				ctxTO, cancel := context.WithTimeout(ctx, 2*time.Second)
				tAB, eAB := by.GetTicker(ctxTO, tri.AB)
				tBC, eBC := by.GetTicker(ctxTO, tri.BC)
				tCA, eCA := by.GetTicker(ctxTO, tri.CA)
				cancel()
				if eAB != nil || eBC != nil || eCA != nil { continue }
				grossF, netF := strategy.EvalTriangleForward(
					tAB.Bid, tBC.Bid, tCA.Bid,
					e.cfg.Trading.FeesBps["bybit"],
					e.cfg.Trading.SlippageBps,
					e.cfg.Trading.RiskReserveBps,
				)
				grossR, netR := strategy.EvalTriangleReverse(
					tAB.Ask, tBC.Bid, tCA.Ask,
					e.cfg.Trading.FeesBps["bybit"],
					e.cfg.Trading.SlippageBps,
					e.cfg.Trading.RiskReserveBps,
				)
				net := netF
				gross := grossF
				if netR > netF { net = netR; gross = grossR }
				metrics.TrianglesCheckedTotal.Inc()
				metrics.TriangleGrossBps.Observe(gross)
				metrics.TriangleNetBps.Observe(net)
				if net >= e.cfg.Trading.MinNetBps {
					metrics.ArbOppsFound.Inc()
					// Paper mode: mark executed
					metrics.ArbOppsExecuted.Inc()
					// push to best tracker
					select {
					case e.bestCh <- spread{Tri: tri, Dir: func() string { if net == netF { return "F" } else { return "R" } }(), Gross: gross, Net: net, Ts: time.Now() }:
					default:
					}
				}
			}
		}
	}
}
