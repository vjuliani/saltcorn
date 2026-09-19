package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

type Manifest struct {
	Format        int               `json:"format"`
	Compatibility int               `json:"compatibility"`
	Release       string            `json:"release"`
	Schema        int               `json:"schema"`
	Driver        string            `json:"driver"`
	Files         map[string]string `json:"sha256"`
	Plugins       map[string]string `json:"plugins"`
}

// copyTree accepts only directories and regular files; no symlinks, devices,
// FIFOs or executable hooks from plugins are followed/executed.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("installation: backup/plugins recusam links e arquivos especiais")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if err == nil {
			err = out.Sync()
		}
		e := out.Close()
		if err != nil {
			return err
		}
		return e
	})
}
func hashes(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("installation: arquivo especial no backup")
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "manifest.json" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return result, err
}
func pgTool(ctx context.Context, c Config, tool string, args ...string) error {
	cfg, err := pgx.ParseConfig(c.DatabaseURL)
	if err != nil {
		return errors.New("installation: DSN inválida")
	}
	// Credentials stay out of argv and error messages; inherit SSL options via
	// the libpq connection string environment rather than printing it.
	if tool == "pg_restore" {
		args = append([]string{"--dbname=" + cfg.Database}, args...)
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	uri, _ := url.Parse(c.DatabaseURL)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PG") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PGHOST="+cfg.Host, "PGPORT="+fmt.Sprint(cfg.Port), "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password, "PGDATABASE="+cfg.Database)
	for _, key := range []string{"sslmode", "sslrootcert", "sslcert", "sslkey"} {
		if value := uri.Query().Get(key); value != "" {
			cmd.Env = append(cmd.Env, "PG"+strings.ToUpper(key)+"="+value)
		}
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("installation: %s falhou: %w", tool, err)
	}
	return nil
}
func pluginInventory(root string) (map[string]string, error) {
	result := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, errors.New("installation: plugins devem ser diretórios")
		}
		b, err := os.ReadFile(filepath.Join(root, "plugins", entry.Name(), "package.json"))
		if err != nil {
			return nil, err
		}
		var p struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		if p.Name == "" || p.Version == "" || result[p.Name] != "" {
			return nil, errors.New("installation: nome/versão de plugin inválido ou duplicado")
		}
		result[p.Name] = p.Version
	}
	return result, nil
}
func SyncPlugins(ctx context.Context, root string, c Config) error {
	inv, err := pluginInventory(root)
	if err != nil {
		return err
	}
	// Inspect the complete tree first, including links in nested dependencies.
	if _, err = hashes(filepath.Join(root, "plugins")); err != nil {
		return err
	}
	return transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
		if err := tx.Exec(ctx, `DELETE FROM _sc_plugin_versions`); err != nil {
			return err
		}
		names := make([]string, 0, len(inv))
		for name := range inv {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := tx.Exec(ctx, `INSERT INTO _sc_plugin_versions(name,version) VALUES ($1,$2)`, name, inv[name]); err != nil {
				return err
			}
		}
		return nil
	})
}
func Backup(ctx context.Context, root string, c Config, dest string) error {
	if err := Check(ctx, root, c); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return errors.New("installation: destino do backup deve ser novo")
	}
	root, _ = filepath.Abs(root)
	dest, _ = filepath.Abs(dest)
	if dest == root || strings.HasPrefix(dest, root+string(os.PathSeparator)) {
		return errors.New("installation: backup deve ficar fora da instância")
	}
	if err := SyncPlugins(ctx, root, c); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, dir := range []string{"files", "plugins", "sqlite"} {
		if err = copyTree(filepath.Join(root, dir), filepath.Join(stage, dir)); err != nil {
			return err
		}
	}
	if err = WriteJSON(filepath.Join(stage, "instance.json"), c); err != nil {
		return err
	}
	if c.Driver == "postgres" {
		if err = pgTool(ctx, c, "pg_dump", "--format=custom", "--no-owner", "--no-acl", "--file="+filepath.Join(stage, "database.dump")); err != nil {
			return err
		}
	}
	h, err := hashes(stage)
	if err != nil {
		return err
	}
	inv, err := pluginInventory(stage)
	if err != nil {
		return err
	}
	m := Manifest{1, 1, Release, SchemaVersion, c.Driver, h, inv}
	if err = WriteJSON(filepath.Join(stage, "manifest.json"), m); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}

