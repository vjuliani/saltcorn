// Testa e2e-seed diretamente (chamando a função Go, não o binário) contra
// Postgres real — mesmo t.Skip de condição de todos os demais pacotes
// internal/* quando SALTCORN_GO_TEST_DATABASE_URL não está definida.
package main

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	return dsn
}

func TestE2ESeed_CreatesTenantAdminAndOwnership(t *testing.T) {
	dsn := testDSN(t)
	tenant := tenancy.Tenant("cli_e2e_seed_test")

	if err := e2eSeed([]string{"--dsn", dsn, "--tenant", string(tenant), "--email", "seed-test@example.com"}); err != nil {
		t.Fatalf("e2eSeed() erro inesperado: %v", err)
	}
	t.Cleanup(func() {
		db, err := database.Open(context.Background(), dsn)
		if err != nil {
			return
		}
		defer db.Close()
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{string(tenant)}.Sanitize()+" CASCADE")
			return err
		})
	})

	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	var user *identity.User
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		user, err = identity.FindUserByEmail(ctx, tx, "seed-test@example.com")
		return err
	}); err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if user.RoleID != identity.RoleAdmin {
		t.Errorf("user.RoleID = %v, esperado RoleAdmin", user.RoleID)
	}

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		for _, capability := range []string{"tables.records", "tables.schema", "tables.views"} {
			owner, err := cutover.OwnerOf(ctx, tx, tenant, capability)
			if err != nil {
				return err
			}
			if owner != cutover.OwnerGo {
				t.Errorf("owner de %s = %v, esperado OwnerGo", capability, owner)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("verificar ownership: %v", err)
	}
}

func TestE2ESeed_RerunResetsSchema(t *testing.T) {
	dsn := testDSN(t)
	tenant := tenancy.Tenant("cli_e2e_seed_rerun_test")

	if err := e2eSeed([]string{"--dsn", dsn, "--tenant", string(tenant), "--email", "first@example.com"}); err != nil {
		t.Fatalf("primeira execução: %v", err)
	}
	t.Cleanup(func() {
		db, err := database.Open(context.Background(), dsn)
		if err != nil {
			return
		}
		defer db.Close()
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{string(tenant)}.Sanitize()+" CASCADE")
			return err
		})
	})

	// Segunda execução com um e-mail diferente — se o schema não fosse
	// recriado do zero, o usuário da primeira execução ainda existiria
	// (não é o bug que este teste previne, mas confirma que a segunda
	// chamada não falha por "já existe" nem deixa resíduo).
	if err := e2eSeed([]string{"--dsn", dsn, "--tenant", string(tenant), "--email", "second@example.com"}); err != nil {
		t.Fatalf("segunda execução (re-seed): %v", err)
	}

	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := identity.FindUserByEmail(ctx, tx, "first@example.com"); err == nil {
			t.Error("usuário da primeira execução ainda existe após re-seed — schema não foi recriado do zero")
		}
		_, err := identity.FindUserByEmail(ctx, tx, "second@example.com")
		return err
	}); err != nil {
		t.Fatalf("verificar usuário da segunda execução: %v", err)
	}
}
