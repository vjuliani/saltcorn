// Corpus de disparo agendado exigido pelo escopo de GO-025
// ("agendamento, timezone... retomada") — contra Postgres real.
package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func TestCreateScheduledTrigger_ComputesNextRunAt(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	var st ScheduledTrigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		st, err = CreateScheduledTrigger(ctx, tx, "tick", "noop", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("CreateScheduledTrigger: %v", err)
	}
	if st.Timezone != "UTC" {
		t.Errorf("Timezone = %q, esperado \"UTC\" (padrão quando não informado)", st.Timezone)
	}
	if !st.NextRunAt.After(time.Now().Add(-time.Minute)) {
		t.Errorf("NextRunAt = %v, esperado próximo do agora (expressão de todo minuto)", st.NextRunAt)
	}
}

func TestRunDue_ExecutesDueTrigger_AdvancesSchedule(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	var st ScheduledTrigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		st, err = CreateScheduledTrigger(ctx, tx, "tick", "increment", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("CreateScheduledTrigger: %v", err)
	}

	called := 0
	d := &Dispatcher{Actions: map[string]ActionFuncTx{
		"increment": func(ctx context.Context, tx database.Tx) error {
			called++
			return nil
		},
	}}

	// Simula o tempo tendo passado até depois de NextRunAt.
	simulatedNow := st.NextRunAt.Add(time.Second)
	var ran, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ran, failed, err = d.RunDue(ctx, tx, simulatedNow)
		return err
	}); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if ran != 1 || failed != 0 {
		t.Fatalf("ran=%d failed=%d, esperado ran=1 failed=0", ran, failed)
	}
	if called != 1 {
		t.Fatalf("called = %d, esperado 1", called)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		due, err := DueTriggers(ctx, tx, simulatedNow)
		if err != nil {
			return err
		}
		if len(due) != 0 {
			t.Errorf("DueTriggers após RunDue = %d, esperado 0 (next_run_at deveria ter avançado para o futuro)", len(due))
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestRunDue_NotYetDue_Untouched(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	var st ScheduledTrigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		st, err = CreateScheduledTrigger(ctx, tx, "tick", "increment", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("CreateScheduledTrigger: %v", err)
	}

	called := 0
	d := &Dispatcher{Actions: map[string]ActionFuncTx{
		"increment": func(ctx context.Context, tx database.Tx) error { called++; return nil },
	}}

	// Simula "agora" ANTES de NextRunAt — nada deveria disparar.
	simulatedNow := st.NextRunAt.Add(-30 * time.Second)
	var ran, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ran, failed, err = d.RunDue(ctx, tx, simulatedNow)
		return err
	}); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if ran != 0 || failed != 0 || called != 0 {
		t.Fatalf("ran=%d failed=%d called=%d, esperado todos 0 (ainda não está na hora)", ran, failed, called)
	}
}

