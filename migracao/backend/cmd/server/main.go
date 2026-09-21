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
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/health"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// recordsCapability identifica, para o registro de ownership (GO-009), a
// capacidade servida pelas rotas de registros — não um catálogo formal de
// capacidades (isso é trabalho futuro de GO-011+), mas o nome já usado em
// produção desde a rota de exemplo original (GO-009), agora servindo
// dados reais (GO-017).
const recordsCapability = "tables.records"

// recordsRoute é o nome lógico e de baixa cardinalidade das rotas de
// registros para fins de métrica (GO-010) — nunca o path bruto, que
// contém o tenant.
const recordsRoute = "tenant_records"

// actorRoute é o nome lógico da rota GET .../actor (GO-017) para métrica.
const actorRoute = "tenant_actor"

// tablesSchemaCapability (GO-019) identifica, para o registro de
// ownership, a capacidade de mutar o catálogo (criar tabela/campo) — uma
// capacidade distinta de recordsCapability: o corte gradual Node/Go pode
// liberar leitura/escrita de registros para Go antes (ou depois) de
// liberar mutação de schema, mesmo espírito granular de GO-009.
const tablesSchemaCapability = "tables.schema"
const tablesSchemaRoute = "tenant_tables_schema"

// viewsCapability (GO-019) identifica a capacidade de criar/ler/editar
// views — também distinta de recordsCapability, pelo mesmo motivo.
const viewsCapability = "tables.views"
const viewsRoute = "tenant_views"

// usersAdminCapability (GO-044) identifica a capacidade de administrar
// usuários (listar/mudar papel/redefinir senha/deletar/listar tokens/
// impersonar) — distinta de recordsCapability/tablesSchemaCapability: é
// administração de identidade, não de dados de domínio, e pode ser cortada
// para Go independentemente dos dois.
const usersAdminCapability = "identity.admin"
const usersAdminRoute = "tenant_users_admin"

