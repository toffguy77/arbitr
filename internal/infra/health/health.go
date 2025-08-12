package health

import (
    "net/http"
    "sync/atomic"
)

var ready atomic.Bool

// SetReady marks readiness state
func SetReady(v bool) { ready.Store(v) }

// Ready returns current readiness
func Ready() bool { return ready.Load() }

// Healthz is a simple liveness probe
func Healthz(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("ok"))
}

// Readyz reflects application readiness state
func Readyz(w http.ResponseWriter, r *http.Request) {
    if Ready() {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ready"))
        return
    }
    http.Error(w, "not ready", http.StatusServiceUnavailable)
}