// Restore uses a private validated staging copy to avoid reading a changing
// backup while restoring. Existing data is never overwritten. pg_restore's
// single transaction rolls back the entire DB on error.
func Restore(ctx context.Context, root, source, dsn string) (Config, error) {
	var c Config
	root, _ = filepath.Abs(root)
	source, _ = filepath.Abs(source)
	if root == source || strings.HasPrefix(root, source+string(os.PathSeparator)) {
		return c, errors.New("installation: restore deve ficar fora do diretório de backup")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return c, err
	}
	for _, e := range entries {
		if e.Name() != ".operation.lock" {
			return c, errors.New("installation: restore exige diretório vazio")
		}
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".restore-*")
	if err != nil {
		return c, err
	}
	defer os.RemoveAll(stage)
	if err = copyTree(source, stage); err != nil {
		return c, err
	}
	b, err := os.ReadFile(filepath.Join(stage, "manifest.json"))
	if err != nil {
		return c, err
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return c, err
	}
	if m.Format != 1 || m.Compatibility != 1 || m.Schema < 1 || m.Schema > SchemaVersion {
		return c, errors.New("installation: backup incompatível")
	}
	h, err := hashes(stage)
	if err != nil {
		return c, err
	}
	if !reflect.DeepEqual(h, m.Files) {
		return c, errors.New("installation: integridade do backup inválida")
	}
	c, err = Load(stage)
	if err != nil {
		return c, err
	}
	if m.Driver != c.Driver {
		return c, errors.New("installation: driver divergente")
	}
	inv, err := pluginInventory(stage)
	if err != nil {
		return c, err
	}
	if !reflect.DeepEqual(inv, m.Plugins) {
		return c, errors.New("installation: versões de plugins divergentes")
	}
	for _, dir := range []string{"files", "plugins", "sqlite"} {
		info, e := os.Stat(filepath.Join(stage, dir))
		if e != nil {
			return c, e
		}
		if !info.IsDir() {
			return c, errors.New("installation: diretório obrigatório ausente no backup")
		}
	}
	if c.Driver == "postgres" {
		if dsn == "" {
			return c, errors.New("installation: restore PostgreSQL exige DSN de destino explícita")
		}
		c.DatabaseURL = dsn
		// Caller holds the filesystem lock; the destination DB lease is independent.
		lease := &Lock{}
		if err = lease.PostgresLease(ctx, c); err != nil {
			return c, err
		}
		defer lease.conn.Close(context.Background())
		var n int
		err = lease.conn.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','S','f')`).Scan(&n)
		if err != nil {
			return c, err
		}
		if n != 0 {
			return c, errors.New("installation: restore exige banco PostgreSQL vazio")
		}
		if err = pgTool(ctx, c, "pg_restore", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", filepath.Join(stage, "database.dump")); err != nil {
			return c, err
		}
	}
	// Install files before the instance config. A failed restore stays unusable;
	// retain the source backup and use a fresh destination for retry.
	for _, dir := range []string{"files", "plugins", "sqlite"} {
		if err = copyTree(filepath.Join(stage, dir), filepath.Join(root, dir)); err != nil {
			return c, err
		}
	}
	if err = Migrate(ctx, root, &c, "", ""); err != nil {
		return c, err
	}
	if err = Check(ctx, root, c); err != nil {
		return c, err
	}
	if err = SyncPlugins(ctx, root, c); err != nil {
		return c, err
	}
	if err = WriteJSON(filepath.Join(root, "instance.json"), c); err != nil {
		return c, err
	}
	return c, nil
}
func ConfigValue(ctx context.Context, root string, c Config, key, value string, write bool) (string, error) {
	var result string
	if key == "" {
		return "", errors.New("installation: chave obrigatória")
	}
	if write && !json.Valid([]byte(value)) {
		return "", errors.New("installation: valor deve ser JSON")
	}
	err := transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
		if write {
			return tx.Exec(ctx, `INSERT INTO _sc_config(key,value) VALUES ($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		}
		var b []byte
		if c.Driver == "sqlite" {
			return tx.QueryRow(ctx, `SELECT value FROM _sc_config WHERE key=$1`, key).Scan(&result)
		}
		if err := tx.QueryRow(ctx, `SELECT value FROM _sc_config WHERE key=$1`, key).Scan(&b); err != nil {
			return err
		}
		result = string(b)
		return nil
	})
	return result, err
}
