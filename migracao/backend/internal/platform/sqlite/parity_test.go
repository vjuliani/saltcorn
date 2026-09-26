package sqlite_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/files"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
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
				if err := views.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				if err := config.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				if err := files.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				if err := notify.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				if err := triggers.EnsureSchemaTx(ctx, tx); err != nil {
					return err
				}
				return scheduler.EnsureSchemaTx(ctx, tx)
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

// TestParityTriggers prova o núcleo de GO-055 nos dois dialetos: um
// trigger Insert nativo disparado por HooksForTx dentro da MESMA escrita
// de registro (CreateRecordTx), e um trigger de evento nomeado disparado
// por EmitEventTx — mesmas garantias já provadas só contra Postgres em
// internal/triggers, agora também contra SQLite.
func TestParityTriggers(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		var tableID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "posts", metadata.TableOptions{})
			if err != nil {
				return err
			}
			tableID = table.ID
			_, err = metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
			return err
		})

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			_, err := triggers.CreateTriggerTx(ctx, tx, triggers.Trigger{TableID: tableID, When: triggers.WhenInsert, Action: "mark"})
			return err
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			_, err := triggers.CreateTriggerTx(ctx, tx, triggers.Trigger{When: "MyEvent", Action: "mark"})
			return err
		})

		var fired []string
		d := &triggers.Dispatcher{Actions: map[string]triggers.ActionFuncTx{
			"mark": func(ctx context.Context, tx database.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
				fired = append(fired, fmt.Sprint(row["title"]))
				return nil
			},
		}}

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			hooks := d.HooksForTx("parity", identity.RoleAdmin, nil)
			_, err := records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "via insert"}, hooks)
			return err
		})
		if len(fired) != 1 || fired[0] != "via insert" {
			t.Fatalf("HooksForTx/AfterInsert não disparou: %v", fired)
		}

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			n, err := d.EmitEventTx(ctx, tx, "parity", identity.RoleAdmin, "MyEvent", nil, map[string]any{"title": "via evento"})
			if err != nil {
				return err
			}
			if n != 1 {
				t.Fatalf("EmitEventTx: fired=%d, esperado 1", n)
			}
			return nil
		})
		if len(fired) != 2 || fired[1] != "via evento" {
			t.Fatalf("EmitEventTx não disparou o trigger de evento nomeado: %v", fired)
		}

		// AfterCommit nunca dispara sincronamente — só enfileira em
		// _sc_outbox, mesma garantia de TestAfterCommit_EnqueuesOutboxEvent_
		// NeverRunsSynchronously (Postgres-only, internal/triggers). Ação
		// com NOME PRÓPRIO ("mark_deferred"), nunca reaproveitando "mark" —
		// o trigger Insert síncrono já registrado acima continua disparando
		// para toda escrita nesta tabela; misturar os dois na mesma ação
		// tornaria impossível distinguir "a ação síncrona rodou de novo" de
		// "a ação adiada rodou fora de hora".
		deferredFired := false
		d.Actions["mark_deferred"] = func(ctx context.Context, tx database.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			deferredFired = true
			return nil
		}
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			_, err := triggers.CreateTriggerTx(ctx, tx, triggers.Trigger{TableID: tableID, When: triggers.WhenInsert, Action: "mark_deferred", AfterCommit: true})
			return err
		})
		syncedBefore := len(fired)
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			hooks := d.HooksForTx("parity", identity.RoleAdmin, nil)
			_, err := records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "adiado"}, hooks)
			return err
		})
		if deferredFired {
			t.Fatal("ação AfterCommit disparou sincronamente")
		}
		if len(fired) != syncedBefore+1 {
			t.Fatalf("trigger síncrono pré-existente não disparou para o novo registro: %v", fired)
		}
	})
}

// TestParityScheduler prova RunDueTx e a savepoint portável (SAVEPOINT/
// ROLLBACK TO/RELEASE via SQL cru, GO-055) nos dois dialetos: uma ação
// que falha não aborta o lote nem impede o avanço de next_run_at das
// demais — mesma garantia já provada só contra Postgres em
// internal/scheduler.
func TestParityScheduler(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		var okID, failID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			st, err := scheduler.CreateScheduledTriggerTx(ctx, tx, "ok-trigger", "increment", "* * * * *", "")
			if err != nil {
				return err
			}
			okID = st.ID
			st, err = scheduler.CreateScheduledTriggerTx(ctx, tx, "fail-trigger", "boom", "* * * * *", "")
			failID = st.ID
			return err
		})

		called := 0
		d := &scheduler.Dispatcher{Actions: map[string]scheduler.ActionFuncTx{
			"increment": func(ctx context.Context, tx database.Tx) error { called++; return nil },
			"boom":      func(ctx context.Context, tx database.Tx) error { return errors.New("falha proposital") },
		}}

		far := time.Now().Add(time.Hour)
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			ran, failed, err := d.RunDueTx(ctx, tx, far)
			if err != nil {
				return err
			}
			if ran != 1 || failed != 1 {
				t.Fatalf("RunDueTx: ran=%d failed=%d, esperado ran=1 failed=1", ran, failed)
			}
			return nil
		})
		if called != 1 {
			t.Fatalf("ação increment chamada %d vezes, esperado 1", called)
		}

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			all, err := scheduler.ListAllTx(ctx, tx)
			if err != nil {
				return err
			}
			byID := map[int]scheduler.ScheduledTrigger{}
			for _, st := range all {
				byID[st.ID] = st
			}
			if byID[okID].LastError != "" {
				t.Fatalf("trigger ok com last_error: %q", byID[okID].LastError)
			}
			if byID[failID].LastError == "" {
				t.Fatal("trigger com falha sem last_error registrado")
			}
			if !byID[okID].NextRunAt.After(far) || !byID[failID].NextRunAt.After(far) {
				t.Fatalf("next_run_at não avançou para os dois: ok=%v fail=%v", byID[okID].NextRunAt, byID[failID].NextRunAt)
			}
			return nil
		})
	})
}

