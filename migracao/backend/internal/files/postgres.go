// Wrappers pgx.Tx (GO-055) — mesmo padrão de internal/records/postgres.go
// (GO-030): preservam o nome/assinatura ORIGINAL para todo chamador
// Postgres existente, delegando para as versões Tx via database.AsTx.
package files

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

func CreateFile(ctx context.Context, tx pgx.Tx, f File) (File, error) {
	return CreateFileTx(ctx, database.AsTx(tx), f)
}

func GetFile(ctx context.Context, tx pgx.Tx, id int) (File, error) {
	return GetFileTx(ctx, database.AsTx(tx), id)
}

func Upload(ctx context.Context, tx pgx.Tx, backend Backend, meta File, r io.Reader) (File, error) {
	return UploadTx(ctx, database.AsTx(tx), backend, meta, r)
}

func Download(ctx context.Context, tx pgx.Tx, backend Backend, actorRole identity.RoleID, actorUserID *int, fileID int) (io.ReadCloser, File, error) {
	return DownloadTx(ctx, database.AsTx(tx), backend, actorRole, actorUserID, fileID)
}
