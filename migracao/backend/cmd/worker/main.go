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
// checagem duplicada por processo, é a mesma checagem reutilizada.
package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
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
	if err != nil {
		log.Fatalf("configuração inválida: %v", err)
	}

	tracker := shutdown.NewTracker()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var db *database.DB
	guard := cutover.NewGuard()
	if cfg.DatabaseURL != "" {
		db, err = database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("conectar ao banco: %v", err)
		}
		defer db.Close()

		// Mesmo cuidado de cmd/server: sem registro de ownership
		// carregável ainda (tabela não existe até o schema ser aplicado,
		// GO-011), a Guard fica vazia e nenhum job roda até o registro
		// existir e ser recarregado — padrão seguro, não erro fatal.
		if err := cutover.LoadFromRegistry(ctx, db, guard); err != nil {
			log.Printf("aviso: não foi possível carregar o registro de ownership de corte (%v) — nenhum job rodará até o registro existir e ser recarregado", err)
		}
	}

	ticker := time.NewTicker(jobInterval)
	defer ticker.Stop()

	log.Printf("saltcorn-go worker (ambiente=%s) iniciado, intervalo=%s, tenants=%v",
		cfg.Environment, jobInterval, cfg.WorkerTenants)

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
			runCycle(ctx, cfg.WorkerTenants, db, guard)
			end()
		}
	}

	stop()
	log.Printf("sinal de encerramento recebido, aguardando job em curso (timeout %s)...", cfg.ShutdownTimeout)

	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := tracker.Drain(drainCtx); err != nil {
		log.Printf("aviso: job em curso não terminou dentro do timeout de shutdown: %v", err)
	}
	log.Printf("encerrado")
}

// runCycle processa um job placeholder por tenant configurado — a prova de
// que o tenant é propagado ao trabalho de background, não só a requisições
// HTTP. Sem SALTCORN_GO_WORKER_TENANTS configurada, roda um único ciclo sem
// tenant nem banco (comportamento idêntico ao da fundação GO-005).
func runCycle(ctx context.Context, tenants []string, db *database.DB, guard *cutover.Guard) {
	if len(tenants) == 0 {
		runPlaceholderJob(ctx, "", db, guard)
		return
	}
	for _, t := range tenants {
		runPlaceholderJob(ctx, t, db, guard)
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
func runPlaceholderJob(ctx context.Context, tenant string, db *database.DB, guard *cutover.Guard) {
	if db == nil || tenant == "" {
		time.Sleep(50 * time.Millisecond)
		return
	}

	end, err := cutover.Acquire(guard, tenancy.Tenant(tenant), placeholderJobCapability)
	if err != nil {
		if errors.Is(err, cutover.ErrNotOwner) || errors.Is(err, cutover.ErrRouteDraining) {
			log.Printf("job do tenant %q pulado: %v", tenant, err)
			return
		}
		log.Printf("job do tenant %q: erro inesperado ao verificar ownership: %v", tenant, err)
		return
	}
	defer end()

	jobCtx := tenancy.WithTenant(ctx, tenancy.Tenant(tenant))
	err = db.WithTenant(jobCtx, tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT pg_sleep(0.05)")
		return err
	})
	if err != nil {
		log.Printf("job do tenant %q falhou: %v", tenant, err)
	}
}
