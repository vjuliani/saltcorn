package files

import (
	"strconv"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

// CanRead reaproveita identity.CanRead/identity.IsOwnerByField (GO-008) —
// nenhuma checagem de autorização nova inventada, mesmo compromisso já
// registrado em docs/migracao-go/identidade/GO-008-matriz-autorizacao.md.
// Um ator pode ler um arquivo se seu papel atende MinRoleRead, OU se é o
// dono do arquivo — mesma regra do legado (packages/server/routes/
// files.ts: `role <= file.min_role_read || user_id === file.user_id`).
func CanRead(actorRole identity.RoleID, actorUserID *int, f File) bool {
	if identity.CanRead(actorRole, f.MinRoleRead) {
		return true
	}
	if actorUserID == nil || f.UserID == nil {
		return false
	}
	return identity.IsOwnerByField(strconv.Itoa(*actorUserID), strconv.Itoa(*f.UserID))
}
