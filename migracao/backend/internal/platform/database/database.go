// Package database dá acesso ao Postgres com isolamento de tenant seguro
// sob reuso de conexão pooled — o requisito central de GO-007. GO-008
// estende isso com identidade de ator/papel na mesma transação, para que
// políticas RLS nativas (`current_setting('app.current_user_id')`) tenham o
// que ler. GO-010 acrescenta log estruturado e métricas por transação
// (resultado classificado e duração, nunca texto de SQL ou parâmetros).
// Nenhuma tabela de domínio de usuário é criada aqui (isso é GO-011); este
// pacote só resolve "em qual schema, como qual ator" uma transação roda, de
// forma que nunca vaze entre tenants/atores quando o pool devolve a mesma
// conexão física para uma chamada diferente.
package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// DB envolve um pool de conexões Postgres.
type DB struct {
	pool    *pgxpool.Pool
	metrics *telemetry.SQLMetrics
}

// SetMetrics associa m a esta instância — chamado uma vez, na inicialização
// do processo (cmd/server, cmd/worker), depois de criar o
// *telemetry.Registry compartilhado (GO-010). Sem chamar SetMetrics, as
// transações continuam sendo logadas normalmente, só não geram métricas
// (m == nil é seguro, ver telemetry.SQLMetrics.Observe).
func (db *DB) SetMetrics(m *telemetry.SQLMetrics) { db.metrics = m }

// Stat expõe estatísticas do pool subjacente (conexões adquiridas, ociosas,
// totais, máximas) — usado por cmd/server para publicar gauges de
// saturação (GO-010): quantas conexões estão em uso vs. disponíveis é o
// sinal mais direto de saturação de banco que este pacote pode oferecer
// sem instrumentar cada query individualmente.
func (db *DB) Stat() *pgxpool.Stat { return db.pool.Stat() }

// Open cria o pool a partir de uma DSN (`postgres://...`). O pool em si não
// fixa nenhum schema — isso é decidido por transação em WithTenant.
func Open(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("database: DSN inválida: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: abrir pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping inicial falhou: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close fecha o pool. Não drena trabalho em curso — isso é responsabilidade
// de quem chama (ver internal/platform/shutdown, GO-005): pare de aceitar
// trabalho novo e espere o Tracker antes de chamar Close.
func (db *DB) Close() { db.pool.Close() }

// Pool expõe o pool subjacente para casos que genuinamente precisam dele
// (ex.: métricas, health check de conectividade). Código de domínio deve
// usar WithTenant/WithTenantAndActor, não este método, para qualquer
// operação que leia ou escreva dados de tenant.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// WithTenant adquire uma conexão do pool, abre uma transação, escopa essa
// transação ao schema do tenant via `SET LOCAL search_path` — nunca `SET`
// sem `LOCAL`, que persistiria na conexão física depois que ela voltasse ao
// pool e vazaria para a próxima chamada, de qualquer tenant, que reusasse
// essa mesma conexão (a classe de bug que GO-001 e o critério de aceite de
// GO-007 apontam) — executa fn, e commita ou desfaz conforme o resultado.
// `SET LOCAL` reverte sozinho ao fim da transação (commit ou rollback),
// então a conexão sempre volta ao pool sem search_path residual.
//
// Cancelamento de ctx durante fn propaga para a query em andamento (pgx
// cancela a query no servidor) e o defer garante rollback — a conexão não
// fica em transação pendurada nem com estado inconsistente.
func (db *DB) WithTenant(ctx context.Context, t tenancy.Tenant, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.withTenantTx(ctx, t, fn)
}

// WithTenantAndActor faz tudo que WithTenant faz, e também define, na mesma
// transação, as GUCs `app.current_user_id` e `app.current_user_role`
// (GO-008) — os nomes que uma política RLS nativa consulta via
// `current_setting(...)`, replicando o mecanismo de
// `table.ts`/`enableOwnershipRLS` da produção Node (matriz GO-001 §2.1).
// Usa `set_config(..., true)` (o terceiro argumento `true` = escopo local à
// transação, equivalente a `SET LOCAL`) em vez de montar SQL com o valor
// interpolado: `set_config` é uma função normal, então o valor do ator (que
// vem de uma claim de token, não é um identificador de schema) é sempre
// parametrizado, nunca concatenado.
func (db *DB) WithTenantAndActor(ctx context.Context, t tenancy.Tenant, actorID string, roleID int, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.withTenantTx(ctx, t, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", actorID); err != nil {
			return fmt.Errorf("database: definir app.current_user_id: %w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_role', $1, true)", strconv.Itoa(roleID)); err != nil {
			return fmt.Errorf("database: definir app.current_user_role: %w", err)
		}
		return fn(ctx, tx)
	})
}

// withTenantTx concentra o ciclo de vida de transação comum a WithTenant e
// WithTenantAndActor: begin, SET LOCAL search_path, executar fn, e
// commit/rollback conforme o resultado.
func (db *DB) withTenantTx(ctx context.Context, t tenancy.Tenant, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	start := time.Now()
	defer func() {
		db.instrument(ctx, t, time.Since(start), err)
	}()

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		err = fmt.Errorf("database: iniciar transação: %w", err)
		return err
	}

	defer tx.Rollback(context.Background())

	schema := tenancy.SchemaName(t)
	setSearchPath := fmt.Sprintf("SET LOCAL search_path TO %s", pgx.Identifier{schema}.Sanitize())
	if _, execErr := tx.Exec(ctx, setSearchPath); execErr != nil {
		err = fmt.Errorf("database: definir search_path para tenant %q: %w", t, execErr)
		return err
	}

	if fnErr := fn(ctx, tx); fnErr != nil {
		err = fnErr
		return err
	}
	return tx.Commit(ctx)
}

// instrument loga e mede uma transação concluída (GO-010) — nunca inclui
// texto de SQL, parâmetros ou a mensagem crua do erro do driver: uma
// mensagem de erro do Postgres pode ecoar de volta um valor de linha (ex.:
// violação de constraint única mostrando o valor duplicado), então só o
// resultado CLASSIFICADO (um conjunto fixo e pequeno de rótulos) é
// registrado, tanto no log quanto na métrica.
func (db *DB) instrument(ctx context.Context, t tenancy.Tenant, elapsed time.Duration, err error) {
	result := classifyResult(err)
	logger := telemetry.LoggerFor(ctx).With("schema", string(t), "duration_ms", elapsed.Milliseconds(), "result", result)
	if err != nil {
		logger.Warn("transação SQL concluída com erro")
	} else {
		logger.Debug("transação SQL concluída")
	}
	db.metrics.Observe(elapsed, result)
}

func classifyResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}
