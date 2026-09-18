package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(testDir(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpen_RejeitaDiretorioInexistente(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nao-existe")); err == nil {
		t.Fatal("Open() com diretório inexistente deveria falhar, não silenciosamente criar nada")
	}
}

func TestWithTenant_CommitPersiste(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v text)")
	}); err != nil {
		t.Fatalf("criar tabela: %v", err)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "INSERT INTO t (v) VALUES ($1)", "olá")
	}); err != nil {
		t.Fatalf("inserir: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		var v string
		if err := tx.QueryRow(ctx, "SELECT v FROM t WHERE id = 1").Scan(&v); err != nil {
			return err
		}
		if v != "olá" {
			t.Errorf("v = %q, esperado %q", v, "olá")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ler de volta: %v", err)
	}
}

func TestWithTenant_ErroDesfazTransacao(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v text)")
	}); err != nil {
		t.Fatalf("criar tabela: %v", err)
	}

	errDeliberado := errors.New("erro deliberado do teste")
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		if err := tx.Exec(ctx, "INSERT INTO t (v) VALUES ($1)", "nunca deveria persistir"); err != nil {
			return err
		}
		return errDeliberado
	})
	if !errors.Is(err, errDeliberado) {
		t.Fatalf("err = %v, esperado errDeliberado", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("count = %d, esperado 0 (rollback deveria ter desfeito o insert)", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestWithTenant_TenantsDiferentesSaoArquivosDiferentes é o teste direto
// de isolamento "modo desktop" (GO-030): ao contrário do Postgres (schema
// dentro do MESMO banco), aqui o isolamento é o PRÓPRIO ARQUIVO — uma
// tabela criada no tenant A nunca existe fisicamente no arquivo do tenant B.
func TestWithTenant_TenantsDiferentesSaoArquivosDiferentes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenancy.Tenant("acme"), func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "CREATE TABLE only_in_acme (id INTEGER PRIMARY KEY)")
	}); err != nil {
		t.Fatalf("criar tabela em acme: %v", err)
	}

	err := db.WithTenant(ctx, tenancy.Tenant("beta"), func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "SELECT 1 FROM only_in_acme")
	})
	if err == nil {
		t.Fatal("tenant beta enxergou uma tabela criada só no tenant acme — vazamento entre arquivos")
	}

	pathAcme := db.FilePath(tenancy.Tenant("acme"))
	pathBeta := db.FilePath(tenancy.Tenant("beta"))
	if pathAcme == pathBeta {
		t.Fatalf("FilePath(acme) == FilePath(beta) == %q", pathAcme)
	}
	if _, err := os.Stat(pathAcme); err != nil {
		t.Errorf("arquivo do tenant acme não existe em %q: %v", pathAcme, err)
	}
}

// TestWithTenant_ConcorrenciaMesmoTenant_SerializaSemPerderEscrita é o
// teste do critério de aceite "concorrência ... verificada sem sintaxe
// exclusiva de PG": N goroutines incrementando um contador na MESMA linha
// do MESMO tenant, concorrentemente — SetMaxOpenConns(1) (db.go) garante
// que o *sql.DB do tenant serializa as transações entre si; o resultado
// final tem que ser EXATAMENTE N incrementos, nenhum perdido.
func TestWithTenant_ConcorrenciaMesmoTenant_SerializaSemPerderEscrita(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		if err := tx.Exec(ctx, "CREATE TABLE counter (id INTEGER PRIMARY KEY, n integer NOT NULL)"); err != nil {
			return err
		}
		return tx.Exec(ctx, "INSERT INTO counter (id, n) VALUES (1, 0)")
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
				var current int
				if err := tx.QueryRow(ctx, "SELECT n FROM counter WHERE id = 1").Scan(&current); err != nil {
					return err
				}
				return tx.Exec(ctx, "UPDATE counter SET n = $1 WHERE id = 1", current+1)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("incremento concorrente falhou: %v", err)
		}
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		var final int
		if err := tx.QueryRow(ctx, "SELECT n FROM counter WHERE id = 1").Scan(&final); err != nil {
			return err
		}
		if final != n {
			t.Errorf("contador final = %d, esperado %d — pelo menos um incremento concorrente foi perdido", final, n)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação final: %v", err)
	}
}

func TestWithTenant_QueryRow_SemLinhas_DevolveErrNoRowsNeutro(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY)")
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		var id int
		return tx.QueryRow(ctx, "SELECT id FROM t WHERE id = 999").Scan(&id)
	})
	if !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("err = %v, esperado database.ErrNoRows (mesmo sentinel neutro que o adapter Postgres usa)", err)
	}
}

func TestWithTenant_PanicDesfazTransacao(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		return tx.Exec(ctx, "CREATE TABLE panic_test (id INTEGER PRIMARY KEY)")
	}); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("panic do callback")
	func() {
		defer func() {
			if got := recover(); got != sentinel {
				t.Errorf("panic = %v", got)
			}
		}()
		_ = db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
			if err := tx.Exec(ctx, "INSERT INTO panic_test VALUES (1)"); err != nil {
				t.Fatal(err)
			}
			panic(sentinel)
		})
	}()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM panic_test").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("panic persistiu %d registros", count)
		}
		return tx.Exec(ctx, "INSERT INTO panic_test VALUES (2)")
	}); err != nil {
		t.Fatal(err)
	}
}
