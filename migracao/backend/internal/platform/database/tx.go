package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNoRows é o sentinel neutro de dialeto para "nenhuma linha encontrada"
// — nunca comparar diretamente contra pgx.ErrNoRows ou sql.ErrNoRows fora
// deste pacote e do adapter de internal/platform/sqlite; os dois
// traduzem para este erro antes de devolver ao chamador.
var ErrNoRows = errors.New("database: nenhuma linha encontrada")

// Dialect identifica o SGBD para DDL, concorrência e consultas de domínio.
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

// Rows permite scans tipados e mapas de registros nos dois drivers.
type Rows interface {
	Columns() ([]string, error)
	Values() ([]any, error)
	Row
	Next() bool
	Close()
	Err() error
}

// Tx é a transação usada por metadata, records e outbox. WithTenant
// continua responsável pelo commit/rollback da transação externa.
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

func (r pgxRows) Columns() ([]string, error) {
	fields := r.rows.FieldDescriptions()
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names, nil
}
func (r pgxRows) Values() ([]any, error) { return r.rows.Values() }

// CollectMaps lê e fecha rows, preservando os valores do driver — com UMA
// normalização deliberada: todo `time.Time` (colunas `timestamptz`, ex.
// FieldDate) é convertido para UTC antes de sair deste pacote (GO-042,
// achado real de execução ponta a ponta contra Postgres real). pgx
// decodifica `timestamptz` para `time.Time` no fuso LOCAL do PROCESSO
// cliente (`time.Local`, nunca UTC), não da sessão Postgres — um valor
// gravado como meia-noite UTC (`coerceJSONValue`, internal/records)
// voltava, num processo rodando num fuso negativo, como 21h do dia
// ANTERIOR: o "dia" de um campo de data mudava dependendo só de em que
// fuso o servidor Go estava rodando, nunca do valor gravado. `.UTC()`
// aqui não perde precisão nem muda o instante — só normaliza a
// REPRESENTAÇÃO para o único fuso que `coerceJSONValue` também usa na
// escrita, fechando o ciclo.
func CollectMaps(rows Rows) ([]map[string]any, error) {
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(names))
		for i, name := range names {
			if t, ok := values[i].(time.Time); ok {
				row[name] = t.UTC()
			} else {
				row[name] = values[i]
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
