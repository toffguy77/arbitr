package arbitrage

import (
	"context"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/exchange/common"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/strategy"
)

type Engine struct {
	cfg config.Config
	adapters map[string]common.ExchangeAdapter
}

func New(cfg config.Config, adapters map[string]common.ExchangeAdapter) *Engine {
	return &Engine{cfg: cfg, adapters: adapters}
}

func (e *Engine) Run(ctx context.Context) error {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			for _, pair := range e.cfg.Trading.Pairs {
				// pairwise: binance vs kraken
				ba := e.adapters["binance"]
				kr := e.adapters["kraken"]
				ctxTO, cancel := context.WithTimeout(ctx, 2*time.Second)
				bt, be := ba.GetTicker(ctxTO, pair)
				kt, ke := kr.GetTicker(ctxTO, pair)
				cancel()
				if be != nil || ke != nil { continue }

				// two directions: buy on cheaper ask, sell on higher bid
				check := func(buyEx, sellEx string, buyAsk, sellBid float64) {
					if buyAsk <= 0 || sellBid <= 0 { return }
					grossBps := (sellBid/buyAsk - 1.0) * 10000.0
					fees := e.cfg.Trading.FeesBps[buyEx] + e.cfg.Trading.FeesBps[sellEx]
					net := strategy.NetSpreadBps(grossBps, fees, e.cfg.Trading.SlippageBps, e.cfg.Trading.RiskReserveBps)
					if net >= e.cfg.Trading.MinNetBps {
						metrics.ArbOppsFound.Inc()
						// Paper mode: we only record and mark as executed
						metrics.ArbOppsExecuted.Inc()
					}
				}
				check("binance","kraken", bt.Ask, kt.Bid)
				check("kraken","binance", kt.Ask, bt.Bid)
			}
		}
	}
}
