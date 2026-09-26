package files

import (
	"context"
	"errors"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// File é uma entrada do catálogo _sc_files — o equivalente reduzido do
// File do legado (models/file.ts), sem o caminho paralelo de metadado em
// xattrs do sistema de arquivos (ver comentário do pacote em schema.go).
type File struct {
	ID          int
	Filename    string
	MimeSuper   string
	MimeSub     string
	SizeBytes   int64
	MinRoleRead identity.RoleID
	// UserID é o dono do arquivo — nil significa "sem dono" (só o papel
	// decide leitura, nunca um bypass de ownership implícito).
	UserID     *int
	StorageKey string
}

// CreateFileTx insere a entrada de catálogo — chamado DEPOIS de
// Backend.Save ter sucesso (ver Upload em upload.go), nunca antes: nunca
// existe uma linha de catálogo apontando para bytes que não foram
// gravados com sucesso.
func CreateFileTx(ctx context.Context, tx database.Tx, f File) (File, error) {
	err := tx.QueryRow(ctx,
		`INSERT INTO _sc_files (filename, mime_super, mime_sub, size_bytes, min_role_read, user_id, storage_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		f.Filename, f.MimeSuper, f.MimeSub, f.SizeBytes, int(f.MinRoleRead), f.UserID, f.StorageKey,
	).Scan(&f.ID)
	if err != nil {
		return File{}, err
	}
	return f, nil
}

// GetFileTx lê uma entrada de catálogo por ID — ErrNotFound se não existir.
func GetFileTx(ctx context.Context, tx database.Tx, id int) (File, error) {
	var f File
	var minRole int
	err := tx.QueryRow(ctx,
		`SELECT id, filename, mime_super, mime_sub, size_bytes, min_role_read, user_id, storage_key FROM _sc_files WHERE id = $1`,
		id,
	).Scan(&f.ID, &f.Filename, &f.MimeSuper, &f.MimeSub, &f.SizeBytes, &minRole, &f.UserID, &f.StorageKey)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return File{}, ErrNotFound
		}
		return File{}, err
	}
	f.MinRoleRead = identity.RoleID(minRole)
	return f, nil
}
