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
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
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

// EnsureSchemaTx cria o catálogo de arquivos, idempotente — chamar dentro
// de db.WithTenant, uma vez por tenant (mesmo padrão de
// internal/triggers, internal/scheduler). Dialect-rewrite (GO-055) igual
// ao já usado por internal/platform/outbox desde GO-030.
func EnsureSchemaTx(ctx context.Context, tx database.Tx) error {
	ddl := createFilesTableSQL
	if tx.Dialect() == database.DialectSQLite {
		ddl = strings.NewReplacer(
			"serial PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT",
			"timestamptz", "timestamp",
			"now()", "CURRENT_TIMESTAMP",
		).Replace(ddl)
	}
	return tx.Exec(ctx, ddl)
}
