// Corpus de lease exigido pelo critério de aceite de GO-025: "dois
// workers não executam simultaneamente job exclusivo; reinício e
// expiração de lease têm comportamento testado."
package lease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func sanitizeForSchema(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

func leaseFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("lease_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
	})

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return db, tenant
}

func TestAcquire_ExclusiveWhileValid(t *testing.T) {
	db, tenant := leaseFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-a", time.Minute)
	}); err != nil {
		t.Fatalf("Acquire (worker-a): %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-b", time.Minute)
	})
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("Acquire (worker-b) = %v, esperado ErrLeaseHeld — o lease de worker-a ainda é válido", err)
	}
}

func TestAcquire_ExpiredLeaseCanBeReacquired(t *testing.T) {
	db, tenant := leaseFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-a", 50*time.Millisecond)
	}); err != nil {
		t.Fatalf("Acquire (worker-a): %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-b", time.Minute)
	}); err != nil {
		t.Fatalf("Acquire (worker-b) após expiração: %v, esperado sucesso", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		owner, err := HeldBy(ctx, tx, "scheduler")
		if err != nil {
			return err
		}
		if owner != "worker-b" {
			t.Errorf("HeldBy = %q, esperado \"worker-b\"", owner)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestAcquire_SameOwnerRenews(t *testing.T) {
	db, tenant := leaseFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-a", 100*time.Millisecond)
	}); err != nil {
		t.Fatalf("Acquire inicial: %v", err)
	}

	// Renovação pelo MESMO owner antes de expirar — sucede mesmo com o
	// lease ainda válido, porque owner_id bate.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-a", time.Minute)
	}); err != nil {
		t.Fatalf("Acquire (renovação): %v, esperado sucesso", err)
	}

	// Passado o TTL original (mas não o renovado), worker-b ainda NÃO
	// consegue adquirir — a renovação realmente estendeu a expiração.
	time.Sleep(150 * time.Millisecond)
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-b", time.Minute)
	})
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("Acquire (worker-b) = %v, esperado ErrLeaseHeld — a renovação deveria ter estendido a expiração", err)
	}
}

func TestRelease_OnlyByOwner(t *testing.T) {
	db, tenant := leaseFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-a", time.Minute)
	}); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// worker-b tenta liberar o lease de worker-a — nunca deveria conseguir.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Release(ctx, tx, "scheduler", "worker-b")
	}); err != nil {
		t.Fatalf("Release (worker-b): %v", err)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		owner, err := HeldBy(ctx, tx, "scheduler")
		if err != nil {
			return err
		}
		if owner != "worker-a" {
			t.Errorf("HeldBy após Release de worker-b = %q, esperado \"worker-a\" (nunca deveria ter liberado)", owner)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}

	// worker-a libera o próprio lease — sucede, e fica imediatamente
	// disponível para outro owner.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Release(ctx, tx, "scheduler", "worker-a")
	}); err != nil {
		t.Fatalf("Release (worker-a): %v", err)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Acquire(ctx, tx, "scheduler", "worker-b", time.Minute)
	}); err != nil {
		t.Fatalf("Acquire (worker-b) após Release: %v, esperado sucesso", err)
	}
}

// TestAcquire_ConcurrentClaims_OnlyOneSucceeds prova o critério de aceite
// "dois workers não executam simultaneamente job exclusivo" contra uma
// corrida REAL: duas goroutines, cada uma com sua PRÓPRIA transação,
// disputando o MESMO lease ao mesmo tempo — exatamente um sucede.
func TestAcquire_ConcurrentClaims_OnlyOneSucceeds(t *testing.T) {
	db, tenant := leaseFixture(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]error, 2)
	owners := []string{"worker-a", "worker-b"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				return Acquire(ctx, tx, "scheduler", owners[i], time.Minute)
			})
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrLeaseHeld) {
			t.Fatalf("erro inesperado: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, esperado exatamente 1 (as duas disputaram o MESMO lease ao mesmo tempo)", successes)
	}
}
