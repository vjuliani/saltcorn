// Wrappers pgx.Tx (GO-055) — mesmo padrão de internal/records/postgres.go
// (GO-030): preservam o nome/assinatura ORIGINAL para todo chamador
// Postgres existente, delegando para as versões Tx via database.AsTx.
package triggers

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

func CreateTrigger(ctx context.Context, tx pgx.Tx, t Trigger) (Trigger, error) {
	return CreateTriggerTx(ctx, database.AsTx(tx), t)
}

func TriggersFor(ctx context.Context, tx pgx.Tx, tableID int, when WhenTrigger) ([]Trigger, error) {
	return TriggersForTx(ctx, database.AsTx(tx), tableID, when)
}

func TriggersForEvent(ctx context.Context, tx pgx.Tx, eventName string) ([]Trigger, error) {
	return TriggersForEventTx(ctx, database.AsTx(tx), eventName)
}

func ListAll(ctx context.Context, tx pgx.Tx) ([]Trigger, error) {
	return ListAllTx(ctx, database.AsTx(tx))
}

func GetTriggerByID(ctx context.Context, tx pgx.Tx, id int) (Trigger, error) {
	return GetTriggerByIDTx(ctx, database.AsTx(tx), id)
}

// RunOne é o equivalente pgx.Tx de Dispatcher.RunOneTx — mesmo padrão de
// wrapper, chamador Postgres existente (cmd/worker) não muda nenhuma
// linha.
func (d *Dispatcher) RunOne(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, trig Trigger, row map[string]any) error {
	return d.RunOneTx(ctx, database.AsTx(tx), tenant, actorRole, table, trig, row)
}

// HooksFor é o equivalente pgx.Tx de Dispatcher.HooksForTx — devolve um
// *records.Hooks (pgx.Tx-shaped) cujos callbacks convertem o tx recebido
// via database.AsTx antes de delegar aos mesmos runBeforeTx/runAfterTx
// que o caminho SQLite usa — nenhuma lógica duplicada entre os dois
// dialetos, só a casca do tipo muda.
func (d *Dispatcher) HooksFor(tenant tenancy.Tenant, actorRole identity.RoleID, user map[string]any) *records.Hooks {
	txHooks := d.HooksForTx(tenant, actorRole, user)
	return &records.Hooks{
		BeforeInsert: func(ctx context.Context, tx pgx.Tx, table metadata.Table, values map[string]any) error {
			return txHooks.BeforeInsert(ctx, database.AsTx(tx), table, values)
		},
		AfterInsert: func(ctx context.Context, tx pgx.Tx, table metadata.Table, record map[string]any) error {
			return txHooks.AfterInsert(ctx, database.AsTx(tx), table, record)
		},
		BeforeUpdate: func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int, values map[string]any) error {
			return txHooks.BeforeUpdate(ctx, database.AsTx(tx), table, id, values)
		},
		AfterUpdate: func(ctx context.Context, tx pgx.Tx, table metadata.Table, record map[string]any) error {
			return txHooks.AfterUpdate(ctx, database.AsTx(tx), table, record)
		},
		BeforeDelete: func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int) error {
			return txHooks.BeforeDelete(ctx, database.AsTx(tx), table, id)
		},
		AfterDelete: func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int) error {
			return txHooks.AfterDelete(ctx, database.AsTx(tx), table, id)
		},
	}
}

// EmitEvent é o equivalente pgx.Tx de Dispatcher.EmitEventTx.
func (d *Dispatcher) EmitEvent(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, eventName string, user map[string]any, payload map[string]any) (fired int, err error) {
	return d.EmitEventTx(ctx, database.AsTx(tx), tenant, actorRole, eventName, user, payload)
}
