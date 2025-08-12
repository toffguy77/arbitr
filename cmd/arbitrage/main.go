package main

import (
	"context"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"arbitr/internal/config"
	"arbitr/internal/arbitrage"
	"arbitr/internal/exchange/binance"
	"arbitr/internal/exchange/common"
	"arbitr/internal/exchange/kraken"
	"arbitr/internal/infra/health"
	"arbitr/internal/infra/http/middleware"
	"arbitr/internal/infra/log"
	"arbitr/internal/infra/metrics"
	"arbitr/internal/infra/netutil"
	"arbitr/internal/infra/version"
	"arbitr/internal/infra/runner"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()
	logger := log.NewLogger(cfg)

	// Init metrics and start HTTP endpoint
	registry := metrics.Init(logger)
	mux := http.NewServeMux()
	// admin endpoints (metrics, pprof) behind IP allowlist gate
	adminCIDRs := netutil.MustParseCIDRs(cfg.Server.AdminAllowCIDRs)
	mux.Handle("/metrics", middleware.AdminGate(adminCIDRs, metrics.Handler(registry)))
	mux.HandleFunc("/healthz", health.Healthz)
	mux.HandleFunc("/readyz", health.Readyz)
	mux.HandleFunc("/version", version.Handler)
	if cfg.Server.Pprof {
		mux.Handle("/debug/pprof/", middleware.AdminGate(adminCIDRs, http.HandlerFunc(pprof.Index)))
		mux.Handle("/debug/pprof/cmdline", middleware.AdminGate(adminCIDRs, http.HandlerFunc(pprof.Cmdline)))
		mux.Handle("/debug/pprof/profile", middleware.AdminGate(adminCIDRs, http.HandlerFunc(pprof.Profile)))
		mux.Handle("/debug/pprof/symbol", middleware.AdminGate(adminCIDRs, http.HandlerFunc(pprof.Symbol)))
		mux.Handle("/debug/pprof/trace", middleware.AdminGate(adminCIDRs, http.HandlerFunc(pprof.Trace)))
	}

	// wrap mux with middlewares (request id and logging)
	handler := middleware.RequestID(middleware.Logger(logger)(mux))

	server := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeoutSeconds) * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("http server error")
		}
	}()

	logger.Info().Str("region", cfg.Network.Region).Str("addr", cfg.Server.Addr).Msg("Arbitrage system started")

	// start workers (placeholder) and monitor errors
	g := &runner.Group{}
	// arbitrage engine worker
	workerErrCh := g.Go(ctx, func(ctx context.Context) error {
		adapters := map[string]common.ExchangeAdapter{}
		adapters["binance"] = binance.New(cfg)
		adapters["kraken"] = kraken.New(cfg)
		eng := arbitrage.New(cfg, adapters)
		return eng.Run(ctx)
	})

	// mark ready after initialization completes
	health.SetReady(true)

	// Wait for termination signals or worker error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case s := <-sigCh:
		logger.Info().Str("signal", s.String()).Msg("shutdown signal received")
	case err := <-workerErrCh:
		if err != nil { logger.Error().Err(err).Msg("worker error"); health.SetReady(false) }
	}

	// mark not ready before shutdown
	health.SetReady(false)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("graceful shutdown failed")
	}
	logger.Info().Msg("shutdown complete")
}
