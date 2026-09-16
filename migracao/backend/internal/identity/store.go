package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// User é o registro de identidade persistido — o shape mínimo que esta
// tarefa precisa, não o modelo completo de usuário da produção Node.
type User struct {
	ID           int
	Email        string
	PasswordHash string
	RoleID       RoleID
	TOTPSecret   string
	TOTPEnabled  bool
}

var (
	ErrUserNotFound           = errors.New("identity: usuário não encontrado")
	ErrInvalidCredentials     = errors.New("identity: credenciais inválidas")
	ErrTokenNotFoundOrRevoked = errors.New("identity: token de API não encontrado ou revogado")
)

// CreateUser insere um novo usuário. passwordHash já deve vir de
// HashPassword — este pacote nunca grava senha em texto plano.
func CreateUser(ctx context.Context, tx pgx.Tx, email, passwordHash string, roleID RoleID) (int, error) {
	var id int
	err := tx.QueryRow(ctx,
		"INSERT INTO _sc_users (email, password_hash, role_id) VALUES ($1, $2, $3) RETURNING id",
		email, passwordHash, int(roleID),
	).Scan(&id)
	return id, err
}

// FindUserByEmail busca um usuário pelo e-mail. Retorna ErrUserNotFound
// (não o erro cru do driver) quando não existe, para que quem chama não
// precise conhecer o tipo de erro do pgx.
func FindUserByEmail(ctx context.Context, tx pgx.Tx, email string) (*User, error) {
	u := &User{}
	var roleID int
	var totpSecret *string
	err := tx.QueryRow(ctx,
		"SELECT id, email, password_hash, role_id, totp_secret, totp_enabled FROM _sc_users WHERE email = $1",
		email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &roleID, &totpSecret, &u.TOTPEnabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	u.RoleID = RoleID(roleID)
	if totpSecret != nil {
		u.TOTPSecret = *totpSecret
	}
	return u, nil
}

// Authenticate combina busca por e-mail e verificação de senha, sem
// distinguir "usuário não existe" de "senha errada" no erro retornado —
// distinguir os dois no lado do cliente é um vetor de enumeração de contas.
func Authenticate(ctx context.Context, tx pgx.Tx, email, password string) (*User, error) {
	u, err := FindUserByEmail(ctx, tx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// EnableTOTP grava o segredo TOTP de um usuário e marca MFA como ativado.
func EnableTOTP(ctx context.Context, tx pgx.Tx, userID int, secret string) error {
	_, err := tx.Exec(ctx,
		"UPDATE _sc_users SET totp_secret = $1, totp_enabled = true WHERE id = $2",
		secret, userID,
	)
	return err
}

// CreateAPITokenForUser gera um novo token de API para o usuário e persiste
// só o hash (ver token.go). Retorna o texto plano — a única vez que ele
// existe fora da memória de quem chamou GenerateAPIToken.
func CreateAPITokenForUser(ctx context.Context, tx pgx.Tx, userID int) (plaintext string, err error) {
	plaintext, hash, err := GenerateAPIToken()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO _sc_api_tokens (user_id, token_hash) VALUES ($1, $2)",
		userID, hash,
	); err != nil {
		return "", err
	}
	return plaintext, nil
}

// FindUserByAPIToken busca o usuário dono de um token de API em texto
// plano, rejeitando tokens revogados — o caminho de autenticação da API
// pública (matriz GO-001 §2.1, estratégia AuthStrategyAPIToken).
func FindUserByAPIToken(ctx context.Context, tx pgx.Tx, plaintext string) (*User, error) {
	hash := HashAPIToken(plaintext)
	u := &User{}
	var roleID int
	err := tx.QueryRow(ctx, `
		SELECT u.id, u.email, u.password_hash, u.role_id
		FROM _sc_users u
		JOIN _sc_api_tokens t ON t.user_id = u.id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL
	`, hash).Scan(&u.ID, &u.Email, &u.PasswordHash, &roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenNotFoundOrRevoked
		}
		return nil, err
	}
	u.RoleID = RoleID(roleID)
	return u, nil
}

// RevokeAPIToken marca um token como revogado — chamadas seguintes a
// FindUserByAPIToken com o mesmo texto plano passam a falhar com
// ErrTokenNotFoundOrRevoked. Idempotente: revogar um token já revogado não
// é erro.
func RevokeAPIToken(ctx context.Context, tx pgx.Tx, plaintext string) error {
	hash := HashAPIToken(plaintext)
	_, err := tx.Exec(ctx,
		"UPDATE _sc_api_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL",
		hash,
	)
	return err
}
