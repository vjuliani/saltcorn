// Comando worker: processo de background do backend Go. A fundação (GO-005)
// trouxe o loop periódico e o encerramento gracioso. GO-007 acrescenta
// propagação de tenant por job (internal/platform/tenancy) e, quando
// SALTCORN_GO_DATABASE_URL está configurada, executa cada ciclo dentro de
// internal/platform/database.WithTenant, isolado por schema. GO-009
// acrescenta a mesma guarda de ownership de escrita usada por cmd/server
// (internal/platform/cutover): um job só roda para um tenant se este
// backend for o proprietário registrado dessa capacidade — "bloquear
// caminhos alternativos, inclusive jobs" não é uma checagem duplicada por
// processo, é a mesma checagem reutilizada. GO-010 acrescenta log
// estruturado, métricas de job e um trace novo por execução
// (internal/platform/telemetry) — cada job é uma operação correlacionável
// como uma requisição HTTP seria, com seu próprio trace_id. GO-014
// acrescenta um segundo job por tenant, de processamento de outbox
// (internal/platform/outbox.ProcessPending) — o worker real que drena os
// eventos gravados por escritas idempotentes (internal/records + GO-013),
// com retries e falhas inspecionáveis via _sc_outbox. GO-025 acrescenta um
// terceiro job, de disparo de triggers agendados (internal/scheduler) —
// serializado entre MÚLTIPLAS instâncias deste processo por
// internal/platform/lease (arrendamento com expiração real em banco,
// distinto de cutover.Guard, que só coordena legado↔Go dentro de UM
// processo, nunca entre processos concorrentes).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/lease"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
)

// jobInterval é fixo nesta fundação — permanece assim mesmo após GO-025:
// a granularidade de "está na hora?" dos triggers agendados vem de
// internal/scheduler (cron por minuto, com next_run_at próprio por
// trigger, ver internal/scheduler/cron.go), não da frequência do tick do
// worker. Configurável por frequência de PROCESSO (não por job) fica como
// extensão futura, não fabricada aqui.
const jobInterval = 5 * time.Second

// placeholderJobCapability identifica, para o registro de ownership
// (GO-009), a capacidade servida pelo job placeholder — um nome de
// exemplo, não um catálogo formal de capacidades (isso é trabalho futuro).
const placeholderJobCapability = "worker.placeholder_job"

// outboxJobCapability identifica, para o registro de ownership (GO-009), a
// capacidade servida pelo job de processamento de outbox (GO-014) — mesma
// convenção do placeholder acima.
const outboxJobCapability = "worker.outbox_processor"

// outboxBatchLimit e outboxMaxAttempts são fixos nesta fundação, como
// jobInterval — configuráveis quando houver operação real.
const (
	outboxBatchLimit  = 20
	outboxMaxAttempts = 5
)

