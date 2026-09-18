// Fixture de Postgres real — mesmo padrão de internal/notify/
// fixture_test.go, internal/triggers/fixture_test.go.
package realtime

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

// shortSanitizedName trunca o nome de teste sanitizado a maxLen — nomes de
// função de teste em Go são livres de tamanho, e um schema Postgres é
// silenciosamente truncado em 63 bytes (NAMEDATALEN-1). Sem este limite,
// dois tenants construídos a partir do MESMO nome de teste longo (ex.:
// "<prefixo><nome>" e "<prefixo><nome>_b" — ver TestListSinceForActor_
// NeverLeaksAcrossTenantSchemas) podem colidir no MESMO schema após o
// truncamento do Postgres, porque os dois compartilham os primeiros 63
// bytes — achado real desta tarefa, mesma classe de bug (mas causa
// diferente) do achado de GO-027 em internal/pack/fixture_test.go.
func shortSanitizedName(name string, maxLen int) string {
	s := sanitizeForSchema(name)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

func realtimeFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("realtime_test_%s", shortSanitizedName(t.Name(), 40)))

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
