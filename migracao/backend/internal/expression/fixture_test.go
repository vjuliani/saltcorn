// Fixture de Postgres real para os testes deste pacote — mesmo padrão de
// internal/pluginhost/fixture_test.go (schema de tenant isolado e
// descartável por teste, catálogo real via internal/metadata, registro
// real via internal/records), mais o schema de cutover (GO-009) e a
// SwitchOwner explícita para plugins.expr que TODO teste deste pacote
// precisa antes de poder chamar Evaluator.Eval — sem ela, ErrLegacyOwner é
// o resultado correto e esperado (ver TestEval_LegacyOwner_NeverTouchesHost).
package expression

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pluginhost"
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

func hostScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller falhou")
	}
	// internal/expression/fixture_test.go -> ../../../packages/pluginhost/dist/src/host.js
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "packages", "pluginhost", "dist", "src", "host.js")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("host.js compilado não encontrado em %s — rode `npm run build` em migracao/packages/pluginhost primeiro: %v", path, err)
	}
	return path
}

func newTestClient(t *testing.T) *pluginhost.Client {
	t.Helper()
	c := pluginhost.NewClient(pluginhost.ClientOptions{NodeBin: "node", HostScript: hostScriptPath(t)})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// testFixture cria um tenant isolado com uma tabela "books" (leitura
// pública por padrão) E o schema de cutover, aplicando SwitchOwner para
// que plugins.expr pertença ao Go neste tenant — o estado que qualquer
// teste de Eval bem-sucedido precisa. Devolve o tenant e a *cutover.Guard
// já "quente" para essa chave.
func testFixture(t *testing.T, db *database.DB) (tenancy.Tenant, *cutover.Guard) {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("expression_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize())); err != nil {
			return err
		}
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("setup de schema: %v", err)
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
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		books, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		_, err = records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Dune"}, nil)
		return err
	}); err != nil {
		t.Fatalf("setup do fixture de domínio: %v", err)
	}

	guard := cutover.NewGuard()
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, pluginhost.ExpressionCapability, cutover.OwnerGo, 5*time.Second); err != nil {
		t.Fatalf("SwitchOwner(plugins.expr, OwnerGo): %v", err)
	}
	return tenant, guard
}

// dbReadCallback reaproveita internal/records + internal/identity — o
// MESMO callback de leitura já provado em
// internal/pluginhost/client_test.go — nenhuma checagem de autorização
// nova inventada para este pacote.
func dbReadCallback(db *database.DB, tenant tenancy.Tenant, actorRole identity.RoleID) pluginhost.CallbackFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		var rows []map[string]any
		err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			rows, err = records.Rows(ctx, tx, actorRole, records.Query{Table: "books", Limit: 1})
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return rows[0], nil
	}
}
