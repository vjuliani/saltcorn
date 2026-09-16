package records

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

// Rows compila q (ver Compile) e executa a consulta resultante na
// transação já aberta tx (escopada ao tenant correto por
// internal/platform/database.WithTenant — este pacote nunca abre sua
// própria transação, mesmo estilo de internal/identity e
// internal/metadata). Cada linha do resultado é um mapa nome-de-coluna →
// valor, incluindo colunas trazidas por Join (`"<campo>__<coluna>"`) e
// Aggregation (a chave é o Alias).
func Rows(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, q Query) ([]map[string]any, error) {
	sql, args, err := Compile(ctx, tx, actorRole, q)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToMap)
}
