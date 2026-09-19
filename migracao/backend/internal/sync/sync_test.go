package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

type transaction func(func(context.Context, database.Tx) error) error

func matrix(t *testing.T, test func(*testing.T, transaction, Scope)) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			scope := Scope{Tenant: fmt.Sprintf("sync_%d", time.Now().UnixNano()), Actor: "1", Table: "items"}
			var run transaction
			if backend == "sqlite" {
				db, err := sqlite.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				run = func(fn func(context.Context, database.Tx) error) error {
					return db.WithTenant(ctx, tenancy.Tenant(scope.Tenant), fn)
				}
			} else {
				dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
				if dsn == "" {
					t.Skip("PostgreSQL DSN não definida")
				}
				db, err := database.Open(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(db.Close)
				schema := pgx.Identifier{scope.Tenant}.Sanitize()
				if _, err := db.Pool().Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
				run = func(fn func(context.Context, database.Tx) error) error {
					return db.WithTenant(ctx, tenancy.Tenant(scope.Tenant), func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, database.AsTx(tx)) })
				}
			}
			if err := run(func(ctx context.Context, tx database.Tx) error {
				if err := metadata.EnsureSchema(ctx, tx); err != nil {
					return err
				}
				if err := EnsureSchema(ctx, tx); err != nil {
					return err
				}
				table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "items", metadata.TableOptions{})
				if err != nil {
					return err
				}
				for _, field := range []metadata.FieldDef{{Name: "name", Type: metadata.FieldText, Unique: true, Required: true}, {Name: "count", Type: metadata.FieldInteger}, {Name: "date", Type: metadata.FieldDate}} {
					if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, table.ID, field); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			test(t, run, scope)
		})
	}
}
func exchange(t *testing.T, run transaction, scope Scope, req Request) *Response {
	t.Helper()
	var result *Response
	if err := run(func(ctx context.Context, tx database.Tx) error {
		var err error
		result, err = Exchange(ctx, tx, identity.RoleAdmin, scope, req)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func bootstrap(t *testing.T, run transaction, scope Scope) (Request, *Response) {
	req := Request{Protocol: 1, Scope: scope, ClientID: "device_a", Mutations: []Mutation{}}
	result := exchange(t, run, scope, req)
	req.SchemaVersion = result.SchemaVersion
	req.Checkpoint = result.Checkpoint
	return req, result
}
func TestExchangeParity(t *testing.T) {
	matrix(t, func(t *testing.T, run transaction, scope Scope) {
		req, empty := bootstrap(t, run, scope)
		if len(empty.Rows) != 0 {
			t.Fatal(empty)
		}
		req.Mutations = []Mutation{{ID: "create_1", Kind: "create", Values: map[string]any{"name": "offline", "count": json.Number("7"), "date": "2026-09-18T12:00:00Z"}}}
		created := exchange(t, run, scope, req)
		if len(created.Rows) != 1 || created.Results[0].Status != "applied" {
			t.Fatal(created)
		}
		repeated := exchange(t, run, scope, req)
		if len(repeated.Rows) != 1 || repeated.Results[0] != created.Results[0] {
			t.Fatal("retry duplicou", repeated)
		}
		// An acknowledgement lost on the network must not run the mutation again.
		if err := run(func(ctx context.Context, tx database.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM _sc_outbox").Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				t.Fatalf("events=%d", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		collision := req
		collision.Mutations = []Mutation{{ID: "create_1", Kind: "create", Values: map[string]any{"name": "changed payload"}}}
		err := run(func(ctx context.Context, tx database.Tx) error {
			_, err := Exchange(ctx, tx, identity.RoleAdmin, scope, collision)
			return err
		})
		if !errors.Is(err, outbox.ErrKeyConflict) {
			t.Fatalf("reused key: %v", err)
		}
		version := created.Rows[0]["_version"].(string)
		id := created.Results[0].RowID
		update := req
		update.ClientID = "device_b"
		update.Mutations = []Mutation{{ID: "update_b", Kind: "update", RowID: id, BaseVersion: version, Values: map[string]any{"name": "server update"}}}
		updated := exchange(t, run, scope, update)
		if updated.Results[0].Status != "applied" {
			t.Fatal(updated)
		}
		stale := req
		stale.Mutations = []Mutation{{ID: "update_a", Kind: "update", RowID: id, BaseVersion: version, Values: map[string]any{"name": "local draft"}}}
		conflict := exchange(t, run, scope, stale)
		if conflict.Results[0].Code != "version_conflict" || conflict.Rows[0]["name"] != "server update" {
			t.Fatal(conflict)
		}
		// Rebase is an explicit operation with a NEW ID and the current version.
		stale.Mutations[0].ID = "resolve_a"
		stale.Mutations[0].BaseVersion = conflict.Rows[0]["_version"].(string)
		resolved := exchange(t, run, scope, stale)
		if resolved.Rows[0]["name"] != "local draft" {
			t.Fatal(resolved)
		}
		req.Mutations = []Mutation{{ID: "delete_a", Kind: "delete", RowID: id, BaseVersion: resolved.Rows[0]["_version"].(string)}}
		deleted := exchange(t, run, scope, req)
		if len(deleted.Rows) != 0 || deleted.Results[0].Status != "applied" {
			t.Fatal(deleted)
		}
		if len(exchange(t, run, scope, req).Rows) != 0 {
			t.Fatal("delete replay resurrected row")
		}
		stale.Mutations[0].ID = "edit_deleted"
		if result := exchange(t, run, scope, stale); result.Results[0].Code != "missing_or_inaccessible" {
			t.Fatal(result)
		}
	})
}
func TestScopeSchemaAndRollback(t *testing.T) {
	matrix(t, func(t *testing.T, run transaction, scope Scope) {
		req, _ := bootstrap(t, run, scope)
		req.Mutations = []Mutation{{ID: "one", Kind: "create", Values: map[string]any{"name": "a"}}}
		for _, variant := range []string{"actor", "tenant", "table", "protocol", "schema", "role"} {
			t.Run(variant, func(t *testing.T) {
				copy := req
				role := identity.RoleAdmin
				want := ErrScope
				switch variant {
				case "actor":
					copy.Scope.Actor = "2"
				case "tenant":
					copy.Scope.Tenant = "other"
				case "table":
					copy.Scope.Table = "other"
				case "protocol":
					copy.Protocol = 2
					want = ErrVersion
				case "schema":
					copy.SchemaVersion++
					want = ErrSchema
				case "role":
					role = identity.RolePublic
					want = records.ErrNotAuthorized
				}
				err := run(func(ctx context.Context, tx database.Tx) error {
					_, err := Exchange(ctx, tx, role, scope, copy)
					return err
				})
				if !errors.Is(err, want) {
					t.Fatalf("got %v want %v", err, want)
				}
			})
		}
		abort := errors.New("simulated disconnect before commit")
		err := run(func(ctx context.Context, tx database.Tx) error {
			if _, err := Exchange(ctx, tx, identity.RoleAdmin, scope, req); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatal(err)
		}
		_, result := bootstrap(t, run, scope)
		if len(result.Rows) != 0 {
			t.Fatal("partial commit")
		}
		if result := exchange(t, run, scope, req); len(result.Rows) != 1 {
			t.Fatal(result)
		}
		// Constraint failure must roll back just this operation, preserving the next.
		req.Mutations = []Mutation{{ID: "duplicate", Kind: "create", Values: map[string]any{"name": "a"}}, {ID: "valid", Kind: "create", Values: map[string]any{"name": "b"}}}
		result = exchange(t, run, scope, req)
		if result.Results[0].Code != "duplicate_value" || result.Results[1].Status != "applied" || len(result.Rows) != 2 {
			t.Fatal(result)
		}
	})
}
func TestSnapshotLimitRollsBackBatch(t *testing.T) {
	matrix(t, func(t *testing.T, run transaction, scope Scope) {
		req, _ := bootstrap(t, run, scope)
		if err := run(func(ctx context.Context, tx database.Tx) error {
			return tx.Exec(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<2000) INSERT INTO items (name) SELECT CAST(i AS TEXT) FROM n`)
		}); err != nil {
			t.Fatal(err)
		}
		req.Mutations = []Mutation{{ID: "overflow", Kind: "create", Values: map[string]any{"name": "overflow"}}}
		err := run(func(ctx context.Context, tx database.Tx) error {
			_, err := Exchange(ctx, tx, identity.RoleAdmin, scope, req)
			return err
		})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatal(err)
		}
		if err := run(func(ctx context.Context, tx database.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM items").Scan(&n); err != nil {
				return err
			}
			if n != 2000 {
				t.Fatalf("partial batch persisted: %d", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresOwnershipAndRevocation(t *testing.T) {
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL DSN não definida")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := fmt.Sprintf("sync_rls_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := db.Pool().Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Pool().Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	if _, err := db.Pool().Exec(ctx, "CREATE ROLE "+quoted+" NOLOGIN NOSUPERUSER NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Pool().Exec(ctx, "DROP OWNED BY "+quoted)
		_, _ = db.Pool().Exec(ctx, "DROP ROLE "+quoted)
	}()
	run := func(fn func(context.Context, database.Tx) error) error {
		return db.WithTenant(ctx, tenancy.Tenant(name), func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, database.AsTx(tx)) })
	}
	var tableID int
	if err := run(func(ctx context.Context, tx database.Tx) error {
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := outbox.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "private", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = table.ID
		for _, f := range []metadata.FieldDef{{Name: "owner", Type: metadata.FieldText}, {Name: "name", Type: metadata.FieldText}} {
			if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, table.ID, f); err != nil {
				return err
			}
		}
		if _, err := records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "private", map[string]any{"owner": "1", "name": "mine"}, nil); err != nil {
			return err
		}
		if _, err := records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "private", map[string]any{"owner": "2", "name": "secret"}, nil); err != nil {
			return err
		}
		for _, sql := range []string{"ALTER TABLE private ENABLE ROW LEVEL SECURITY", "ALTER TABLE private FORCE ROW LEVEL SECURITY", `CREATE POLICY owner_only ON private USING (owner = current_setting('app.current_user_id',true)) WITH CHECK (owner = current_setting('app.current_user_id',true))`, "GRANT USAGE, CREATE ON SCHEMA " + quoted + " TO " + quoted, "GRANT ALL ON ALL TABLES IN SCHEMA " + quoted + " TO " + quoted, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA " + quoted + " TO " + quoted} {
			if err := tx.Exec(ctx, sql); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restricted := func(fn func(context.Context, database.Tx) error) error {
		return run(func(ctx context.Context, tx database.Tx) error {
			if err := tx.Exec(ctx, "SET LOCAL ROLE "+quoted); err != nil {
				return err
			}
			if err := tx.Exec(ctx, "SELECT set_config('app.current_user_id','1',true)"); err != nil {
				return err
			}
			return fn(ctx, tx)
		})
	}
	scope := Scope{Tenant: name, Actor: "1", Table: "private"}
	req, first := bootstrap(t, restricted, scope)
	if len(first.Rows) != 1 || first.Rows[0]["name"] != "mine" {
		t.Fatal("ownership leaked", first.Rows)
	}
	req.Mutations = []Mutation{{ID: "own", Kind: "update", RowID: 1, BaseVersion: first.Rows[0]["_version"].(string), Values: map[string]any{"name": "new"}}}
	forbiddenCreate := req
	forbiddenCreate.Mutations = []Mutation{{ID: "wrong_owner", Kind: "create", Values: map[string]any{"owner": "2", "name": "forbidden"}}}
	err = restricted(func(ctx context.Context, tx database.Tx) error {
		_, err := Exchange(ctx, tx, identity.RoleAdmin, scope, forbiddenCreate)
		return err
	})
	if !errors.Is(err, records.ErrNotAuthorized) {
		t.Fatalf("RLS WITH CHECK: %v", err)
	}
	applied := exchange(t, restricted, scope, req)
	if applied.Results[0].Status != "applied" {
		t.Fatal(applied)
	}
	attack := req
	attack.Mutations = []Mutation{{ID: "other", Kind: "update", RowID: 2, BaseVersion: "1", Values: map[string]any{"name": "attack"}}}
	denied := exchange(t, restricted, scope, attack)
	if denied.Results[0].Code != "missing_or_inaccessible" || len(denied.Rows) != 1 {
		t.Fatal(denied)
	}
	if err := run(func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "UPDATE private SET owner='2' WHERE id=1")
	}); err != nil {
		t.Fatal(err)
	}
	replay := exchange(t, restricted, scope, req)
	if len(replay.Rows) != 0 {
		t.Fatal("replay exposed revoked row", replay)
	}
	if err := run(func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "UPDATE _sc_tables SET min_role_read=1 WHERE id=$1", tableID)
	}); err != nil {
		t.Fatal(err)
	}
	err = restricted(func(ctx context.Context, tx database.Tx) error {
		_, err := Exchange(ctx, tx, identity.RolePublic, scope, req)
		return err
	})
	if !errors.Is(err, records.ErrNotAuthorized) {
		t.Fatalf("revoked role: %v", err)
	}
}
