// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cada teste cria seu
// próprio schema de tenant isolado, mesmo padrão de
// internal/metadata/catalog_test.go.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
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

func testTenant(t *testing.T, db *database.DB) tenancy.Tenant {
	t.Helper()
	tenant := tenancy.Tenant(fmt.Sprintf("outbox_test_%s", sanitizeForSchema(t.Name())))
	ctx := context.Background()

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
	return tenant
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

func countRows(t *testing.T, db *database.DB, tenant tenancy.Tenant, table string) int {
	t.Helper()
	var n int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n)
	}); err != nil {
		t.Fatalf("countRows(%s): %v", table, err)
	}
	return n
}

func TestDo_FreshExecutesFnOnce(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var calls atomic.Int32
	var result any
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var replayed bool
		var err error
		result, replayed, err = Do(ctx, tx, "key-1", map[string]any{"a": 1}, func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
			calls.Add(1)
			return map[string]any{"created": true}, []Event{{Type: "thing.created", Payload: map[string]any{"id": 1}}}, nil
		})
		if replayed {
			t.Error("primeira execução não deveria ser replayed")
		}
		return err
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("fn chamada %d vezes, esperado 1", calls.Load())
	}
	if result == nil {
		t.Error("resultado nil")
	}
	if got := countRows(t, db, tenant, "_sc_outbox"); got != 1 {
		t.Errorf("linhas em _sc_outbox = %d, esperado 1", got)
	}
}

// TestDo_RedeliveryDoesNotRepeatEffect é a prova direta de "redelivery não
// repete efeito interno": chamar Do de novo com a MESMA chave e o MESMO
// payload, numa transação NOVA (simulando o cliente perder a resposta e
// tentar de novo, ou um crash do processo depois do commit original), não
// chama fn de novo e devolve o resultado já gravado.
func TestDo_RedeliveryDoesNotRepeatEffect(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)

	var calls atomic.Int32
	fn := func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
		calls.Add(1)
		return map[string]any{"id": float64(42)}, []Event{{Type: "thing.created", Payload: map[string]any{"id": 42}}}, nil
	}

	first, err := runDo(t, db, tenant, "key-2", map[string]any{"a": 1}, fn)
	if err != nil {
		t.Fatalf("primeira chamada: %v", err)
	}

	second, err := runDo(t, db, tenant, "key-2", map[string]any{"a": 1}, fn)
	if err != nil {
		t.Fatalf("segunda chamada (redelivery): %v", err)
	}

	if calls.Load() != 1 {
		t.Errorf("fn chamada %d vezes, esperado 1 (redelivery não deveria rodar fn de novo)", calls.Load())
	}
	firstMap := first.(map[string]any)
	secondMap := second.(map[string]any)
	if firstMap["id"] != secondMap["id"] {
		t.Errorf("resultados diferentes entre a chamada original e o replay: %v vs %v", first, second)
	}

	// Deduplicação: o evento não deveria ter sido gravado duas vezes.
	if got := countRows(t, db, tenant, "_sc_outbox"); got != 1 {
		t.Errorf("linhas em _sc_outbox após redelivery = %d, esperado 1 (sem duplicar)", got)
	}
	if got := countRows(t, db, tenant, "_sc_idempotency_keys"); got != 1 {
		t.Errorf("linhas em _sc_idempotency_keys = %d, esperado 1", got)
	}
}

func runDo(t *testing.T, db *database.DB, tenant tenancy.Tenant, key string, payload any, fn func(ctx context.Context, tx pgx.Tx) (any, []Event, error)) (any, error) {
	t.Helper()
	var result any
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, _, err = Do(ctx, tx, key, payload, fn)
		return err
	})
	return result, err
}

func TestDo_SameKeyDifferentPayloadRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)

	fn := func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
		return map[string]any{"ok": true}, nil, nil
	}
	if _, err := runDo(t, db, tenant, "key-3", map[string]any{"a": 1}, fn); err != nil {
		t.Fatalf("primeira chamada: %v", err)
	}

	var calls atomic.Int32
	_, err := runDo(t, db, tenant, "key-3", map[string]any{"a": 2}, func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
		calls.Add(1)
		return nil, nil, nil
	})
	if !errors.Is(err, ErrKeyConflict) {
		t.Errorf("chave repetida com payload diferente = %v, esperado ErrKeyConflict", err)
	}
	if calls.Load() != 0 {
		t.Error("fn não deveria ter rodado quando a chave conflita")
	}
}

