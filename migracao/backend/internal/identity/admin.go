// Administração de usuários (GO-044) — a superfície de
// `packages/server/auth/admin.ts` do legado que faltava neste pacote:
// listar/editar papel/deletar usuário, redefinir senha administrativamente,
// e listar (sem nunca reexibir) os tokens de API de um usuário. Todas as
// funções são admin-only (`requireAdmin`, mesma convenção de
// `internal/metadata`/`internal/views`) — nenhuma delas é alcançável por um
// ator que não seja `RoleAdmin`.
//
// Deliberadamente FORA de escopo desta tarefa (ver
// docs/migracao-go/execucoes/GO-044.md para a decisão completa):
// impersonação de usuário e força de logout vivem no BFF (a sessão de
// navegador nunca é responsabilidade do Go, ADR-0007) — este pacote só
// registra a AUDITORIA da impersonação (impersonation.go) e confirma que o
// usuário-alvo existe/está autorizado; emissão de certificado TLS/Let's
// Encrypt fica na borda/infraestrutura (ADR-0008, mesmo raciocínio já usado
// para o proxy reverso de corte); CRUD de papel dinâmico (roles
// customizadas) fica fora — `RoleID` continua um conjunto fechado por
// desenho desta migração.
package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// ErrNotAuthorized é devolvido quando o ator não tem papel suficiente
// (RoleAdmin) para uma operação administrativa deste arquivo.
var ErrNotAuthorized = errors.New("identity: ator não tem papel suficiente para esta operação")

func requireAdmin(actorRole RoleID) error {
	if !CanWrite(actorRole, RoleAdmin) {
		return ErrNotAuthorized
	}
	return nil
}

// ListUsersTx lista todos os usuários do tenant, em ordem de criação —
// equivalente Go de `auth/admin.ts` (listagem de usuários), sem nunca expor
// PasswordHash/TOTPSecret além do que já está em User (que não deveria ser
// serializado como está para uma resposta HTTP — quem chama isto por HTTP é
// responsável por projetar só os campos seguros).
func ListUsersTx(ctx context.Context, tx database.Tx, actorRole RoleID) ([]User, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		"SELECT id, email, password_hash, role_id, COALESCE(totp_secret, ''), totp_enabled FROM _sc_users ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var roleID int
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &roleID, &u.TOTPSecret, &u.TOTPEnabled); err != nil {
			return nil, err
		}
		u.RoleID = RoleID(roleID)
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUserRoleTx muda o papel de um usuário — equivalente ao formulário de
// edição de usuário do legado (mudar role_id). Idempotente: definir o
// mesmo papel que o usuário já tem não é erro. `UPDATE ... RETURNING id`
// (não `RowsAffected`, que `database.Tx.Exec` não expõe — mesmo padrão de
// detecção de "zero linhas afetadas" já usado por
// internal/records.UpdateRecordTx) detecta usuário inexistente.
func UpdateUserRoleTx(ctx context.Context, tx database.Tx, actorRole RoleID, userID int, newRole RoleID) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	var id int
	err := tx.QueryRow(ctx, "UPDATE _sc_users SET role_id = $1 WHERE id = $2 RETURNING id", int(newRole), userID).Scan(&id)
	if errors.Is(err, database.ErrNoRows) {
		return ErrUserNotFound
	}
	return err
}

// SetPasswordTx redefine a senha de um usuário a partir de um hash já
// calculado (chamador usa HashPassword) — o equivalente Go de
// "reset-password"/"set-random-password" de `auth/admin.ts`. Nunca aceita
// senha em texto plano diretamente, mesma disciplina de CreateUserTx.
func SetPasswordTx(ctx context.Context, tx database.Tx, actorRole RoleID, userID int, newPasswordHash string) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	var id int
	err := tx.QueryRow(ctx, "UPDATE _sc_users SET password_hash = $1 WHERE id = $2 RETURNING id", newPasswordHash, userID).Scan(&id)
	if errors.Is(err, database.ErrNoRows) {
		return ErrUserNotFound
	}
	return err
}

// GenerateRandomPassword produz uma senha temporária aleatória (para
// "set-random-password" — nunca gerada de forma previsível), em texto
// plano: quem chama ainda precisa passá-la por HashPassword antes de
// SetPasswordTx, e é responsável por entregá-la ao usuário por um canal
// seguro (nunca logada).
func GenerateRandomPassword() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: gerar senha aleatória: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// DeleteUserTx remove um usuário (e seus tokens de API, via ON DELETE
// CASCADE do schema) — idempotente: remover um usuário que não existe é um
// no-op bem-sucedido, mesma convenção de DropField/RevokeAPITokenTx.
func DeleteUserTx(ctx context.Context, tx database.Tx, actorRole RoleID, userID int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	return tx.Exec(ctx, "DELETE FROM _sc_users WHERE id = $1", userID)
}

// APIToken é a projeção segura de um token de API para fins administrativos
// — nunca inclui o hash nem (obviamente) o texto plano, que só existe uma
// vez, no momento de GenerateAPIToken.
type APIToken struct {
	ID        int
	CreatedAt string
	Revoked   bool
}

// ListAPITokensForUserTx lista os tokens de API de um usuário (revogados
// inclusos, com o status visível) — a metade "admin" que faltava ao lado de
// CreateAPITokenForUserTx/RevokeAPITokenTx (token.go), sem nunca reexibir
// hash/texto plano. `CAST(... AS TEXT)` (não `::text`) — sintaxe ANSI
// entendida pelos dois dialetos.
func ListAPITokensForUserTx(ctx context.Context, tx database.Tx, actorRole RoleID, userID int) ([]APIToken, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		"SELECT id, CAST(created_at AS TEXT), revoked_at IS NOT NULL FROM _sc_api_tokens WHERE user_id = $1 ORDER BY id",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.CreatedAt, &t.Revoked); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
