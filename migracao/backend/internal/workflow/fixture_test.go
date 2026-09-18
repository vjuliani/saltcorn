// Fixture de Postgres real — mesmo padrão de internal/triggers/
// fixture_test.go e internal/expression/fixture_test.go.
package workflow

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

// workflowFixture cria um tenant isolado com o schema de workflow +
// cutover, uma tabela "counters" (usada pelos steps de teste como efeito
// interno observável) e um *expression.Evaluator pronto (host real +
// Guard "quente" via SwitchOwner) para os testes que usam Step.OnlyIf.
func workflowFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant, evaluator *expression.Evaluator) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("workflow_test_%s", sanitizeForSchema(t.Name())))

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
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := EnsureSchema(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS counters (name text PRIMARY KEY, value integer NOT NULL DEFAULT 0)`)
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
	return db, tenant, evaluator
}

// incrementCounter é o efeito interno reaproveitado pelos testes — soma
// delta na linha "name" de counters (criando-a com valor 0 se ainda não
// existir) e devolve o valor novo lido de volta, útil para os steps
// registrarem no contexto do workflow.
func incrementCounter(ctx context.Context, tx pgx.Tx, name string, delta int) (int, error) {
	var value int
	err := tx.QueryRow(ctx, `
		INSERT INTO counters (name, value) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET value = counters.value + $2
		RETURNING value
	`, name, delta).Scan(&value)
	return value, err
}

func readCounter(ctx context.Context, tx pgx.Tx, name string) (int, error) {
	var value int
	err := tx.QueryRow(ctx, `SELECT value FROM counters WHERE name = $1`, name).Scan(&value)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return value, nil
}
