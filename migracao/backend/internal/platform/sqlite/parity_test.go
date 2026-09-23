package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sync"
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
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

type runner func(context.Context, func(context.Context, database.Tx) error) error

func adapters(t *testing.T, test func(*testing.T, runner, runner)) {
	t.Helper()
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var run, other runner
			ctx := context.Background()
			if backend == "sqlite" {
				dir := t.TempDir()
				db, err := sqlite.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				// Outro handle para o MESMO arquivo: locking deve funcionar entre conexões.
				second, err := sqlite.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = second.Close() })
				run = func(ctx context.Context, fn func(context.Context, database.Tx) error) error {
					return db.WithTenant(ctx, "parity", fn)
				}
				other = func(ctx context.Context, fn func(context.Context, database.Tx) error) error {
					return second.WithTenant(ctx, "parity", fn)
				}
			} else {
				dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
				if dsn == "" {
					t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida")
				}
				db, err := database.Open(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(db.Close)
				tenant := tenancy.Tenant(fmt.Sprintf("parity_%d", time.Now().UnixNano()))
				if _, err := db.Pool().Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{string(tenant)}.Sanitize()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = db.Pool().Exec(ctx, "DROP SCHEMA "+pgx.Identifier{string(tenant)}.Sanitize()+" CASCADE")
				})
				run = func(ctx context.Context, fn func(context.Context, database.Tx) error) error {
					return db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, database.AsTx(tx)) })
				}
				other = run
			}
			if err := run(ctx, func(ctx context.Context, tx database.Tx) error {
				if err := metadata.EnsureSchema(ctx, tx); err != nil {
					return err
				}
				if err := outbox.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				if err := identity.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				return views.EnsureSchemaTx(ctx, tx)
			}); err != nil {
				t.Fatal(err)
			}
			test(t, run, other)
		})
	}
}

func mustRun(t *testing.T, run runner, fn func(context.Context, database.Tx) error) {
	t.Helper()
	if err := run(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}

func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("got %s, want %s", a, b)
	}
}

