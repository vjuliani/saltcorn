package metadata

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CurrentVersion lê o contador de versão do catálogo do tenant atual (ver
// schema.go) — o número que um cache (Cache) compara para saber se precisa
// recarregar. Chame dentro de db.WithTenant do tenant desejado.
func CurrentVersion(ctx context.Context, tx pgx.Tx) (int64, error) {
	var version int64
	err := tx.QueryRow(ctx, "SELECT version FROM _sc_metadata_version WHERE id = 1").Scan(&version)
	return version, err
}

// bumpVersion incrementa e retorna a nova versão — chamado internamente por
// toda operação que muta o catálogo (CreateTable, AddField, DropField,
// DropTable), na MESMA transação da mutação, para que o incremento nunca
// fique dessincronizado da mudança real.
func bumpVersion(ctx context.Context, tx pgx.Tx) (int64, error) {
	var version int64
	err := tx.QueryRow(ctx, "UPDATE _sc_metadata_version SET version = version + 1 WHERE id = 1 RETURNING version").Scan(&version)
	return version, err
}
