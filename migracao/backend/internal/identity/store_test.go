// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cada teste cria seu
// próprio schema de tenant isolado (nome derivado do nome do teste) e limpa
// ao final, para não interferir com os testes de internal/platform/database.
package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// testTenant cria um schema isolado por teste (evita que testes concorrentes
// do pacote colidam em _sc_users/_sc_api_tokens) e garante o schema de
// identidade dentro dele, via EnsureSchema. suffix distingue múltiplos
// tenants dentro do mesmo teste (ex.: testes de isolamento cross-tenant).
func testTenant(t *testing.T, db *database.DB, suffix string) tenancy.Tenant {
	t.Helper()
	tenant := tenancy.Tenant(fmt.Sprintf("identity_test_%s_%s", sanitizeForSchema(t.Name()), suffix))
	ctx := context.Background()

	// Cria o schema diretamente (fora de WithTenant, que só troca
	// search_path — o schema em si precisa existir antes).
	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgIdent(string(tenant))))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgIdent(string(tenant))))
			return err
		})
	})

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return tenant
}

func sanitizeForSchema(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

func pgIdent(name string) string {
	return `"` + name + `"`
}

func TestCreateUser_FindUserByEmail(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "main")
	ctx := context.Background()

	hash, _ := HashPassword("hunter2")
	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		userID, err = CreateUser(ctx, tx, "ada@example.com", hash, 80)
		return err
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var found *User
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		found, err = FindUserByEmail(ctx, tx, "ada@example.com")
		return err
	}); err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if found.ID != userID || found.Email != "ada@example.com" || found.RoleID != 80 {
		t.Errorf("found = %+v, esperado id=%d email=ada@example.com role=80", found, userID)
	}
}

// TestFindUserByID cobre o caminho que GO-017 usa para resolver o papel
// atual do ator a partir do `sub` (ID do usuário) de uma ServiceIdentity —
// nunca confiar num papel vindo de fora, sempre reconsultar (ADR-0007).
func TestFindUserByID(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "main")
	ctx := context.Background()

	hash, _ := HashPassword("hunter2")
	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		userID, err = CreateUser(ctx, tx, "ada@example.com", hash, 80)
		return err
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var found *User
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		found, err = FindUserByID(ctx, tx, userID)
		return err
	}); err != nil {
		t.Fatalf("FindUserByID: %v", err)
	}
	if found.ID != userID || found.Email != "ada@example.com" || found.RoleID != 80 {
		t.Errorf("found = %+v, esperado id=%d email=ada@example.com role=80", found, userID)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := FindUserByID(ctx, tx, userID+999)
		return err
	}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("FindUserByID(id inexistente) = %v, esperado ErrUserNotFound", err)
	}
}

// TestAuthenticate_PositiveAndNegative cobre a linha "APIs" da matriz de
// autorização no caminho de senha: credenciais corretas autenticam,
// qualquer uma errada (senha errada, e-mail inexistente) é recusada com o
// MESMO erro — evita enumeração de contas.
func TestAuthenticate_PositiveAndNegative(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "main")
	ctx := context.Background()

	hash, _ := HashPassword("hunter2")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateUser(ctx, tx, "ada@example.com", hash, 80)
		return err
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Positivo.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Authenticate(ctx, tx, "ada@example.com", "hunter2")
		return err
	}); err != nil {
		t.Errorf("Authenticate com credenciais corretas falhou: %v", err)
	}

	// Negativo: senha errada.
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Authenticate(ctx, tx, "ada@example.com", "senha-errada")
		return err
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate com senha errada = %v, esperado ErrInvalidCredentials", err)
	}

	// Negativo: usuário inexistente — mesmo erro que senha errada.
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Authenticate(ctx, tx, "nao-existe@example.com", "qualquer-coisa")
		return err
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate com usuário inexistente = %v, esperado ErrInvalidCredentials", err)
	}
}

