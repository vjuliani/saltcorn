// Package installation implements offline administration of a dedicated Go
// installation. It never adopts a legacy database or performs a live cutover.
package installation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
)

const Release = "0.1.0-go046"
const SchemaVersion = 3

var tenantPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type Config struct {
	Format      int    `json:"format"`
	ID          string `json:"id"`
	Driver      string `json:"driver"`
	Tenant      string `json:"tenant"`
	DatabaseURL string `json:"database_url,omitempty"`
	Secret      string `json:"service_identity_secret"`
	AdminID     int    `json:"admin_id"`
	HTTPAddr    string `json:"http_addr"`
	BackendAddr string `json:"backend_addr"`
}

func RandomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func (c Config) Validate() error {
	if c.Format != 1 || !tenantPattern.MatchString(c.Tenant) || c.Tenant == "public" || c.Tenant == "information_schema" || strings.HasPrefix(c.Tenant, "pg_") || len(c.Secret) < 32 || len(c.ID) != 64 {
		return errors.New("installation: configuração inválida ou versão incompatível")
	}
	if c.Driver != "postgres" && c.Driver != "sqlite" {
		return errors.New("installation: driver deve ser postgres ou sqlite")
	}
	if c.Driver == "postgres" && c.DatabaseURL == "" {
		return errors.New("installation: DATABASE_URL obrigatória")
	}
	if c.Driver == "postgres" {
		u, err := url.Parse(c.DatabaseURL)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path == "" {
			return errors.New("installation: DSN deve ser URL PostgreSQL com host/banco explícitos")
		}
	}
	return nil
}
func Load(root string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(root, "instance.json"))
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func WriteJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// Lock serializes local CLI/serve operations; PostgresLease also serializes
// instances that point to the same dedicated database from different paths.
type Lock struct {
	file *os.File
	conn *pgx.Conn
}

func Acquire(root string) (*Lock, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".operation.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("installation: instância em uso; encerre serve antes de administrar")
	}
	return &Lock{file: f}, nil
}
func (l *Lock) PostgresLease(ctx context.Context, c Config) error {
	if c.Driver != "postgres" {
		return nil
	}
	conn, err := pgx.Connect(ctx, c.DatabaseURL)
	if err != nil {
		return errors.New("installation: não foi possível conectar ao PostgreSQL")
	}
	var ok bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(73032001)`).Scan(&ok)
	if err != nil || !ok {
		conn.Close(ctx)
		return errors.New("installation: banco em uso por outra operação/serve")
	}
	l.conn = conn
	return nil
}
func (l *Lock) Ping(ctx context.Context) error {
	if l.conn != nil {
		return l.conn.Ping(ctx)
	}
	return nil
}
func (l *Lock) Close() {
	if l.conn != nil {
		_ = l.conn.Close(context.Background())
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}
func PrepareDirs(root string) error {
	for _, p := range []string{"files", "plugins", "sqlite"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0700); err != nil {
			return err
		}
	}
	return nil
}
