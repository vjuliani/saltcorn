package sqlite

import (
	"context"
	"database/sql"

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
	_, err := t.tx.ExecContext(ctx, query, args...)
	return translateErr(err)
}

func (t Tx) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, translateErr(err)
	}
	return sqlRows{rows}, nil
}

func (t Tx) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	return sqlRow{t.tx.QueryRowContext(ctx, query, args...)}
}

type sqlRow struct{ row *sql.Row }

func (r sqlRow) Scan(dest ...any) error { return translateErr(r.row.Scan(dest...)) }

type sqlRows struct{ rows *sql.Rows }

func (r sqlRows) Scan(dest ...any) error { return translateErr(r.rows.Scan(dest...)) }
func (r sqlRows) Next() bool             { return r.rows.Next() }
func (r sqlRows) Close()                 { _ = r.rows.Close() }
func (r sqlRows) Err() error             { return translateErr(r.rows.Err()) }
