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

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/files"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/health"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pluginhost"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
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

// workflowsCapability (GO-048) identifica a capacidade de criar/ler/
// editar/rodar workflows — também distinta de recordsCapability/
// viewsCapability, pelo mesmo motivo granular de GO-009/GO-019.
const workflowsCapability = "workflows"
const workflowsRoute = "tenant_workflows"

// filesCapability (GO-051) identifica a capacidade de enviar/baixar
// arquivos (internal/files, GO-026 — sem consumidor HTTP até esta
// tarefa) — capacidade própria, pelo mesmo motivo granular de GO-009.
const filesCapability = "files"
const filesRoute = "tenant_files"

// eventsCapability (GO-052) identifica a capacidade de emitir um evento
// nomeado (Dispatcher.EmitEvent) — capacidade própria, pelo mesmo motivo
// granular de GO-009: o corte gradual Node/Go pode liberar este mecanismo
// independentemente de recordsCapability, mesmo ambos passando pelo
// mesmo Dispatcher internamente.
const eventsCapability = "events"
const eventsRoute = "tenant_events"

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

	// sqliteDB (GO-041) — o adapter "modo desktop" (internal/platform/
	// sqlite, GO-030), mutuamente exclusivo com o Postgres no MESMO
	// processo: DatabaseURL configurada sempre tem precedência (log de
	// aviso se os dois estiverem presentes, nunca um erro fatal — mesmo
	// espírito de config redundante não travar a subida). Escopo NARROW
	// desta entrega (ver cmd/server/sqlite.go): só tabelas/campos,
	// registros (sem trigger) e views (CRUD+render List/Show/Edit+submit)
	// respondem de verdade neste modo — todas as demais rotas (sync,
	// histórico, workflows, arquivos, eventos, admin de usuário, etc.)
	// continuam exclusivamente Postgres, 503 se só SQLite estiver
	// configurado (GO-055 registrada para fechar essa lacuna).
	var sqliteDB *sqlite.DB
	sqliteMode := false
	if cfg.SQLiteDir != "" {
		if cfg.DatabaseURL != "" {
			logger.Warn("SALTCORN_GO_SQLITE_DIR e SALTCORN_GO_DATABASE_URL configuradas juntas — Postgres tem precedência, SQLite ignorado nesta instância")
		} else {
			sqliteDB, err = sqlite.Open(cfg.SQLiteDir)
			if err != nil {
				logger.Error("abrir diretório de tenants SQLite", "error", err.Error())
				os.Exit(1)
			}
			defer sqliteDB.Close()
			sqliteMode = true
			logger.Info("adapter SQLite ativo (modo desktop) — escopo narrow: tabelas/campos, registros (sem trigger) e views", "dir", cfg.SQLiteDir)
		}
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

		// Dispatcher de triggers/ações (GO-040) — PRIMEIRO ponto em que
		// internal/triggers.Dispatcher (mecanismo desde GO-024) e
		// internal/expression.Evaluator (fachada desde GO-023) são ligados a
		// uma requisição HTTP real; até aqui só existiam em teste. O
		// cliente do host de plugins (GO-022) é sempre construído — a
		// inicialização do processo Node é preguiçosa (só sobe no primeiro
		// Eval real), então HostScript vazio nunca custa nada na subida, só
		// falha de forma explícita se algum only_if/run_js_code realmente
		// tentar avaliar sem host configurado (nunca um pânico por
		// Expression nil dentro de shouldFire). run_js_code fica registrado
		// no Dispatcher só quando PluginHostScript está configurado —
		// caso contrário, disparar essa ação nativa devolve ErrUnknownAction
		// explícito (mesmo espírito de FilesRootDir/SMTPHost vazios), em vez
		// de tentar subir um host que não existe.
		pluginClient := pluginhost.NewClient(pluginhost.ClientOptions{
			NodeBin:    cfg.PluginHostNodeBin,
			HostScript: cfg.PluginHostScript,
		})
		defer pluginClient.Close()
		evaluator := &expression.Evaluator{Client: pluginClient, Guard: guard}
		dispatcher := &triggers.Dispatcher{Expression: evaluator, Actions: triggers.BuiltinActions()}
		if cfg.PluginHostScript != "" {
			dispatcher.RunJSCode = triggers.NewRunJSCode(evaluator)
		} else {
			logger.Warn("SALTCORN_GO_PLUGINHOST_SCRIPT não configurada — ação nativa run_js_code indisponível (ErrUnknownAction ao disparar)")
		}

		// filesBackend (GO-051) — PRIMEIRO ponto em que internal/files
		// (mecanismo desde GO-026) é ligado a uma requisição HTTP real;
		// até aqui só existia em teste/no worker (limpeza de órfãos).
		// FilesRootDir vazio = feature indisponível (mesmo espírito de
		// PluginHostScript/SMTPHost acima) — uploadFileHandler/
		// downloadFileHandler devolvem 503 explícito, nunca tentam usar
		// um backend nil.
		var filesBackend *files.LocalBackend
		if cfg.FilesRootDir != "" {
			filesBackend = files.NewLocalBackend(cfg.FilesRootDir)
		} else {
			logger.Warn("SALTCORN_GO_FILES_ROOT_DIR não configurada — upload/download de arquivo indisponível (503 files_unavailable)")
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
		// delete por ID não eram wireados aqui ainda. GO-040 liga
		// update/delete (getRecord por ID continua fora — bff-api.yaml
		// ainda não o expõe a nenhum consumidor React).
		if !sqliteMode {
			mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					cutover.RequireOwnership(guard, recordsCapability, listRecordsHandler(tracker, db)))))
			mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/records",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					cutover.RequireOwnership(guard, recordsCapability, createRecordHandler(tracker, db, dispatcher)))))
			// updateRecord/deleteRecord (GO-040) — a rota já existia no
			// contrato desde GO-006 (ver nota de escopo acima), nunca tinha
			// handler; ligar aqui também é o ponto em que triggers/ações
			// (Dispatcher.HooksFor) passam a disparar de uma escrita HTTP
			// real (ver GO-040 abaixo).
			mux.Handle("PATCH /v1/tenants/{tenant}/tables/{table}/records/{id}",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					cutover.RequireOwnership(guard, recordsCapability, updateRecordHandler(tracker, db, dispatcher)))))
			mux.Handle("DELETE /v1/tenants/{tenant}/tables/{table}/records/{id}",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					cutover.RequireOwnership(guard, recordsCapability, deleteRecordHandler(tracker, db, dispatcher)))))
		} else {
			// GO-041: mesmos paths, contra o adapter SQLite — SEM
			// disparo de trigger (hooks=nil, ver cmd/server/sqlite.go).
			mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					sqliteListRecordsHandler(tracker, sqliteDB))))
			mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/records",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					sqliteCreateRecordHandler(tracker, sqliteDB))))
			mux.Handle("PATCH /v1/tenants/{tenant}/tables/{table}/records/{id}",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					sqliteUpdateRecordHandler(tracker, sqliteDB))))
			mux.Handle("DELETE /v1/tenants/{tenant}/tables/{table}/records/{id}",
				tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
					sqliteDeleteRecordHandler(tracker, sqliteDB))))
		}
		mux.Handle("POST /v1/tenants/{tenant}/sync/{table}/exchange",
			tenancy.Middleware(verifier, telemetry.Middleware("tenant_sync", httpMetrics,
				cutover.RequireOwnership(guard, recordsCapability, syncExchangeHandler(tracker, db)))))
		// getRecordHistory/restoreRecordVersion (GO-045) — versionamento
		// de linha, só relevante para tabelas versioned=true (ver
		// createTableHandler); mesma capacidade de ownership de qualquer
		// outra rota de registro.
		mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records/{id}/history",
			tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
				cutover.RequireOwnership(guard, recordsCapability, getRecordHistoryHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/records/{id}/restore",
			tenancy.Middleware(verifier, telemetry.Middleware(recordsRoute, httpMetrics,
				cutover.RequireOwnership(guard, recordsCapability, restoreRecordVersionHandler(tracker, db)))))

		// getActor não passa por cutover.RequireOwnership: resolução de
		// identidade não é uma capacidade de domínio sujeita a corte
		// Node/Go, é infraestrutura que já vive inteiramente em Go desde
		// GO-008 — só precisa de tenancy.Middleware (identidade+tenant
		// verificados).
		if !sqliteMode {
			mux.Handle("GET /v1/tenants/{tenant}/actor",
				tenancy.Middleware(verifier, telemetry.Middleware(actorRoute, httpMetrics, getActorHandler(tracker, db))))
		} else {
			mux.Handle("GET /v1/tenants/{tenant}/actor",
				tenancy.Middleware(verifier, telemetry.Middleware(actorRoute, httpMetrics, sqliteGetActorHandler(tracker, sqliteDB))))
		}
		// setActorLanguage (GO-047) — mesmo raciocínio de getActor acima:
		// preferência de idioma é self-service sobre a PRÓPRIA identidade
		// delegada, nunca uma capacidade de domínio sujeita a corte.
		mux.Handle("PATCH /v1/tenants/{tenant}/actor",
			tenancy.Middleware(verifier, telemetry.Middleware(actorRoute, httpMetrics, setActorLanguageHandler(tracker, db))))

		// GO-019: tabelas/campos (metadata.CreateTable/AddField, GO-011) e
		// views (internal/views, novo) nunca tinham rota HTTP — o ciclo do
		// editor (criar, salvar, reabrir, publicar) exige as duas.
		// Capacidades próprias de cutover (não recordsCapability): schema e
		// views são concerns distintos de dados de registro, cada um pode
		// ser cortado para Go independentemente (mesmo espírito granular de
		// GO-009).
		if !sqliteMode {
			mux.Handle("POST /v1/tenants/{tenant}/tables",
				tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
					cutover.RequireOwnership(guard, tablesSchemaCapability, createTableHandler(tracker, db)))))
			mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/fields",
				tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
					cutover.RequireOwnership(guard, tablesSchemaCapability, addFieldHandler(tracker, db)))))
		} else {
			mux.Handle("POST /v1/tenants/{tenant}/tables",
				tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
					sqliteCreateTableHandler(tracker, sqliteDB))))
			mux.Handle("POST /v1/tenants/{tenant}/tables/{table}/fields",
				tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
					sqliteAddFieldHandler(tracker, sqliteDB))))
		}
		mux.Handle("PATCH /v1/tenants/{tenant}/tables/{table}/permissions",
			tenancy.Middleware(verifier, telemetry.Middleware(tablesSchemaRoute, httpMetrics,
				cutover.RequireOwnership(guard, tablesSchemaCapability, updateTablePermissionsHandler(tracker, db)))))
		if !sqliteMode {
			mux.Handle("POST /v1/tenants/{tenant}/views",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					cutover.RequireOwnership(guard, viewsCapability, createViewHandler(tracker, db)))))
			mux.Handle("GET /v1/tenants/{tenant}/views",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					cutover.RequireOwnership(guard, viewsCapability, listViewsHandler(tracker, db)))))
		} else {
			mux.Handle("POST /v1/tenants/{tenant}/views",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					sqliteCreateViewHandler(tracker, sqliteDB))))
			mux.Handle("GET /v1/tenants/{tenant}/views",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					sqliteListViewsHandler(tracker, sqliteDB))))
		}
		mux.Handle("GET /v1/tenants/{tenant}/views/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, getViewHandler(tracker, db)))))
		mux.Handle("PATCH /v1/tenants/{tenant}/views/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, updateViewHandler(tracker, db)))))
		// submit (form_action) e rows/{recordId} (ação de coluna "Delete")
		// — GO-039, o formulário de escrita real de Edit e a exclusão de
		// linha de List.
		if !sqliteMode {
			mux.Handle("GET /v1/tenants/{tenant}/views/{id}/render",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					cutover.RequireOwnership(guard, viewsCapability, renderViewHandler(tracker, db)))))
			mux.Handle("POST /v1/tenants/{tenant}/views/{id}/submit",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					cutover.RequireOwnership(guard, viewsCapability, submitViewHandler(tracker, db)))))
		} else {
			mux.Handle("GET /v1/tenants/{tenant}/views/{id}/render",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					sqliteRenderViewHandler(tracker, sqliteDB))))
			mux.Handle("POST /v1/tenants/{tenant}/views/{id}/submit",
				tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
					sqliteSubmitViewHandler(tracker, sqliteDB))))
		}
		mux.Handle("DELETE /v1/tenants/{tenant}/views/{id}/rows/{recordId}",
			tenancy.Middleware(verifier, telemetry.Middleware(viewsRoute, httpMetrics,
				cutover.RequireOwnership(guard, viewsCapability, deleteViewRowHandler(tracker, db)))))

		// GO-028: o BFF faz polling desta rota (uma vez por socket
		// conectado) para saber o que relayar por Socket.IO — ver
		// realtime.go e docs/migracao-go/execucoes/GO-028.md.
		mux.Handle("GET /v1/tenants/{tenant}/realtime/events",
			tenancy.Middleware(verifier, telemetry.Middleware(realtimeRoute, httpMetrics,
				cutover.RequireOwnership(guard, realtimeCapability, realtimeEventsHandler(tracker, db)))))

		// GO-048: CRUD da definição de workflow (internal/workflow, novo
		// nesta tarefa — até aqui só existia estado de EXECUÇÃO
		// persistido, nunca a definição) + o endpoint de execução ponta a
		// ponta que o editor visual usa. runWorkflowHandler reaproveita o
		// MESMO evaluator já construído acima para o Dispatcher de
		// triggers (GO-040).
		mux.Handle("POST /v1/tenants/{tenant}/workflows",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, createWorkflowHandler(tracker, db)))))
		mux.Handle("GET /v1/tenants/{tenant}/workflows",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, listWorkflowsHandler(tracker, db)))))
		mux.Handle("GET /v1/tenants/{tenant}/workflows/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, getWorkflowHandler(tracker, db)))))
		mux.Handle("PATCH /v1/tenants/{tenant}/workflows/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, updateWorkflowHandler(tracker, db)))))
		mux.Handle("DELETE /v1/tenants/{tenant}/workflows/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, deleteWorkflowHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/workflows/{id}/steps",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, createStepHandler(tracker, db)))))
		mux.Handle("PATCH /v1/tenants/{tenant}/workflows/{id}/steps/{stepId}",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, updateStepHandler(tracker, db)))))
		mux.Handle("DELETE /v1/tenants/{tenant}/workflows/{id}/steps/{stepId}",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, deleteStepHandler(tracker, db)))))
		mux.Handle("POST /v1/tenants/{tenant}/workflows/{id}/run",
			tenancy.Middleware(verifier, telemetry.Middleware(workflowsRoute, httpMetrics,
				cutover.RequireOwnership(guard, workflowsCapability, runWorkflowHandler(tracker, db, evaluator)))))

		// GO-051: upload/download de arquivo — o consumidor HTTP que
		// internal/files (GO-026) não tinha (decisão de escopo
		// explícita daquela tarefa). Necessário para a fieldview
		// "upload" do Edit funcionar de ponta a ponta.
		mux.Handle("POST /v1/tenants/{tenant}/files",
			tenancy.Middleware(verifier, telemetry.Middleware(filesRoute, httpMetrics,
				cutover.RequireOwnership(guard, filesCapability, uploadFileHandler(tracker, db, filesBackend)))))
		mux.Handle("GET /v1/tenants/{tenant}/files/{id}",
			tenancy.Middleware(verifier, telemetry.Middleware(filesRoute, httpMetrics,
				cutover.RequireOwnership(guard, filesCapability, downloadFileHandler(tracker, db, filesBackend)))))

		// GO-052: evento nomeado (Trigger.emitEvent do legado) — o
		// mecanismo por trás de POST /api/emit-event, que o pack piloto
		// guitars usa via receive_share_trigger (when_trigger=
		// "ReceiveMobileShareData"). Reaproveita o MESMO dispatcher já
		// construído acima para o corte de triggers/ações (GO-040).
		mux.Handle("POST /v1/tenants/{tenant}/events/{eventname}",
			tenancy.Middleware(verifier, telemetry.Middleware(eventsRoute, httpMetrics,
				cutover.RequireOwnership(guard, eventsCapability, emitEventHandler(tracker, db, dispatcher)))))

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
