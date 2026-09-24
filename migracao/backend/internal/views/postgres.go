// postgres.go (GO-041) — wrappers pgx.Tx finos sobre as funções
// dialeto-neutras (sufixo Tx) deste pacote, por compatibilidade com todos
// os chamadores HTTP existentes de cmd/server (exclusivamente Postgres até
// esta tarefa) — mesmo padrão já estabelecido em
// internal/records/postgres.go e internal/identity/postgres.go. Nenhuma
// lógica nova aqui, só database.AsTx(tx) + delegação.
package views

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func CreateView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, name string, tableID int, template string, configuration map[string]any, opts ViewOptions) (View, error) {
	return CreateViewTx(ctx, database.AsTx(tx), actorRole, name, tableID, template, configuration, opts)
}

func GetView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int) (View, error) {
	return GetViewTx(ctx, database.AsTx(tx), actorRole, id)
}

func GetViewByName(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, name string) (View, error) {
	return GetViewByNameTx(ctx, database.AsTx(tx), actorRole, name)
}

func ListViews(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tableID int) ([]View, error) {
	return ListViewsTx(ctx, database.AsTx(tx), actorRole, tableID)
}

func UpdateView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int, expectedVersion string, update ViewUpdate) (View, error) {
	return UpdateViewTx(ctx, database.AsTx(tx), actorRole, id, expectedVersion, update)
}

func CompileListPlan(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, limit, offset int) (*ListPlan, bool, error) {
	return CompileListPlanTx(ctx, database.AsTx(tx), actorRole, viewID, limit, offset)
}

func DeleteListRow(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, recordID int, expectedVersion string) error {
	return DeleteListRowTx(ctx, database.AsTx(tx), actorRole, viewID, recordID, expectedVersion)
}

func CompileShowPlan(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, recordID int) (*ShowPlan, error) {
	return CompileShowPlanTx(ctx, database.AsTx(tx), actorRole, viewID, recordID)
}

func CompileEditPlan(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, recordID int) (*EditPlan, error) {
	return CompileEditPlanTx(ctx, database.AsTx(tx), actorRole, viewID, recordID)
}

func SubmitEditView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, recordID int, expectedVersion string, values map[string]any) (*EditSubmitResult, error) {
	return SubmitEditViewTx(ctx, database.AsTx(tx), actorRole, viewID, recordID, expectedVersion, values)
}

func CompileFeedPlan(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, limit, offset int) (*FeedPlan, bool, error) {
	return CompileFeedPlanTx(ctx, database.AsTx(tx), actorRole, viewID, limit, offset)
}
