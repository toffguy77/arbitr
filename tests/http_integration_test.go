package tests

import (
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "arbitr/internal/config"
    ilog "arbitr/internal/infra/log"
    "arbitr/internal/infra/metrics"
    "arbitr/internal/infra/health"
    "arbitr/internal/infra/version"
)

// buildMux mirrors the HTTP setup in cmd/arbitrage/main.go
func buildMux(t *testing.T) http.Handler {
    t.Helper()
    cfg := config.Load()
    logger := ilog.NewLogger(cfg)
    reg := metrics.Init(logger)
    mux := http.NewServeMux()
    mux.Handle("/metrics", metrics.Handler(reg))
    mux.HandleFunc("/healthz", health.Healthz)
    // mark ready and add /readyz
    health.SetReady(true)
    mux.HandleFunc("/readyz", health.Readyz)
    mux.HandleFunc("/version", version.Handler)
    return mux
}

func TestReadyzAndVersion(t *testing.T) {
    srv := httptest.NewServer(buildMux(t))
    t.Cleanup(srv.Close)

    // readyz should return 200 once ready is set to true in buildMux
    resp, err := http.Get(srv.URL + "/readyz")
    if err != nil { t.Fatalf("GET /readyz error: %v", err) }
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("/readyz expected 200, got %d", resp.StatusCode)
    }
    _ = resp.Body.Close()

    // version should return json
    resp, err = http.Get(srv.URL + "/version")
    if err != nil { t.Fatalf("GET /version error: %v", err) }
    defer resp.Body.Close()
    if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
        t.Fatalf("/version expected application/json, got %s", ct)
    }
}

func TestHealthzEndpoint(t *testing.T) {
    srv := httptest.NewServer(buildMux(t))
    t.Cleanup(srv.Close)

    resp, err := http.Get(srv.URL + "/healthz")
    if err != nil {
        t.Fatalf("GET /healthz error: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("expected 200, got %d", resp.StatusCode)
    }
}

func TestMetricsEndpoint(t *testing.T) {
    srv := httptest.NewServer(buildMux(t))
    t.Cleanup(srv.Close)

    resp, err := http.Get(srv.URL + "/metrics")
    if err != nil {
        t.Fatalf("GET /metrics error: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("expected 200, got %d", resp.StatusCode)
    }
    // Basic smoke-check: the registry should expose at least one of our metrics
    b, _ := io.ReadAll(resp.Body)
    body := string(b)
    if body == "" || !(strings.Contains(body, "decision_latency_ms") || strings.Contains(body, "arbitrage_opportunities_found")) {
        t.Fatalf("metrics output did not contain expected metrics, got: %q", body)
    }
}
