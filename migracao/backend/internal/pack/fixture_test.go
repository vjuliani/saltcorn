// Fixture de Postgres real — mesmo padrão de internal/triggers/
// fixture_test.go, internal/scheduler/fixture_test.go. Não precisa do
// host real de GO-022 (internal/expression): os triggers/views de teste
// deste pacote nunca usam OnlyIf/fórmula JS — Import/Export são
// ortogonais à avaliação de expressão, que já é testada em
// internal/triggers/internal/scheduler.
package pack

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/library"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/tags"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
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

// newTenant cria um schema de tenant isolado e descartável com todo o
// schema de framework necessário (metadata/views/triggers/scheduler/
// library/config) já aplicado — vazio, sem nenhuma entidade de aplicação
// ainda.
func newTenant(t *testing.T, db *database.DB, suffix string) tenancy.Tenant {
	t.Helper()
	ctx := context.Background()
	// tenancy.SchemaName(...) é aplicado AQUI, na construção — não só
	// dentro de WithTenant — para que o nome de schema que este helper usa
	// em CREATE/DROP SCHEMA seja IDENTICO ao que WithTenant resolve
	// internamente (database.go: `schema := tenancy.SchemaName(t)`).
	// Nomes de teste longos (comuns nesta suíte, com nomes de função
	// descritivos) combinados com caracteres que SchemaName remove (nunca
	// substitui por "_", REMOVE) podem truncar em pontos diferentes do que
	// `pgx.Identifier{...}.Sanitize()` sozinho produziria nos 63 bytes de
	// limite de identificador do Postgres — aplicar a MESMA normalização
	// aqui elimina a divergência pela raiz, não só evitando hífens.
	raw := fmt.Sprintf("pack_test_%s_%s", sanitizeForSchema(t.Name()), suffix)
	tenant := tenancy.Tenant(tenancy.SchemaName(tenancy.Tenant(raw)))

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
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := views.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := triggers.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := scheduler.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := library.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := tags.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		return config.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("aplicar schema de framework: %v", err)
	}
	return tenant
}
