// Package database dá acesso ao Postgres com isolamento de tenant seguro
// sob reuso de conexão pooled — o requisito central de GO-007. Nenhuma
// tabela de domínio é criada aqui (isso é GO-011); este pacote só resolve
// "em qual schema esta transação roda" de forma que nunca vaze entre
// tenants quando o pool devolve a mesma conexão física para uma chamada
// diferente.
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// DB envolve um pool de conexões Postgres.
type DB struct {
	pool *pgxpool.Pool
}

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
// usar WithTenant, não este método, para qualquer operação que leia ou
// escreva dados de tenant.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// WithTenant adquire uma conexão do pool, abre uma transação, escopa essa
// transação ao schema do tenant via `SET LOCAL search_path` — nunca `SET`
// sem `LOCAL`, que persistiria na conexão física depois que ela voltasse ao
// pool e vazaria para a próxima chamada, de qualquer tenant, que reusasse
// essa mesma conexão (a classe de bug que GO-001 e o critério de aceite
// desta tarefa apontam) — executa fn, e commita ou desfaz conforme o
// resultado. `SET LOCAL` reverte sozinho ao fim da transação (commit ou
// rollback), então a conexão sempre volta ao pool sem search_path residual.
//
// Cancelamento de ctx durante fn propaga para a query em andamento (pgx
// cancela a query no servidor) e o defer garante rollback — a conexão não
// fica em transação pendurada nem com estado inconsistente.
func (db *DB) WithTenant(ctx context.Context, t tenancy.Tenant, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: iniciar transação: %w", err)
	}

	defer func() {
		if err != nil {
			// Rollback em melhor esforço com um contexto próprio: se ctx já
			// foi cancelado (a causa mais comum de err aqui), usar ctx para
			// o rollback poderia falhar silenciosamente e deixar a
			// transação pendurada até o servidor detectar a conexão caída.
			_ = tx.Rollback(context.Background())
			return
		}
		err = tx.Commit(ctx)
	}()

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
	return nil
}
