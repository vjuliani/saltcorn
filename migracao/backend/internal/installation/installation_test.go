package installation

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func testDatabase(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL administrativo descartável não configurado")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	must(t, err)
	name := "go032_" + RandomID()[:12]
	_, err = conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize())
	must(t, err)
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
		_ = conn.Close(ctx)
	})
	u, err := url.Parse(dsn)
	must(t, err)
	u.Path = "/" + name
	return u.String()
}
func newConfig(t *testing.T, driver string) (string, Config) {
	t.Helper()
	root := t.TempDir()
	c := Config{Format: 1, ID: RandomID(), Driver: driver, Tenant: "app", Secret: RandomID(), HTTPAddr: "127.0.0.1:3100", BackendAddr: "127.0.0.1:8090"}
	if driver == "postgres" {
		c.DatabaseURL = testDatabase(t)
	}
	must(t, PrepareDirs(root))
	must(t, Migrate(context.Background(), root, &c, "admin@example.com", "test-password-123456"))
	must(t, WriteJSON(filepath.Join(root, "instance.json"), c))
	return root, c
}
func TestInstallationRecoveryParity(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			root, c := newConfig(t, driver)
			must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
				table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "notes", metadata.TableOptions{})
				if err != nil {
					return err
				}
				if _, err = metadata.AddField(ctx, tx, identity.RoleAdmin, table.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText}); err != nil {
					return err
				}
				if err = tx.Exec(ctx, `INSERT INTO notes(title) VALUES ('offline draft')`); err != nil {
					return err
				}
				if err = tx.Exec(ctx, `INSERT INTO _sc_outbox(idempotency_key,event_type,payload_json) VALUES ('stable-id','pending','{}')`); err != nil {
					return err
				}
				if err = tx.Exec(ctx, `INSERT INTO _sc_idempotency_keys(key,payload_hash,result_json) VALUES ('stable-id','hash','{"id":1}')`); err != nil {
					return err
				}
				if err = tx.Exec(ctx, `DROP TABLE _sc_plugin_versions`); err != nil {
					return err
				}
				return tx.Exec(ctx, `DELETE FROM _sc_migrations WHERE version=2`)
			}))
			_, err := ConfigValue(ctx, root, c, "site_name", `"preserved"`, true)
			must(t, err)
			if Check(ctx, root, c) == nil {
				t.Fatal("old schema accepted without migrate")
			}
			must(t, Migrate(ctx, root, &c, "", ""))
			must(t, Migrate(ctx, root, &c, "", ""))
			must(t, Check(ctx, root, c))
			must(t, os.WriteFile(filepath.Join(root, "files", "report.txt"), []byte("file-content"), 0600))
			must(t, os.Mkdir(filepath.Join(root, "plugins", "test-plugin"), 0700))
			must(t, os.WriteFile(filepath.Join(root, "plugins", "test-plugin", "package.json"), []byte(`{"name":"test-plugin","version":"1.2.3"}`), 0600))
			must(t, os.WriteFile(filepath.Join(root, "plugins", "test-plugin", "index.js"), []byte("module.exports={}"), 0600))
			backup := filepath.Join(t.TempDir(), "backup")
			must(t, Backup(ctx, root, c, backup))
			restored := t.TempDir()
			targetDSN := ""
			if driver == "postgres" {
				targetDSN = testDatabase(t)
			}
			rc, err := Restore(ctx, restored, backup, targetDSN)
			must(t, err)
			must(t, Check(ctx, restored, rc))
			if rc.ID != c.ID || rc.Secret != c.Secret || rc.AdminID != c.AdminID {
				t.Fatal("configuration not preserved")
			}
			value, err := ConfigValue(ctx, restored, rc, "site_name", "", false)
			must(t, err)
			if value != `"preserved"` {
				t.Fatal(value)
			}
			data, err := os.ReadFile(filepath.Join(restored, "files", "report.txt"))
			must(t, err)
			if string(data) != "file-content" {
				t.Fatal("file lost")
			}
			inv, err := pluginInventory(restored)
			must(t, err)
			if !reflect.DeepEqual(inv, map[string]string{"test-plugin": "1.2.3"}) {
				t.Fatal(inv)
			}
			must(t, transaction(ctx, restored, rc, func(tx database.Tx, pg pgx.Tx) error {
				var title, key, version string
				var pending int
				if err := tx.QueryRow(ctx, `SELECT title FROM notes WHERE id=1`).Scan(&title); err != nil {
					return err
				}
				if title != "offline draft" {
					t.Fatal(title)
				}
				if err := tx.QueryRow(ctx, `SELECT idempotency_key FROM _sc_outbox WHERE status='pending'`).Scan(&key); err != nil {
					return err
				}
				if key != "stable-id" {
					t.Fatal(key)
				}
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_idempotency_keys WHERE key='stable-id'`).Scan(&pending); err != nil {
					return err
				}
				if pending != 1 {
					t.Fatal("idempotency lost")
				}
				if err := tx.QueryRow(ctx, `SELECT version FROM _sc_plugin_versions WHERE name='test-plugin'`).Scan(&version); err != nil {
					return err
				}
				if version != "1.2.3" {
					t.Fatal(version)
				}
				if pg != nil {
					u, err := identity.Authenticate(ctx, pg, "admin@example.com", "test-password-123456")
					if err != nil {
						return err
					}
					if int(u.RoleID) != 1 {
						t.Fatal("authorization lost")
					}
				}
				return tx.Exec(ctx, `INSERT INTO notes(title) VALUES ('after restore')`)
			}))
			if _, err = Restore(ctx, restored, backup, targetDSN); err == nil {
				t.Fatal("overwrite allowed")
			}
			if driver == "postgres" {
				if _, err = Restore(ctx, t.TempDir(), backup, targetDSN); err == nil {
					t.Fatal("nonempty database accepted")
				}
			}
			must(t, os.WriteFile(filepath.Join(backup, "files", "report.txt"), []byte("corrupted"), 0600))
			if _, err = Restore(ctx, t.TempDir(), backup, targetDSN); err == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
}
func TestMigrationRollbackAndFutureVersion(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
		return tx.Exec(ctx, `DELETE FROM _sc_migrations WHERE version=2`)
	}))
	if Migrate(ctx, root, &c, "", "") == nil {
		t.Fatal("migration should fail on conflicting table")
	}
	must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
		var v int
		if err := tx.QueryRow(ctx, `SELECT max(version) FROM _sc_migrations`).Scan(&v); err != nil {
			return err
		}
		if v != 1 {
			t.Fatal("partial journal commit")
		}
		return tx.Exec(ctx, `INSERT INTO _sc_migrations VALUES(99,'future')`)
	}))
	if Migrate(ctx, root, &c, "", "") == nil {
		t.Fatal("future schema accepted")
	}
}
func TestLocksAndInvalidBackup(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root)
	must(t, err)
	defer lock.Close()
	if other, err := Acquire(root); err == nil {
		other.Close()
		t.Fatal("concurrent lock")
	}
	ctx := context.Background()
	src, c := newConfig(t, "sqlite")
	must(t, os.Symlink("/etc/passwd", filepath.Join(src, "files", "bad")))
	if Backup(ctx, src, c, filepath.Join(t.TempDir(), "backup")) == nil {
		t.Fatal("symlink accepted")
	}
	must(t, os.Remove(filepath.Join(src, "files", "bad")))
	backup := filepath.Join(t.TempDir(), "backup")
	must(t, Backup(ctx, src, c, backup))
	var m Manifest
	b, err := os.ReadFile(filepath.Join(backup, "manifest.json"))
	must(t, err)
	must(t, json.Unmarshal(b, &m))
	m.Schema = 999
	must(t, WriteJSON(filepath.Join(backup, "manifest.json"), m))
	if _, err = Restore(ctx, t.TempDir(), backup, ""); err == nil {
		t.Fatal("future backup accepted")
	}
}
func TestPostgresLease(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "postgres")
	one, err := Acquire(root)
	must(t, err)
	defer one.Close()
	must(t, one.PostgresLease(ctx, c))
	two, err := Acquire(t.TempDir())
	must(t, err)
	defer two.Close()
	if two.PostgresLease(ctx, c) == nil {
		t.Fatal("same database concurrently managed")
	}
}

func TestRejectUnmanagedAndWrongIdentity(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error { return tx.Exec(ctx, `DROP TABLE _sc_instance`) }))
	if Migrate(ctx, root, &c, "admin@example.com", "test-password") == nil {
		t.Fatal("unmanaged database adopted")
	}
	root, c = newConfig(t, "sqlite")
	c.ID = RandomID()
	if Migrate(ctx, root, &c, "", "") == nil {
		t.Fatal("wrong instance adopted")
	}
}
func TestRejectRestoreInsideBackup(t *testing.T) {
	root, c := newConfig(t, "sqlite")
	backup := filepath.Join(t.TempDir(), "backup")
	must(t, Backup(context.Background(), root, c, backup))
	inside := filepath.Join(backup, "restored")
	must(t, os.Mkdir(inside, 0700))
	if _, err := Restore(context.Background(), inside, backup, ""); err == nil {
		t.Fatal("recursive restore accepted")
	}
}
func TestReleaseMismatch(t *testing.T) {
	dir := t.TempDir()
	must(t, WriteJSON(filepath.Join(dir, "release.json"), ReleaseManifest{Format: 1, Version: "future", Schema: 99}))
	if CheckRelease(dir) == nil {
		t.Fatal("mixed release accepted")
	}
}
