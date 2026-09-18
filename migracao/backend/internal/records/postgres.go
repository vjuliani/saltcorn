package records

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

type Hooks struct {
	BeforeInsert func(ctx context.Context, tx pgx.Tx, table metadata.Table, values map[string]any) error
	AfterInsert  func(ctx context.Context, tx pgx.Tx, table metadata.Table, record map[string]any) error
	BeforeUpdate func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int, values map[string]any) error
	AfterUpdate  func(ctx context.Context, tx pgx.Tx, table metadata.Table, record map[string]any) error
	BeforeDelete func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int) error
	AfterDelete  func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int) error
}

func adaptHooks(tx pgx.Tx, h *Hooks) *TxHooks {
	if h == nil {
		return nil
	}
	result := &TxHooks{}
	if h.BeforeInsert != nil {
		result.BeforeInsert = func(ctx context.Context, _ database.Tx, table metadata.Table, values map[string]any) error {
			return h.BeforeInsert(ctx, tx, table, values)
		}
	}
	if h.AfterInsert != nil {
		result.AfterInsert = func(ctx context.Context, _ database.Tx, table metadata.Table, record map[string]any) error {
			return h.AfterInsert(ctx, tx, table, record)
		}
	}
	if h.BeforeUpdate != nil {
		result.BeforeUpdate = func(ctx context.Context, _ database.Tx, table metadata.Table, id int, values map[string]any) error {
			return h.BeforeUpdate(ctx, tx, table, id, values)
		}
	}
	if h.AfterUpdate != nil {
		result.AfterUpdate = func(ctx context.Context, _ database.Tx, table metadata.Table, record map[string]any) error {
			return h.AfterUpdate(ctx, tx, table, record)
		}
	}
	if h.BeforeDelete != nil {
		result.BeforeDelete = func(ctx context.Context, _ database.Tx, table metadata.Table, id int) error {
			return h.BeforeDelete(ctx, tx, table, id)
		}
	}
	if h.AfterDelete != nil {
		result.AfterDelete = func(ctx context.Context, _ database.Tx, table metadata.Table, id int) error {
			return h.AfterDelete(ctx, tx, table, id)
		}
	}
	return result
}
func CreateRecord(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tableName string, values map[string]any, hooks *Hooks) (map[string]any, error) {
	return CreateRecordTx(ctx, database.AsTx(tx), actorRole, tableName, values, adaptHooks(tx, hooks))
}
func UpdateRecord(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tableName string, id int, expectedVersion string, values map[string]any, hooks *Hooks) (map[string]any, error) {
	return UpdateRecordTx(ctx, database.AsTx(tx), actorRole, tableName, id, expectedVersion, values, adaptHooks(tx, hooks))
}
func DeleteRecord(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tableName string, id int, expectedVersion string, hooks *Hooks) error {
	return DeleteRecordTx(ctx, database.AsTx(tx), actorRole, tableName, id, expectedVersion, adaptHooks(tx, hooks))
}
func Compile(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, q Query) (string, []any, error) {
	return CompileTx(ctx, database.AsTx(tx), actorRole, q)
}
func Rows(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, q Query) ([]map[string]any, error) {
	return RowsTx(ctx, database.AsTx(tx), actorRole, q)
}
