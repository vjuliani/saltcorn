// Comando server: processo HTTP do backend Go. A fundação (GO-005) trouxe
// configuração, health/readiness e encerramento gracioso. GO-007 acrescenta
// resolução/propagação de tenant+ator (internal/platform/tenancy) e, quando
// SALTCORN_GO_DATABASE_URL está configurada, uma conexão a Postgres com
// isolamento de schema por tenant (internal/platform/database) — ainda sem
// nenhuma tabela de domínio real (isso é GO-011). GO-009 acrescenta a
// guarda de ownership de escrita (internal/platform/cutover): a rota de
// exemplo só responde se este backend for o proprietário registrado para
// a capacidade, nunca por presunção. GO-010 acrescenta log estruturado
// (com redação automática de dados sensíveis), métricas em GET /metrics e
// correlação de trace (internal/platform/telemetry) — /healthz e /readyz
// não são instrumentadas de propósito (probes de alta frequência, baixo
// valor de log/métrica, ruído que atrapalha mais do que ajuda).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/health"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// exampleCapability identifica, para o registro de ownership (GO-009), a
// capacidade servida pela rota de exemplo — um nome de exemplo, não um
// catálogo formal de capacidades (isso é trabalho futuro de GO-011+).
const exampleCapability = "tables.records"

// exampleRoute é o nome lógico e de baixa cardinalidade da rota de exemplo
// para fins de métrica (GO-010) — nunca o path bruto, que contém o tenant.
const exampleRoute = "tenant_records"

func main() {
	cfg, err := config.Load()
	logger := slog.New(telemetry.NewHandler(os.Stdout, cfg.LogLevel))
	slog.SetDefault(logger)
	if err != nil {
		logger.Error("configuração inválida", "error", err.Error())
		os.Exit(1)
	}

	checker := &health.Checker{}
	tracker := shutdown.NewTracker()
	registry := telemetry.NewRegistry()
	httpMetrics := telemetry.NewHTTPMetrics(registry)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var db *database.DB
	if cfg.DatabaseURL != "" {
		db, err = database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			logger.Error("conectar ao banco", "error", err.Error())
			os.Exit(1)
		}
		defer db.Close()
		db.SetMetrics(telemetry.NewSQLMetrics(registry))
		registerPoolGauges(registry, db)
		logger.Info("conectado ao banco (isolamento de tenant via internal/platform/database)")
	} else {
		logger.Warn("SALTCORN_GO_DATABASE_URL não configurada — rotas que dependem de banco responderão 503")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", checker.LivenessHandler())
	mux.HandleFunc("/readyz", checker.ReadinessHandler())
	mux.Handle("/metrics", registry.Handler())
	mux.Handle("/", telemetry.Middleware("root", httpMetrics, placeholderHandler(tracker)))

	// A rota de exemplo protegida por identidade delegada só é registrada
	// se houver um segredo válido para verificar assinatura (GO-008) — sem
	// isso, tenancy.Middleware não tem como funcionar com segurança, então
	// preferimos 404 (rota não existe) a registrar algo que aceitaria
	// qualquer token ou que entraria em pânico com um Verifier nulo.
	if cfg.ServiceIdentitySecret == "" {
		logger.Warn("SALTCORN_GO_SERVICE_IDENTITY_SECRET não configurada — rota /v1/tenants/{tenant}/... não registrada")
	} else {
		verifier, err := tenancy.NewVerifier([]byte(cfg.ServiceIdentitySecret))
		if err != nil {
			logger.Error("segredo de identidade delegada inválido", "error", err.Error())
			os.Exit(1)
		}

		// Guard/registro de ownership de escrita (GO-009): carrega o estado
		// persistido antes de aceitar qualquer requisição — sem banco
		// configurado, ou se a tabela ainda não existir (schema ainda não
		// aplicado, GO-011), a Guard fica vazia e trata toda
		// tenant/capacidade como "não é Go" (padrão seguro, nunca aceita
		// escrita por engano); registrar isso como aviso, não erro fatal.
		guard := cutover.NewGuard()
		if db != nil {
			if err := cutover.LoadFromRegistry(ctx, db, guard); err != nil {
				logger.Warn("não foi possível carregar o registro de ownership de corte — nenhuma tenant/capacidade será tratada como proprietária de Go até o registro existir e ser recarregado",
					"error", err.Error())
			}
		}

		// telemetry.Middleware envolve tenancy.Middleware e
		// cutover.RequireOwnership (não o contrário) para que o tenant/ator
		// já verificado esteja disponível ao logar a conclusão da
		// requisição, e para que rejeições de identidade/ownership também
		// entrem nas métricas — não só o caminho de sucesso.
		mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records",
			tenancy.Middleware(verifier, telemetry.Middleware(exampleRoute, httpMetrics,
				cutover.RequireOwnership(guard, exampleCapability, tenantProbeHandler(tracker, db)))))
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, BaseContext: func(net.Listener) context.Context {
		return telemetry.WithLogger(context.Background(), logger)
	}}

	serveErr := make(chan error, 1)
	go func() {
		checker.SetReady(true)
		logger.Info("saltcorn-go server iniciado", "ambiente", cfg.Environment, "addr", cfg.HTTPAddr)
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
			logger.Error("erro no servidor HTTP", "error", err.Error())
			os.Exit(1)
		}
		return
	}

	stop() // para de reagir a um segundo sinal enquanto já estamos encerrando
	checker.SetReady(false)
	logger.Info("sinal de encerramento recebido, drenando", "timeout", cfg.ShutdownTimeout.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// srv.Shutdown para de aceitar novas conexões e espera as respostas HTTP
	// em curso terminarem. tracker.Drain espera qualquer trabalho registrado
	// explicitamente que sobreviva além da resposta HTTP.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("srv.Shutdown não concluiu a tempo", "error", err.Error())
	}
	if err := tracker.Drain(shutdownCtx); err != nil {
		logger.Warn("trabalho em curso não terminou dentro do timeout de shutdown", "error", err.Error())
	}
	logger.Info("encerrado")
}

// registerPoolGauges publica o estado do pool de conexões como gauges
// (GO-010) — o sinal mais direto de saturação de banco que
// internal/platform/database pode oferecer sem instrumentar cada query
// individualmente: quantas conexões estão em uso vs. disponíveis no limite
// configurado.
func registerPoolGauges(registry *telemetry.Registry, db *database.DB) {
	registry.RegisterGaugeFunc(telemetry.NewGaugeFunc("sql_pool_acquired_connections",
		"Conexões do pool atualmente em uso.", func() float64 { return float64(db.Stat().AcquiredConns()) }))
	registry.RegisterGaugeFunc(telemetry.NewGaugeFunc("sql_pool_idle_connections",
		"Conexões do pool atualmente ociosas.", func() float64 { return float64(db.Stat().IdleConns()) }))
	registry.RegisterGaugeFunc(telemetry.NewGaugeFunc("sql_pool_total_connections",
		"Total de conexões do pool (adquiridas + ociosas + em construção).", func() float64 { return float64(db.Stat().TotalConns()) }))
	registry.RegisterGaugeFunc(telemetry.NewGaugeFunc("sql_pool_max_connections",
		"Tamanho máximo configurado do pool.", func() float64 { return float64(db.Stat().MaxConns()) }))
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
