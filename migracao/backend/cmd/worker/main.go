// Comando worker: processo de background do backend Go. A fundação (GO-005)
// trouxe o loop periódico e o encerramento gracioso. GO-007 acrescenta
// propagação de tenant por job (internal/platform/tenancy) e, quando
// SALTCORN_GO_DATABASE_URL está configurada, executa cada ciclo dentro de
// internal/platform/database.WithTenant, isolado por schema — a automação
// real (triggers/workflow/scheduler) entra em GO-024/GO-025, reutilizando o
// mesmo internal/platform/* usado aqui (ADR-0001: "CLI e worker reutilizam
// os mesmos serviços").
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// jobInterval é fixo nesta fundação; vira configurável quando houver jobs
// reais com frequências próprias (GO-025).
const jobInterval = 5 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuração inválida: %v", err)
	}

	tracker := shutdown.NewTracker()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var db *database.DB
	if cfg.DatabaseURL != "" {
		db, err = database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("conectar ao banco: %v", err)
		}
		defer db.Close()
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
			runCycle(ctx, cfg.WorkerTenants, db)
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
func runCycle(ctx context.Context, tenants []string, db *database.DB) {
	if len(tenants) == 0 {
		runPlaceholderJob(ctx, "", db)
		return
	}
	for _, t := range tenants {
		runPlaceholderJob(ctx, t, db)
	}
}

// runPlaceholderJob simula uma unidade de trabalho ("transação") com
// duração perceptível, isolada no schema do tenant quando um banco está
// configurado. Nenhuma automação de produto existe aqui ainda (GO-024/025).
func runPlaceholderJob(ctx context.Context, tenant string, db *database.DB) {
	if db == nil || tenant == "" {
		time.Sleep(50 * time.Millisecond)
		return
	}
	jobCtx := tenancy.WithTenant(ctx, tenancy.Tenant(tenant))
	err := db.WithTenant(jobCtx, tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT pg_sleep(0.05)")
		return err
	})
	if err != nil {
		log.Printf("job do tenant %q falhou: %v", tenant, err)
	}
}
