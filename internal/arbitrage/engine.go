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
	cfg      config.Config
	adapters map[string]common.ExchangeAdapter
	logger   log.Logger
	bestCh   chan spread
	// risk/state controls
	consec   map[string]int
	cooldown map[string]time.Time
	pnlUSD   float64
	stopped  bool
}

type spread struct {
	Tri   config.Triangle
	Dir   string // F or R
	Gross float64
	Net   float64
	Ts    time.Time
}

func New(cfg config.Config, adapters map[string]common.ExchangeAdapter, logger log.Logger) *Engine {
	return &Engine{cfg: cfg, adapters: adapters, logger: logger, bestCh: make(chan spread, 1024), consec: make(map[string]int), cooldown: make(map[string]time.Time)}
}

func (e *Engine) Run(ctx context.Context) error {
	// Log balances at startup if adapters support it
	for name, ad := range e.adapters {
		if b, ok := ad.(common.Balancer); ok {
			ctxTO, cancel := context.WithTimeout(ctx, 5*time.Second)
			balances, err := b.GetBalances(ctxTO)
			cancel()
			if err != nil {
				e.logger.Debug().Err(err).Str("exchange", name).Msg("failed to fetch balances")
			} else {
				for _, bl := range balances {
					e.logger.Info().Str("exchange", name).Str("asset", bl.Asset).Float64("free", bl.Free).Msg("balance at startup")
				}
			}
		}
	}

	// one-time: filter triangles by available markets if possible
	triangles := make([]config.Triangle, 0)
	by := e.adapters["bybit"]
	if by != nil {
		// optional symbol lister
		type symbolLister interface {
			ListSpotSymbols(context.Context) (map[string]struct{}, error)
		}
		if lister, ok := by.(symbolLister); ok {
			ctxTO, cancel := context.WithTimeout(ctx, 5*time.Second)
			syms, err := lister.ListSpotSymbols(ctxTO)
			cancel()
			if err == nil {
				for _, tri := range e.cfg.Trading.Triangles {
					if _, ok1 := syms[tri.AB]; !ok1 {
						continue
					}
					if _, ok2 := syms[tri.BC]; !ok2 {
						continue
					}
					if _, ok3 := syms[tri.CA]; !ok3 {
						continue
					}
					triangles = append(triangles, tri)
				}
			} else {
				e.logger.Debug().Err(err).Msg("failed to list Bybit spot symbols; will skip filtering")
			}
		}
	}
	if len(triangles) == 0 {
		// Fallback to configured triangles, but drop invalid (empty) ones
		valid := make([]config.Triangle, 0, len(e.cfg.Trading.Triangles))
		for _, tri := range e.cfg.Trading.Triangles {
			if tri.AB == "" || tri.BC == "" || tri.CA == "" {
				continue
			}
			valid = append(valid, tri)
		}
		triangles = valid
		e.logger.Info().Int("configured", len(e.cfg.Trading.Triangles)).Int("available", len(triangles)).Msg("triangle filtering skipped (symbols list unavailable)")
	} else {
		// Log filtering outcome
		rejected := len(e.cfg.Trading.Triangles) - len(triangles)
		e.logger.Info().Int("configured", len(e.cfg.Trading.Triangles)).Int("available", len(triangles)).Int("rejected", rejected).Msg("triangles filtered by Bybit instruments list")
		if rejected > 0 {
			// Build details for debug
			missing := []string{}
			// For simplicity, re-evaluate and log what was missing
			present := map[string]struct{}{}
			for _, tri := range triangles {
				present[tri.AB] = struct{}{}
				present[tri.BC] = struct{}{}
				present[tri.CA] = struct{}{}
			}
			for _, tri := range e.cfg.Trading.Triangles {
				count := 0
				if _, ok := present[tri.AB]; ok {
					count++
				}
				if _, ok := present[tri.BC]; ok {
					count++
				}
				if _, ok := present[tri.CA]; ok {
					count++
				}
				if count < 3 {
					missing = append(missing, tri.AB+","+tri.BC+","+tri.CA)
				}
			}
			e.logger.Debug().Strs("rejected_triangles", missing).Msg("triangles missing one or more markets")
		}
		// After triangles prepared, prefetch symbol steps if adapter supports it
		allSyms := make(map[string]struct{})
		for _, tri := range triangles {
			allSyms[tri.AB] = struct{}{}
			allSyms[tri.BC] = struct{}{}
			allSyms[tri.CA] = struct{}{}
		}
		if pf, ok := by.(common.SymbolStepsPrefetcher); ok {
			slice := make([]string, 0, len(allSyms))
			for s := range allSyms {
				slice = append(slice, s)
			}
			ctxTO, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = pf.PrefetchSymbolSteps(ctxTO, slice)
			cancel()
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
				if len(buf) == 0 {
					continue
				}
				// simple selection for top 10
				n := 10
				if len(buf) < n {
					n = len(buf)
				}
				for i := 0; i < n; i++ {
					maxIdx := i
					for j := i + 1; j < len(buf); j++ {
						if buf[j].Net > buf[maxIdx].Net {
							maxIdx = j
						}
					}
					buf[i], buf[maxIdx] = buf[maxIdx], buf[i]
				}
				for i := 0; i < n; i++ {
					_ = i // hook for future logging
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
			if by == nil {
				continue
			}
			for _, tri := range triangles {
				if tri.AB == "" || tri.BC == "" || tri.CA == "" {
					continue
				}
				ctxTO, cancel := context.WithTimeout(ctx, 4*time.Second)
				tAB, eAB := by.GetTicker(ctxTO, tri.AB)
				tBC, eBC := by.GetTicker(ctxTO, tri.BC)
				tCA, eCA := by.GetTicker(ctxTO, tri.CA)
				cancel()
				if eAB != nil || eBC != nil || eCA != nil {
					continue
				}
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
				if netR > netF {
					net = netR
					gross = grossR
				}
				metrics.TrianglesCheckedTotal.Inc()
				metrics.TriangleGrossBps.Observe(gross)
				metrics.TriangleNetBps.Observe(net)
				// risk gating: consecutive confirmations
				key := tri.AB + "|" + tri.BC + "|" + tri.CA
				if net >= e.cfg.Trading.MinNetBps {
					e.consec[key]++
				} else {
					e.consec[key] = 0
				}
				if until, ok := e.cooldown[key]; ok && time.Now().Before(until) {
					continue
				}
				if e.cfg.Trading.EntryConfirmTicks > 0 && e.consec[key] < e.cfg.Trading.EntryConfirmTicks {
					continue
				}
				if net >= e.cfg.Trading.MinNetBps {
					metrics.ArbOppsFound.Inc()
					if e.cfg.Trading.Live && !e.stopped {
						if e.allowedForLive(tri) {
							if id, pnl, err := e.placeFirstLeg(ctx, by, tri, tAB, tBC, tCA); err != nil {
								e.logger.Error().Err(err).Msg("place first leg failed")
							} else if id != "" {
								metrics.OrdersSubmittedTotal.Inc()
								// apply cooldown and accumulate pnl
								if e.cfg.Trading.TriangleCooldownSeconds > 0 {
									e.cooldown[key] = time.Now().Add(time.Duration(e.cfg.Trading.TriangleCooldownSeconds) * time.Second)
								}
								e.pnlUSD += pnl
								if e.cfg.Trading.DailyPnLStopUSD > 0 && e.pnlUSD <= -e.cfg.Trading.DailyPnLStopUSD {
									e.stopped = true
									e.logger.Warn().Float64("pnl_usd", e.pnlUSD).Msg("daily PnL stop reached; halting live orders")
								}
							}
						}
					}
					// Paper mode: mark executed
					metrics.ArbOppsExecuted.Inc()
					// push to best tracker
					select {
					case e.bestCh <- spread{Tri: tri, Dir: func() string {
						if net == netF {
							return "F"
						} else {
							return "R"
						}
					}(), Gross: gross, Net: net, Ts: time.Now()}:
					default:
					}
				}
			}
		}
	}
}
