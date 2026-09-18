package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Tx implementa database.Tx sobre um *sql.Tx nativo do driver
// modernc.org/sqlite (puro Go, sem cgo — mesmo raciocínio de dependência
// mínima já usado para outras bibliotecas maduras desta migração:
// reescrever um parser/driver SQLite à mão seria a classe de superfície
// grande e mal compreendida que este projeto evita).
type Tx struct{ tx *sql.Tx }

func (t Tx) Dialect() database.Dialect { return database.DialectSQLite }

func (t Tx) Exec(ctx context.Context, query string, args ...any) error {
	_, err := t.tx.ExecContext(ctx, query, sqliteArgs(args)...)
	return translateErr(err)
}

func (t Tx) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, sqliteArgs(args)...)
	if err != nil {
		return nil, translateErr(err)
	}
	return sqlRows{rows}, nil
}

func (t Tx) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	return sqlRow{t.tx.QueryRowContext(ctx, query, sqliteArgs(args)...)}
}

type sqlRow struct{ row *sql.Row }

func (r sqlRow) Scan(dest ...any) error { return translateErr(r.row.Scan(dest...)) }

type sqlRows struct{ rows *sql.Rows }

func (r sqlRows) Scan(dest ...any) error { return translateErr(r.rows.Scan(dest...)) }
func (r sqlRows) Next() bool             { return r.rows.Next() }
func (r sqlRows) Close()                 { _ = r.rows.Close() }
func (r sqlRows) Err() error             { return translateErr(r.rows.Err()) }

func (r sqlRows) Columns() ([]string, error) { return r.rows.Columns() }
func (r sqlRows) Values() ([]any, error) {
	names, err := r.rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(names))
	dest := make([]any, len(names))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := r.rows.Scan(dest...); err != nil {
		return nil, translateErr(err)
	}
	types, err := r.rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	for i, column := range types {
		if strings.EqualFold(column.DatabaseTypeName(), "boolean") && values[i] != nil {
			if value, ok := values[i].(int64); ok {
				values[i] = value != 0
			}
		}
	}
	return values, nil
}

// Datas com timezone são armazenadas em UTC e precisão de microssegundos,
// mantendo a mesma ordenação textual e precisão do timestamptz PostgreSQL.
func sqliteArgs(args []any) []any {
	result := append([]any(nil), args...)
	for i, arg := range result {
		if value, ok := arg.(time.Time); ok {
			result[i] = value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000000-07:00")
		}
	}
	return result
}
