package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ActionFunc é uma ação de trigger agendado nativa em Go (ADR-0005: núcleo
// nativo, nunca via host de plugins) — sem tabela/linha (ver
// ScheduledTrigger), roda DENTRO de uma savepoint própria (ver RunDue),
// nunca aborta o processamento dos demais triggers agendados do mesmo
// lote se falhar.
type ActionFunc func(ctx context.Context, tx pgx.Tx) error

// Dispatcher liga o catálogo de triggers agendados a um registro de ações
// nativas. Uma Action referenciada por um ScheduledTrigger mas ausente
// deste registro nunca é tratada como no-op silencioso — vira
// ErrUnknownAction, registrado como last_error do trigger (ver RunDue).
type Dispatcher struct {
	Actions map[string]ActionFunc
}

// RunDue processa todos os triggers agendados cujo NextRunAt já passou —
// cada um em sua PRÓPRIA savepoint (mesma técnica de
// internal/platform/outbox.ProcessPending: uma ação que falha desfaz só o
// PRÓPRIO efeito, nunca aborta o lote inteiro nem deixa de avançar
// next_run_at dos demais). NextRunAt é sempre avançado a partir de `now`,
// nunca do valor antigo — um processo que ficou parado atravessando
// várias janelas PULA direto para a próxima ocorrência futura, nunca
// acumula ("catch up") execuções perdidas, mesma política documentada do
// legado (models/scheduler.ts, comentário "we must have skipped
// events... if not running continuously").
func (d *Dispatcher) RunDue(ctx context.Context, tx pgx.Tx, now time.Time) (ran, failed int, err error) {
	due, err := DueTriggers(ctx, tx, now)
	if err != nil {
		return 0, 0, err
	}

	for _, st := range due {
		loc, locErr := time.LoadLocation(st.Timezone)
		if locErr != nil {
			loc = time.UTC
		}
		expr, exprErr := ParseCron(st.CronExpr)

		var runErr error
		switch {
		case exprErr != nil:
			runErr = exprErr
		default:
			action, ok := d.Actions[st.Action]
			if !ok {
				runErr = fmt.Errorf("%w: %q (trigger %d)", ErrUnknownAction, st.Action, st.ID)
			} else {
				runErr = runInSavepoint(ctx, tx, action)
			}
		}

		nextRunAt := st.NextRunAt
		if exprErr == nil {
			if next, nextErr := expr.NextAfter(now.In(loc)); nextErr == nil {
				nextRunAt = next
			} else {
				runErr = nextErr
			}
		}

		errMsg := ""
		if runErr != nil {
			errMsg = runErr.Error()
			failed++
		} else {
			ran++
		}
		if err := advanceSchedule(ctx, tx, st.ID, nextRunAt, errMsg); err != nil {
			return ran, failed, err
		}
	}
	return ran, failed, nil
}

// runInSavepoint executa fn dentro de uma savepoint de tx — mesma técnica
// de internal/platform/outbox.runInSavepoint (não exportada de lá,
// duplicada aqui por ser uma função pequena e este pacote não depender de
// outbox para nenhuma outra finalidade).
func runInSavepoint(ctx context.Context, tx pgx.Tx, fn func(ctx context.Context, tx pgx.Tx) error) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: abrir savepoint: %w", err)
	}
	if err := fn(ctx, sp); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	return sp.Commit(ctx)
}
