// Wrappers pgx.Tx (GO-055) — mesmo padrão de internal/records/postgres.go
// (GO-030): preservam o nome/assinatura ORIGINAL para todo chamador
// Postgres existente, delegando para as versões Tx via database.AsTx.
package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

func CreateScheduledTrigger(ctx context.Context, tx pgx.Tx, name, action, cronExpr, timezone string) (ScheduledTrigger, error) {
	return CreateScheduledTriggerTx(ctx, database.AsTx(tx), name, action, cronExpr, timezone)
}

func DueTriggers(ctx context.Context, tx pgx.Tx, now time.Time) ([]ScheduledTrigger, error) {
	return DueTriggersTx(ctx, database.AsTx(tx), now)
}

func ListAll(ctx context.Context, tx pgx.Tx) ([]ScheduledTrigger, error) {
	return ListAllTx(ctx, database.AsTx(tx))
}

// RunDue é o equivalente pgx.Tx de Dispatcher.RunDueTx.
func (d *Dispatcher) RunDue(ctx context.Context, tx pgx.Tx, now time.Time) (ran, failed int, err error) {
	return d.RunDueTx(ctx, database.AsTx(tx), now)
}
