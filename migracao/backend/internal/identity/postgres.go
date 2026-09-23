// postgres.go (GO-041) — wrappers pgx.Tx finos sobre as funções
// dialeto-neutras (sufixo Tx) deste pacote, por compatibilidade com todos
// os chamadores HTTP existentes de cmd/server/cmd/cli (exclusivamente
// Postgres até esta tarefa) — mesmo padrão já estabelecido em
// internal/records/postgres.go. Nenhuma lógica nova aqui, só
// database.AsTx(tx) + delegação.
package identity

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func CreateUser(ctx context.Context, tx pgx.Tx, email, passwordHash string, roleID RoleID) (int, error) {
	return CreateUserTx(ctx, database.AsTx(tx), email, passwordHash, roleID)
}

func FindUserByEmail(ctx context.Context, tx pgx.Tx, email string) (*User, error) {
	return FindUserByEmailTx(ctx, database.AsTx(tx), email)
}

func FindUserByID(ctx context.Context, tx pgx.Tx, id int) (*User, error) {
	return FindUserByIDTx(ctx, database.AsTx(tx), id)
}

func Authenticate(ctx context.Context, tx pgx.Tx, email, password string) (*User, error) {
	return AuthenticateTx(ctx, database.AsTx(tx), email, password)
}

func SetUserLanguage(ctx context.Context, tx pgx.Tx, userID int, language string) error {
	return SetUserLanguageTx(ctx, database.AsTx(tx), userID, language)
}

func EnableTOTP(ctx context.Context, tx pgx.Tx, userID int, secret string) error {
	return EnableTOTPTx(ctx, database.AsTx(tx), userID, secret)
}

func CreateAPITokenForUser(ctx context.Context, tx pgx.Tx, userID int) (plaintext string, err error) {
	return CreateAPITokenForUserTx(ctx, database.AsTx(tx), userID)
}

func FindUserByAPIToken(ctx context.Context, tx pgx.Tx, plaintext string) (*User, error) {
	return FindUserByAPITokenTx(ctx, database.AsTx(tx), plaintext)
}

func RevokeAPIToken(ctx context.Context, tx pgx.Tx, plaintext string) error {
	return RevokeAPITokenTx(ctx, database.AsTx(tx), plaintext)
}

func ListUsers(ctx context.Context, tx pgx.Tx, actorRole RoleID) ([]User, error) {
	return ListUsersTx(ctx, database.AsTx(tx), actorRole)
}

func UpdateUserRole(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int, newRole RoleID) error {
	return UpdateUserRoleTx(ctx, database.AsTx(tx), actorRole, userID, newRole)
}

func SetPassword(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int, newPasswordHash string) error {
	return SetPasswordTx(ctx, database.AsTx(tx), actorRole, userID, newPasswordHash)
}

func DeleteUser(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int) error {
	return DeleteUserTx(ctx, database.AsTx(tx), actorRole, userID)
}

func ListAPITokensForUser(ctx context.Context, tx pgx.Tx, actorRole RoleID, userID int) ([]APIToken, error) {
	return ListAPITokensForUserTx(ctx, database.AsTx(tx), actorRole, userID)
}

func StartImpersonation(ctx context.Context, tx pgx.Tx, actorRole RoleID, adminUserID, targetUserID int) (logID int, err error) {
	return StartImpersonationTx(ctx, database.AsTx(tx), actorRole, adminUserID, targetUserID)
}

func EndImpersonation(ctx context.Context, tx pgx.Tx, logID int) error {
	return EndImpersonationTx(ctx, database.AsTx(tx), logID)
}

func GetImpersonation(ctx context.Context, tx pgx.Tx, logID int) (*ImpersonationRecord, error) {
	return GetImpersonationTx(ctx, database.AsTx(tx), logID)
}