// schedulerLeaseTTL é o prazo do arrendamento (internal/platform/lease,
// GO-025) que serializa o job de scheduler entre múltiplas instâncias do
// processo worker — maior que jobInterval para que o MESMO worker sempre
// consiga renovar antes de expirar em operação normal (o lease só deveria
// expirar de verdade se o worker que o detém morrer ou travar), mas curto
// o bastante para que um failover não demore muito: se o dono do lease
// cair, outro worker assume no próximo tick após a expiração, nunca
// esperando o encerramento gracioso de uma conexão morta (ao contrário de
// um pg_advisory_lock, que soltaria só quando a conexão TCP cair — sem
// TTL, sem prazo determinístico).
const schedulerLeaseTTL = 3 * jobInterval

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

	// workerInstanceID identifica ESTE processo de forma única entre
	// reinícios e entre instâncias concorrentes — o "owner" que
	// internal/platform/lease usa para decidir quem detém o arrendamento
	// do job de scheduler (GO-025). Não precisa de aleatoriedade
	// criptográfica: só precisa ser, na prática, distinto de qualquer
	// outra instância viva ao mesmo tempo.
	workerInstanceID := fmt.Sprintf("%s-%d-%d", hostnameOrUnknown(), os.Getpid(), time.Now().UnixNano())

	// scheduler é o mecanismo de disparo de triggers agendados (GO-025) —
	// registro de ações nativas, mesmo espírito de registeredFunctions em
	// migracao/packages/pluginhost (demonstração do MECANISMO, não um
	// catálogo de produção: nenhum trigger agendado real existe neste
	// checkout ainda).
	schedulerDispatcher := &scheduler.Dispatcher{Actions: map[string]scheduler.ActionFunc{
		"log": func(ctx context.Context, tx pgx.Tx) error {
			telemetry.LoggerFor(ctx).Info("trigger agendado executado (demonstração, sem catálogo real ainda)")
			return nil
		},
	}}

	logger.Info("saltcorn-go worker iniciado", "ambiente", cfg.Environment, "intervalo", jobInterval.String(), "tenants", cfg.WorkerTenants, "worker_id", workerInstanceID)

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
			runCycle(baseCtx, cfg.WorkerTenants, db, guard, jobMetrics, workerInstanceID, schedulerDispatcher)
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
func runCycle(ctx context.Context, tenants []string, db *database.DB, guard *cutover.Guard, metrics *telemetry.JobMetrics, workerID string, dispatcher *scheduler.Dispatcher) {
	if len(tenants) == 0 {
		runPlaceholderJob(ctx, "", db, guard, metrics)
		return
	}
	for _, t := range tenants {
		runPlaceholderJob(ctx, t, db, guard, metrics)
		runOutboxJob(ctx, t, db, guard, metrics)
		runScheduledTriggersJob(ctx, t, db, guard, metrics, workerID, dispatcher)
	}
}

// hostnameOrUnknown devolve os.Hostname(), ou "unknown" se indisponível —
// só compõe workerInstanceID (um identificador de diagnóstico, nunca uma
// chave de segurança), então uma falha aqui não deveria impedir o worker
// de subir.
func hostnameOrUnknown() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
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

// runOutboxJob drena até outboxBatchLimit eventos pendentes de
// _sc_outbox por ciclo (GO-014), reaproveitando a mesma guarda de
// ownership (GO-009) e telemetria (GO-010) do job placeholder — o handler
// aqui só loga o evento (nenhum consumidor real de eventos de trigger
// AfterCommit de GO-024 foi conectado ainda; runScheduledTriggersJob, mais
// abaixo, segue o MESMO padrão para os triggers agendados de GO-025).
func runOutboxJob(ctx context.Context, tenant string, db *database.DB, guard *cutover.Guard, metrics *telemetry.JobMetrics) {
	if db == nil || tenant == "" {
		return
	}

	jobCtx := telemetry.WithTraceID(ctx, telemetry.NewTraceID())
	jobCtx = telemetry.WithSpanID(jobCtx, telemetry.NewSpanID())
	jobCtx = tenancy.WithTenant(jobCtx, tenancy.Tenant(tenant))
	logger := telemetry.LoggerFor(jobCtx)
	start := time.Now()

	end, err := cutover.Acquire(guard, tenancy.Tenant(tenant), outboxJobCapability)
	if err != nil {
		result := "skipped_error"
		switch {
		case errors.Is(err, cutover.ErrNotOwner):
			result = "skipped_not_owner"
		case errors.Is(err, cutover.ErrRouteDraining):
			result = "skipped_draining"
		}
		logger.Info("job de outbox pulado", "result", result)
		metrics.Observe(time.Since(start), result)
		return
	}
	defer end()

	var processed, failed int
	err = db.WithTenant(jobCtx, tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = outbox.ProcessPending(ctx, tx, outboxBatchLimit, outboxMaxAttempts,
			func(ctx context.Context, tx pgx.Tx, ev outbox.OutboxEvent) error {
				telemetry.LoggerFor(ctx).Info("evento de outbox processado (demonstração, sem consumidor real)",
					"event_type", ev.Type, "attempts", ev.Attempts)
				return nil
			})
		return err
	})
	elapsed := time.Since(start)
	if err != nil {
		logger.Warn("job de outbox falhou", "result", "error")
		metrics.Observe(elapsed, "error")
		return
	}
	logger.Debug("job de outbox concluído", "result", "ok", "processed", processed, "failed", failed)
	metrics.Observe(elapsed, "ok")
}

