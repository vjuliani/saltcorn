package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// Status é o estado de um Run — mesmo vocabulário do legado
// (workflow_run.ts), restrito ao subconjunto que este pacote implementa
// (sem "Waiting", ver comentário do pacote em definition.go).
type Status string

const (
	StatusRunning  Status = "running"
	StatusFinished Status = "finished"
	StatusError    Status = "error"
)

// Run é o estado persistido de uma execução — carregado/gravado em
// _sc_workflow_runs.
type Run struct {
	ID          int
	Name        string
	Context     map[string]any
	Status      Status
	CurrentStep string
	StepSeq     int
	Error       string
}

// Start cria um novo Run na etapa inicial de def, com Status=Running.
func Start(ctx context.Context, tx pgx.Tx, name string, def Definition, wfContext map[string]any) (int, error) {
	if def.Initial == "" {
		return 0, ErrNoInitialStep
	}
	if wfContext == nil {
		wfContext = map[string]any{}
	}
	payload, err := json.Marshal(wfContext)
	if err != nil {
		return 0, fmt.Errorf("workflow: codificar contexto inicial: %w", err)
	}
	var id int
	err = tx.QueryRow(ctx,
		`INSERT INTO _sc_workflow_runs (name, context, status, current_step) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, payload, string(StatusRunning), def.Initial,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Get lê um Run sem travar a linha — para inspeção (testes, exibição),
// nunca para decidir um Advance (ver loadRunForUpdate).
func Get(ctx context.Context, tx pgx.Tx, runID int) (Run, error) {
	return scanRun(tx.QueryRow(ctx, selectRunSQL+` WHERE id = $1`, runID))
}

const selectRunSQL = `SELECT id, name, context, status, current_step, step_seq, COALESCE(error, '') FROM _sc_workflow_runs`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var contextJSON []byte
	var status string
	if err := row.Scan(&r.ID, &r.Name, &contextJSON, &status, &r.CurrentStep, &r.StepSeq, &r.Error); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, ErrRunNotFound
		}
		return Run{}, err
	}
	r.Status = Status(status)
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &r.Context); err != nil {
			return Run{}, fmt.Errorf("workflow: decodificar contexto do run %d: %w", r.ID, err)
		}
	}
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	return r, nil
}

func loadRunForUpdate(ctx context.Context, tx pgx.Tx, runID int) (Run, error) {
	return scanRun(tx.QueryRow(ctx, selectRunSQL+` WHERE id = $1 FOR UPDATE`, runID))
}

// Advance processa EXATAMENTE o passo atual do run — nunca mais que um
// por chamada, para que uma retomada após queda continue de onde parou,
// um passo de cada vez, sem "recuperar" vários de uma vez de forma
// oculta. `SELECT ... FOR UPDATE` na linha do run serializa Advance
// concorrentes do MESMO run (o equivalente Postgres nativo ao
// MultiNodeMutex do legado): uma segunda chamada concorrente bloqueia até
// a primeira commitar, e então enxerga o current_step JÁ avançado — nunca
// processa o mesmo passo em paralelo.
//
// Se run.Status não for StatusRunning (já Finished ou Error), Advance é
// um no-op que devolve o Run como está — idempotente para chamadas
// repetidas depois do fim.
func Advance(ctx context.Context, tx pgx.Tx, expr *expression.Evaluator, tenant tenancy.Tenant, def Definition, runID int) (Run, error) {
	run, err := loadRunForUpdate(ctx, tx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != StatusRunning {
		return run, nil
	}

	step, ok := def.Steps[run.CurrentStep]
	if !ok {
		return Run{}, fmt.Errorf("%w: %q", ErrUnknownStep, run.CurrentStep)
	}

	fire := true
	if step.OnlyIf != "" {
		result, err := expr.Eval(ctx, tenant, expression.Request{
			Code:         step.OnlyIf,
			Row:          run.Context,
			ExpectedType: metadata.FieldBoolean,
		}, nil)
		if err != nil {
			return Run{}, fmt.Errorf("workflow: avaliar only_if do passo %q: %w", step.Name, err)
		}
		fire, _ = result.(bool)
	}

	nextSeq := run.StepSeq + 1
	started := time.Now()
	newContext := run.Context
	var stepErr error

	if fire {
		// A chave de idempotência inclui step_seq (não só o nome do
		// passo): uma retomada que refaz ESTA MESMA tentativa de Advance
		// (processo caiu antes de committar) recalcula o MESMO nextSeq —
		// a chave é estável entre retentativas — mas uma segunda VISITA
		// futura ao mesmo Step.Name (um loop no grafo) usa um nextSeq
		// diferente, então nunca é confundida com uma repetição da
		// primeira visita.
		key := fmt.Sprintf("workflow:%d:%d:%s", runID, nextSeq, step.Name)
		result, _, doErr := outbox.Do(ctx, tx, key, run.Context, func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
			nc, err := step.Run(ctx, tx, run.Context)
			return nc, nil, err
		})
		if doErr != nil {
			stepErr = doErr
		} else if m, ok := result.(map[string]any); ok {
			newContext = m
		}
	}

	elapsed := time.Since(started)
	statusLabel := "skipped"
	if fire {
		statusLabel = "ok"
	}
	if stepErr != nil {
		statusLabel = "error"
	}
	if err := traceStep(ctx, tx, runID, step.Name, nextSeq, statusLabel, errString(stepErr), elapsed); err != nil {
		return Run{}, err
	}

	if stepErr != nil {
		if step.ErrorStep != "" {
			return persistAdvance(ctx, tx, runID, nextSeq, step.ErrorStep, run.Context, StatusRunning, "")
		}
		return persistAdvance(ctx, tx, runID, nextSeq, run.CurrentStep, run.Context, StatusError, stepErr.Error())
	}

	// Ramificação: um passo pulado (OnlyIf presente e falso) sempre segue
	// para Else, nunca para Next — "" finaliza o run (mesma semântica de
	// Next=""). Isso permite loops reais (voltar a um passo anterior
	// enquanto uma condição for verdadeira, terminando por Else quando
	// deixar de ser) sem nenhuma ambiguidade entre "Else não definido" e
	// "Else é terminar".
	next := step.Next
	if step.OnlyIf != "" && !fire {
		next = step.Else
	}
	nextStatus := StatusRunning
	if next == "" {
		nextStatus = StatusFinished
	}
	return persistAdvance(ctx, tx, runID, nextSeq, next, newContext, nextStatus, "")
}

// RunToCompletion chama Advance repetidamente, cada chamada em SUA
// PRÓPRIA transação (db.WithTenant) — não uma única transação de longa
// duração cobrindo o workflow inteiro (ver comentário do pacote) — até o
// run sair de StatusRunning ou maxSteps ser atingido (limite de
// segurança contra um grafo com ciclo infinito sem condição de parada).
// É só um laço de conveniência sobre Advance, não um mecanismo novo —
// testes que precisam observar um passo de cada vez chamam Advance
// diretamente.
func RunToCompletion(ctx context.Context, db *database.DB, tenant tenancy.Tenant, expr *expression.Evaluator, def Definition, runID int, maxSteps int) (Run, error) {
	var last Run
	for i := 0; i < maxSteps; i++ {
		var stepErr error
		txErr := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			r, err := Advance(ctx, tx, expr, tenant, def, runID)
			last = r
			stepErr = err
			return err
		})
		if txErr != nil {
			return Run{}, txErr
		}
		if stepErr != nil {
			return Run{}, stepErr
		}
		if last.Status != StatusRunning {
			return last, nil
		}
	}
	return last, fmt.Errorf("workflow: run %d não terminou em %d passos", runID, maxSteps)
}

func persistAdvance(ctx context.Context, tx pgx.Tx, runID, stepSeq int, currentStep string, wfContext map[string]any, status Status, errMsg string) (Run, error) {
	payload, err := json.Marshal(wfContext)
	if err != nil {
		return Run{}, fmt.Errorf("workflow: codificar contexto: %w", err)
	}
	var errArg any
	if errMsg != "" {
		errArg = errMsg
	}
	_, err = tx.Exec(ctx,
		`UPDATE _sc_workflow_runs SET context = $1, status = $2, current_step = $3, step_seq = $4, error = $5, updated_at = now() WHERE id = $6`,
		payload, string(status), currentStep, stepSeq, errArg, runID,
	)
	if err != nil {
		return Run{}, err
	}
	return Get(ctx, tx, runID)
}

func traceStep(ctx context.Context, tx pgx.Tx, runID int, stepName string, stepSeq int, status, errMsg string, elapsed time.Duration) error {
	var errArg any
	if errMsg != "" {
		errArg = errMsg
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO _sc_workflow_trace (run_id, step_name, step_seq, status, error, elapsed_ms) VALUES ($1, $2, $3, $4, $5, $6)`,
		runID, stepName, stepSeq, status, errArg, elapsed.Milliseconds(),
	)
	return err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
