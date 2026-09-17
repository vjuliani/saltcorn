// Fixture de Postgres real para TestEval_DBReadCallback_RealAuthorization
// — mesmo padrão de internal/views/commands_test.go: schema de tenant
// isolado e descartável por teste, catálogo real (internal/metadata),
// registro real (internal/records).
package pluginhost

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
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

// testFixture cria um tenant isolado com uma tabela "books" — leitura
// pública por padrão (metadata.TableOptions{} zero-value, ver
// internal/metadata) — o suficiente para o callback de leitura ter algo
// real para consultar.
func testFixture(t *testing.T, db *database.DB) (tenancy.Tenant, int) {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("pluginhost_test_%s", sanitizeForSchema(t.Name())))

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

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		books, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = books.ID
		_, err = metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}
	return tenant, tableID
}

func seedBook(t *testing.T, db *database.DB, tenant tenancy.Tenant, tableID int, title string) {
	t.Helper()
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": title}, nil)
		return err
	}); err != nil {
		t.Fatalf("semear registro: %v", err)
	}
	_ = tableID
}
