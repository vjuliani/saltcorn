// Package files implementa o catálogo e o armazenamento físico de
// arquivos (GO-026) — o equivalente reduzido de models/file.ts do legado.
//
// Divergências deliberadas do legado:
//   - Um único caminho de metadado (linha em _sc_files) — o legado tem
//     DOIS coexistindo (linha de _sc_files quando File.create() é chamado
//     explicitamente, OU xattrs do próprio arquivo em disco quando
//     "descoberto" via from_file_on_disk) — uma peculiaridade que nunca é
//     necessária aqui: todo arquivo Go passa por CreateFile.
//   - "Uploads interrompidos são limpos" é uma garantia NOVA, não uma
//     porta: o legado (express-fileupload + File.create() em duas etapas
//     sem transação entre elas) não tem nenhuma limpeza de arquivo órfão
//     documentada ou testada — ver Backend.Save/CleanupOrphans.
package files

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const createFilesTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_files (
	id serial PRIMARY KEY,
	filename text NOT NULL,
	mime_super text NOT NULL,
	mime_sub text NOT NULL,
	size_bytes bigint NOT NULL,
	min_role_read integer NOT NULL DEFAULT 100,
	user_id integer,
	storage_key text NOT NULL UNIQUE,
	created_at timestamptz NOT NULL DEFAULT now()
)`

// EnsureSchema cria o catálogo de arquivos, idempotente — chamar dentro de
// db.WithTenant, uma vez por tenant (mesmo padrão de internal/triggers,
// internal/scheduler).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, createFilesTableSQL)
	return err
}
