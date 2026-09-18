// Corpus de execução de workflow exigido pelo critério de aceite de
// GO-024: "ordem e rollback equivalem às fixtures; falhas/repetições não
// duplicam efeitos internos; workflows interrompidos retomam de forma
// documentada." Sobe Postgres real e o host real de GO-022 (para os casos
// com OnlyIf).
package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
)

func stepIncrement(counterName string, delta int) StepFunc {
	return func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error) {
		value, err := incrementCounter(ctx, tx, counterName, delta)
		if err != nil {
			return nil, err
		}
		out := map[string]any{}
		for k, v := range wfContext {
			out[k] = v
		}
		out[counterName] = value
		return out, nil
	}
}

func TestRun_HappyPath_MultiStep(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{
		Initial: "step1",
		Steps: map[string]Step{
			"step1": {Name: "step1", Run: stepIncrement("happy", 1), Next: "step2"},
			"step2": {Name: "step2", Run: stepIncrement("happy", 1), Next: "step3"},
			"step3": {Name: "step3", Run: stepIncrement("happy", 1), Next: ""},
		},
	}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "happy-path", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	final, err := RunToCompletion(ctx, db, tenant, evaluator, def, runID, 10)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if final.Status != StatusFinished {
		t.Fatalf("Status = %q, esperado Finished", final.Status)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, err := readCounter(ctx, tx, "happy")
		if err != nil {
			return err
		}
		if value != 3 {
			t.Errorf("counter happy = %d, esperado 3 (um incremento por passo)", value)
		}
		var traceCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_workflow_trace WHERE run_id = $1 AND status = 'ok'`, runID).Scan(&traceCount); err != nil {
			return err
		}
		if traceCount != 3 {
			t.Errorf("rastreamento com status=ok = %d, esperado 3", traceCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestRun_OnlyIf_SkipsStep(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{
		Initial: "step1",
		Steps: map[string]Step{
			"step1": {Name: "step1", Run: stepIncrement("gated", 1), Next: "step2"},
			"step2": {Name: "step2", Run: stepIncrement("gated", 100), OnlyIf: "false", Next: ""},
		},
	}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "gated", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	final, err := RunToCompletion(ctx, db, tenant, evaluator, def, runID, 10)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if final.Status != StatusFinished {
		t.Fatalf("Status = %q, esperado Finished", final.Status)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, err := readCounter(ctx, tx, "gated")
		if err != nil {
			return err
		}
		if value != 1 {
			t.Errorf("counter gated = %d, esperado 1 — step2 (OnlyIf=false) nunca deveria ter rodado", value)
		}
		var skipped string
		if err := tx.QueryRow(ctx, `SELECT status FROM _sc_workflow_trace WHERE run_id = $1 AND step_name = 'step2'`, runID).Scan(&skipped); err != nil {
			return err
		}
		if skipped != "skipped" {
			t.Errorf("status do rastreamento de step2 = %q, esperado \"skipped\"", skipped)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestRun_ErrorStep_NoDuplicateEffect replica a fixture do legado
// "Workflow run error handling" (workflow_run_test.ts): um passo que
// sempre falha desvia para um ErrorStep que faz o efeito real — o
// resultado final tem exatamente UM incremento, nunca dois (a tentativa
// que falhou não deixou nada, o handler não duplicou nada).
func TestRun_ErrorStep_NoDuplicateEffect(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	errRisky := errors.New("efeito arriscado sempre falha")
	def := Definition{
		Initial: "risky",
		Steps: map[string]Step{
			"risky": {
				Name: "risky",
				Run: func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error) {
					return nil, errRisky
				},
				ErrorStep: "recover",
			},
			"recover": {Name: "recover", Run: stepIncrement("recovered", 1), Next: ""},
		},
	}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "error-handling", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	final, err := RunToCompletion(ctx, db, tenant, evaluator, def, runID, 10)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if final.Status != StatusFinished {
		t.Fatalf("Status = %q, esperado Finished (recuperado pelo ErrorStep)", final.Status)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, err := readCounter(ctx, tx, "recovered")
		if err != nil {
			return err
		}
		if value != 1 {
			t.Errorf("counter recovered = %d, esperado exatamente 1", value)
		}
		var errorTraces int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_workflow_trace WHERE run_id = $1 AND status = 'error'`, runID).Scan(&errorTraces); err != nil {
			return err
		}
		if errorTraces != 1 {
			t.Errorf("rastreamentos com status=error = %d, esperado 1 (a tentativa de 'risky')", errorTraces)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestAdvance_RolledBackAttempt_LeavesNoPartialEffect_ThenCleanRetry prova
// "falhas/repetições não duplicam efeitos internos" no nível de uma
// ÚNICA tentativa de Advance: se a transação da tentativa nunca commitar
// (queda simulada), nem o efeito do passo, nem o avanço de current_step,
// nem a chave de idempotência persistem — uma nova tentativa encontra o
// estado exatamente como estava antes e roda de forma limpa, sem duplicar
// nem perder nada.
func TestAdvance_RolledBackAttempt_LeavesNoPartialEffect_ThenCleanRetry(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{
		Initial: "step1",
		Steps: map[string]Step{
			"step1": {Name: "step1", Run: stepIncrement("resilient", 1), Next: ""},
		},
	}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "resilient", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	errDeliberateRollback := errors.New("queda simulada antes do commit")
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := Advance(ctx, tx, evaluator, tenant, def, runID); err != nil {
			return err
		}
		return errDeliberateRollback
	})
	if !errors.Is(err, errDeliberateRollback) {
		t.Fatalf("err = %v, esperado errDeliberateRollback", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		run, err := Get(ctx, tx, runID)
		if err != nil {
			return err
		}
		if run.CurrentStep != "step1" || run.StepSeq != 0 || run.Status != StatusRunning {
			t.Errorf("run após rollback = %+v, esperado current_step=step1, step_seq=0, status=running (nada avançou)", run)
		}
		value, err := readCounter(ctx, tx, "resilient")
		if err != nil {
			return err
		}
		if value != 0 {
			t.Errorf("counter resilient = %d após rollback, esperado 0", value)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-rollback: %v", err)
	}

	final, err := RunToCompletion(ctx, db, tenant, evaluator, def, runID, 5)
	if err != nil {
		t.Fatalf("RunToCompletion (retentativa limpa): %v", err)
	}
	if final.Status != StatusFinished {
		t.Fatalf("Status = %q, esperado Finished", final.Status)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, err := readCounter(ctx, tx, "resilient")
		if err != nil {
			return err
		}
		if value != 1 {
			t.Errorf("counter resilient = %d após retentativa, esperado exatamente 1 (nunca duplicado pela tentativa que sofreu rollback)", value)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação final: %v", err)
	}
}

// TestAdvance_ConcurrentCallsDoNotDuplicateEffect prova que `SELECT ...
// FOR UPDATE` na linha do run serializa Advance concorrentes do MESMO
// run — duas chamadas verdadeiramente concorrentes processam passos
// DIFERENTES (uma bloqueia até a outra commitar), nunca o mesmo passo em
// paralelo.
func TestAdvance_ConcurrentCallsDoNotDuplicateEffect(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{
		Initial: "step1",
		Steps: map[string]Step{
			"step1": {Name: "step1", Run: stepIncrement("concurrent", 1), Next: "step2"},
			"step2": {Name: "step2", Run: stepIncrement("concurrent", 1), Next: ""},
		},
	}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "concurrent", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, err := Advance(ctx, tx, evaluator, tenant, def, runID)
				return err
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Advance concorrente %d: %v", i, err)
		}
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		run, err := Get(ctx, tx, runID)
		if err != nil {
			return err
		}
		if run.Status != StatusFinished {
			t.Errorf("Status = %q, esperado Finished (as duas chamadas concorrentes deveriam, juntas, processar os dois passos)", run.Status)
		}
		value, err := readCounter(ctx, tx, "concurrent")
		if err != nil {
			return err
		}
		if value != 2 {
			t.Errorf("counter concurrent = %d, esperado exatamente 2 (um incremento por passo, nunca duplicado)", value)
		}
		var traceCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_workflow_trace WHERE run_id = $1`, runID).Scan(&traceCount); err != nil {
			return err
		}
		if traceCount != 2 {
			t.Errorf("total de rastreamentos = %d, esperado 2 (nunca um passo processado duas vezes)", traceCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestRun_LoopRevisitsSameStepName_EachVisitEffectRuns prova que a chave
// de idempotência de Advance inclui step_seq, não só o nome do passo: um
// workflow com um loop real revisita o MESMO Step.Name ("iterate") três
// vezes, e o efeito de CADA visita precisa contar — se a chave fosse só
// (run, nome do passo), a segunda/terceira visita colidiria com a
// primeira (outbox.Do trataria como a MESMA tentativa).
func TestRun_LoopRevisitsSameStepName_EachVisitEffectRuns(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	incrementCount := func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error) {
		if _, err := incrementCounter(ctx, tx, "loop_effect", 1); err != nil {
			return nil, err
		}
		count, _ := wfContext["count"].(float64)
		out := map[string]any{"count": count + 1}
		return out, nil
	}
	noop := func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error) {
		return wfContext, nil
	}

	def := Definition{
		Initial: "iterate",
		Steps: map[string]Step{
			"iterate": {Name: "iterate", Run: incrementCount, Next: "check"},
			"check":   {Name: "check", Run: noop, OnlyIf: "row.count < 3", Next: "iterate", Else: ""},
		},
	}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "loop", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	final, err := RunToCompletion(ctx, db, tenant, evaluator, def, runID, 20)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if final.Status != StatusFinished {
		t.Fatalf("Status = %q, esperado Finished", final.Status)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, err := readCounter(ctx, tx, "loop_effect")
		if err != nil {
			return err
		}
		if value != 3 {
			t.Errorf("counter loop_effect = %d, esperado 3 — cada uma das 3 visitas a 'iterate' deveria ter rodado seu efeito", value)
		}
		var iterateRuns int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_workflow_trace WHERE run_id = $1 AND step_name = 'iterate' AND status = 'ok'`, runID).Scan(&iterateRuns); err != nil {
			return err
		}
		if iterateRuns != 3 {
			t.Errorf("rastreamentos de 'iterate' com status=ok = %d, esperado 3", iterateRuns)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestAdvance_UnknownStep_ReturnsExplicitError(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{Initial: "step1", Steps: map[string]Step{
		"step1": {Name: "step1", Run: stepIncrement("x", 1), Next: "fantasma"},
	}}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "quebrado", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Primeiro Advance roda step1 e tenta ir para "fantasma", que não está
	// em def.Steps.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Advance(ctx, tx, evaluator, tenant, def, runID)
		return err
	}); err != nil {
		t.Fatalf("primeiro Advance: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Advance(ctx, tx, evaluator, tenant, def, runID)
		return err
	})
	if !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("err = %v, esperado ErrUnknownStep", err)
	}
}