func TestParityRecords(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		var tableID int
		date := time.Date(2026, 9, 18, 12, 30, 0, 0, time.UTC)
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "items", metadata.TableOptions{})
			if err != nil {
				return err
			}
			tableID = table.ID
			for _, f := range []metadata.FieldDef{
				{Name: "name", Type: metadata.FieldText, Required: true, Unique: true},
				{Name: "quantity", Type: metadata.FieldInteger}, {Name: "active", Type: metadata.FieldBoolean},
				{Name: "price", Type: metadata.FieldFloat}, {Name: "day", Type: metadata.FieldDate},
				{Name: "parent", Type: metadata.FieldKey, References: "items"},
			} {
				if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, table.ID, f); err != nil {
					return err
				}
			}
			return nil
		})
		var first, second map[string]any
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			var err error
			first, err = records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "items", map[string]any{"name": "Alpha", "quantity": 2, "active": true, "price": 3.5, "day": date}, nil)
			return err
		})
		version := first["_version"].(string)
		delete(first, "_version")
		gotDate, ok := first["day"].(time.Time)
		if !ok || !gotDate.Equal(date) {
			t.Fatalf("data: %v", first["day"])
		}
		first["day"] = gotDate.UTC()
		sameJSON(t, first, map[string]any{"id": 1, "name": "Alpha", "quantity": 2, "active": true, "price": 3.5, "day": date, "parent": nil})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			var err error
			second, err = records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "items", map[string]any{"name": "Beta", "parent": 1}, nil)
			return err
		})
		cases := []struct {
			name   string
			values map[string]any
			role   identity.RoleID
			want   error
		}{
			{"duplicate", map[string]any{"name": "Alpha"}, identity.RoleAdmin, records.ErrDuplicateValue},
			{"reference", map[string]any{"name": "Bad", "parent": 999}, identity.RoleAdmin, records.ErrInvalidReference},
			{"required", map[string]any{}, identity.RoleAdmin, records.ErrRequiredField},
			{"null", map[string]any{"name": nil}, identity.RoleAdmin, records.ErrRequiredField},
			{"type", map[string]any{"name": "Bad", "quantity": "not integer"}, identity.RoleAdmin, records.ErrTypeMismatch},
			{"role", map[string]any{"name": "Bad"}, identity.RolePublic, records.ErrNotAuthorized},
			{"version", map[string]any{"name": "Bad", "_version": "1"}, identity.RoleAdmin, records.ErrUnknownField},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := run(context.Background(), func(ctx context.Context, tx database.Tx) error {
					_, err := records.CreateRecordTx(ctx, tx, tc.role, "items", tc.values, nil)
					return err
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
			})
		}
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			rows, err := records.RowsTx(ctx, tx, identity.RolePublic, records.Query{Table: "items", Where: records.Like{Field: "name", Substring: "aLP"}})
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				t.Fatalf("LIKE: %v", rows)
			}
			rows, err = records.RowsTx(ctx, tx, identity.RolePublic, records.Query{Table: "items", OrderBy: []records.OrderTerm{{Field: "id"}}, Offset: 1})
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0]["name"] != "Beta" {
				t.Fatalf("offset: %v", rows)
			}
			rows, err = records.RowsTx(ctx, tx, identity.RolePublic, records.Query{Table: "items", Where: records.Eq{Field: "name", Value: "' OR 1=1 --"}})
			if err != nil {
				return err
			}
			if len(rows) != 0 {
				t.Fatal("SQL injection")
			}
			rows, err = records.RowsTx(ctx, tx, identity.RolePublic, records.Query{Table: "items", Where: records.Eq{Field: "id", Value: 2}, Joins: []records.Join{{Field: "parent", Select: []string{"name"}}}})
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0]["parent__name"] != "Alpha" {
				t.Fatalf("join: %v", rows)
			}
			return nil
		})
		mustRun(t, other, func(ctx context.Context, tx database.Tx) error {
			updated, err := records.UpdateRecordTx(ctx, tx, identity.RoleAdmin, "items", 1, version, map[string]any{"quantity": 3}, nil)
			if err == nil && updated["_version"] == version {
				t.Fatal("versão não mudou")
			}
			return err
		})
		err := run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := records.UpdateRecordTx(ctx, tx, identity.RoleAdmin, "items", 1, version, map[string]any{"quantity": 4}, nil)
			return err
		})
		if !errors.Is(err, records.ErrVersionConflict) {
			t.Fatalf("stale: %v", err)
		}
		sentinel := errors.New("falha hook")
		err = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "items", map[string]any{"name": "Rollback"}, &records.TxHooks{AfterInsert: func(context.Context, database.Tx, metadata.Table, map[string]any) error { return sentinel }})
			return err
		})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			rows, err := records.RowsTx(ctx, tx, identity.RolePublic, records.Query{Table: "items"})
			if err != nil {
				return err
			}
			if len(rows) != 2 {
				t.Fatalf("hook não desfez: %v", rows)
			}
			return records.DeleteRecordTx(ctx, tx, identity.RoleAdmin, "items", 2, second["_version"].(string), nil)
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return metadata.DropField(ctx, tx, identity.RoleAdmin, tableID, "name")
		})
	})
}

