// Corpus de GO-044: administração de usuários (ListUsers/UpdateUserRole/
// SetPassword/DeleteUser/ListAPITokensForUser) — todas admin-only.
package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func mustCreateUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string, role RoleID) int {
	t.Helper()
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	id, err := CreateUser(ctx, tx, email, hash, role)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", email, err)
	}
	return id
}

func TestListUsers_RequiresAdmin(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := ListUsers(ctx, tx, RolePublic)
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, esperado ErrNotAuthorized", err)
	}
}

func TestListUsers_ReturnsAllInCreationOrder(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var id1, id2 int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		id1 = mustCreateUser(t, ctx, tx, "alice@example.com", RoleID(80))
		id2 = mustCreateUser(t, ctx, tx, "bob@example.com", RoleID(80))
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		users, err := ListUsers(ctx, tx, RoleAdmin)
		if err != nil {
			return err
		}
		if len(users) != 2 || users[0].ID != id1 || users[1].ID != id2 {
			t.Fatalf("ListUsers = %+v, esperado [id=%d, id=%d]", users, id1, id2)
		}
		if users[0].PasswordHash == "" {
			t.Error("PasswordHash vazio — CreateUser/ListUsers deveriam preservar o hash")
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestUpdateUserRole_ChangesRoleAndIsIdempotent(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		userID = mustCreateUser(t, ctx, tx, "carol@example.com", RoleID(80))
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return UpdateUserRole(ctx, tx, RoleAdmin, userID, RoleAdmin)
		}); err != nil {
			t.Fatalf("UpdateUserRole (chamada %d): %v", i+1, err)
		}
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		u, err := FindUserByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		if u.RoleID != RoleAdmin {
			t.Errorf("RoleID = %v, esperado RoleAdmin", u.RoleID)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestUpdateUserRole_UnknownUser_ReturnsErrUserNotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return UpdateUserRole(ctx, tx, RoleAdmin, 999999, RoleAdmin)
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, esperado ErrUserNotFound", err)
	}
}

func TestSetPassword_ChangesHash_OldPasswordStopsWorking(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		mustCreateUser(t, ctx, tx, "dave@example.com", RoleID(80))
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	newHash, err := HashPassword("nova-senha-forte")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		u, err := FindUserByEmail(ctx, tx, "dave@example.com")
		if err != nil {
			return err
		}
		return SetPassword(ctx, tx, RoleAdmin, u.ID, newHash)
	}); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := Authenticate(ctx, tx, "dave@example.com", "hunter2"); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Authenticate com senha ANTIGA = %v, esperado ErrInvalidCredentials", err)
		}
		if _, err := Authenticate(ctx, tx, "dave@example.com", "nova-senha-forte"); err != nil {
			t.Errorf("Authenticate com senha NOVA falhou: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestGenerateRandomPassword_ProducesDifferentValues(t *testing.T) {
	a, err := GenerateRandomPassword()
	if err != nil {
		t.Fatalf("GenerateRandomPassword: %v", err)
	}
	b, err := GenerateRandomPassword()
	if err != nil {
		t.Fatalf("GenerateRandomPassword: %v", err)
	}
	if a == b {
		t.Fatalf("duas chamadas produziram a MESMA senha: %q", a)
	}
	if len(a) == 0 {
		t.Fatal("senha gerada vazia")
	}
}

func TestDeleteUser_IdempotentAndCascadesTokens(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		userID = mustCreateUser(t, ctx, tx, "erin@example.com", RoleID(80))
		_, err := CreateAPITokenForUser(ctx, tx, userID)
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return DeleteUser(ctx, tx, RoleAdmin, userID)
		}); err != nil {
			t.Fatalf("DeleteUser (chamada %d): %v", i+1, err)
		}
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := FindUserByID(ctx, tx, userID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("FindUserByID após DeleteUser = %v, esperado ErrUserNotFound", err)
		}
		var tokenCount int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM _sc_api_tokens WHERE user_id = $1", userID).Scan(&tokenCount); err != nil {
			return err
		}
		if tokenCount != 0 {
			t.Errorf("_sc_api_tokens ainda tem %d linha(s) do usuário deletado — ON DELETE CASCADE não funcionou", tokenCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestListAPITokensForUser_ShowsStatusNeverHash(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var userID int
	var plaintext string
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		userID = mustCreateUser(t, ctx, tx, "frank@example.com", RoleID(80))
		var err error
		plaintext, err = CreateAPITokenForUser(ctx, tx, userID)
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tokens, err := ListAPITokensForUser(ctx, tx, RoleAdmin, userID)
		if err != nil {
			return err
		}
		if len(tokens) != 1 {
			t.Fatalf("ListAPITokensForUser = %+v, esperado 1 token", tokens)
		}
		if tokens[0].Revoked {
			t.Error("token recém-criado marcado como revogado")
		}
		return RevokeAPIToken(ctx, tx, plaintext)
	}); err != nil {
		t.Fatalf("verificação/revoke: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tokens, err := ListAPITokensForUser(ctx, tx, RoleAdmin, userID)
		if err != nil {
			return err
		}
		if len(tokens) != 1 || !tokens[0].Revoked {
			t.Fatalf("após revoke, ListAPITokensForUser = %+v, esperado 1 token com Revoked=true", tokens)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-revoke: %v", err)
	}
}
