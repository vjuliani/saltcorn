package records

import (
	"context"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

// Rows compila q (ver Compile) e executa a consulta resultante na
// transação já aberta tx (escopada ao tenant correto por
// internal/platform/database.WithTenant — este pacote nunca abre sua
// própria transação, mesmo estilo de internal/identity e
// internal/metadata). Cada linha do resultado é um mapa nome-de-coluna →
// valor, incluindo colunas trazidas por Join (`"<campo>__<coluna>"`) e
// Aggregation (a chave é o Alias).
func RowsTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, q Query) ([]map[string]any, error) {
	sql, args, err := CompileTx(ctx, tx, actorRole, q)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return database.CollectMaps(rows)
}