func TestParityOutbox(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return tx.Exec(ctx, "CREATE TABLE effects (id INTEGER PRIMARY KEY, n INTEGER NOT NULL)")
		})
		var mu sync.Mutex
		calls := 0
		produce := func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			if err := tx.Exec(ctx, "INSERT INTO effects VALUES (1, 0)"); err != nil {
				return nil, nil, err
			}
			return map[string]any{"id": 1}, []outbox.Event{{Type: "created", Payload: map[string]any{"id": 1}}}, nil
		}
		errs := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := run
				if i%2 == 1 {
					r = other
				}
				errs <- r(context.Background(), func(ctx context.Context, tx database.Tx) error {
					result, _, err := outbox.DoTx(ctx, tx, "key", map[string]any{"input": 1}, produce)
					if err == nil {
						b, _ := json.Marshal(result)
						if string(b) != `{"id":1}` {
							return fmt.Errorf("resultado: %s", b)
						}
					}
					return err
				})
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Fatalf("efeito executado %d vezes", calls)
		}
		err := run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, _, err := outbox.DoTx(ctx, tx, "key", 2, produce)
			return err
		})
		if !errors.Is(err, outbox.ErrKeyConflict) {
			t.Fatalf("conflito: %v", err)
		}
		sentinel := errors.New("abortar antes do commit")
		err = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, _, err := outbox.DoTx(ctx, tx, "rollback", nil, func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
				if err := tx.Exec(ctx, "INSERT INTO effects VALUES (2, 0)"); err != nil {
					return nil, nil, err
				}
				return 2, []outbox.Event{{Type: "rollback"}}, nil
			})
			if err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			var count int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM effects").Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				t.Fatal("efeito sobreviveu rollback")
			}
			events, err := outbox.ListPendingTx(ctx, tx, 10)
			if err != nil {
				return err
			}
			if len(events) != 1 {
				t.Fatalf("eventos: %v", events)
			}
			p, f, err := outbox.ProcessPendingTx(ctx, tx, 10, 2, func(ctx context.Context, tx database.Tx, event outbox.OutboxEvent) error {
				if err := tx.Exec(ctx, "UPDATE effects SET n=n+1 WHERE id=1"); err != nil {
					return err
				}
				return sentinel
			})
			if err == nil && (p != 0 || f != 1) {
				t.Fatalf("retry: %d %d", p, f)
			}
			return err
		})
		mustRun(t, other, func(ctx context.Context, tx database.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, "SELECT n FROM effects WHERE id=1").Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Fatalf("savepoint persistiu %d", n)
			}
			p, f, err := outbox.ProcessPendingTx(ctx, tx, 10, 2, func(ctx context.Context, tx database.Tx, event outbox.OutboxEvent) error {
				return tx.Exec(ctx, "UPDATE effects SET n=n+1 WHERE id=1")
			})
			if err == nil && (p != 1 || f != 0) {
				t.Fatalf("processamento: %d %d", p, f)
			}
			return err
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			p, f, err := outbox.ProcessPendingTx(ctx, tx, 10, 2, func(context.Context, database.Tx, outbox.OutboxEvent) error { t.Fatal("evento duplicado"); return nil })
			if err == nil && (p != 0 || f != 0) {
				t.Fatal("duplicado")
			}
			return err
		})
	})
}

func TestParityPanicRollback(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return tx.Exec(ctx, "CREATE TABLE panic_effect (id INTEGER PRIMARY KEY)")
		})
		sentinel := errors.New("panic")
		func() {
			defer func() {
				if !reflect.DeepEqual(recover(), sentinel) {
					t.Error("panic perdido")
				}
			}()
			_ = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
				if err := tx.Exec(ctx, "INSERT INTO panic_effect VALUES (1)"); err != nil {
					t.Fatal(err)
				}
				panic(sentinel)
			})
		}()
		mustRun(t, other, func(ctx context.Context, tx database.Tx) error {
			var count int
			err := tx.QueryRow(ctx, "SELECT count(*) FROM panic_effect").Scan(&count)
			if err == nil && count != 0 {
				t.Fatal("commit durante panic")
			}
			return err
		})
	})
}