// TestDo_CrashBeforeCommitLosesNothingBecauseNothingWasConfirmed é a prova
// direta de "crash antes do commit não perde evento confirmado": a
// transação externa aborta DEPOIS de Do "suceder" internamente — como
// nada commitou, nada persiste (nem o efeito, nem a chave, nem o evento) —
// e uma nova tentativa com a mesma chave/payload roda fn do zero,
// legitimamente, porque nada tinha sido confirmado antes.
func TestDo_CrashBeforeCommitLosesNothingBecauseNothingWasConfirmed(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var calls atomic.Int32
	fn := func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
		calls.Add(1)
		return map[string]any{"ok": true}, []Event{{Type: "thing.created"}}, nil
	}

	simulatedCrash := errors.New("crash simulado antes do commit")
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, _, err := Do(ctx, tx, "key-4", map[string]any{"a": 1}, fn); err != nil {
			return err
		}
		// Do "succedeu" dentro desta transação, mas agora forçamos o
		// abort inteiro — simula o processo caindo (ou qualquer outro
		// motivo de rollback) antes do commit externo.
		return simulatedCrash
	})
	if !errors.Is(err, simulatedCrash) {
		t.Fatalf("WithTenant erro = %v, esperado o crash simulado", err)
	}

	if got := countRows(t, db, tenant, "_sc_idempotency_keys"); got != 0 {
		t.Errorf("linhas em _sc_idempotency_keys após rollback = %d, esperado 0 (nada deveria ter persistido)", got)
	}
	if got := countRows(t, db, tenant, "_sc_outbox"); got != 0 {
		t.Errorf("linhas em _sc_outbox após rollback = %d, esperado 0", got)
	}

	// Nova tentativa: como nada persistiu, fn roda de novo legitimamente
	// (não é uma "repetição de efeito confirmado" — o efeito anterior
	// nunca foi confirmado).
	if _, err := runDo(t, db, tenant, "key-4", map[string]any{"a": 1}, fn); err != nil {
		t.Fatalf("nova tentativa após rollback: %v", err)
	}
	if calls.Load() != 2 { // uma vez na tentativa abortada, uma vez na nova
		t.Errorf("fn chamada %d vezes no total, esperado 2 (1 abortada + 1 bem-sucedida)", calls.Load())
	}
	if got := countRows(t, db, tenant, "_sc_idempotency_keys"); got != 1 {
		t.Errorf("linhas em _sc_idempotency_keys após nova tentativa = %d, esperado 1", got)
	}
}

// TestDo_ConcurrentSameKeyRunsFnOnce é a prova de "redelivery não repete
// efeito interno" sob concorrência REAL (não sequencial): duas transações
// Postgres genuínas disputando a MESMA chave ao mesmo tempo — o lock de
// advisory por chave serializa, e só uma das duas realmente roda fn.
//
// fn inclui um atraso artificial (time.Sleep) de propósito: sem ele, a
// janela entre a leitura ("chave não existe") e a escrita (INSERT) é curta
// o bastante que, neste ambiente de teste (Postgres em localhost, baixa
// latência), o Postgres/scheduler do Go raramente sobrepõe duas tentativas
// o suficiente para expor a corrida — um teste sem o atraso passou
// consistentemente mesmo com lockKey desativado numa verificação manual
// desta tarefa (10/10 e depois 20/20 execuções), um falso positivo
// silencioso. Alongar a janela artificialmente é o que torna este teste
// capaz de detectar a ausência do lock de forma confiável, não só "por
// sorte" — confirmado com uma sonda manual durante esta tarefa: sem o
// lock, 5 chamadas de fn em vez de 1 quando a janela é alongada assim.
func TestDo_ConcurrentSameKeyRunsFnOnce(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)

	var calls atomic.Int32
	fn := func(ctx context.Context, tx pgx.Tx) (any, []Event, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // alonga a janela crítica de propósito — ver comentário acima
		return map[string]any{"winner": true}, []Event{{Type: "thing.created"}}, nil
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	replayedCount := atomic.Int32{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, replayed, err := Do(ctx, tx, "key-concurrent", map[string]any{"a": 1}, fn)
				if replayed {
					replayedCount.Add(1)
				}
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
		t.Errorf("Do concorrente falhou: %v", err)
	}

	if calls.Load() != 1 {
		t.Errorf("fn chamada %d vezes sob concorrência, esperado exatamente 1", calls.Load())
	}
	if got := countRows(t, db, tenant, "_sc_outbox"); got != 1 {
		t.Errorf("linhas em _sc_outbox sob concorrência = %d, esperado 1 (sem duplicar)", got)
	}
}
