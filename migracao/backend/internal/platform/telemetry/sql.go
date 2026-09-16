package telemetry

import "time"

// SQLMetrics agrupa as métricas de transação SQL — "result" é um conjunto
// fixo e pequeno de rótulos (ex.: "ok"/"error"/"canceled"), nunca a
// consulta em si nem valores de parâmetro: internal/platform/database
// nunca loga texto de SQL ou parâmetros (que podem carregar dados de
// domínio sensíveis), só o resultado classificado e a duração.
type SQLMetrics struct {
	transactions *Counter
	duration     *Histogram
}

// NewSQLMetrics cria e registra as métricas SQL em registry.
func NewSQLMetrics(registry *Registry) *SQLMetrics {
	return &SQLMetrics{
		transactions: registry.RegisterCounter(NewCounter(
			"sql_transactions_total",
			"Total de transações SQL concluídas, por resultado.",
			"result",
		)),
		duration: registry.RegisterHistogram(NewHistogram(
			"sql_transaction_duration_seconds",
			"Duração das transações SQL, em segundos.",
			[]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		)),
	}
}

// Observe registra a duração e o resultado (classificado, nunca a
// mensagem de erro bruta — ver comentário do tipo) de uma transação SQL.
func (m *SQLMetrics) Observe(elapsed time.Duration, result string) {
	if m == nil {
		return
	}
	m.transactions.Inc(result)
	m.duration.Observe(elapsed.Seconds())
}
