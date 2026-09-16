package telemetry

import (
	"net/http"
	"time"
)

// HTTPMetrics agrupa as métricas HTTP compartilhadas entre todas as rotas
// instrumentadas por Middleware — criado uma vez por processo (as labels
// "method"/"route"/"status_class" são um conjunto fixo e pequeno; "route"
// é o nome lógico da rota, nunca o path bruto com segmentos dinâmicos como
// tenant/id, que teria cardinalidade ilimitada).
type HTTPMetrics struct {
	requests *Counter
	duration *Histogram
}

// NewHTTPMetrics cria e registra as métricas HTTP em registry.
func NewHTTPMetrics(registry *Registry) *HTTPMetrics {
	return &HTTPMetrics{
		requests: registry.RegisterCounter(NewCounter(
			"http_requests_total",
			"Total de requisições HTTP concluídas, por método, rota e classe de status.",
			"method", "route", "status_class",
		)),
		duration: registry.RegisterHistogram(NewHistogram(
			"http_request_duration_seconds",
			"Duração das requisições HTTP, em segundos, por método e rota.",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
			"method", "route",
		)),
	}
}

// Middleware gera (ou reaproveita, via header `traceparent` de entrada) o
// trace da requisição, registra métricas e loga uma linha de conclusão —
// route é o nome lógico e de baixa cardinalidade da rota (ex.:
// "tenant_records"), nunca r.URL.Path bruto (ver comentário de HTTPMetrics).
// Deve envolver a rota inteira, incluindo tenancy.Middleware e
// cutover.RequireOwnership quando presentes, para capturar também
// rejeições de identidade/ownership nas métricas e no log — não só o
// caminho de sucesso.
func Middleware(route string, metrics *HTTPMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := traceForIncoming(r)
		ctx := WithTraceID(r.Context(), traceID)
		ctx = WithSpanID(ctx, spanID)
		r = r.WithContext(ctx)

		w.Header().Set("traceparent", FormatTraceparent(traceID, spanID))

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		statusClass := classifyStatus(rec.status)
		metrics.requests.Inc(r.Method, route, statusClass)
		metrics.duration.Observe(elapsed.Seconds(), r.Method, route)

		LoggerFor(ctx).Info("requisição HTTP concluída",
			"method", r.Method,
			"route", route,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", elapsed.Milliseconds(),
		)
	})
}

// traceForIncoming reaproveita o trace do header `traceparent` de entrada,
// se válido — gerando um novo SpanID para esta operação, mas mantendo o
// mesmo TraceID do chamador (a correlação "entre runtimes" do critério de
// aceite). Um header ausente ou inválido inicia um trace novo, nunca falha
// a requisição.
func traceForIncoming(r *http.Request) (TraceID, SpanID) {
	if header := r.Header.Get("traceparent"); header != "" {
		if traceID, _, err := ParseTraceparent(header); err == nil {
			return traceID, NewSpanID()
		}
	}
	return NewTraceID(), NewSpanID()
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func classifyStatus(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "other"
	}
}