func TestSQLiteCrashRecovery(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			db, err := sqlite.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.WithTenant(context.Background(), "crash", func(ctx context.Context, tx database.Tx) error {
				if err := outbox.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				return tx.Exec(ctx, "CREATE TABLE effects (id INTEGER PRIMARY KEY)")
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestSQLiteCrashHelper$")
			child.Env = append(os.Environ(), "GO030_CRASH_DIR="+dir, "GO030_CRASH_STAGE="+stage)
			output, err := child.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
				t.Fatalf("child: %v\n%s", err, output)
			}
			db, err = sqlite.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.WithTenant(context.Background(), "crash", func(ctx context.Context, tx database.Tx) error {
				expected := 0
				if stage == "after" {
					expected = 1
				}
				for _, table := range []string{"effects", "_sc_idempotency_keys", "_sc_outbox"} {
					var count int
					if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
						return err
					}
					if count != expected {
						t.Fatalf("%s: %d, esperado %d", table, count, expected)
					}
				}
				_, replayed, err := outbox.DoTx(ctx, tx, "crash", nil, crashEffect)
				if err != nil {
					return err
				}
				if replayed != (stage == "after") {
					t.Fatalf("replayed=%v", replayed)
				}
				p, f, err := outbox.ProcessPendingTx(ctx, tx, 10, 2, func(context.Context, database.Tx, outbox.OutboxEvent) error { return nil })
				if err == nil && (p != 1 || f != 0) {
					t.Fatalf("recovery: %d %d", p, f)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func crashEffect(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
	if err := tx.Exec(ctx, "INSERT INTO effects VALUES (1)"); err != nil {
		return nil, nil, err
	}
	return 1, []outbox.Event{{Type: "crash", Payload: map[string]any{"id": 1}}}, nil
}

func TestSQLiteCrashHelper(t *testing.T) {
	dir := os.Getenv("GO030_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocesso de crash recovery")
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithTenant(context.Background(), "crash", func(ctx context.Context, tx database.Tx) error {
		_, _, err := outbox.DoTx(ctx, tx, "crash", nil, crashEffect)
		if err != nil {
			return err
		}
		if os.Getenv("GO030_CRASH_STAGE") == "before" {
			os.Exit(23)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(23) // sem Close/defer: morte real do processo após o commit
}

func TestParityOutboxTerminalFailure(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			_, _, err := outbox.DoTx(ctx, tx, "failure", nil, func(context.Context, database.Tx) (any, []outbox.Event, error) {
				return nil, []outbox.Event{{Type: "fail"}, {Type: "success"}}, nil
			})
			return err
		})
		for i := 0; i < 2; i++ {
			mustRun(t, other, func(ctx context.Context, tx database.Tx) error {
				_, _, err := outbox.ProcessPendingTx(ctx, tx, 10, 2, func(ctx context.Context, tx database.Tx, ev outbox.OutboxEvent) error {
					if ev.Type == "fail" {
						return errors.New("retry")
					}
					return nil
				})
				return err
			})
		}
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			failed, err := outbox.ListFailedTx(ctx, tx, 10)
			if err != nil {
				return err
			}
			if len(failed) != 1 || failed[0].Attempts != 2 {
				t.Fatalf("failed: %v", failed)
			}
			pending, err := outbox.ListPendingTx(ctx, tx, 10)
			if err == nil && len(pending) != 0 {
				t.Fatalf("pending: %v", pending)
			}
			return err
		})
	})
}

func TestSQLiteUpgradePreservesLocalRecords(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.WithTenant(ctx, "upgrade", func(ctx context.Context, tx database.Tx) error {
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "legacy", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, table.ID, metadata.FieldDef{Name: "name", Type: metadata.FieldText}); err != nil {
			return err
		}
		if err := tx.Exec(ctx, `ALTER TABLE legacy DROP COLUMN "_version"`); err != nil {
			return err
		}
		return tx.Exec(ctx, "INSERT INTO legacy (name) VALUES ('pendente offline')")
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, "upgrade", func(ctx context.Context, tx database.Tx) error {
			if err := metadata.EnsureSchema(ctx, tx); err != nil {
				return err
			}
			rows, err := records.RowsTx(ctx, tx, identity.RoleAdmin, records.Query{Table: "legacy"})
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0]["name"] != "pendente offline" || rows[0]["_version"] != "1" {
				t.Fatalf("upgrade: %v", rows)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestParityIdentity (GO-041) prova que internal/identity produz o MESMO
// comportamento observável nos dois dialetos — criação/busca/autenticação
// de usuário, token de API (criação/verificação/revogação), administração
// (papel/senha/exclusão, via UPDATE ... RETURNING, já que database.Tx não
// expõe RowsAffected) e a trilha de auditoria de impersonação.
func TestParityIdentity(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		var userID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			id, err := identity.CreateUserTx(ctx, tx, "ana@example.com", "hash-inicial", identity.RolePublic)
			userID = id
			return err
		})

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			u, err := identity.FindUserByEmailTx(ctx, tx, "ana@example.com")
			if err != nil {
				return err
			}
			if u.ID != userID || u.RoleID != identity.RolePublic {
				t.Fatalf("FindUserByEmailTx: %+v", u)
			}
			u2, err := identity.FindUserByIDTx(ctx, tx, userID)
			if err != nil {
				return err
			}
			if u2.Email != "ana@example.com" {
				t.Fatalf("FindUserByIDTx: %+v", u2)
			}
			return nil
		})

		err := run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := identity.FindUserByEmailTx(ctx, tx, "nunca@example.com")
			return err
		})
		if !errors.Is(err, identity.ErrUserNotFound) {
			t.Fatalf("got %v, want ErrUserNotFound", err)
		}

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return identity.SetUserLanguageTx(ctx, tx, userID, "pt")
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			u, err := identity.FindUserByIDTx(ctx, tx, userID)
			if err != nil {
				return err
			}
			if u.Language != "pt" {
				t.Fatalf("Language = %q, esperado pt", u.Language)
			}
			return nil
		})

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return identity.UpdateUserRoleTx(ctx, tx, identity.RoleAdmin, userID, identity.RoleAdmin)
		})
		err = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			return identity.UpdateUserRoleTx(ctx, tx, identity.RoleAdmin, userID+999, identity.RoleAdmin)
		})
		if !errors.Is(err, identity.ErrUserNotFound) {
			t.Fatalf("UpdateUserRoleTx (id inexistente): got %v, want ErrUserNotFound", err)
		}

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return identity.SetPasswordTx(ctx, tx, identity.RoleAdmin, userID, "hash-novo")
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			_, err := identity.AuthenticateTx(ctx, tx, "ana@example.com", "senha-qualquer")
			if !errors.Is(err, identity.ErrInvalidCredentials) {
				t.Fatalf("Authenticate com hash não-bcrypt: got %v", err)
			}
			return nil
		})

		var plaintext string
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			var err error
			plaintext, err = identity.CreateAPITokenForUserTx(ctx, tx, userID)
			return err
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			u, err := identity.FindUserByAPITokenTx(ctx, tx, plaintext)
			if err != nil {
				return err
			}
			if u.ID != userID {
				t.Fatalf("FindUserByAPITokenTx: %+v", u)
			}
			tokens, err := identity.ListAPITokensForUserTx(ctx, tx, identity.RoleAdmin, userID)
			if err != nil {
				return err
			}
			if len(tokens) != 1 || tokens[0].Revoked {
				t.Fatalf("ListAPITokensForUserTx: %+v", tokens)
			}
			return nil
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return identity.RevokeAPITokenTx(ctx, tx, plaintext)
		})
		err = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := identity.FindUserByAPITokenTx(ctx, tx, plaintext)
			return err
		})
		if !errors.Is(err, identity.ErrTokenNotFoundOrRevoked) {
			t.Fatalf("token revogado: got %v, want ErrTokenNotFoundOrRevoked", err)
		}

		var adminID, logID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			id, err := identity.CreateUserTx(ctx, tx, "admin@example.com", "hash", identity.RoleAdmin)
			adminID = id
			return err
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			id, err := identity.StartImpersonationTx(ctx, tx, identity.RoleAdmin, adminID, userID)
			logID = id
			return err
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			rec, err := identity.GetImpersonationTx(ctx, tx, logID)
			if err != nil {
				return err
			}
			if !rec.StillActive || rec.AdminUserID != adminID || rec.TargetUserID != userID {
				t.Fatalf("GetImpersonationTx (ativa): %+v", rec)
			}
			return nil
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return identity.EndImpersonationTx(ctx, tx, logID)
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			rec, err := identity.GetImpersonationTx(ctx, tx, logID)
			if err != nil {
				return err
			}
			if rec.StillActive || rec.EndedAt == "" {
				t.Fatalf("GetImpersonationTx (encerrada): %+v", rec)
			}
			return nil
		})

		// DeleteUserTx num usuário SEM linha em _sc_impersonation_log —
		// userID/adminID têm ON DELETE sem CASCADE de propósito nessa
		// tabela (a trilha de auditoria nunca desaparece junto com o
		// usuário que documenta, ver comentário de schema.go), então a
		// prova de exclusão usa um TERCEIRO usuário, nunca envolvido em
		// nenhuma impersonação.
		var throwawayID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			id, err := identity.CreateUserTx(ctx, tx, "descartavel@example.com", "hash", identity.RolePublic)
			throwawayID = id
			return err
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			users, err := identity.ListUsersTx(ctx, tx, identity.RoleAdmin)
			if err != nil {
				return err
			}
			if len(users) != 3 {
				t.Fatalf("ListUsersTx: %d usuários, esperado 3", len(users))
			}
			return nil
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return identity.DeleteUserTx(ctx, tx, identity.RoleAdmin, throwawayID)
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			users, err := identity.ListUsersTx(ctx, tx, identity.RoleAdmin)
			if err != nil {
				return err
			}
			if len(users) != 2 {
				t.Fatalf("ListUsersTx após DeleteUserTx: %d usuários, esperado 2", len(users))
			}
			return nil
		})
	})
}