func TestAdvance_OnlyIf_LegacyOwner_FailsClosed(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{Initial: "step1", Steps: map[string]Step{
		"step1": {Name: "step1", Run: stepIncrement("x", 1), OnlyIf: "true", Next: ""},
	}}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "condicional", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	coldEvaluator := &expression.Evaluator{Client: evaluator.Client, Guard: cutover.NewGuard()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Advance(ctx, tx, coldEvaluator, tenant, def, runID)
		return err
	})
	if err == nil {
		t.Fatal("esperado erro — only_if não deveria ser avaliável com uma capacidade de expressão nunca trocada para Go")
	}
}

func TestAdvance_NoopOnFinishedRun(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	def := Definition{Initial: "step1", Steps: map[string]Step{
		"step1": {Name: "step1", Run: stepIncrement("done", 1), Next: ""},
	}}

	var runID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runID, err = Start(ctx, tx, "termina-rapido", def, nil)
		return err
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := RunToCompletion(ctx, db, tenant, evaluator, def, runID, 5); err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}

	// Uma chamada extra depois de Finished é um no-op — nunca reprocessa
	// step1 nem erra.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		run, err := Advance(ctx, tx, evaluator, tenant, def, runID)
		if err != nil {
			return err
		}
		if run.Status != StatusFinished {
			return fmt.Errorf("Status = %q, esperado Finished", run.Status)
		}
		return nil
	}); err != nil {
		t.Fatalf("Advance extra: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		value, err := readCounter(ctx, tx, "done")
		if err != nil {
			return err
		}
		if value != 1 {
			t.Errorf("counter done = %d, esperado 1 (a chamada extra não deveria reprocessar step1)", value)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
