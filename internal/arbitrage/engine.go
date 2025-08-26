package arbitrage

import (
	"context"
	"strings"
	"sync"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/log"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/orderbook"
	"arbitr/internal/risk"
	"arbitr/internal/slippage"
	"arbitr/internal/strategy"
)

type Engine struct {
	cfg      config.Config
	adapters map[string]common.ExchangeAdapter
	logger   log.Logger
	bestCh   chan spread
	// guards for shared state
	mu sync.Mutex
	// risk/state controls
	consec   map[string]int
	cooldown map[string]time.Time
	stopped  bool
	riskEng  *risk.Engine
	// adaptive controls
	triStats map[string]*triStat
	triBan   map[string]time.Time
	// order rate limiting (token bucket)
	tokens     float64
	lastRefill time.Time
	// concurrency limiter for executing triangles
	execSem chan struct{}
}

type triStat struct {
	count    int
	success  int
	partials int
	emaNet   float64
}

type spread struct {
	Tri   config.Triangle
	Dir   string // F or R
	Gross float64
	Net   float64
	Ts    time.Time
}

func New(cfg config.Config, adapters map[string]common.ExchangeAdapter, logger log.Logger) *Engine {
	// Initialize basic risk limits from config; percent-based left zero for now
	limits := risk.Limits{
		MaxInventoryUSD: cfg.Trading.MaxInventoryUSDPerBase,
		SoftStopLossUSD: func() float64 {
			if cfg.Trading.DailyPnLStopUSD > 0 {
				return cfg.Trading.DailyPnLStopUSD * 0.5
			}
			return 0
		}(),
		HardStopLossUSD: cfg.Trading.DailyPnLStopUSD,
	}
	return &Engine{cfg: cfg, adapters: adapters, logger: logger, bestCh: make(chan spread, 1024), consec: make(map[string]int), cooldown: make(map[string]time.Time), riskEng: risk.NewEngine(limits), triStats: make(map[string]*triStat), triBan: make(map[string]time.Time), tokens: float64(cfg.Trading.MaxOrdersPerMin), lastRefill: time.Now(), execSem: make(chan struct{}, max(1, cfg.Trading.MaxConcurrentTriangles))}
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
		// Subscribe WS for live feeds if available
		if feeder, ok := by.(common.LiveMarketFeeder); ok {
			syms := func() []string {
				out := make([]string, 0, len(allSyms))
				for s := range allSyms {
					out = append(out, s)
				}
				return out
			}()
			ctxS, cancelS := context.WithTimeout(ctx, 5*time.Second)
			_ = feeder.SubscribeSymbols(ctxS, syms)
			cancelS()
			// Log subscription summary for visibility
			e.logger.Info().Int("symbols", len(syms)).Msg("subscribed to Bybit WS for symbols")
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

	// periodic portfolio value updater (best-effort)
	go func() {
		upd := time.NewTicker(60 * time.Second)
		defer upd.Stop()
		by := e.adapters["bybit"]
		for {
			select {
			case <-ctx.Done():
				return
			case <-upd.C:
				if by == nil {
					continue
				}
				balancer, ok := by.(common.Balancer)
				if !ok {
					continue
				}
				ctxTO, cancel := context.WithTimeout(ctx, 5*time.Second)
				balances, err := balancer.GetBalances(ctxTO)
				cancel()
				if err != nil {
					continue
				}
				var totalUSD float64
				for _, b := range balances {
					if strings.EqualFold(b.Asset, "USDT") || strings.EqualFold(b.Asset, "USD") {
						totalUSD += b.Free
						continue
					}
					// try to price asset in USDT
					tick, err2 := by.GetTicker(ctx, strings.ToUpper(b.Asset)+"USDT")
					if err2 == nil {
						totalUSD += b.Free * tick.Bid
					}
				}
				if e.riskEng != nil {
					e.riskEng.UpdatePortfolioValue(totalUSD)
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
			by := e.adapters["bybit"]
			if by == nil {
				continue
			}

			// refill order tokens per minute
			e.mu.Lock()
			elapsed := time.Since(e.lastRefill).Minutes()
			if elapsed > 0 {
				e.tokens += float64(e.cfg.Trading.MaxOrdersPerMin) * elapsed
				if e.tokens > float64(e.cfg.Trading.MaxOrdersPerMin) {
					e.tokens = float64(e.cfg.Trading.MaxOrdersPerMin)
				}
				e.lastRefill = time.Now()
			}
			e.mu.Unlock()

			// Parallel scan with bounded workers
			workers := 8
			sem := make(chan struct{}, workers)
			var wg sync.WaitGroup
			for _, tri := range triangles {
				tri := tri
				if tri.AB == "" || tri.BC == "" || tri.CA == "" {
					continue
				}
				sem <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() { <-sem; wg.Done() }()
					ctxTO, cancel := context.WithTimeout(ctx, 4*time.Second)
					tAB, eAB := by.GetTicker(ctxTO, tri.AB)
					tBC, eBC := by.GetTicker(ctxTO, tri.BC)
					tCA, eCA := by.GetTicker(ctxTO, tri.CA)
					cancel()
					if eAB != nil || eBC != nil || eCA != nil {
						return
					}

					// Estimate dynamic slippage for tentative qty
					qtyBase := e.cfg.Trading.MaxNotionalUSD
					if qtyBase <= 0 {
						qtyBase = e.cfg.Trading.NotionalUSD
					}
					if qtyBase <= 0 {
						qtyBase = 50
					}
					qtyAB := qtyBase / maxf(tAB.Bid, tAB.Ask)
					// Estimate slippage per direction for more accurate net calculations
					slipF := e.estimateTriangleSlippageBps(ctx, by, tri, qtyAB, tAB, tBC, tCA, "F")
					slipR := e.estimateTriangleSlippageBps(ctx, by, tri, qtyAB, tAB, tBC, tCA, "R")

					grossF, netF := strategy.EvalTriangleForward(
						tAB.Bid, tBC.Bid, tCA.Bid,
						e.cfg.Trading.FeesBps["bybit"],
						slipF,
						e.cfg.Trading.RiskReserveBps,
					)
					grossR, netR := strategy.EvalTriangleReverse(
						tAB.Ask, tBC.Bid, tCA.Ask,
						e.cfg.Trading.FeesBps["bybit"],
						slipR,
						e.cfg.Trading.RiskReserveBps,
					)
					net := netF; gross := grossF; dir := "F"; slipUse := slipF
					if netR > netF { net = netR; gross = grossR; dir = "R"; slipUse = slipR }

					metrics.TrianglesCheckedTotal.Inc()
					metrics.TriangleGrossBps.Observe(gross)
					metrics.TriangleNetBps.Observe(net)

					key := tri.AB + "|" + tri.BC + "|" + tri.CA
					// skip/bump guarded by mutex
					e.mu.Lock()
					if until, ok := e.triBan[key]; ok && time.Now().Before(until) {
						e.mu.Unlock()
						return
					}
					// dynamic threshold per triangle
					minBps := e.dynamicMinNetBps(key)
					if net >= minBps {
						e.consec[key]++
					} else {
						e.consec[key] = 0
					}
					if until, ok := e.cooldown[key]; ok && time.Now().Before(until) {
						e.mu.Unlock()
						return
					}
					okConfirm := !(e.cfg.Trading.EntryConfirmTicks > 0 && e.consec[key] < e.cfg.Trading.EntryConfirmTicks)
					e.mu.Unlock()
					if !okConfirm {
						return
					}

					if net >= minBps {
						metrics.ArbOppsFound.Inc()
						triKey := tri.AB + "," + tri.BC + "," + tri.CA
						// check global hard stop from risk engine
						if e.riskEng != nil && e.riskEng.IsHardStopped() {
							e.mu.Lock()
							e.stopped = true
							e.mu.Unlock()
						}
						e.mu.Lock()
						stopped := e.stopped
						e.mu.Unlock()
						if e.cfg.Trading.Live && !stopped && e.allowedForLive(tri) {
							// check token bucket: need up to 1 unit per attempt (conservative)
							e.mu.Lock()
							allow := e.tokens >= 1
							if allow {
								e.tokens -= 1
							}
							e.mu.Unlock()
							if !allow {
								return
							}
							// try acquire concurrency slot without blocking
							select {
							case e.execSem <- struct{}{}:
							default:
								return
							}
							defer func() { <-e.execSem }()
							metrics.TrianglesAttemptedTotal.Inc()
if err := e.executeTriangle(ctx, by, tri, dir, tAB, tBC, tCA, slipUse, net); err != nil {
								e.logger.Error().Err(err).Msg("triangle execution failed")
								metrics.TrianglesOutcomeTotal.WithLabelValues(triKey, "fail").Inc()
								metrics.TrianglesFailReasonsTotal.WithLabelValues(triKey, "exec_fail").Inc()
								// update stats (no partial info here)
								e.updateTriStats(key, net, false, false)
							} else {
								metrics.ArbOppsExecuted.Inc()
								metrics.OrdersSubmittedTotal.Inc()
								metrics.TrianglesOutcomeTotal.WithLabelValues(triKey, "success").Inc()
								// update stats (no partial info here)
								e.updateTriStats(key, net, true, false)
								if e.cfg.Trading.TriangleCooldownSeconds > 0 {
									e.mu.Lock()
									e.cooldown[key] = time.Now().Add(time.Duration(e.cfg.Trading.TriangleCooldownSeconds) * time.Second)
									e.mu.Unlock()
								}
							}
						}
						select {
						case e.bestCh <- spread{Tri: tri, Dir: dir, Gross: gross, Net: net, Ts: time.Now()}:
						default:
						}
					}
				}()
			}
			wg.Wait()
		}
	}
}

func (e *Engine) updateTriStats(key string, net float64, success bool, partial bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.triStats[key]
	if st == nil {
		st = &triStat{}
		e.triStats[key] = st
	}
	st.count++
	if success {
		st.success++
	}
	if partial {
		st.partials++
	}
	// EMA with alpha=0.2
	alpha := 0.2
	if st.count == 1 {
		st.emaNet = net
	} else {
		st.emaNet = alpha*net + (1-alpha)*st.emaNet
	}
	metrics.TrianglesNetBpsEma.WithLabelValues(key).Set(st.emaNet)
	// Ban logic
	if st.count >= 20 {
		ratio := float64(st.success) / float64(st.count)
		partialRatio := float64(st.partials) / float64(st.count)
		if ratio < 0.2 || partialRatio > 0.6 {
			// ban for 10 minutes and reset stats
			e.triBan[key] = time.Now().Add(10 * time.Minute)
			e.triStats[key] = &triStat{}
		}
	}
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// dynamicMinNetBps computes per-triangle threshold based on EMA and success ratio
func (e *Engine) dynamicMinNetBps(key string) float64 {
	base := e.cfg.Trading.MinNetBps
	e.mu.Lock()
	st := e.triStats[key]
	e.mu.Unlock()
	if st == nil || st.count < 5 {
		return base
	}
	ratio := 0.0
	if st.count > 0 {
		ratio = float64(st.success) / float64(st.count)
	}
	bump := 0.0
	// if success ratio < 50%, add up to +5 bps
	if ratio < 0.5 {
		bump += (0.5 - ratio) * 10.0
	}
	if bump > 5.0 {
		bump = 5.0
	}
	// if EMA below base, add half the gap, capped
	if st.emaNet < base {
		bump += (base - st.emaNet) * 0.5
	}
	if bump > 10.0 {
		bump = 10.0
	}
	out := base + bump
	return out
}

// dynamicSizeFactor returns [0.3,1.0] based on success ratio and EMA
func (e *Engine) dynamicSizeFactor(key string) float64 {
	e.mu.Lock()
	st := e.triStats[key]
	e.mu.Unlock()
	if st == nil || st.count < 10 {
		return 0.6
	}
	ratio := float64(st.success) / float64(st.count)
	// base on success ratio
	f := 0.3 + 0.7*ratio // if ratio=1 -> 1.0; ratio=0 -> 0.3
	// adjust down if EMA below global threshold
	if st.emaNet < e.cfg.Trading.MinNetBps {
		f *= 0.8
	}
	if f < 0.3 {
		f = 0.3
	}
	if f > 1.0 {
		f = 1.0
	}
	return f
}

// estimateTriangleSlippageBps
func (e *Engine) estimateTriangleSlippageBps(ctx context.Context, by common.ExchangeAdapter, tri config.Triangle, qtyAB float64, tAB, tBC, tCA common.Ticker, dir string) float64 {
	ob, ok := by.(common.OrderbookProvider)
	if !ok {
		return e.cfg.Trading.SlippageBps
	}
	// Helper to build L2
	build := func(bids [][2]float64, asks [][2]float64) orderbook.L2 {
		l2 := orderbook.L2{}
		for _, b := range bids {
			l2.Bids = append(l2.Bids, orderbook.Level{Price: b[0], Qty: b[1]})
		}
		for _, a := range asks {
			l2.Asks = append(l2.Asks, orderbook.Level{Price: a[0], Qty: a[1]})
		}
		return l2
	}
	ctxTO, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bidsAB, asksAB, ok1 := ob.GetOrderbookL2(ctxTO, tri.AB, 10)
	bidsBC, asksBC, ok2 := ob.GetOrderbookL2(ctxTO, tri.BC, 10)
	bidsCA, asksCA, ok3 := ob.GetOrderbookL2(ctxTO, tri.CA, 10)
	if !ok1 || !ok2 || !ok3 {
		return e.cfg.Trading.SlippageBps
	}
	midAB := (tAB.Bid + tAB.Ask) / 2
	midBC := (tBC.Bid + tBC.Ask) / 2
	midCA := (tCA.Bid + tCA.Ask) / 2
	// Directional slippage per leg
	var slAB, slBC, slCA float64
	if dir == "F" {
		// sells against bids
		slAB = slippage.IntegralBps(build(bidsAB, asksAB), qtyAB, false, midAB)
		slBC = slippage.IntegralBps(build(bidsBC, asksBC), qtyAB, false, midBC)
		slCA = slippage.IntegralBps(build(bidsCA, asksCA), qtyAB, false, midCA)
	} else {
		// buy CA, sell BC, buy AB
		slAB = slippage.IntegralBps(build(bidsAB, asksAB), qtyAB, true, midAB)
		slBC = slippage.IntegralBps(build(bidsBC, asksBC), qtyAB, false, midBC)
		slCA = slippage.IntegralBps(build(bidsCA, asksCA), qtyAB, true, midCA)
	}
	return (slAB + slBC + slCA) / 3.0
}