// runScheduledTriggersJob dispara os triggers agendados (GO-025) cujo
// horário já passou, para tenant. Dois portões, nesta ordem:
//
//  1. cutover.Acquire(scheduler.Capability) — o mesmo padrão de
//     ownership de GO-009 usado pelos outros jobs: "scheduler antigo é
//     desativado por escopo" (critério de aceite) vale por construção —
//     enquanto ninguém chamar cutover.SwitchOwner para esta capacidade e
//     este tenant, o owner em cache nunca é OwnerGo, e este job NUNCA
//     chega a tocar _sc_scheduled_triggers.
//  2. lease.Acquire — cutover.Guard é uma guarda EM MEMÓRIA de UM
//     processo (não impede duas instâncias diferentes do worker, ambas
//     "Go owner", de rodarem o MESMO job ao mesmo tempo); o lease
//     (internal/platform/lease, com expiração real em banco) é o que de
//     fato serializa entre PROCESSOS — "dois workers não executam
//     simultaneamente job exclusivo" (critério de aceite) depende deste
//     segundo portão, não do primeiro.
//
// Perder a disputa pelo lease (lease.ErrLeaseHeld) é um resultado normal
// em operação com mais de um worker, não um erro — outro processo já
// está cuidando deste tenant neste instante.
func runScheduledTriggersJob(ctx context.Context, tenant string, db *database.DB, guard *cutover.Guard, metrics *telemetry.JobMetrics, workerID string, dispatcher *scheduler.Dispatcher) {
	if db == nil || tenant == "" {
		return
	}

	jobCtx := telemetry.WithTraceID(ctx, telemetry.NewTraceID())
	jobCtx = telemetry.WithSpanID(jobCtx, telemetry.NewSpanID())
	jobCtx = tenancy.WithTenant(jobCtx, tenancy.Tenant(tenant))
	logger := telemetry.LoggerFor(jobCtx)
	start := time.Now()

	end, err := cutover.Acquire(guard, tenancy.Tenant(tenant), scheduler.Capability)
	if err != nil {
		result := "skipped_error"
		switch {
		case errors.Is(err, cutover.ErrNotOwner):
			result = "skipped_not_owner"
		case errors.Is(err, cutover.ErrRouteDraining):
			result = "skipped_draining"
		}
		logger.Info("job de scheduler pulado", "result", result)
		metrics.Observe(time.Since(start), result)
		return
	}
	defer end()

	leaseErr := db.WithTenant(jobCtx, tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
		return lease.Acquire(ctx, tx, schedulerLeaseName(tenant), workerID, schedulerLeaseTTL)
	})
	if leaseErr != nil {
		result := "skipped_error"
		if errors.Is(leaseErr, lease.ErrLeaseHeld) {
			result = "skipped_lease_held"
		}
		logger.Debug("job de scheduler pulado", "result", result)
		metrics.Observe(time.Since(start), result)
		return
	}

	var ran, failed int
	err = db.WithTenant(jobCtx, tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ran, failed, err = dispatcher.RunDue(ctx, tx, time.Now())
		return err
	})
	elapsed := time.Since(start)
	if err != nil {
		logger.Warn("job de scheduler falhou", "result", "error")
		metrics.Observe(elapsed, "error")
		return
	}
	logger.Debug("job de scheduler concluído", "result", "ok", "ran", ran, "failed", failed)
	metrics.Observe(elapsed, "ok")
}

// schedulerLeaseName isola o lease por tenant — dois tenants diferentes
// nunca disputam o mesmo lease (o scheduler de um não deveria esperar o
// do outro), só duas instâncias de worker cuidando do MESMO tenant.
func schedulerLeaseName(tenant string) string {
	return "scheduler:" + tenant
}
