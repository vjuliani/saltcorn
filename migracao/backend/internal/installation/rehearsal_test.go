package installation

// GO-034 is a laboratory rehearsal, NOT an importer for arbitrary legacy
// backups. Every database is created by testDatabase and removed by t.Cleanup.
// The relational source is synthetic; the Node legacy reader is not certified.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pack"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

type rehearsalSnapshot struct {
	SHA256 string                     `json:"sha256"`
	Tables map[string]json.RawMessage `json:"tables"`
}

// One repeatable-read transaction: counts, IDs and values belong to the same
// checkpoint. Fixed table/column allowlist deliberately excludes arbitrary SQL.
func rehearsalCapture(t *testing.T, dsn, schema string, managed bool) rehearsalSnapshot {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	must(t, err)
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	must(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SET LOCAL search_path = `+pgx.Identifier{schema}.Sanitize())
	must(t, err)
	queries := map[string]string{
		"authors": `SELECT id,name FROM authors ORDER BY id`,
		"books":   `SELECT id,title,author FROM books ORDER BY id`,
	}
	if managed {
		for _, name := range []string{"_sc_tables", "_sc_fields", "_sc_migrations", "_sc_config", "_sc_plugin_versions", "_sc_outbox", "_sc_idempotency_keys"} {
			queries[name] = `SELECT * FROM ` + pgx.Identifier{name}.Sanitize()
		}
	}
	result := rehearsalSnapshot{Tables: map[string]json.RawMessage{}}
	for name, query := range queries {
		var raw []byte
		// Sorting canonical jsonb also stabilizes metadata with non-numeric keys.
		must(t, tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb) FROM (`+query+`) r`).Scan(&raw))
		var value any
		must(t, json.Unmarshal(raw, &value))
		result.Tables[name], err = json.Marshal(value)
		must(t, err)
	}
	must(t, tx.Commit(ctx))
	data, err := json.Marshal(result.Tables)
	must(t, err)
	sum := sha256.Sum256(data)
	result.SHA256 = hex.EncodeToString(sum[:])
	return result
}

func rehearsalData(snapshot rehearsalSnapshot) map[string]json.RawMessage {
	return map[string]json.RawMessage{"authors": snapshot.Tables["authors"], "books": snapshot.Tables["books"]}
}