// TestParityViews (GO-041) prova que internal/views produz o MESMO
// comportamento observável nos dois dialetos — criação/leitura/listagem
// de view e, principalmente, o controle de concorrência otimista via
// "_version" (records.VersionExpr: xmin no Postgres, coluna explícita no
// SQLite) — a peça que exigia mudança de schema, não só de tipo de Tx.
func TestParityViews(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		var tableID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{})
			if err != nil {
				return err
			}
			tableID = table.ID
			_, err = metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
			return err
		})

		var viewID int
		var version string
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			v, err := views.CreateViewTx(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", map[string]any{
				"columns": []any{map[string]any{"type": "Field", "field_name": "title"}},
			}, views.ViewOptions{})
			viewID = v.ID
			version = v.Version
			return err
		})
		if version == "" {
			t.Fatal("CreateViewTx: _version vazia")
		}

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			v, err := views.GetViewByNameTx(ctx, tx, identity.RoleAdmin, "booklist")
			if err != nil {
				return err
			}
			if v.ID != viewID {
				t.Fatalf("GetViewByNameTx: %+v", v)
			}
			all, err := views.ListViewsTx(ctx, tx, identity.RoleAdmin, 0)
			if err != nil {
				return err
			}
			if len(all) != 1 {
				t.Fatalf("ListViewsTx: %v", all)
			}
			return nil
		})

		// Concorrência otimista: a MESMA leitura (version) usada por duas
		// escritas concorrentes — a segunda precisa falhar com
		// ErrVersionConflict, nunca uma sobrescrita silenciosa, exatamente
		// o que xmin/coluna _version existem para garantir.
		mustRun(t, other, func(ctx context.Context, tx database.Tx) error {
			updated, err := views.UpdateViewTx(ctx, tx, identity.RoleAdmin, viewID, version, views.ViewUpdate{})
			if err != nil {
				return err
			}
			if updated.Version == version {
				t.Fatal("_version não mudou após UpdateViewTx")
			}
			return nil
		})
		err := run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := views.UpdateViewTx(ctx, tx, identity.RoleAdmin, viewID, version, views.ViewUpdate{})
			return err
		})
		if !errors.Is(err, views.ErrVersionConflict) {
			t.Fatalf("UpdateViewTx com version obsoleta: got %v, want ErrVersionConflict", err)
		}

		err = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := views.UpdateViewTx(ctx, tx, identity.RoleAdmin, viewID+999, version, views.ViewUpdate{})
			return err
		})
		if !errors.Is(err, views.ErrViewNotFound) {
			t.Fatalf("UpdateViewTx com id inexistente: got %v, want ErrViewNotFound", err)
		}

		err = run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := views.CreateViewTx(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", map[string]any{
				"columns": []any{map[string]any{"type": "Field", "field_name": "title"}},
			}, views.ViewOptions{})
			return err
		})
		if !errors.Is(err, views.ErrDuplicateName) {
			t.Fatalf("nome duplicado: got %v, want ErrDuplicateName", err)
		}
	})
}

