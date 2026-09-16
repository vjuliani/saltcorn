// Testes deste arquivo exigem Postgres real — pulam automaticamente
// (t.Skip) se SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Ver
// migracao/backend/README.md para como preparar um banco de teste
// descartável e os schemas `acme`/`beta` com a tabela `probe` que estes
// testes esperam.
package database

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	return dsn
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), testDSN(t))
	if err != nil {
		t.Fatalf("Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func probeMarker(ctx context.Context, tx pgx.Tx) (string, error) {
	var marker string
	err := tx.QueryRow(ctx, "SELECT marker FROM probe LIMIT 1").Scan(&marker)
	return marker, err
}

func TestWithTenant_IsolatesDataBetweenTenants(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var acmeMarker, betaMarker string
	if err := db.WithTenant(ctx, tenancy.Tenant("acme"), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		acmeMarker, err = probeMarker(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("WithTenant(acme) erro inesperado: %v", err)
	}
	if acmeMarker != "acme-secret" {
		t.Errorf("marker do tenant acme = %q, esperado acme-secret", acmeMarker)
	}

	if err := db.WithTenant(ctx, tenancy.Tenant("beta"), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		betaMarker, err = probeMarker(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("WithTenant(beta) erro inesperado: %v", err)
	}
	if betaMarker != "beta-secret" {
		t.Errorf("marker do tenant beta = %q, esperado beta-secret", betaMarker)
	}
}

// TestWithTenant_ConcurrentReuseNoLeak é o teste central do critério de
// aceite desta tarefa: um pool deliberadamente pequeno (menor que o número
// de chamadas concorrentes) força a mesma conexão física a ser reaproveitada
// entre tenants diferentes. Se o isolamento por SET LOCAL search_path
// vazasse (por exemplo, se o código usasse SET sem LOCAL por engano), esta
// checagem pegaria uma conexão respondendo com o marker errado.
func TestWithTenant_ConcurrentReuseNoLeak(t *testing.T) {
	dsn := testDSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 3 // pequeno de propósito — bem menor que os 60 workers abaixo
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	defer pool.Close()
	db := &DB{pool: pool}

	const workers = 60
	const perWorker = 20
	expected := map[tenancy.Tenant]string{
		"acme": "acme-secret",
		"beta": "beta-secret",
	}
	tenants := []tenancy.Tenant{"acme", "beta"}

	var wg sync.WaitGroup
	mismatches := make(chan string, workers*perWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenant := tenants[w%len(tenants)]
			for i := 0; i < perWorker; i++ {
				var marker string
				err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					marker, err = probeMarker(ctx, tx)
					return err
				})
				if err != nil {
					mismatches <- "erro inesperado: " + err.Error()
					continue
				}
				if marker != expected[tenant] {
					mismatches <- "tenant " + string(tenant) + " viu marker " + marker + " (vazamento de schema entre conexões pooled)"
				}
			}
		}(w)
	}
	wg.Wait()
	close(mismatches)

	var count int
	for m := range mismatches {
		t.Error(m)
		count++
		if count > 10 {
			t.Fatal("muitos vazamentos, parando de listar")
		}
	}
}

// TestWithTenant_RollbackDiscardsWrites confirma que um erro retornado por
// fn desfaz toda a transação — nenhuma escrita parcial persiste.
func TestWithTenant_RollbackDiscardsWrites(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	sentinelErr := errors.New("erro deliberado para forçar rollback")
	err := db.WithTenant(ctx, tenancy.Tenant("acme"), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO probe (marker) VALUES ('deveria-sumir-no-rollback')"); err != nil {
			return err
		}
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("WithTenant erro = %v, esperado sentinelErr", err)
	}

	if err := db.WithTenant(ctx, tenancy.Tenant("acme"), func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM probe WHERE marker = 'deveria-sumir-no-rollback'").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("count = %d após rollback, esperado 0 — escrita parcial persistiu", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant (verificação) erro inesperado: %v", err)
	}
}

// TestWithTenant_DoesNotLeakSearchPathToNextConnectionUse é o teste que
// realmente prova por que `SET LOCAL` (não `SET`) é obrigatório aqui: em vez
// de passar sempre por WithTenant (que reafirma o próprio search_path a cada
// chamada, mascarando um vazamento de sessão), este teste faz um COMMIT bem
// sucedido via WithTenant e depois adquire uma conexão *diretamente* do
// mesmo pool (pool de 1 conexão só, garantindo ser a mesma conexão física) e
// verifica `SHOW search_path` sem passar por WithTenant de novo. Com `SET`
// (sem LOCAL) esse valor persistiria após o commit e vazaria para este uso
// direto da conexão — reproduzido manualmente durante o desenvolvimento
// desta tarefa (ver docs/migracao-go/execucoes/GO-007.md) trocando
// temporariamente `SET LOCAL` por `SET` e confirmando que só este teste
// específico (não o de reuso concorrente) pega a regressão.
func TestWithTenant_DoesNotLeakSearchPathToNextConnectionUse(t *testing.T) {
	dsn := testDSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	defer pool.Close()
	db := &DB{pool: pool}

	if err := db.WithTenant(context.Background(), tenancy.Tenant("acme"), func(ctx context.Context, tx pgx.Tx) error {
		_, err := probeMarker(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("WithTenant(acme) erro inesperado: %v", err)
	}

	// Bypassa WithTenant de propósito: pega a conexão crua (mesma física,
	// pool de tamanho 1) e olha o search_path que ela carrega agora.
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	var searchPath string
	if err := conn.QueryRow(context.Background(), "SHOW search_path").Scan(&searchPath); err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}
	if searchPath == "acme" {
		t.Fatalf("search_path = %q após o commit de WithTenant(acme) — vazou da transação para a sessão da conexão pooled (SET sem LOCAL)", searchPath)
	}
}

// TestWithTenant_CancellationRollsBackWithoutLeaking cobre "cancelamento"
// do critério de aceite: um contexto cancelado no meio de uma operação lenta
// precisa resultar em rollback, e a conexão devolvida ao pool não pode ficar
// com search_path residual — verificado fazendo, logo em seguida, uma
// chamada bem-sucedida para um tenant diferente sobre o mesmo pool pequeno
// (MaxConns=1, forçando reutilização da mesmíssima conexão física).
func TestWithTenant_CancellationRollsBackWithoutLeaking(t *testing.T) {
	dsn := testDSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1 // força a MESMA conexão física na segunda chamada
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	defer pool.Close()
	db := &DB{pool: pool}

	cancelCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = db.WithTenant(cancelCtx, tenancy.Tenant("acme"), func(ctx context.Context, tx pgx.Tx) error {
		// pg_sleep bem mais longo que o timeout do contexto — a query deve
		// ser cancelada pelo servidor antes de terminar.
		_, err := tx.Exec(ctx, "SELECT pg_sleep(5)")
		return err
	})
	if err == nil {
		t.Fatal("esperava erro por cancelamento de contexto, obteve nil")
	}

	// Conexão física reaproveitada (pool com 1 conexão só) para um tenant
	// DIFERENTE — se o search_path do cancelamento anterior tivesse vazado,
	// esta chamada veria o marker errado ou falharia.
	var marker string
	err = db.WithTenant(context.Background(), tenancy.Tenant("beta"), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		marker, err = probeMarker(ctx, tx)
		return err
	})
	if err != nil {
		t.Fatalf("WithTenant(beta) após cancelamento anterior: erro inesperado: %v", err)
	}
	if marker != "beta-secret" {
		t.Errorf("marker = %q após reuso de conexão cancelada, esperado beta-secret — vazamento de search_path", marker)
	}
}
