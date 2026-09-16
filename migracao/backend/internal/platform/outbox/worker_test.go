// Testes deste arquivo exigem Postgres real (mesma DSN de
// idempotency_test.go).
package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func listPending(t *testing.T, db *database.DB, tenant tenancy.Tenant) []OutboxEvent {
	t.Helper()
	var events []OutboxEvent
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		events, err = ListPending(ctx, tx, 100)
		return err
	}); err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	return events
}

func listFailed(t *testing.T, db *database.DB, tenant tenancy.Tenant) []OutboxEvent {
	t.Helper()
	var events []OutboxEvent
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		events, err = ListFailed(ctx, tx, 100)
		return err
	}); err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	return events
}

// enqueue grava um evento diretamente via Do, com chave/payload únicos.
func enqueue(t *testing.T, db *database.DB, tenant tenancy.Tenant, key, eventType string) {
	t.Helper()
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := Do(ctx, tx, key, map[string]any{"key": key}, func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
			return nil, []Event{{Type: eventType, Payload: map[string]any{"key": key}}}, nil
		})
		return err
	}); err != nil {
		t.Fatalf("enqueue(%s): %v", key, err)
	}
}

func TestProcessPending_SuccessMarksDone(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	enqueue(t, db, tenant, "evt-1", "thing.created")

	var processed, failed int
	var handledTypes []string
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = ProcessPending(ctx, tx, 10, 3, func(ctx context.Context, tx pgx.Tx, ev OutboxEvent) error {
			handledTypes = append(handledTypes, ev.Type)
			return nil
		})
		return err
	}); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if processed != 1 || failed != 0 {
		t.Errorf("processed=%d failed=%d, esperado 1 e 0", processed, failed)
	}
	if len(handledTypes) != 1 || handledTypes[0] != "thing.created" {
		t.Errorf("handledTypes = %v", handledTypes)
	}
	if pending := listPending(t, db, tenant); len(pending) != 0 {
		t.Errorf("ainda há %d eventos pendentes após processamento bem-sucedido", len(pending))
	}
}

// TestProcessPending_OneFailureDoesNotAffectOthers confirma que a
// savepoint por evento isola falhas: um handler que falha só para o
// evento problemático, o lote inteiro continua processando os demais.
func TestProcessPending_OneFailureDoesNotAffectOthers(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	enqueue(t, db, tenant, "evt-ok-1", "thing.created")
	enqueue(t, db, tenant, "evt-bad", "thing.broken")
	enqueue(t, db, tenant, "evt-ok-2", "thing.created")

	boom := errors.New("falha proposital de handler")
	var processed, failed int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = ProcessPending(ctx, tx, 10, 3, func(ctx context.Context, tx pgx.Tx, ev OutboxEvent) error {
			if ev.Type == "thing.broken" {
				return boom
			}
			return nil
		})
		return err
	}); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if processed != 2 || failed != 1 {
		t.Errorf("processed=%d failed=%d, esperado 2 e 1", processed, failed)
	}

	pending := listPending(t, db, tenant)
	if len(pending) != 1 || pending[0].Type != "thing.broken" {
		t.Errorf("pendentes após o lote = %v, esperado só thing.broken (retry agendado)", pending)
	}
}

// TestProcessPending_RetriesThenTerminalFailure confirma "retries com
// corte": um evento que falha repetidamente volta para 'pending' até
// atingir maxAttempts, e então vira 'failed' (terminal, inspecionável via
// ListFailed) — nunca fica pendente para sempre.
func TestProcessPending_RetriesThenTerminalFailure(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	enqueue(t, db, tenant, "evt-always-fails", "thing.broken")

	const maxAttempts = 3
	alwaysFail := func(ctx context.Context, tx pgx.Tx, ev OutboxEvent) error {
		return fmt.Errorf("tentativa %d falhou de propósito", ev.Attempts+1)
	}

	for i := 0; i < maxAttempts; i++ {
		if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, _, err := ProcessPending(ctx, tx, 10, maxAttempts, alwaysFail)
			return err
		}); err != nil {
			t.Fatalf("ProcessPending (tentativa %d): %v", i+1, err)
		}
	}

	if pending := listPending(t, db, tenant); len(pending) != 0 {
		t.Errorf("eventos ainda pendentes após esgotar as tentativas = %v, esperado 0", pending)
	}
	failedEvents := listFailed(t, db, tenant)
	if len(failedEvents) != 1 {
		t.Fatalf("eventos com status failed = %d, esperado 1", len(failedEvents))
	}
	if failedEvents[0].Attempts != maxAttempts {
		t.Errorf("attempts = %d, esperado %d", failedEvents[0].Attempts, maxAttempts)
	}
}

// TestProcessPending_ConcurrentWorkersDoNotDoubleProcess é a prova de
// "deduplicação" entre workers: duas transações concorrentes chamando
// ProcessPending sobre o mesmo lote de eventos pendentes — graças a
// `FOR UPDATE SKIP LOCKED`, cada evento é processado por exatamente um dos
// dois workers, nunca pelos dois.
func TestProcessPending_ConcurrentWorkersDoNotDoubleProcess(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	const n = 20
	for i := 0; i < n; i++ {
		enqueue(t, db, tenant, fmt.Sprintf("evt-%d", i), "thing.created")
	}

	var mu sync.Mutex
	handledCount := make(map[string]int)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, _, err := ProcessPending(ctx, tx, n, 3, func(ctx context.Context, tx pgx.Tx, ev OutboxEvent) error {
					mu.Lock()
					handledCount[ev.Key]++
					mu.Unlock()
					return nil
				})
				return err
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("worker concorrente falhou: %v", err)
	}

	if len(handledCount) != n {
		t.Errorf("eventos distintos processados = %d, esperado %d", len(handledCount), n)
	}
	for key, count := range handledCount {
		if count != 1 {
			t.Errorf("evento %q processado %d vezes, esperado exatamente 1", key, count)
		}
	}
	if pending := listPending(t, db, tenant); len(pending) != 0 {
		t.Errorf("eventos ainda pendentes após os dois workers = %d, esperado 0", len(pending))
	}
}