// TestParityViewsEditPlan (GO-041) prova que a camada de RENDERIZAÇÃO de
// views (CompileEditPlanTx/SubmitEditViewTx — o que cmd/server de fato
// expõe via HTTP para o botão "Salvar" de um Edit) produz o MESMO
// comportamento nos dois dialetos, não só o CRUD de _sc_views em si
// (já coberto por TestParityViews).
func TestParityViewsEditPlan(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		var tableID, viewID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "notes", metadata.TableOptions{})
			if err != nil {
				return err
			}
			tableID = table.ID
			if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
				return err
			}
			v, err := views.CreateViewTx(ctx, tx, identity.RoleAdmin, "editnote", tableID, "Edit", map[string]any{
				"columns": []any{
					map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
					map[string]any{"type": "Action", "action_name": "Save"},
				},
			}, views.ViewOptions{})
			viewID = v.ID
			return err
		})

		var recordID int
		var recordVersion string
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			plan, err := views.CompileEditPlanTx(ctx, tx, identity.RoleAdmin, viewID, 0)
			if err != nil {
				return err
			}
			if len(plan.Fields) != 1 || plan.Fields[0].FieldName != "title" || plan.RecordID != 0 {
				t.Fatalf("CompileEditPlanTx (criação): %+v", plan)
			}
			result, err := views.SubmitEditViewTx(ctx, tx, identity.RoleAdmin, viewID, 0, "", map[string]any{"title": "Primeira nota"})
			if err != nil {
				return err
			}
			recordID = int(idAsFloat(result.Record["id"]))
			recordVersion = result.Record["_version"].(string)
			if result.Navigate.Type != "reload" {
				t.Fatalf("Navigate: %+v", result.Navigate)
			}
			return nil
		})

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			plan, err := views.CompileEditPlanTx(ctx, tx, identity.RoleAdmin, viewID, recordID)
			if err != nil {
				return err
			}
			if plan.Fields[0].Value != "Primeira nota" {
				t.Fatalf("CompileEditPlanTx (edição): %+v", plan.Fields[0])
			}
			return nil
		})

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			_, err := views.SubmitEditViewTx(ctx, tx, identity.RoleAdmin, viewID, recordID, recordVersion, map[string]any{"title": "Nota atualizada"})
			return err
		})
		err := run(context.Background(), func(ctx context.Context, tx database.Tx) error {
			_, err := views.SubmitEditViewTx(ctx, tx, identity.RoleAdmin, viewID, recordID, recordVersion, map[string]any{"title": "Nota conflitante"})
			return err
		})
		if !errors.Is(err, records.ErrVersionConflict) {
			t.Fatalf("SubmitEditViewTx com version obsoleta: got %v, want ErrVersionConflict", err)
		}
	})
}

// idAsFloat normaliza um valor de "id" devolvido por um registro — pgx
// devolve int32, o driver SQLite devolve int64, o valor genérico já
// decodificado de outbox/JSON pode chegar como float64; este helper de
// teste aceita os três, mesma disciplina defensiva de internal/views.idAsInt.
func idAsFloat(v any) float64 {
	switch n := v.(type) {
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}