// TestRunDue_ActionFailure_RecordsErrorDoesNotAbortBatch prova que uma
// ação que falha não aborta o processamento das demais no mesmo lote
// (savepoint por trigger) — mesma disciplina de
// internal/platform/outbox.ProcessPending.
func TestRunDue_ActionFailure_RecordsErrorDoesNotAbortBatch(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	var failing, ok ScheduledTrigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		failing, err = CreateScheduledTrigger(ctx, tx, "falha", "boom", "* * * * *", "")
		if err != nil {
			return err
		}
		ok, err = CreateScheduledTrigger(ctx, tx, "sucesso", "increment", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	errBoom := errors.New("efeito sempre falha")
	okCalled := 0
	d := &Dispatcher{Actions: map[string]ActionFuncTx{
		"boom":      func(ctx context.Context, tx database.Tx) error { return errBoom },
		"increment": func(ctx context.Context, tx database.Tx) error { okCalled++; return nil },
	}}

	simulatedNow := failing.NextRunAt.Add(time.Second)
	if ok.NextRunAt.After(simulatedNow) {
		simulatedNow = ok.NextRunAt.Add(time.Second)
	}
	var ran, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ran, failed, err = d.RunDue(ctx, tx, simulatedNow)
		return err
	}); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if ran != 1 || failed != 1 {
		t.Fatalf("ran=%d failed=%d, esperado ran=1 failed=1", ran, failed)
	}
	if okCalled != 1 {
		t.Fatalf("okCalled = %d, esperado 1 — a falha do outro trigger não deveria ter impedido este de rodar", okCalled)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT name, last_error FROM _sc_scheduled_triggers ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		found := map[string]string{}
		for rows.Next() {
			var name, lastErr string
			var lastErrPtr *string
			if err := rows.Scan(&name, &lastErrPtr); err != nil {
				return err
			}
			if lastErrPtr != nil {
				lastErr = *lastErrPtr
			}
			found[name] = lastErr
		}
		if found["falha"] == "" {
			t.Error("last_error de 'falha' está vazio, esperado a mensagem de errBoom")
		}
		if found["sucesso"] != "" {
			t.Errorf("last_error de 'sucesso' = %q, esperado vazio", found["sucesso"])
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestRunDue_UnknownAction_RecordsErrorAdvancesSchedule(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	var st ScheduledTrigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		st, err = CreateScheduledTrigger(ctx, tx, "orfao", "nao_existe", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("CreateScheduledTrigger: %v", err)
	}

	d := &Dispatcher{Actions: map[string]ActionFuncTx{}}
	simulatedNow := st.NextRunAt.Add(time.Second)
	var ran, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ran, failed, err = d.RunDue(ctx, tx, simulatedNow)
		return err
	}); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if ran != 0 || failed != 1 {
		t.Fatalf("ran=%d failed=%d, esperado ran=0 failed=1", ran, failed)
	}

	// A agenda avança mesmo com ação desconhecida — nunca fica presa
	// tentando a mesma execução para sempre.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		due, err := DueTriggers(ctx, tx, simulatedNow)
		if err != nil {
			return err
		}
		if len(due) != 0 {
			t.Errorf("DueTriggers = %d, esperado 0 (agenda deveria ter avançado apesar do erro)", len(due))
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestRunDue_ActionFailure_RollsBackPartialWrite prova a garantia real de
// uma savepoint por trigger (RunDueTx): uma ação que escreve no banco e
// DEPOIS falha nunca deixa esse efeito parcial visível — o mesmo espírito
// de internal/platform/outbox.ProcessPendingTx (uma tentativa que falha
// desfaz só o PRÓPRIO efeito). Sem este teste, remover o ROLLBACK TO
// SAVEPOINT de runInSavepoint passaria despercebido por toda a suíte
// existente (nenhum outro teste escreve de dentro da própria ação).
func TestRunDue_ActionFailure_RollsBackPartialWrite(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TABLE side_effect (id serial primary key)`); err != nil {
			return err
		}
		_, err := CreateScheduledTrigger(ctx, tx, "escreve-e-falha", "write_then_fail", "* * * * *", "")
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	errBoom := errors.New("falha proposital depois de escrever")
	d := &Dispatcher{Actions: map[string]ActionFuncTx{
		"write_then_fail": func(ctx context.Context, tx database.Tx) error {
			if err := tx.Exec(ctx, `INSERT INTO side_effect DEFAULT VALUES`); err != nil {
				return err
			}
			return errBoom
		},
	}}

	var st ScheduledTrigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		all, err := ListAll(ctx, tx)
		st = all[0]
		return err
	}); err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		ran, failed, err := d.RunDue(ctx, tx, st.NextRunAt.Add(time.Second))
		if err != nil {
			return err
		}
		if ran != 0 || failed != 1 {
			t.Fatalf("ran=%d failed=%d, esperado ran=0 failed=1", ran, failed)
		}
		return nil
	}); err != nil {
		t.Fatalf("RunDue: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM side_effect`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("side_effect tem %d linha(s), esperado 0 — a escrita parcial deveria ter sido desfeita pela savepoint", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestCreateScheduledTrigger_InvalidTimezone_ReturnsExplicitError prova
// que um fuso horário inválido nunca é silenciosamente tratado como UTC —
// erro explícito, mesma disciplina de internal/types (GO-023).
func TestCreateScheduledTrigger_InvalidTimezone_ReturnsExplicitError(t *testing.T) {
	db, tenant := schedulerFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateScheduledTrigger(ctx, tx, "invalido", "noop", "* * * * *", "Nao/Existe")
		return err
	})
	if err == nil {
		t.Fatal("esperado erro para fuso horário inválido")
	}
}
