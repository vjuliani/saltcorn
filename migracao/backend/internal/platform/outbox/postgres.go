package outbox

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Handler mantém compatibilidade com os consumidores PostgreSQL.
type Handler func(context.Context, pgx.Tx, OutboxEvent) error

func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}
func Do(ctx context.Context, tx pgx.Tx, key string, payload any, fn func(context.Context, pgx.Tx) (any, []Event, error)) (any, bool, error) {
	return DoTx(ctx, database.AsTx(tx), key, payload, func(ctx context.Context, _ database.Tx) (any, []Event, error) { return fn(ctx, tx) })
}
func ListPending(ctx context.Context, tx pgx.Tx, limit int) ([]OutboxEvent, error) {
	return ListPendingTx(ctx, database.AsTx(tx), limit)
}
func ListFailed(ctx context.Context, tx pgx.Tx, limit int) ([]OutboxEvent, error) {
	return ListFailedTx(ctx, database.AsTx(tx), limit)
}
func ProcessPending(ctx context.Context, tx pgx.Tx, limit, maxAttempts int, handler Handler) (int, int, error) {
	return ProcessPendingTx(ctx, database.AsTx(tx), limit, maxAttempts, func(ctx context.Context, _ database.Tx, ev OutboxEvent) error { return handler(ctx, tx, ev) })
}
