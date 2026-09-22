// Teste de integração do consumidor de outbox de triggers AfterCommit
// (GO-040) — exige Postgres real, pula (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida, mesmo padrão de
// cmd/server/records_test.go. Prova o critério de aceite que só o worker
// pode provar: um trigger AfterCommit enfileirado por
// internal/triggers.Dispatcher.enqueueAfterCommit é de fato EXECUTADO por
// triggerOutboxHandler via outbox.ProcessPending — não só logado, como o
// fallback de demonstração fazia antes desta tarefa (comentário histórico
// de GO-014, "sem consumidor real ainda").
package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

func workerTestDB(t *testing.T) *database.DB {
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

// TestTriggerOutboxHandler_AfterCommitTriggerActuallyExecutes é o cenário
// completo: records.CreateRecord com hooks reais de um Dispatcher (mesmo
// caminho que cmd/server usa) grava um registro E enfileira
// "trigger:send_email" na MESMA transação (AfterCommit). Depois do commit,
// uma segunda transação roda outbox.ProcessPending com
// triggerOutboxHandler — a MESMA função usada pelo processo worker real —
// e prova que o evento foi consumido executando send_email de verdade
// (um novo evento "notify.email" aparece em _sc_outbox com o assunto
// interpolado a partir da linha real), não um no-op de log.
func TestTriggerOutboxHandler_AfterCommitTriggerActuallyExecutes(t *testing.T) {
	db := workerTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("wrk_test_aftercommit")

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

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := triggers.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		widgets, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "widgets", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = widgets.ID
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, widgets.ID, metadata.FieldDef{Name: "label", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}

	dispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			TableID: tableID, When: triggers.WhenInsert, Action: triggers.ActionSendEmail, AfterCommit: true,
			Configuration: map[string]any{"to": "dest@example.com", "subject": "Novo (async): {{label}}"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	// Escreve o registro real — o trigger AfterCommit enfileira
	// "trigger:send_email" na MESMA transação (Dispatcher.enqueueAfterCommit),
	// nunca executa a ação aqui.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "widgets", map[string]any{"label": "gizmo"}, dispatcher.HooksFor(tenant, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox WHERE event_type = $1 AND status = 'pending'`, "trigger:"+triggers.ActionSendEmail).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("eventos trigger:send_email pendentes = %d, esperado 1 (o AfterCommit deveria só enfileirar, nunca executar de sincronamente)", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pré-worker: %v", err)
	}

	// Simula o ciclo do worker real: outbox.ProcessPending com o MESMO
	// triggerOutboxHandler que cmd/worker/main.go liga a notify.Handler.
	handlerCalled := false
	fallback := func(ctx context.Context, tx pgx.Tx, ev outbox.OutboxEvent) error {
		handlerCalled = true
		return nil
	}
	var processed, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = outbox.ProcessPending(ctx, tx, 20, 5, triggerOutboxHandler(dispatcher, fallback))
		return err
	}); err != nil {
		t.Fatalf("outbox.ProcessPending: %v", err)
	}
	if processed != 1 || failed != 0 {
		t.Fatalf("processed=%d failed=%d, esperado processed=1 failed=0", processed, failed)
	}
	if handlerCalled {
		t.Fatal("fallback de demonstração foi chamado — triggerOutboxHandler deveria ter reconhecido \"trigger:send_email\" e executado de verdade, nunca cair no fallback de log")
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var subject string
		err := tx.QueryRow(ctx, `SELECT payload_json->>'subject' FROM _sc_outbox WHERE event_type = $1 ORDER BY id DESC LIMIT 1`, "notify.email").Scan(&subject)
		if err != nil {
			return fmt.Errorf("consultar _sc_outbox por notify.email: %w", err)
		}
		if subject != "Novo (async): gizmo" {
			t.Errorf("subject enfileirado por send_email = %q, esperado \"Novo (async): gizmo\"", subject)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-worker: %v", err)
	}
}

// TestTriggerOutboxHandler_NonTriggerEventDelegatesToFallback prova o
// outro lado do contrato: um evento cujo Type não começa com "trigger:"
// (ex.: um evento de demonstração de um consumidor futuro qualquer)
// continua indo para o fallback — triggerOutboxHandler nunca engole
// silenciosamente um tipo de evento que não reconhece.
func TestTriggerOutboxHandler_NonTriggerEventDelegatesToFallback(t *testing.T) {
	dispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}
	called := false
	fallback := func(ctx context.Context, tx pgx.Tx, ev outbox.OutboxEvent) error {
		called = true
		return nil
	}
	handler := triggerOutboxHandler(dispatcher, fallback)
	if err := handler(context.Background(), nil, outbox.OutboxEvent{Type: "algo.outro", Payload: map[string]any{}}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !called {
		t.Fatal("fallback não foi chamado para um evento que não é \"trigger:...\"")
	}
}
