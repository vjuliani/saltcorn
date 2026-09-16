package telemetry

import "time"

// JobMetrics agrupa as métricas de execução de job de background —
// "result" é um conjunto fixo (ex.: "ok"/"error"/"skipped_not_owner"),
// nunca o tenant: um worker com milhares de tenants não pode virar
// milhares de séries temporais só porque cada job "pertence" a um tenant
// diferente — essa granularidade fica nos logs estruturados (via
// LoggerFor, que inclui tenant quando presente no contexto), não nas
// métricas.
type JobMetrics struct {
	runs     *Counter
	duration *Histogram
}

// NewJobMetrics cria e registra as métricas de job em registry.
func NewJobMetrics(registry *Registry) *JobMetrics {
	return &JobMetrics{
		runs: registry.RegisterCounter(NewCounter(
			"job_runs_total",
			"Total de execuções de job, por resultado.",
			"result",
		)),
		duration: registry.RegisterHistogram(NewHistogram(
			"job_duration_seconds",
			"Duração das execuções de job, em segundos.",
			[]float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10},
		)),
	}
}

// Observe registra a duração e o resultado de uma execução de job.
func (m *JobMetrics) Observe(elapsed time.Duration, result string) {
	if m == nil {
		return
	}
	m.runs.Inc(result)
	m.duration.Observe(elapsed.Seconds())
}
