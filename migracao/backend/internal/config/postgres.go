// Wrappers pgx.Tx (GO-055) — mesmo padrão de internal/records/postgres.go
// (GO-030): preservam o nome/assinatura ORIGINAL para todo chamador
// Postgres existente, delegando para as versões Tx via database.AsTx.
package config

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

func Set(ctx context.Context, tx pgx.Tx, key string, value any) error {
	return SetTx(ctx, database.AsTx(tx), key, value)
}

func Get(ctx context.Context, tx pgx.Tx, key string) (value any, ok bool, err error) {
	return GetTx(ctx, database.AsTx(tx), key)
}

func Delete(ctx context.Context, tx pgx.Tx, key string) error {
	return DeleteTx(ctx, database.AsTx(tx), key)
}

func ListAll(ctx context.Context, tx pgx.Tx) (map[string]any, error) {
	return ListAllTx(ctx, database.AsTx(tx))
}