// minimalFakeSMTP aceita uma única conexão e captura o corpo DATA —
// SendEmail (ao contrário de SendWebhook) não faz checagem de SSRF, então
// um listener TCP puro em loopback já basta, sem precisar de um resolver
// falso (compare com internal/notify/webhook_test.go, que precisa de um
// para SendWebhook).
func minimalFakeSMTP(t *testing.T) (host string, port int, body *strings.Builder, mu *sync.Mutex) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	body = &strings.Builder{}
	mu = &sync.Mutex{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 fake.smtp ESMTP\r\n")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if inData {
				if trimmed == "." {
					inData = false
					fmt.Fprint(conn, "250 OK\r\n")
					continue
				}
				mu.Lock()
				body.WriteString(line)
				mu.Unlock()
				continue
			}
			upper := strings.ToUpper(trimmed)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				fmt.Fprint(conn, "250 fake.smtp\r\n")
			case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
				fmt.Fprint(conn, "250 OK\r\n")
			case upper == "DATA":
				inData = true
				fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
			case upper == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, body, mu
}

// TestParityNotify prova o enfileiramento e a entrega de e-mail via
// outbox (EnqueueEmailTx/HandlerTx/outbox.ProcessPendingTx) e as
// notificações in-app somente-leitura (MarkReadTx/ListForUserTx) nos
// dois dialetos — Create (que publica em internal/realtime, ainda
// pgx.Tx-only) fica deliberadamente fora, ver GO-055.md.
func TestParityNotify(t *testing.T) {
	adapters(t, func(t *testing.T, run, other runner) {
		host, port, body, mu := minimalFakeSMTP(t)

		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return notify.EnqueueEmailTx(ctx, tx, "parity-email-1", notify.EmailMessage{
				To: []string{"dest@example.com"}, Subject: "assunto", Body: "texto", HTMLBody: "<b>html</b>",
			})
		})
		// Repetição deliberada da MESMA chave — nunca duplica o evento.
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return notify.EnqueueEmailTx(ctx, tx, "parity-email-1", notify.EmailMessage{
				To: []string{"dest@example.com"}, Subject: "assunto", Body: "texto", HTMLBody: "<b>html</b>",
			})
		})

		handler := notify.HandlerTx(notify.SMTPConfig{Host: host, Port: port, From: "remetente@example.com"}, nil, nil)
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			processed, failed, err := outbox.ProcessPendingTx(ctx, tx, 10, 5, handler)
			if err != nil {
				return err
			}
			if processed != 1 || failed != 0 {
				t.Fatalf("ProcessPendingTx: processed=%d failed=%d, esperado 1/0 (idempotência)", processed, failed)
			}
			return nil
		})

		mu.Lock()
		delivered := body.String()
		mu.Unlock()
		if !strings.Contains(delivered, "<b>html</b>") || !strings.Contains(delivered, "multipart/alternative") {
			t.Fatalf("HTMLBody não entregue via outbox: %q", delivered)
		}

		var notificationID int
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return tx.QueryRow(ctx,
				`INSERT INTO _sc_notifications (user_id, title, body, link) VALUES ($1, $2, $3, $4) RETURNING id`,
				1, "titulo", "corpo", "",
			).Scan(&notificationID)
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			list, err := notify.ListForUserTx(ctx, tx, 1, true, 10)
			if err != nil {
				return err
			}
			if len(list) != 1 || list[0].ID != notificationID {
				t.Fatalf("ListForUserTx (não lidas): %+v", list)
			}
			return nil
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			return notify.MarkReadTx(ctx, tx, notificationID)
		})
		mustRun(t, run, func(ctx context.Context, tx database.Tx) error {
			list, err := notify.ListForUserTx(ctx, tx, 1, true, 10)
			if err != nil {
				return err
			}
			if len(list) != 0 {
				t.Fatalf("ListForUserTx após MarkReadTx ainda lista como não lida: %+v", list)
			}
			return nil
		})
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
