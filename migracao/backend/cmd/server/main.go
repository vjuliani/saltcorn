// Comando server: processo HTTP do backend Go. A fundação (GO-005) trouxe
// configuração, health/readiness e encerramento gracioso. GO-007 acrescenta
// resolução/propagação de tenant+ator (internal/platform/tenancy) e, quando
// SALTCORN_GO_DATABASE_URL está configurada, uma conexão a Postgres com
// isolamento de schema por tenant (internal/platform/database) — ainda sem
// nenhuma tabela de domínio real (isso é GO-011).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/health"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuração inválida: %v", err)
	}

	checker := &health.Checker{}
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
		log.Printf("conectado ao banco (isolamento de tenant via internal/platform/database)")
	} else {
		log.Printf("SALTCORN_GO_DATABASE_URL não configurada — rotas que dependem de banco responderão 503")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", checker.LivenessHandler())
	mux.HandleFunc("/readyz", checker.ReadinessHandler())
	mux.HandleFunc("/", placeholderHandler(tracker))

	// A rota de exemplo protegida por identidade delegada só é registrada
	// se houver um segredo válido para verificar assinatura (GO-008) — sem
	// isso, tenancy.Middleware não tem como funcionar com segurança, então
	// preferimos 404 (rota não existe) a registrar algo que aceitaria
	// qualquer token ou que entraria em pânico com um Verifier nulo.
	if cfg.ServiceIdentitySecret == "" {
		log.Printf("SALTCORN_GO_SERVICE_IDENTITY_SECRET não configurada — rota /v1/tenants/{tenant}/... não registrada")
	} else {
		verifier, err := tenancy.NewVerifier([]byte(cfg.ServiceIdentitySecret))
		if err != nil {
			log.Fatalf("segredo de identidade delegada inválido: %v", err)
		}
		mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records",
			tenancy.Middleware(verifier, tenantProbeHandler(tracker, db)))
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}

	serveErr := make(chan error, 1)
	go func() {
		checker.SetReady(true)
		log.Printf("saltcorn-go server (ambiente=%s) ouvindo em %s", cfg.Environment, cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			log.Fatalf("erro no servidor HTTP: %v", err)
		}
		return
	}

	stop() // para de reagir a um segundo sinal enquanto já estamos encerrando
	checker.SetReady(false)
	log.Printf("sinal de encerramento recebido, drenando (timeout %s)...", cfg.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// srv.Shutdown para de aceitar novas conexões e espera as respostas HTTP
	// em curso terminarem. tracker.Drain espera qualquer trabalho registrado
	// explicitamente que sobreviva além da resposta HTTP.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("aviso: srv.Shutdown não concluiu a tempo: %v", err)
	}
	if err := tracker.Drain(shutdownCtx); err != nil {
		log.Printf("aviso: trabalho em curso não terminou dentro do timeout de shutdown: %v", err)
	}
	log.Printf("encerrado")
}

// placeholderHandler demonstra o padrão que toda rota segue: registrar a
// unidade de trabalho no tracker antes de processar, para que um shutdown
// gracioso saiba esperar por ela. Não há regra de negócio real aqui ainda.
func placeholderHandler(tracker *shutdown.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("saltcorn-go: fundação (GO-005) — sem regras de domínio ainda\n"))
	}
}

// tenantProbeHandler prova, de ponta a ponta, que uma requisição HTTP chega
// com tenant+ator resolvidos (tenancy.Middleware) e que uma operação de
// banco roda isolada no schema correto (database.WithTenant) — sem nenhuma
// tabela de domínio real, que é escopo de GO-011. Sem banco configurado,
// responde 503 em vez de fingir sucesso.
func tenantProbeHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		actor, _ := tenancy.ActorFromContext(r.Context())

		if db == nil {
			http.Error(w, "banco não configurado nesta instância", http.StatusServiceUnavailable)
			return
		}

		var now string
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT now()::text").Scan(&now)
		})
		if err != nil {
			http.Error(w, "erro ao consultar o banco: "+err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tenant":"` + string(tenant) + `","actor":"` + actor + `","db_time":"` + now + `"}`))
	}
}
