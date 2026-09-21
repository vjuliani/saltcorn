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

	"github.com/jackc/pgx/v5"
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

// ListUsers lista todos os usuários do tenant, em ordem de criação —
// equivalente Go de `auth/admin.ts` (listagem de usuários), sem nunca expor
// PasswordHash/TOTPSecret além do que já está em User (que não deveria ser
// serializado como está para uma resposta HTTP — quem chama isto por HTTP é
// responsável por projetar só os campos seguros).
func ListUsers(ctx context.Context, tx pgx.Tx, actorRole RoleID) ([]User, error) {
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

// UpdateUserRole muda o papel de um usuário — equivalente ao formulário de
// edição de usuário do legado (mudar role_id). Idempotente: definir o
// mesmo papel que o usuário já tem não é erro.
func UpdateUserRole(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int, newRole RoleID) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE _sc_users SET role_id = $1 WHERE id = $2", int(newRole), userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetPassword redefine a senha de um usuário a partir de um hash já
// calculado (chamador usa HashPassword) — o equivalente Go de
// "reset-password"/"set-random-password" de `auth/admin.ts`. Nunca aceita
// senha em texto plano diretamente, mesma disciplina de CreateUser.
func SetPassword(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int, newPasswordHash string) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE _sc_users SET password_hash = $1 WHERE id = $2", newPasswordHash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// GenerateRandomPassword produz uma senha temporária aleatória (para
// "set-random-password" — nunca gerada de forma previsível), em texto
// plano: quem chama ainda precisa passá-la por HashPassword antes de
// SetPassword, e é responsável por entregá-la ao usuário por um canal
// seguro (nunca logada).
func GenerateRandomPassword() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: gerar senha aleatória: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// DeleteUser remove um usuário (e seus tokens de API, via ON DELETE CASCADE
// do schema) — idempotente: remover um usuário que não existe é um no-op
// bem-sucedido, mesma convenção de DropField/DropTable/RevokeAPIToken.
func DeleteUser(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "DELETE FROM _sc_users WHERE id = $1", userID)
	return err
}

// APIToken é a projeção segura de um token de API para fins administrativos
// — nunca inclui o hash nem (obviamente) o texto plano, que só existe uma
// vez, no momento de GenerateAPIToken.
type APIToken struct {
	ID        int
	CreatedAt string
	Revoked   bool
}

// ListAPITokensForUser lista os tokens de API de um usuário (revogados
// inclusos, com o status visível) — a metade "admin" que faltava ao lado de
// CreateAPITokenForUser/RevokeAPIToken (token.go), sem nunca reexibir
// hash/texto plano.
func ListAPITokensForUser(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int) ([]APIToken, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		"SELECT id, created_at::text, revoked_at IS NOT NULL FROM _sc_api_tokens WHERE user_id = $1 ORDER BY id",
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