func TestMigrationPauseReconcileRecovery(t *testing.T) {
	ctx := context.Background()
	sourceDSN := testDatabase(t)
	source, err := pgx.Connect(ctx, sourceDSN)
	must(t, err)
	defer source.Close(ctx)
	_, err = source.Exec(ctx, `CREATE SCHEMA source;
 CREATE TABLE source.authors(id serial PRIMARY KEY,name text NOT NULL);
 CREATE TABLE source.books(id serial PRIMARY KEY,title text NOT NULL,author integer NOT NULL REFERENCES source.authors(id));
 INSERT INTO source.authors(id,name) VALUES(10,'Synthetic author A'),(40,'Synthetic author B');
 INSERT INTO source.books(id,title,author) VALUES(100,'Before migration',10),(300,'To delete after cutover',40);
 SELECT setval('source.authors_id_seq',40); SELECT setval('source.books_id_seq',300);`)
	must(t, err)
	baseline := rehearsalCapture(t, sourceDSN, "source", false)
	report := map[string]any{"format": 1, "fixture": "synthetic-authors-books-v1", "scope": "PostgreSQL/pack1; paused recovery, legacy traffic remains blocked", "release": Release, "schema": SchemaVersion, "rpo_target_lost_commits": 0, "rto_target_seconds": 60, "source": baseline, "legacy_traffic_allowed": false}
	checkpoint := func(phase string) {
		report["phase"] = phase
		report["updated_utc"] = time.Now().UTC().Format(time.RFC3339Nano)
		if dir := os.Getenv("SALTCORN_GO_REHEARSAL_OUTPUT"); dir != "" {
			// Runner reserves a NEW directory; no credentials or instance.json exported.
			b, e := json.MarshalIndent(report, "", "  ")
			must(t, e)
			tmp := filepath.Join(dir, "checkpoint.tmp")
			must(t, os.WriteFile(tmp, b, 0600))
			must(t, os.Rename(tmp, filepath.Join(dir, "checkpoint.json")))
		}
	}
	checkpoint("source_captured")
	root, c := newConfig(t, "postgres")
	db, err := database.Open(ctx, c.DatabaseURL)
	must(t, err)
	defer db.Close()
	txfn := func(fn func(pgx.Tx) error) error {
		return db.WithTenant(ctx, tenancy.Tenant(c.Tenant), func(_ context.Context, tx pgx.Tx) error { return fn(tx) })
	}
	p := pack.Pack{Version: 1, Tables: []pack.TablePack{
		{Name: "authors", MinRoleRead: 80, MinRoleWrite: 1, Fields: []pack.FieldPack{{Name: "name", Type: metadata.FieldText, Required: true}}},
		{Name: "books", MinRoleRead: 80, MinRoleWrite: 1, Fields: []pack.FieldPack{{Name: "title", Type: metadata.FieldText, Required: true}, {Name: "author", Type: metadata.FieldKey, Required: true, References: "authors"}}},
	}, Config: map[string]any{"site_name": "GO-034 synthetic copy"}}
	admin, err := Acquire(root)
	must(t, err)
	must(t, admin.PostgresLease(ctx, c))
	t.Cleanup(admin.Close)
	// Competing administrative processes/serve use the same DB lease even with
	// different installation paths. Never change ownership while one is alive.
	competitor, err := Acquire(t.TempDir())
	must(t, err)
	if competitor.PostgresLease(ctx, c) == nil {
		t.Fatal("second executor acquired lease")
	}
	competitor.Close()
	must(t, txfn(func(tx pgx.Tx) error { return pack.Import(ctx, tx, identity.RoleAdmin, p, nil) }))
	// Explicit reviewed mapping; not a claim of importing a complete legacy pack.
	must(t, txfn(func(tx pgx.Tx) error {
		for _, name := range []string{"authors", "books"} {
			query := `INSERT INTO authors(id,name) SELECT id,name FROM jsonb_to_recordset($1::jsonb) AS r(id int,name text)`
			if name == "books" {
				query = `INSERT INTO books(id,title,author) SELECT id,title,author FROM jsonb_to_recordset($1::jsonb) AS r(id int,title text,author int)`
			}
			if _, e := tx.Exec(ctx, query, baseline.Tables[name]); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),(SELECT max(id) FROM `+pgx.Identifier{name}.Sanitize()+`))`, name); e != nil {
				return e
			}
		}
		// Prepare schema-1 fixture BEFORE opening the rollback window.
		_, e := tx.Exec(ctx, `DROP TABLE _sc_plugin_versions; DROP TABLE _sc_metadata; DELETE FROM _sc_migrations WHERE version IN (2,3); ALTER TABLE books ADD COLUMN legacy_annotation text DEFAULT 'compatibility probe'`)
		return e
	}))
	if Check(ctx, root, c) == nil {
		t.Fatal("schema 1 accepted without upgrade")
	}
	// A DB-level guard for this rehearsal: contraction is prohibited throughout
	// the window; even an accidental direct DROP must roll back atomically.
	conn, err := pgx.Connect(ctx, c.DatabaseURL)
	must(t, err)
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `CREATE TABLE public.go034_window(open boolean NOT NULL); INSERT INTO public.go034_window VALUES(true);
 CREATE FUNCTION public.go034_reject_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$
 BEGIN IF (SELECT open FROM public.go034_window) THEN RAISE EXCEPTION 'GO034 rollback window forbids destructive DDL'; END IF; END $$;
 CREATE EVENT TRIGGER go034_no_drop ON sql_drop EXECUTE FUNCTION public.go034_reject_drop();`)
	must(t, err)
	must(t, Migrate(ctx, root, &c, "", ""))
	must(t, Migrate(ctx, root, &c, "", ""))
	must(t, Check(ctx, root, c))
	must(t, txfn(func(tx pgx.Tx) error {
		var value string
		return tx.QueryRow(ctx, `SELECT legacy_annotation FROM books WHERE id=100`).Scan(&value)
	}))
	if e := txfn(func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `ALTER TABLE books DROP COLUMN legacy_annotation`)
		return e
	}); e == nil {
		t.Fatal("contraction allowed in rollback window")
	}
	target := rehearsalCapture(t, c.DatabaseURL, c.Tenant, true)
	if !reflect.DeepEqual(rehearsalData(target), rehearsalData(baseline)) {
		t.Fatal("migration changed IDs/FKs/values")
	}
	var exported pack.Pack
	must(t, txfn(func(tx pgx.Tx) error {
		var e error
		exported, e = pack.Export(ctx, tx, identity.RoleAdmin, nil)
		return e
	}))
	if !reflect.DeepEqual(exported.Tables, p.Tables) || !reflect.DeepEqual(exported.Config, p.Config) {
		t.Fatal("pack definitions/config changed")
	}
	report["pack"] = exported
	report["migrated"] = target
	checkpoint("expanded_and_reconciled")
	must(t, os.WriteFile(filepath.Join(root, "files", "evidence.txt"), []byte("synthetic file before writes"), 0600))
	oldBackup := filepath.Join(t.TempDir(), "before-writes")
	must(t, Backup(ctx, root, c, oldBackup))
	admin.Close()
	// Runtime owns the lease. Guard models the real admission API used by HTTP
	// and jobs, not a substitute for stopping every replica in an actual wave.
	runtimeLock, err := Acquire(root)
	must(t, err)
	must(t, runtimeLock.PostgresLease(ctx, c))
	defer runtimeLock.Close()
	guard := cutover.NewGuard()
	must(t, cutover.LoadFromRegistry(ctx, db, guard))
	write := func(key string, apply func(pgx.Tx) error) error {
		done, e := guard.Begin(tenancy.Tenant(c.Tenant), "tables.records")
		if e != nil {
			return e
		}
		defer done()
		return txfn(func(tx pgx.Tx) error {
			_, _, e := outbox.Do(ctx, tx, key, map[string]any{"operation": key}, func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
				if e := apply(tx); e != nil {
					return nil, nil, e
				}
				return map[string]any{"confirmed": true}, []outbox.Event{{Type: "go034.pending", Payload: map[string]any{"key": key}}}, nil
			})
			return e
		})
	}
	must(t, write("go034-insert", func(tx pgx.Tx) error {
		r, e := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Created by Go", "author": 10}, nil)
		if e == nil && fmt.Sprint(r["id"]) != "301" {
			t.Fatalf("sequence not reconciled: %v", r["id"])
		}
		return e
	}))
	must(t, write("go034-update", func(tx pgx.Tx) error {
		var v string
		if e := tx.QueryRow(ctx, `SELECT xmin::text FROM books WHERE id=100`).Scan(&v); e != nil {
			return e
		}
		_, e := records.UpdateRecord(ctx, tx, identity.RoleAdmin, "books", 100, v, map[string]any{"title": "Updated by Go"}, nil)
		return e
	}))
	must(t, write("go034-delete", func(tx pgx.Tx) error {
		var v string
		if e := tx.QueryRow(ctx, `SELECT xmin::text FROM books WHERE id=300`).Scan(&v); e != nil {
			return e
		}
		return records.DeleteRecord(ctx, tx, identity.RoleAdmin, "books", 300, v, nil)
	}))
	expected := rehearsalCapture(t, c.DatabaseURL, c.Tenant, true)
	var books []struct {
		ID     int
		Title  string
		Author int
	}
	must(t, json.Unmarshal(expected.Tables["books"], &books))
	if len(books) != 2 {
		t.Fatalf("expected two surviving books, got %d", len(books))
	}
	wantTitles := map[int]string{100: "Updated by Go", 301: "Created by Go"}
	for _, b := range books {
		if b.Title != wantTitles[b.ID] || b.Author != 10 {
			t.Fatalf("Go write mismatch: %+v", b)
		}
	}
	count := func(snapshot rehearsalSnapshot, table string) int {
		var rows []json.RawMessage
		must(t, json.Unmarshal(snapshot.Tables[table], &rows))
		return len(rows)
	}
	if count(expected, "_sc_outbox") != 3 || count(expected, "_sc_idempotency_keys") != 3 {
		t.Fatal("confirmed effects/keys incomplete")
	}

	// Interrupted transaction must not leak a record, key or event.
	interrupted := errors.New("injected interruption before commit")
	if e := write("go034-aborted", func(tx pgx.Tx) error {
		_, e := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Must roll back", "author": 40}, nil)
		if e != nil {
			return e
		}
		return interrupted
	}); !errors.Is(e, interrupted) {
		t.Fatal(e)
	}
	if rehearsalCapture(t, c.DatabaseURL, c.Tenant, true).SHA256 != expected.SHA256 {
		t.Fatal("partial write survived rollback")
	}
	report["confirmed_go_writes"] = 3
	report["post_go"] = expected
	checkpoint("go_writes_confirmed")
	// Pause begins BEFORE waiting for in-flight work. A timeout must leave
	// admission closed; retry after the admitted operation finishes.
	pauseStart := time.Now()
	done, err := guard.Begin(tenancy.Tenant(c.Tenant), "tables.records")
	must(t, err)
	if e := cutover.SwitchOwner(ctx, db, guard, tenancy.Tenant(c.Tenant), "tables.records", cutover.OwnerLegacy, time.Millisecond); e == nil {
		t.Fatal("drain did not wait for in-flight work")
	}
	if _, e := guard.Begin(tenancy.Tenant(c.Tenant), "tables.records"); !errors.Is(e, cutover.ErrRouteDraining) {
		t.Fatal("admission reopened after timeout")
	}
	done()
	must(t, cutover.SwitchOwner(ctx, db, guard, tenancy.Tenant(c.Tenant), "tables.records", cutover.OwnerLegacy, time.Second))
	runtimeLock.Close()
	// OwnerLegacy is a durable Go fence, NOT permission to reopen legacy traffic.
	// The legacy process and edge remain stopped in this procedure.
	restarted := cutover.NewGuard()
	must(t, cutover.LoadFromRegistry(ctx, db, restarted))
	if _, e := restarted.Begin(tenancy.Tenant(c.Tenant), "tables.records"); !errors.Is(e, cutover.ErrNotOwner) {
		t.Fatal("restart lost durable Go fence")
	}
	admin, err = Acquire(root)
	must(t, err)
	defer admin.Close()
	must(t, admin.PostgresLease(ctx, c))
	report["ownership"] = "legacy registry; Go denied; edge and legacy stopped"
	checkpoint("paused_fenced")
	backup := filepath.Join(t.TempDir(), "after-writes")
	must(t, Backup(ctx, root, c, backup))
	checkpoint("recovery_backup_complete")
	restoredRoot := t.TempDir()
	restoredDSN := testDatabase(t)
	restored, err := Restore(ctx, restoredRoot, backup, restoredDSN)
	must(t, err)
	must(t, Check(ctx, restoredRoot, restored))
	recoveryLock, err := Acquire(restoredRoot)
	must(t, err)
	defer recoveryLock.Close()
	must(t, recoveryLock.PostgresLease(ctx, restored))
	recovered := rehearsalCapture(t, restoredDSN, c.Tenant, true)
	if recovered.SHA256 != expected.SHA256 {
		t.Fatal("RPO violation: final checkpoint mismatch")
	}
	data, err := os.ReadFile(filepath.Join(restoredRoot, "files", "evidence.txt"))
	must(t, err)
	if string(data) != "synthetic file before writes" {
		t.Fatal("file not recovered")
	}
	recoveryDB, err := database.Open(ctx, restoredDSN)
	must(t, err)
	defer recoveryDB.Close()
	// Lost response AFTER commit: retry reads the durable key; no duplicate
	// effect/event even after restore. Side effects remain pending, not sent.
	must(t, recoveryDB.WithTenant(ctx, tenancy.Tenant(c.Tenant), func(ctx context.Context, tx pgx.Tx) error {
		_, replayed, e := outbox.Do(ctx, tx, "go034-insert", map[string]any{"operation": "go034-insert"}, func(context.Context, pgx.Tx) (any, []outbox.Event, error) {
			t.Fatal("confirmed effect repeated after recovery")
			return nil, nil, nil
		})
		if e == nil && !replayed {
			t.Fatal("idempotency checkpoint lost")
		}
		return e
	}))
	if rehearsalCapture(t, restoredDSN, c.Tenant, true).SHA256 != expected.SHA256 {
		t.Fatal("replay modified recovered state")
	}
	// PostgreSQL sequences are nontransactional: the aborted insert consumed
	// 302. Restore must preserve that high-water mark, not merely max(id)=301.
	must(t, recoveryDB.WithTenant(ctx, tenancy.Tenant(c.Tenant), func(ctx context.Context, tx pgx.Tx) error {
		var last int64
		if e := tx.QueryRow(ctx, `SELECT last_value FROM books_id_seq`).Scan(&last); e != nil {
			return e
		}
		if last != 302 {
			t.Fatalf("sequence checkpoint: got %d, want 302", last)
		}
		return nil
	}))
	report["sequence_high_water_mark"] = 302
	report["row_counts"] = map[string]int{"source_authors": count(baseline, "authors"), "source_books": count(baseline, "books"), "recovered_authors": count(recovered, "authors"), "recovered_books": count(recovered, "books"), "outbox_events": count(recovered, "_sc_outbox"), "confirmed_keys": count(recovered, "_sc_idempotency_keys")}
	// Only the Go candidate is compatible and reconciled; legacy reader remains
	// uncertified. No actual gateway/production traffic is enabled by this test.
	candidateGuard := cutover.NewGuard()
	must(t, cutover.LoadFromRegistry(ctx, recoveryDB, candidateGuard))
	if _, e := candidateGuard.Begin(tenancy.Tenant(c.Tenant), "tables.records"); !errors.Is(e, cutover.ErrNotOwner) {
		t.Fatal("restored DB admitted writes before validation")
	}
	must(t, cutover.SwitchOwner(ctx, recoveryDB, candidateGuard, tenancy.Tenant(c.Tenant), "tables.records", cutover.OwnerGo, time.Second))
	complete, err := candidateGuard.Begin(tenancy.Tenant(c.Tenant), "tables.records")
	must(t, err)
	complete()
	elapsed := time.Since(pauseStart)
	if elapsed > 60*time.Second {
		t.Fatalf("RTO exceeded: %s", elapsed)
	}
	report["recovered"] = recovered
	report["rpo_lost_commits"] = 0
	report["rto_seconds"] = elapsed.Seconds()
	report["go_candidate_validated"] = true
	checkpoint("recovered_go_validated_legacy_blocked")
	// A stale pre-cutover backup looks healthy but loses three confirmed
	// operations. Therefore Check/health alone is insufficient for rollback.
	staleRoot := t.TempDir()
	staleDSN := testDatabase(t)
	stale, err := Restore(ctx, staleRoot, oldBackup, staleDSN)
	must(t, err)
	must(t, Check(ctx, staleRoot, stale))
	if rehearsalCapture(t, staleDSN, c.Tenant, true).SHA256 == expected.SHA256 {
		t.Fatal("stale checkpoint incorrectly considered current")
	}
	// Future schema and corrupted backup are independently rejected.
	rc, err := pgx.Connect(ctx, restoredDSN)
	must(t, err)
	defer rc.Close(ctx)
	tx, err := rc.Begin(ctx)
	must(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO app._sc_migrations VALUES(99,'future')`)
	must(t, err)
	must(t, tx.Commit(ctx))
	if Check(ctx, restoredRoot, restored) == nil {
		t.Fatal("future schema accepted")
	}
	_, err = rc.Exec(ctx, `DELETE FROM app._sc_migrations WHERE version=99`)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(backup, "files", "evidence.txt"), []byte("corrupt"), 0600))
	if _, e := Restore(ctx, t.TempDir(), backup, testDatabase(t)); e == nil {
		t.Fatal("corrupt recovery backup accepted")
	}
	// Contraction is rehearsed ONLY on recovered candidate after explicit window
	// closure. The source and the frozen primary remain untouched and retained.
	_, err = rc.Exec(ctx, `UPDATE public.go034_window SET open=false; ALTER TABLE app.books DROP COLUMN legacy_annotation`)
	must(t, err)
	if rehearsalCapture(t, restoredDSN, c.Tenant, true).SHA256 != expected.SHA256 {
		t.Fatal("contraction changed retained data")
	}
	if rehearsalCapture(t, sourceDSN, "source", false).SHA256 != baseline.SHA256 {
		t.Fatal("original source was modified")
	}
	report["negative_checks"] = []string{"concurrent_executor", "destructive_ddl_during_window", "drain_timeout", "write_during_pause", "restart_while_paused", "uncommitted_write", "stale_backup", "future_schema", "corrupt_backup", "idempotent_replay"}
	report["source_preserved"] = true
	report["contraction_only_after_window"] = true
	checkpoint("complete")
	t.Logf("GO034_REPORT RPO=0 RTO=%.3fs source=%s recovered=%s legacy_traffic_allowed=false", elapsed.Seconds(), baseline.SHA256, recovered.SHA256)
}
