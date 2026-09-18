// Tx/Row/Rows/Dialect (GO-030) são a fronteira mínima e neutra de dialeto
// que permite a UM pacote de domínio (por ora, só internal/metadata) rodar
// contra Postgres OU SQLite sem depender do tipo concreto `pgx.Tx`. Não é
// uma reescrita de `WithTenant`/`WithTenantAndActor` (que continuam
// devolvendo `pgx.Tx` de verdade, sem NENHUMA mudança de comportamento
// para o caminho Postgres já existente) — é um adaptador OPCIONAL, usado
// no ponto de chamada de quem quer que sua transação também sirva a um
// pacote escrito contra esta interface: `metadata.CreateTable(ctx,
// database.AsTx(tx), ...)` em vez de `metadata.CreateTable(ctx, tx, ...)`.
//
// Divergência deliberada de escopo (ver docs/migracao-go/execucoes/GO-030.md):
// só o subconjunto de métodos que internal/metadata de fato usa
// (Exec/Query/QueryRow, nunca Begin/Commit/Rollback — quem abre/fecha a
// transação continua sendo WithTenant) é generalizado aqui. Estender
// outros pacotes (internal/records, internal/platform/outbox, etc.) para
// esta mesma interface fica para tarefas futuras — cada um tem
// dependências de SQL específicas de Postgres mais profundas (`xmin`,
// `pg_advisory_xact_lock`, `FOR UPDATE SKIP LOCKED`) que exigem decisão de
// design própria, não uma generalização mecânica.
package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNoRows é o sentinel neutro de dialeto para "nenhuma linha encontrada"
// — nunca comparar diretamente contra pgx.ErrNoRows ou sql.ErrNoRows fora
// deste pacote e do adapter de internal/platform/sqlite; os dois
// traduzem para este erro antes de devolver ao chamador.
var ErrNoRows = errors.New("database: nenhuma linha encontrada")

// Dialect identifica o SGBD por trás de um Tx — internal/metadata usa
// isto só nos poucos pontos onde a sintaxe realmente diverge (tipo da
// coluna de id autoincrementada, valor padrão de timestamp, mecanismo de
// serialização de mutação de catálogo); todo o resto (placeholders `$N`,
// `RETURNING`, `ON CONFLICT`, citação de identificador via
// `pgx.Identifier.Sanitize()`) é sintaxe compartilhada entre os dois SGBDs
// e não precisa de nenhum branch.
type Dialect int

const (
	DialectPostgres Dialect = iota
	DialectSQLite
)

// Row é o equivalente mínimo de pgx.Row — já a interface exata que pgx.Row
// declara, então um valor pgx.Row satisfaz Row sem nenhum adaptador.
type Row interface {
	Scan(dest ...any) error
}

// Rows é o subconjunto de pgx.Rows/sql.Rows que internal/metadata usa —
// qualquer um dos dois já satisfaz esta interface estruturalmente.
type Rows interface {
	Row
	Next() bool
	Close()
	Err() error
}

// Tx é a fronteira mínima que internal/metadata consome — nunca o pgx.Tx
// inteiro (que também tem Begin/Commit/Rollback/CopyFrom/LargeObjects,
// nada disso é usado por um pacote de catálogo).
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Dialect() Dialect
}

// AsTx adapta um pgx.Tx JÁ ABERTO (por WithTenant/WithTenantAndActor,
// sem nenhuma mudança nesses dois) para o Tx mínimo acima — comportamento
// idêntico ao de chamar os métodos do pgx.Tx diretamente, só o tipo muda.
func AsTx(tx pgx.Tx) Tx { return pgxTx{tx} }

type pgxTx struct{ tx pgx.Tx }

func (p pgxTx) Dialect() Dialect { return DialectPostgres }

func (p pgxTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := p.tx.Exec(ctx, sql, args...)
	return err
}

func (p pgxTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := p.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, translatePgxErr(err)
	}
	return pgxRows{rows}, nil
}

func (p pgxTx) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return pgxRow{p.tx.QueryRow(ctx, sql, args...)}
}

type pgxRow struct{ row pgx.Row }

func (r pgxRow) Scan(dest ...any) error { return translatePgxErr(r.row.Scan(dest...)) }

type pgxRows struct{ rows pgx.Rows }

func (r pgxRows) Scan(dest ...any) error { return translatePgxErr(r.rows.Scan(dest...)) }
func (r pgxRows) Next() bool             { return r.rows.Next() }
func (r pgxRows) Close()                 { r.rows.Close() }
func (r pgxRows) Err() error             { return translatePgxErr(r.rows.Err()) }

func translatePgxErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRows
	}
	return err
}
