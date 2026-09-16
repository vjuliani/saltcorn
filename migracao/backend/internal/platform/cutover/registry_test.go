// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida.
package cutover

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

// testCapability isola cada teste na tabela _sc_capability_ownership
// compartilhada (schema public, sem isolamento por schema de tenant — ver
// schema.go) usando um tenant derivado do nome do teste, e apaga a linha
// correspondente ao final.
func testCapability(t *testing.T, db *database.DB) (tenancy.Tenant, string) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
		return EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	tenant := tenancy.Tenant(fmt.Sprintf("cutover_test_%s", sanitizeForTenant(t.Name())))
	const capability = "test.capability"
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), publicSchema, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2",
				string(tenant), capability)
			return err
		})
	})
	return tenant, capability
}

func sanitizeForTenant(name string) string {
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

func TestOwnerOf_DefaultsToLegacy(t *testing.T) {
	db := testDB(t)
	tenant, capability := testCapability(t, db)
	ctx := context.Background()

	var owner Owner
	if err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		owner, err = OwnerOf(ctx, tx, tenant, capability)
		return err
	}); err != nil {
		t.Fatalf("OwnerOf: %v", err)
	}
	if owner != OwnerLegacy {
		t.Errorf("OwnerOf sem registro = %q, esperado %q", owner, OwnerLegacy)
	}
}

func TestSetOwner_ThenOwnerOf(t *testing.T) {
	db := testDB(t)
	tenant, capability := testCapability(t, db)
	ctx := context.Background()

	set := func(owner Owner) {
		t.Helper()
		if err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
			return SetOwner(ctx, tx, tenant, capability, owner)
		}); err != nil {
			t.Fatalf("SetOwner(%q): %v", owner, err)
		}
	}
	read := func() Owner {
		t.Helper()
		var owner Owner
		if err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			owner, err = OwnerOf(ctx, tx, tenant, capability)
			return err
		}); err != nil {
			t.Fatalf("OwnerOf: %v", err)
		}
		return owner
	}

	set(OwnerGo)
	if got := read(); got != OwnerGo {
		t.Errorf("OwnerOf após SetOwner(go) = %q, esperado go", got)
	}

	// Upsert: trocar de novo (rollback) sobrescreve a mesma linha, não
	// duplica — PRIMARY KEY (tenant, capability) já garantiria isso no
	// banco, mas o teste confirma o comportamento observável.
	set(OwnerLegacy)
	if got := read(); got != OwnerLegacy {
		t.Errorf("OwnerOf após SetOwner(legacy) = %q, esperado legacy", got)
	}
}
