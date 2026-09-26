// Testes do caminho SQLite de GO-055 em cmd/worker — sem Postgres, mesmo
// padrão de cmd/server/sqlite_test.go. Prova o critério de aceite: "cmd/
// worker em modo SQLite executa pelo menos um job real (outbox/scheduler)
// contra um tenant em arquivo SQLite", usando as MESMAS funções
// (triggerOutboxHandlerTx, runOutboxJobSQLite, runScheduledTriggersJobSQLite)
// que o processo real usa — não uma chamada direta a internal/triggers/
// internal/scheduler que contornaria o wiring de cmd/worker em si.
package main

import (
	"context"
	"testing"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/telemetry"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

func newWorkerSQLiteFixture(t *testing.T) (*sqlite.DB, tenancy.Tenant) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenant := tenancy.Tenant("wrk_sqlite_test")
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx database.Tx) error {
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := outbox.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		if err := identity.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		if err := triggers.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		if err := scheduler.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		return notify.EnsureSchemaTx(ctx, tx)
	}); err != nil {
		t.Fatalf("setup do fixture SQLite: %v", err)
	}
	return db, tenant
}

// TestRunOutboxJobSQLite_ExecutesAfterCommitTriggerForReal é o cenário
// completo contra SQLite, espelhando TestTriggerOutboxHandler_
// AfterCommitTriggerActuallyExecutes (Postgres, triggers_test.go):
// records.CreateRecordTx com hooks reais de um Dispatcher grava um
// registro E enfileira "trigger:send_email" na MESMA transação
// (AfterCommit); runOutboxJobSQLite — a MESMA função que o processo
// worker real chama a cada ciclo — drena esse evento e o executa de
// verdade, gerando um novo evento "notify.email" com o assunto
// interpolado a partir da linha real.
func TestRunOutboxJobSQLite_ExecutesAfterCommitTriggerForReal(t *testing.T) {
	db, tenant := newWorkerSQLiteFixture(t)
	ctx := context.Background()

	triggerDispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}
	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "posts", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = table.ID
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		_, err = triggers.CreateTriggerTx(ctx, tx, triggers.Trigger{
			TableID: tableID, When: triggers.WhenInsert, Action: triggers.ActionSendEmail, AfterCommit: true,
			Configuration: map[string]any{"to": "dest@example.com", "subject": "novo post: {{title}}", "body": "..."},
		})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		hooks := triggerDispatcher.HooksForTx(tenant, identity.RoleAdmin, nil)
		_, err := records.CreateRecordTx(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "primeiro post"}, hooks)
		return err
	}); err != nil {
		t.Fatalf("CreateRecordTx: %v", err)
	}

	// A ação send_email NUNCA roda sincronamente (AfterCommit) — antes do
	// job de outbox, só o evento "trigger:send_email" existe.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		rows, err := outbox.ListPendingTx(ctx, tx, 10)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].Type != "trigger:send_email" {
			t.Fatalf("esperado 1 evento trigger:send_email pendente, obtido: %+v", rows)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pré-job: %v", err)
	}

	handler := triggerOutboxHandlerTx(triggerDispatcher, nil)
	metrics := telemetry.NewJobMetrics(telemetry.NewRegistry())
	runOutboxJobSQLite(context.Background(), string(tenant), db, metrics, handler)

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		rows, err := outbox.ListPendingTx(ctx, tx, 10)
		if err != nil {
			return err
		}
		var found bool
		for _, ev := range rows {
			if ev.Type == "notify.email" {
				found = true
				subject, _ := ev.Payload["subject"].(string)
				if subject != "novo post: primeiro post" {
					t.Fatalf("assunto não interpolado a partir da linha real: %q", subject)
				}
			}
		}
		if !found {
			t.Fatalf("evento trigger:send_email não foi executado (sem notify.email pendente): %+v", rows)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-job: %v", err)
	}
}

// TestRunScheduledTriggersJobSQLite_RunsDueTriggerForReal prova o mesmo
// critério de aceite para o job de scheduler: runScheduledTriggersJobSQLite
// (a MESMA função que o processo worker real chama) dispara um trigger
// agendado já vencido e avança next_run_at.
func TestRunScheduledTriggersJobSQLite_RunsDueTriggerForReal(t *testing.T) {
	db, tenant := newWorkerSQLiteFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		_, err := scheduler.CreateScheduledTriggerTx(ctx, tx, "job-real", "increment", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	called := 0
	schedulerDispatcher := &scheduler.Dispatcher{Actions: map[string]scheduler.ActionFuncTx{
		"increment": func(ctx context.Context, tx database.Tx) error { called++; return nil },
	}}

	// Força o gatilho a já estar vencido, como um processo que ficou
	// parado por um tempo real teria encontrado no próximo tick.
	var originalNextRunAt time.Time
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		all, err := scheduler.ListAllTx(ctx, tx)
		if err != nil {
			return err
		}
		originalNextRunAt = all[0].NextRunAt
		return tx.Exec(ctx, `UPDATE _sc_scheduled_triggers SET next_run_at = $1 WHERE id = $2`, time.Now().Add(-time.Hour), all[0].ID)
	}); err != nil {
		t.Fatalf("forçar vencimento: %v", err)
	}

	metrics := telemetry.NewJobMetrics(telemetry.NewRegistry())
	runScheduledTriggersJobSQLite(context.Background(), string(tenant), db, metrics, schedulerDispatcher)

	if called != 1 {
		t.Fatalf("ação increment chamada %d vezes via runScheduledTriggersJobSQLite, esperado 1", called)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		all, err := scheduler.ListAllTx(ctx, tx)
		if err != nil {
			return err
		}
		if !all[0].NextRunAt.After(originalNextRunAt.Add(-2 * time.Hour)) {
			t.Fatalf("next_run_at não avançou: %v", all[0].NextRunAt)
		}
		if all[0].LastRunAt == nil {
			t.Fatal("last_run_at não foi gravado")
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-job: %v", err)
	}
}
