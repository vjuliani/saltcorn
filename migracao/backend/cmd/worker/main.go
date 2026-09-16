// Comando worker: processo de background do backend Go. A fundação (GO-005)
// trouxe o loop periódico e o encerramento gracioso. GO-007 acrescenta
// propagação de tenant por job (internal/platform/tenancy) e, quando
// SALTCORN_GO_DATABASE_URL está configurada, executa cada ciclo dentro de
// internal/platform/database.WithTenant, isolado por schema — a automação
// real (triggers/workflow/scheduler) entra em GO-024/GO-025, reutilizando o
// mesmo internal/platform/* usado aqui (ADR-0001: "CLI e worker reutilizam
// os mesmos serviços"). GO-009 acrescenta a mesma guarda de ownership de
// escrita usada por cmd/server (internal/platform/cutover): um job só roda
// para um tenant se este backend for o proprietário registrado dessa
// capacidade — "bloquear caminhos alternativos, inclusive jobs" não é uma
// checagem duplicada por processo, é a mesma checagem reutilizada. GO-010
// acrescenta log estruturado, métricas de job e um trace novo por execução
// (internal/platform/telemetry) — cada job é uma operação correlacionável
// como uma requisição HTTP seria, com seu próprio trace_id.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// jobInterval é fixo nesta fundação; vira configurável quando houver jobs
// reais com frequências próprias (GO-025).
const jobInterval = 5 * time.Second

// placeholderJobCapability identifica, para o registro de ownership
// (GO-009), a capacidade servida pelo job placeholder — um nome de
// exemplo, não um catálogo formal de capacidades (isso é trabalho futuro).
const placeholderJobCapability = "worker.placeholder_job"

func main() {
	cfg, err := config.Load()
	logger := slog.New(telemetry.NewHandler(os.Stdout, cfg.LogLevel))
	slog.SetDefault(logger)
	if err != nil {
		logger.Error("configuração inválida", "error", err.Error())
		os.Exit(1)
	}

	tracker := shutdown.NewTracker()
	registry := telemetry.NewRegistry()
	jobMetrics := telemetry.NewJobMetrics(registry)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	baseCtx := telemetry.WithLogger(ctx, logger)

	var db *database.DB
	guard := cutover.NewGuard()
	if cfg.DatabaseURL != "" {
		db, err = database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			logger.Error("conectar ao banco", "error", err.Error())
			os.Exit(1)
		}
		defer db.Close()
		db.SetMetrics(telemetry.NewSQLMetrics(registry))

		// Mesmo cuidado de cmd/server: sem registro de ownership
		// carregável ainda (tabela não existe até o schema ser aplicado,
		// GO-011), a Guard fica vazia e nenhum job roda até o registro
		// existir e ser recarregado — padrão seguro, não erro fatal.
		if err := cutover.LoadFromRegistry(ctx, db, guard); err != nil {
			logger.Warn("não foi possível carregar o registro de ownership de corte — nenhum job rodará até o registro existir e ser recarregado",
				"error", err.Error())
		}
	}

	ticker := time.NewTicker(jobInterval)
	defer ticker.Stop()

	logger.Info("saltcorn-go worker iniciado", "ambiente", cfg.Environment, "intervalo", jobInterval.String(), "tenants", cfg.WorkerTenants)

runLoop:
	for {
		select {
		case <-ctx.Done():
			break runLoop
		case <-ticker.C:
			end, err := tracker.Begin()
			if err != nil {
				// Shutdown já em andamento: não inicia mais um ciclo.
				continue
			}
			runCycle(baseCtx, cfg.WorkerTenants, db, guard, jobMetrics)
			end()
		}
	}

	stop()
	logger.Info("sinal de encerramento recebido, aguardando job em curso", "timeout", cfg.ShutdownTimeout.String())

	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := tracker.Drain(drainCtx); err != nil {
		logger.Warn("job em curso não terminou dentro do timeout de shutdown", "error", err.Error())
	}
	logger.Info("encerrado")
}

// runCycle processa um job placeholder por tenant configurado — a prova de
// que o tenant é propagado ao trabalho de background, não só a requisições
// HTTP. Sem SALTCORN_GO_WORKER_TENANTS configurada, roda um único ciclo sem
// tenant nem banco (comportamento idêntico ao da fundação GO-005).
func runCycle(ctx context.Context, tenants []string, db *database.DB, guard *cutover.Guard, metrics *telemetry.JobMetrics) {
	if len(tenants) == 0 {
		runPlaceholderJob(ctx, "", db, guard, metrics)
		return
	}
	for _, t := range tenants {
		runPlaceholderJob(ctx, t, db, guard, metrics)
	}
}

// runPlaceholderJob simula uma unidade de trabalho ("transação") com
// duração perceptível, isolada no schema do tenant quando um banco está
// configurado. Nenhuma automação de produto existe aqui ainda (GO-024/025).
// Antes de tocar o banco, confere com cutover.Acquire que este backend é o
// proprietário registrado de placeholderJobCapability para o tenant — sem
// isso, pula o job (log, não erro fatal): "bloquear caminhos alternativos,
// inclusive jobs" (critério de aceite de GO-009) significa que um job não
// deve rodar só porque o processo está de pé, sem essa confirmação.
//
// Cada execução gera seu próprio trace (GO-010), como uma requisição HTTP
// teria — "result" nos logs/métricas é sempre um rótulo classificado
// ("ok"/"error"/"skipped_not_owner"/...), nunca o tenant (ver
// telemetry.JobMetrics sobre controle de cardinalidade).
func runPlaceholderJob(ctx context.Context, tenant string, db *database.DB, guard *cutover.Guard, metrics *telemetry.JobMetrics) {
	if db == nil || tenant == "" {
		time.Sleep(50 * time.Millisecond)
		return
	}

	jobCtx := telemetry.WithTraceID(ctx, telemetry.NewTraceID())
	jobCtx = telemetry.WithSpanID(jobCtx, telemetry.NewSpanID())
	jobCtx = tenancy.WithTenant(jobCtx, tenancy.Tenant(tenant))
	logger := telemetry.LoggerFor(jobCtx)
	start := time.Now()

	end, err := cutover.Acquire(guard, tenancy.Tenant(tenant), placeholderJobCapability)
	if err != nil {
		result := "skipped_error"
		switch {
		case errors.Is(err, cutover.ErrNotOwner):
			result = "skipped_not_owner"
		case errors.Is(err, cutover.ErrRouteDraining):
			result = "skipped_draining"
		}
		logger.Info("job pulado", "result", result)
		metrics.Observe(time.Since(start), result)
		return
	}
	defer end()

	err = db.WithTenant(jobCtx, tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT pg_sleep(0.05)")
		return err
	})
	elapsed := time.Since(start)
	if err != nil {
		logger.Warn("job falhou", "result", "error")
		metrics.Observe(elapsed, "error")
		return
	}
	logger.Debug("job concluído", "result", "ok")
	metrics.Observe(elapsed, "ok")
}
