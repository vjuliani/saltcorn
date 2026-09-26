package installation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	appconfig "github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/files"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/library"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/realtime"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/tags"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/workflow"
)

func transaction(ctx context.Context, root string, c Config, fn func(database.Tx, pgx.Tx) error) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Driver == "sqlite" {
		db, err := sqlite.Open(filepath.Join(root, "sqlite"))
		if err != nil {
			return err
		}
		defer db.Close()
		return db.WithTenant(ctx, tenancy.Tenant(c.Tenant), func(ctx context.Context, tx database.Tx) error { return fn(tx, nil) })
	}
	db, err := database.Open(ctx, c.DatabaseURL)
	if err != nil {
		return errors.New("installation: conexão PostgreSQL falhou")
	}
	defer db.Close()
	return db.WithTenant(ctx, tenancy.Tenant(c.Tenant), func(ctx context.Context, tx pgx.Tx) error { return fn(database.AsTx(tx), tx) })
}

// Migrate is additive and atomic. Unmanaged schemas and future versions are
// rejected before domain DDL; no legacy table is silently adopted.
func Migrate(ctx context.Context, root string, c *Config, email, password string) error {
	return transaction(ctx, root, *c, func(tx database.Tx, pg pgx.Tx) error {
		if pg != nil {
			if err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(73032002)`); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, c.Tenant).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				if email == "" {
					return errors.New("installation: tenant ausente")
				}
				if err := tx.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{c.Tenant}.Sanitize()); err != nil {
					return err
				}
			}
			// WithTenant sets search_path before CREATE SCHEMA; it becomes effective now.
		}
		var managed bool
		query := `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='_sc_instance')`
		if pg != nil {
			query = `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name='_sc_instance')`
		}
		var err error
		if pg != nil {
			err = tx.QueryRow(ctx, query, c.Tenant).Scan(&managed)
		} else {
			err = tx.QueryRow(ctx, query).Scan(&managed)
		}
		if err != nil {
			return err
		}
		if !managed {
			var n int
			if pg != nil {
				err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','S','f')`).Scan(&n)
			} else {
				err = tx.QueryRow(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
			}
			if err != nil {
				return err
			}
			if n != 0 || email == "" {
				return errors.New("installation: banco não gerenciado; importar legado exige migração dedicada")
			}
			if err = tx.Exec(ctx, `CREATE TABLE _sc_instance (id text PRIMARY KEY)`); err != nil {
				return err
			}
			if err = tx.Exec(ctx, `INSERT INTO _sc_instance(id) VALUES ($1)`, c.ID); err != nil {
				return err
			}
			if err = tx.Exec(ctx, `CREATE TABLE _sc_migrations (version integer PRIMARY KEY, name text NOT NULL)`); err != nil {
				return err
			}
		}
		var id string
		if err = tx.QueryRow(ctx, `SELECT id FROM _sc_instance`).Scan(&id); err != nil {
			return err
		}
		if id != c.ID {
			return errors.New("installation: identidade do banco diverge da instância")
		}
		var version int
		if err = tx.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM _sc_migrations`).Scan(&version); err != nil {
			return err
		}
		if version > SchemaVersion {
			return errors.New("installation: schema mais novo; downgrade recusado")
		}
		if version < 1 {
			if err = metadata.EnsureSchema(ctx, tx); err != nil {
				return err
			}
			if err = outbox.EnsureSchemaTx(ctx, tx); err != nil {
				return err
			}
			if pg != nil {
				for _, ensure := range []func(context.Context, pgx.Tx) error{identity.EnsureSchema, views.EnsureSchema, triggers.EnsureSchema, scheduler.EnsureSchema, workflow.EnsureSchema, files.EnsureSchema, notify.EnsureSchema, realtime.EnsureSchema, library.EnsureSchema, appconfig.EnsureSchema, tags.EnsureSchema} {
					if err = ensure(ctx, pg); err != nil {
						return err
					}
				}
				hash, e := identity.HashPassword(password)
				if e != nil {
					return e
				}
				c.AdminID, err = identity.CreateUser(ctx, pg, email, hash, identity.RoleAdmin)
				if err != nil {
					return err
				}
				// Only a NEW instance assigns ownership. Upgrades never change it.
				if err = tx.Exec(ctx, `SET LOCAL search_path = public`); err != nil {
					return err
				}
				if err = cutover.EnsureSchema(ctx, pg); err != nil {
					return err
				}
				for _, cap := range []string{"tables.records", "tables.schema", "tables.views", "realtime.events"} {
					if err = cutover.SetOwner(ctx, pg, tenancy.Tenant(c.Tenant), cap, cutover.OwnerGo); err != nil {
						return err
					}
				}
				if err = tx.Exec(ctx, `SET LOCAL search_path = `+pgx.Identifier{c.Tenant}.Sanitize()); err != nil {
					return err
				}
			} else {
				if err = tx.Exec(ctx, `CREATE TABLE _sc_config (key text PRIMARY KEY,value text NOT NULL)`); err != nil {
					return err
				}
			}
			if err = tx.Exec(ctx, `INSERT INTO _sc_migrations VALUES (1,'initial-catalog')`); err != nil {
				return err
			}
		}
		if pg != nil && c.AdminID == 0 && email != "" {
			u, err := identity.FindUserByEmail(ctx, pg, email)
			if err != nil {
				return err
			}
			c.AdminID = u.ID
		}
		if version < 2 {
			if err = tx.Exec(ctx, `CREATE TABLE _sc_plugin_versions (name text PRIMARY KEY,version text NOT NULL)`); err != nil {
				return err
			}
			if err = tx.Exec(ctx, `INSERT INTO _sc_migrations VALUES (2,'plugin-inventory')`); err != nil {
				return err
			}
		}
		if version < 3 {
			if err = EnsureMetadataSchema(ctx, tx); err != nil {
				return err
			}
			if err = tx.Exec(ctx, `INSERT INTO _sc_migrations VALUES (3,'instance-metadata')`); err != nil {
				return err
			}
		}
		if _, err = WriteMetadata(ctx, tx, "core_version", "core_version", nil, map[string]any{"release": Release, "schema": SchemaVersion}); err != nil {
			return err
		}
		return nil
	})
}

func Check(ctx context.Context, root string, c Config) error {
	return transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
		var id string
		var version int
		if err := tx.QueryRow(ctx, `SELECT id FROM _sc_instance`).Scan(&id); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM _sc_migrations`).Scan(&version); err != nil {
			return err
		}
		if id != c.ID || version != SchemaVersion {
			return fmt.Errorf("installation: identidade/schema incompatível (schema %d, esperado %d); execute migrate", version, SchemaVersion)
		}
		return nil
	})
}
