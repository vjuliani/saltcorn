package config

import (
	"context"
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

func configFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("config_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
	})

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return db, tenant
}

func TestSetGetDelete(t *testing.T) {
	db, tenant := configFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Set(ctx, tx, "site_name", "Minha Aplicação")
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, ok, err := Get(ctx, tx, "site_name")
		if err != nil {
			return err
		}
		if !ok || value != "Minha Aplicação" {
			t.Errorf("Get(site_name) = (%v, %v), esperado (\"Minha Aplicação\", true)", value, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Delete(ctx, tx, "site_name")
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, ok, err := Get(ctx, tx, "site_name")
		if err != nil {
			return err
		}
		if ok {
			t.Error("Get(site_name) após Delete ainda encontrou a chave")
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestGet_MissingKey_ReturnsNotOkNeverError(t *testing.T) {
	db, tenant := configFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, ok, err := Get(ctx, tx, "nao-existe")
		if err != nil {
			t.Fatalf("Get de chave ausente retornou erro: %v, esperado nil", err)
		}
		if ok {
			t.Error("ok = true para chave ausente, esperado false")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestSet_OverwritesExistingValue(t *testing.T) {
	db, tenant := configFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := Set(ctx, tx, "timezone", "UTC"); err != nil {
			return err
		}
		return Set(ctx, tx, "timezone", "America/Sao_Paulo")
	}); err != nil {
		t.Fatalf("Set (x2): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, ok, err := Get(ctx, tx, "timezone")
		if err != nil {
			return err
		}
		if !ok || value != "America/Sao_Paulo" {
			t.Errorf("Get(timezone) = (%v, %v), esperado (\"America/Sao_Paulo\", true)", value, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestListAll(t *testing.T) {
	db, tenant := configFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := Set(ctx, tx, "site_name", "App"); err != nil {
			return err
		}
		if err := Set(ctx, tx, "menu_items", []any{"Home", "About"}); err != nil {
			return err
		}
		return Set(ctx, tx, "enable_feature_x", true)
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		all, err := ListAll(ctx, tx)
		if err != nil {
			return err
		}
		if len(all) != 3 {
			t.Fatalf("ListAll = %d entradas, esperado 3", len(all))
		}
		if all["site_name"] != "App" {
			t.Errorf("all[site_name] = %v, esperado \"App\"", all["site_name"])
		}
		if all["enable_feature_x"] != true {
			t.Errorf("all[enable_feature_x] = %v, esperado true", all["enable_feature_x"])
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
