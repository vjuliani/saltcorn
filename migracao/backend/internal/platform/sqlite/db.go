// Package sqlite implementa o adapter SQLite (GO-030) — "modo desktop",
// distinto do Postgres multi-tenant de internal/platform/database: em vez
// de um pool único com schema por tenant (`SET LOCAL search_path`), cada
// tenant é um ARQUIVO SQLite PRÓPRIO, mesma decisão do legado
// (`packages/sqlite/sqlite.ts`: "um arquivo por tenant, sem pool" — ver
// matriz de capacidades GO-001 §2.2). SQLite recomenda um único escritor
// por arquivo (concorrência de escrita além disso serializa via
// `SQLITE_BUSY`/retry, não é um ganho real de paralelismo) — este pacote
// nunca abre mais de UMA conexão por arquivo de tenant, o oposto
// deliberado do pool multi-conexão do Postgres.
//
// Consome internal/platform/database.Tx (GO-030) — o mesmo contrato
// mínimo que internal/metadata já usa — em vez de reinventar uma
// interface própria: um pacote de domínio escrito contra database.Tx
// funciona contra este adapter OU contra database.AsTx(pgxTx) sem
// nenhuma mudança.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// DB é o adapter — um diretório onde cada tenant tem seu próprio arquivo
// `<tenant-sanitizado>.sqlite`, e uma conexão *sql.DB cacheada por tenant
// (aberta sob demanda, nunca compartilhada entre tenants).
type DB struct {
	dir string

	mu   sync.Mutex
	open map[string]*sql.DB
}

// Open valida (sem criar) o diretório onde os arquivos de tenant vivem —
// criar o diretório é responsabilidade de quem sobe o processo (mesmo
// espírito de internal/platform/files.LocalBackend, que também não cria
// seu próprio diretório raiz).
func Open(dir string) (*DB, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("sqlite: diretório de tenants %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sqlite: %q não é um diretório", dir)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &DB{dir: absDir, open: make(map[string]*sql.DB)}, nil
}

// Close fecha toda conexão de tenant ainda aberta.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	var firstErr error
	for _, conn := range db.open {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	db.open = make(map[string]*sql.DB)
	return firstErr
}

// FilePath devolve o caminho do arquivo SQLite de um tenant — exposto
// para ferramentas administrativas/backup (o equivalente a "um arquivo,
// uma cópia" do legado), nunca usado pelo caminho de leitura/escrita
// normal (que passa por WithTenant).
func (db *DB) FilePath(t tenancy.Tenant) string {
	return filepath.Join(db.dir, tenancy.SchemaName(t)+".sqlite")
}

// WithTenant abre (ou reusa) a conexão dedicada do tenant, inicia uma
// transação, executa fn, e commita ou desfaz conforme o resultado —
// mesmo contrato observável de database.DB.WithTenant (Postgres), exceto
// que aqui não existe "search_path" para definir: o isolamento entre
// tenants é o PRÓPRIO ARQUIVO, nunca uma instrução SQL que poderia ser
// esquecida ou executada na ordem errada.
func (db *DB) WithTenant(ctx context.Context, t tenancy.Tenant, fn func(ctx context.Context, tx database.Tx) error) (err error) {
	conn, err := db.connFor(t)
	if err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: iniciar transação: %w", err)
	}
	// Rollback também roda em panic/Goexit; commit só após retorno normal.
	defer tx.Rollback()
	if err := fn(ctx, Tx{tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// connFor mantém uma conexão por tenant em cada instância. BEGIN IMMEDIATE
// reserva o escritor antes de qualquer leitura; busy_timeout limita a espera
// entre instâncias/processos que compartilham o arquivo. Assim, metadata e
// outbox não dependem de advisory locks nem de SKIP LOCKED no SQLite.
func (db *DB) connFor(t tenancy.Tenant) (*sql.DB, error) {
	key := tenancy.SchemaName(t)

	db.mu.Lock()
	defer db.mu.Unlock()
	if conn, ok := db.open[key]; ok {
		return conn, nil
	}

	path := filepath.Join(db.dir, key+".sqlite")
	uri := url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Set("_txlock", "immediate")
	uri.RawQuery = query.Encode()
	conn, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite: abrir %q: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite: abrir %q: %w", path, err)
	}
	db.open[key] = conn
	return conn, nil
}

// translateErr normaliza sql.ErrNoRows para database.ErrNoRows — mesmo
// contrato de erro neutro de dialeto que o adapter Postgres
// (database.AsTx) já cumpre.
func translateErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return database.ErrNoRows
	}
	return err
}