// TestAPIToken_PositiveAndNegativeAndRevocation cobre "APIs" e "revogação"
// da matriz: token recém-criado autentica; token revogado passa a ser
// recusado; token nunca existente é recusado.
func TestAPIToken_PositiveAndNegativeAndRevocation(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "main")
	ctx := context.Background()

	var userID int
	var plaintext string
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, _ := HashPassword("hunter2")
		var err error
		userID, err = CreateUser(ctx, tx, "api-user@example.com", hash, 80)
		if err != nil {
			return err
		}
		plaintext, err = CreateAPITokenForUser(ctx, tx, userID)
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Positivo: token válido autentica o usuário certo.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		u, err := FindUserByAPIToken(ctx, tx, plaintext)
		if err != nil {
			return err
		}
		if u.ID != userID {
			t.Errorf("FindUserByAPIToken retornou usuário %d, esperado %d", u.ID, userID)
		}
		return nil
	}); err != nil {
		t.Fatalf("FindUserByAPIToken (positivo): %v", err)
	}

	// Negativo: token que nunca existiu.
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := FindUserByAPIToken(ctx, tx, "token-que-nunca-existiu")
		return err
	})
	if !errors.Is(err, ErrTokenNotFoundOrRevoked) {
		t.Errorf("FindUserByAPIToken com token inexistente = %v, esperado ErrTokenNotFoundOrRevoked", err)
	}

	// Revogação.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return RevokeAPIToken(ctx, tx, plaintext)
	}); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}

	// Negativo: token revogado deixa de funcionar.
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := FindUserByAPIToken(ctx, tx, plaintext)
		return err
	})
	if !errors.Is(err, ErrTokenNotFoundOrRevoked) {
		t.Errorf("FindUserByAPIToken após revogação = %v, esperado ErrTokenNotFoundOrRevoked", err)
	}

	// Revogar de novo é idempotente, não erro.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return RevokeAPIToken(ctx, tx, plaintext)
	}); err != nil {
		t.Errorf("revogar token já revogado retornou erro: %v", err)
	}
}

// TestAPIToken_CrossTenantIsolation é a linha "acesso cruzado" da matriz no
// nível de tenant: um token criado no tenant A não autentica ninguém no
// tenant B, mesmo que ambos os schemas tenham a tabela _sc_api_tokens —
// eles são bancos de dados logicamente distintos (GO-007).
func TestAPIToken_CrossTenantIsolation(t *testing.T) {
	db := testDB(t)
	tenantA := testTenant(t, db, "a")
	tenantB := testTenant(t, db, "b")

	ctx := context.Background()
	var plaintext string
	if err := db.WithTenant(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		hash, _ := HashPassword("hunter2")
		userID, err := CreateUser(ctx, tx, "user@tenant-a.example.com", hash, 80)
		if err != nil {
			return err
		}
		plaintext, err = CreateAPITokenForUser(ctx, tx, userID)
		return err
	}); err != nil {
		t.Fatalf("setup no tenant A: %v", err)
	}

	// O mesmo token, consultado no schema do tenant B, não encontra nada —
	// _sc_api_tokens do tenant B é uma tabela vazia e distinta.
	err := db.WithTenant(ctx, tenantB, func(ctx context.Context, tx pgx.Tx) error {
		_, err := FindUserByAPIToken(ctx, tx, plaintext)
		return err
	})
	if !errors.Is(err, ErrTokenNotFoundOrRevoked) {
		t.Errorf("token do tenant A encontrado no tenant B: erro = %v, esperado ErrTokenNotFoundOrRevoked", err)
	}
}

func TestEnableTOTP_ThenValidate(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "main")
	ctx := context.Background()

	var userID int
	var secret string
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, _ := HashPassword("hunter2")
		var err error
		userID, err = CreateUser(ctx, tx, "mfa-user@example.com", hash, 80)
		if err != nil {
			return err
		}
		secret, _, err = GenerateTOTPSecret("saltcorn-go", "mfa-user@example.com")
		if err != nil {
			return err
		}
		return EnableTOTP(ctx, tx, userID, secret)
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		u, err := FindUserByEmail(ctx, tx, "mfa-user@example.com")
		if err != nil {
			return err
		}
		if !u.TOTPEnabled {
			t.Error("TOTPEnabled = false após EnableTOTP")
		}
		if u.TOTPSecret != secret {
			t.Errorf("TOTPSecret = %q, esperado %q", u.TOTPSecret, secret)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
