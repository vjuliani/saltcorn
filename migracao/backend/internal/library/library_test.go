package library

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

func libraryFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("library_test_%s", sanitizeForSchema(t.Name())))

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

func TestCreateOrReplace_And_ListAll(t *testing.T) {
	db, tenant := libraryFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateOrReplace(ctx, tx, "cabecalho-padrao", "star", map[string]any{"type": "besides", "besides": []any{}}); err != nil {
			return err
		}
		_, err := CreateOrReplace(ctx, tx, "rodape-padrao", "", map[string]any{"type": "container"})
		return err
	}); err != nil {
		t.Fatalf("CreateOrReplace: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		items, err := ListAll(ctx, tx)
		if err != nil {
			return err
		}
		if len(items) != 2 {
			t.Fatalf("ListAll = %d itens, esperado 2", len(items))
		}
		if items[0].Name != "cabecalho-padrao" || items[0].Icon != "star" {
			t.Errorf("items[0] = %+v, não bateu com o esperado", items[0])
		}
		if items[1].Icon != "" {
			t.Errorf("items[1].Icon = %q, esperado vazio", items[1].Icon)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestCreateOrReplace_SameName_UpdatesInPlace prova idempotência por nome
// — reinstalar um pack não deveria duplicar itens de biblioteca.
func TestCreateOrReplace_SameName_UpdatesInPlace(t *testing.T) {
	db, tenant := libraryFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateOrReplace(ctx, tx, "componente", "old-icon", map[string]any{"v": float64(1)}); err != nil {
			return err
		}
		_, err := CreateOrReplace(ctx, tx, "componente", "new-icon", map[string]any{"v": float64(2)})
		return err
	}); err != nil {
		t.Fatalf("CreateOrReplace (x2): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		items, err := ListAll(ctx, tx)
		if err != nil {
			return err
		}
		if len(items) != 1 {
			t.Fatalf("ListAll = %d itens, esperado 1 (segunda chamada deveria substituir, não duplicar)", len(items))
		}
		if items[0].Icon != "new-icon" {
			t.Errorf("Icon = %q, esperado \"new-icon\" (substituído)", items[0].Icon)
		}
		if items[0].Layout["v"] != float64(2) {
			t.Errorf("Layout[v] = %v, esperado 2", items[0].Layout["v"])
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
