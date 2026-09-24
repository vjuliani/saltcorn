// Auditoria de impersonação de usuário (GO-044) — equivalente ao
// "become-user" de `auth/admin.ts`, mas com uma trilha de auditoria que o
// legado NÃO TEM (achado de preflight: `become-user` no legado só troca
// `req.user` na sessão, sem nenhum registro de quem virou quem nem quando).
// Divergência deliberada e MAIS FORTE: aqui, toda impersonação começa e
// termina com uma linha persistida em `_sc_impersonation_log`, na mesma
// transação que a checagem de autorização — nunca é possível impersonar sem
// deixar rastro.
//
// Este pacote NUNCA cria a sessão de navegador do usuário impersonado —
// isso é responsabilidade do BFF (ADR-0007, sessão nunca é do Go). O que
// este arquivo garante é: (1) só um ator RoleAdmin pode iniciar uma
// impersonação; (2) o usuário-alvo precisa existir neste tenant; (3) toda
// impersonação tem início e fim registrados.
package identity

import (
	"context"
	"errors"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// ErrCannotImpersonateSelf é devolvido quando o admin tenta impersonar a
// própria conta — não tem utilidade real e só confundiria a auditoria.
var ErrCannotImpersonateSelf = errors.New("identity: não é possível impersonar o próprio usuário")

// ErrImpersonationNotFound é devolvido quando um logID não corresponde a
// nenhum registro de auditoria de impersonação.
var ErrImpersonationNotFound = errors.New("identity: registro de impersonação não encontrado")

// StartImpersonationTx registra o início de uma impersonação — o admin
// (adminUserID, resolvido da identidade delegada, nunca de um campo de
// formulário) assume a identidade de targetUserID. Falha se o ator não for
// RoleAdmin, se o alvo não existir neste tenant, ou se admin e alvo forem o
// mesmo usuário.
func StartImpersonationTx(ctx context.Context, tx database.Tx, actorRole RoleID, adminUserID, targetUserID int) (logID int, err error) {
	if err := requireAdmin(actorRole); err != nil {
		return 0, err
	}
	if adminUserID == targetUserID {
		return 0, ErrCannotImpersonateSelf
	}
	if _, err := FindUserByIDTx(ctx, tx, targetUserID); err != nil {
		return 0, err
	}
	err = tx.QueryRow(ctx,
		"INSERT INTO _sc_impersonation_log (admin_user_id, target_user_id) VALUES ($1, $2) RETURNING id",
		adminUserID, targetUserID,
	).Scan(&logID)
	return logID, err
}

// EndImpersonationTx marca o fim de uma impersonação — idempotente:
// encerrar um registro já encerrado (ou inexistente) nunca é erro, mesma
// convenção de DropField/RevokeAPITokenTx. Não exige papel de ator: quem
// chama isto é o BFF encerrando sua PRÓPRIA sessão de impersonação (ver
// docs/migracao-go/execucoes/GO-044.md), não um endpoint diretamente
// alcançável por um usuário final escolhendo um logID de outra pessoa — o
// BFF nunca aceita um logID que não seja o que ele mesmo guardou na sessão.
// CURRENT_TIMESTAMP (não `now()`) — entendido pelos dois dialetos.
func EndImpersonationTx(ctx context.Context, tx database.Tx, logID int) error {
	return tx.Exec(ctx, "UPDATE _sc_impersonation_log SET ended_at = CURRENT_TIMESTAMP WHERE id = $1 AND ended_at IS NULL", logID)
}

// ImpersonationRecord é a projeção de uma linha de auditoria — usado por
// testes e por uma futura tela administrativa de "quem impersonou quem".
type ImpersonationRecord struct {
	ID           int
	AdminUserID  int
	TargetUserID int
	StartedAt    string
	EndedAt      string
	StillActive  bool
}

// GetImpersonationTx lê um registro de auditoria por ID — usado pelo BFF
// para validar, antes de encerrar, que o logID guardado na sessão
// corresponde mesmo a uma impersonação ainda ativa (defesa contra um
// logID reaproveitado por engano). `CAST(... AS TEXT)` (não `::text`) —
// sintaxe ANSI entendida pelos dois dialetos, ao contrário do cast
// abreviado do Postgres.
func GetImpersonationTx(ctx context.Context, tx database.Tx, logID int) (*ImpersonationRecord, error) {
	r := &ImpersonationRecord{ID: logID}
	var endedAt *string
	err := tx.QueryRow(ctx,
		"SELECT admin_user_id, target_user_id, CAST(started_at AS TEXT), CAST(ended_at AS TEXT) FROM _sc_impersonation_log WHERE id = $1",
		logID,
	).Scan(&r.AdminUserID, &r.TargetUserID, &r.StartedAt, &endedAt)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, ErrImpersonationNotFound
		}
		return nil, err
	}
	if endedAt != nil {
		r.EndedAt = *endedAt
	} else {
		r.StillActive = true
	}
	return r, nil
}
