package telemetry

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// LimitRequests bounds work admitted to the internal API, including requests
// waiting for a DB connection. It never queues on capacity; cancellation also
// bounds DB waits. Health and metrics remain available for recovery inspection.
func LimitRequests(next http.Handler, limit int, timeout time.Duration, registry *Registry) http.Handler {
	if limit < 1 || timeout <= 0 {
		panic("invalid admission limit")
	}
	slots := make(chan struct{}, limit)
	var active atomic.Int64
	rejected := registry.RegisterCounter(NewCounter("http_admission_rejected_total", "Requests rejected at capacity"))
	registry.RegisterGaugeFunc(NewGaugeFunc("http_admission_active", "Admitted requests", func() float64 { return float64(active.Load()) }))
	registry.RegisterGaugeFunc(NewGaugeFunc("http_admission_limit", "Maximum admitted requests", func() float64 { return float64(limit) }))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			active.Add(1)
			defer func() { active.Add(-1); <-slots }()
		default:
			rejected.Inc()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"service_unavailable","message":"capacidade temporariamente esgotada"}}`))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
