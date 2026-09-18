// Fixture de Postgres real — mesmo padrão de internal/expression/
// fixture_test.go e internal/pluginhost/fixture_test.go: schema de tenant
// isolado e descartável por teste, catálogo real, host real de GO-022
// para os testes que exercitam OnlyIf via internal/expression.
package triggers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pluginhost"
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

func hostScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller falhou")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "packages", "pluginhost", "dist", "src", "host.js")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("host.js compilado não encontrado em %s — rode `npm run build` em migracao/packages/pluginhost primeiro: %v", path, err)
	}
	return path
}

// triggerFixture cria um tenant isolado com uma tabela "posts" (leitura
// pública por padrão), aplica o schema de triggers/outbox/cutover, e
// devolve tudo que os testes de Dispatcher precisam: o *database.DB, o
// tenant, o ID da tabela, e um *expression.Evaluator já pronto (host real
// + Guard "quente" via SwitchOwner para plugins.expr) — mesmo padrão de
// internal/expression/fixture_test.go.
func triggerFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant, tableID int, evaluator *expression.Evaluator) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("triggers_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize())); err != nil {
			return err
		}
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("setup de schema público: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2", string(tenant), pluginhost.ExpressionCapability)
			return err
		})
	})

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := EnsureSchema(ctx, tx); err != nil {
			return err
		}
		posts, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = posts.ID
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "published", Type: metadata.FieldBoolean})
		return err
	}); err != nil {
		t.Fatalf("setup do fixture de domínio: %v", err)
	}

	guard := cutover.NewGuard()
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, pluginhost.ExpressionCapability, cutover.OwnerGo, 5*time.Second); err != nil {
		t.Fatalf("SwitchOwner(plugins.expr, OwnerGo): %v", err)
	}
	client := pluginhost.NewClient(pluginhost.ClientOptions{NodeBin: "node", HostScript: hostScriptPath(t)})
	t.Cleanup(func() { _ = client.Close() })
	evaluator = &expression.Evaluator{Client: client, Guard: guard}
	return db, tenant, tableID, evaluator
}
