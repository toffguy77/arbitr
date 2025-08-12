package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// context key for request id
var reqIDKey = struct{}{}

// RequestID injects a best-effort request id (from header X-Request-Id or generated)
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = generateID()
		}
		r = r.WithContext(context.WithValue(r.Context(), reqIDKey, rid))
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r)
	})
}

// Logger logs basic request info with latency
func Logger(l zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			lat := time.Since(start)
			l.Info().
				Str("rid", GetRequestID(r.Context())).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rw.status).
				Dur("latency", lat).
				Msg("http_request")
		})
	}
}

type responseWriter struct{ http.ResponseWriter; status int }

func (w *responseWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// GetRequestID returns the request id from context
func GetRequestID(ctx context.Context) string {
	if v := ctx.Value(reqIDKey); v != nil {
		if s, ok := v.(string); ok { return s }
	}
	return ""
}

// generateID is tiny, collision-safe enough for logging traces
func generateID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z07:00")
}