// realtimeCapability (GO-028) identifica a capacidade de servir o poll de
// eventos em tempo real ao BFF — sujeita ao mesmo corte gradual Node/Go
// que qualquer outra capacidade de domínio: enquanto não for OwnerGo para
// um tenant, o BFF simplesmente não tem nenhum evento para relayar por
// Socket.IO àquele tenant (nunca um comportamento "quebrado", só ausente).
const realtimeCapability = "realtime.events"
const realtimeRoute = "tenant_realtime_events"

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
		//
		// GO-017 substitui a rota de exemplo/placeholder pelas rotas reais
		// de internal-api.yaml que bff-api.yaml de fato consome
		// (listRecords/createRecord/getActor) — ver nota de escopo em
		// docs/migracao-go/execucoes/GO-017.md sobre por que get/update/
		// delete por ID não são wireados aqui ainda.
		mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records",
			tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
				cutover.RequireOwnership(guard, recordsCapability, listRecordsHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/sync/{table}/exchange",
			tenancy.Middleware(verifier, telemetry.Middleware("tenant_sync", httpMetrics,
				cutover.RequireOwnership(guard, recordsCapability, syncExchangeHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/records",
			tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
				cutover.RequireOwnership(guard, recordsCapability, createRecordHandler(tracker, db)))))

		// getActor não passa por cutover.RequireOwnership: resolução de
		// identidade não é uma capacidade de domínio sujeita a corte
		// Node/Go, é infraestrutura que já vive inteiramente em Go desde
		// GO-008 — só precisa de tenancy.Middleware (identidade+tenant
		// verificados).
		mux.Handle("GET /v1/tenants/{tenant}/actor",
			tenancy.Middleware(verifier, telemetry.Middleware(actorRoute, httpMetrics, getActorHandler(tracker, db))))

		// GO-019: tabelas/campos (metadata.CreateTable/AddField, GO-011) e
		// views (internal/views, novo) nunca tinham rota HTTP — o ciclo do
		// editor (criar, salvar, reabrir, publicar) exige as duas.
		// Capacidades próprias de cutover (não recordsCapability): schema e
		// views são concerns distintos de dados de registro, cada um pode
		// ser cortado para Go independentemente (mesmo espírito granular de
		// GO-009).
		mux.Handle("POST /v1/tenants/{tenant}/tables",
			tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
				cutover.RequireOwnership(guard, tablesSchemaCapability, createTableHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/fields",
			tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
				cutover.RequireOwnership(guard, tablesSchemaCapability, addFieldHandler(tracker, db)))))
		mux.Handle("PATCH /v1/tenants/{tenant}/tables/{table}/permissions",
			tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
				cutover.RequireOwnership(guard, tablesSchemaCapability, updateTablePermissionsHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/views",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, createViewHandler(tracker, db)))))
		mux.Handle("GET /v1/tenants/{tenant}/views",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, listViewsHandler(tracker, db)))))
		mux.Handle("GET /v1/tenants/{tenant}/views/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, getViewHandler(tracker, db)))))
		mux.Handle("PATCH /v1/tenants/{tenant}/views/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, updateViewHandler(tracker, db)))))
		mux.Handle("GET /v1/tenants/{tenant}/views/{id}/render",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, renderViewHandler(tracker, db)))))
		// submit (form_action) e rows/{recordId} (ação de coluna "Delete")
		// — GO-039, o formulário de escrita real de Edit e a exclusão de
		// linha de List.
		mux.Handle("POST /v1/tenants/{tenant}/views/{id}/submit",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, submitViewHandler(tracker, db)))))
		mux.Handle("DELETE /v1/tenants/{tenant}/views/{id}/rows/{recordId}",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, deleteViewRowHandler(tracker, db)))))

		// GO-028: o BFF faz polling desta rota (uma vez por socket
		// conectado) para saber o que relayar por Socket.IO — ver
		// realtime.go e docs/migracao-go/execucoes/GO-028.md.
		mux.Handle("GET /v1/tenants/{tenant}/realtime/events",
			tenancy.Middleware(verifier, telemetry.Middleware(realtimeRoute, httpMetrics,
				cutover.RequireOwnership(guard, realtimeCapability, realtimeEventsHandler(tracker, db)))))

		// GO-044: administração de usuários — a superfície de
		// auth/admin.ts do legado (ver docs/migracao-go/execucoes/GO-044.md
		// para as decisões de escopo). endImpersonationHandler não passa
		// por RequireOwnership: encerrar uma impersonação já iniciada não é
		// uma nova mutação de domínio sujeita a corte, é a limpeza de um
		// efeito que já aconteceu (mesmo raciocínio de getActorHandler não
		// passar por cutover).
		mux.Handle("GET /v1/tenants/{tenant}/users",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				cutover.RequireOwnership(guard, usersAdminCapability, listUsersHandler(tracker, db)))))
		mux.Handle("PATCH /v1/tenants/{tenant}/users/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				cutover.RequireOwnership(guard, usersAdminCapability, updateUserHandler(tracker, db)))))
		mux.Handle("DELETE /v1/tenants/{tenant}/users/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				cutover.RequireOwnership(guard, usersAdminCapability, deleteUserHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/users/{id}/reset-password",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				cutover.RequireOwnership(guard, usersAdminCapability, resetPasswordHandler(tracker, db)))))
		mux.Handle("GET /v1/tenants/{tenant}/users/{id}/tokens",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				cutover.RequireOwnership(guard, usersAdminCapability, listUserTokensHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/users/{id}/impersonate",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				cutover.RequireOwnership(guard, usersAdminCapability, startImpersonationHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/impersonations/{id}/end",
			tenancy.Middleware(verifier, telemetry.Middleware(usersAdminRoute, httpMetrics,
				endImpersonationHandler(tracker, db))))
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: telemetry.LimitRequests(mux, 128, 10*time.Second, registry), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context {
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

// tenantProbeHandler (a prova de ponta a ponta de que tenant+ator chegam
// resolvidos e uma operação de banco roda isolada no schema correto) foi
// substituído por rotas de domínio reais em GO-017 (records.go) — a
// pergunta que ele respondia já é respondida, com mais rigor, pelos testes
// de listRecords/createRecord/getActor contra Postgres real.
