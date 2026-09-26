package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// ActionFuncTx é uma ação de trigger agendado nativa em Go (ADR-0005:
// núcleo nativo, nunca via host de plugins) — sem tabela/linha (ver
// ScheduledTrigger), roda DENTRO de uma savepoint própria (ver
// RunDueTx), nunca aborta o processamento dos demais triggers agendados
// do mesmo lote se falhar.
type ActionFuncTx func(ctx context.Context, tx database.Tx) error

// Dispatcher liga o catálogo de triggers agendados a um registro de ações
// nativas. Uma Action referenciada por um ScheduledTrigger mas ausente
// deste registro nunca é tratada como no-op silencioso — vira
// ErrUnknownAction, registrado como last_error do trigger (ver RunDueTx).
type Dispatcher struct {
	Actions map[string]ActionFuncTx
}

// RunDueTx processa todos os triggers agendados cujo NextRunAt já passou
// — cada um em sua PRÓPRIA savepoint (mesma técnica de
// internal/platform/outbox.ProcessPendingTx: uma ação que falha desfaz só
// o PRÓPRIO efeito, nunca aborta o lote inteiro nem deixa de avançar
// next_run_at dos demais). NextRunAt é sempre avançado a partir de `now`,
// nunca do valor antigo — um processo que ficou parado atravessando
// várias janelas PULA direto para a próxima ocorrência futura, nunca
// acumula ("catch up") execuções perdidas, mesma política documentada do
// legado (models/scheduler.ts, comentário "we must have skipped
// events... if not running continuously").
func (d *Dispatcher) RunDueTx(ctx context.Context, tx database.Tx, now time.Time) (ran, failed int, err error) {
	due, err := DueTriggersTx(ctx, tx, now)
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
		if err := advanceScheduleTx(ctx, tx, st.ID, now, nextRunAt, errMsg); err != nil {
			return ran, failed, err
		}
	}
	return ran, failed, nil
}

// runInSavepoint executa fn dentro de uma savepoint de tx via SQL cru
// (SAVEPOINT/ROLLBACK TO/RELEASE) — portável nos dois dialetos (SQLite
// suporta SAVEPOINT nativamente), ao contrário da API nativa de
// pgx.Tx.Begin/Commit/Rollback usada antes de GO-055 (sem equivalente em
// database.Tx). Mesma técnica de
// internal/platform/outbox.runInSavepoint (não exportada de lá, duplicada
// aqui com um nome de savepoint PRÓPRIO — "sc_scheduler_attempt", nunca
// "sc_outbox_attempt" — por ser uma função pequena e este pacote não
// depender de outbox para nenhuma outra finalidade).
func runInSavepoint(ctx context.Context, tx database.Tx, fn func(ctx context.Context, tx database.Tx) error) error {
	if err := tx.Exec(ctx, "SAVEPOINT sc_scheduler_attempt"); err != nil {
		return fmt.Errorf("scheduler: abrir savepoint: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		if rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sc_scheduler_attempt"); rollbackErr != nil {
			return fmt.Errorf("scheduler: rollback: %w", rollbackErr)
		}
		if releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT sc_scheduler_attempt"); releaseErr != nil {
			return releaseErr
		}
		return err
	}
	return tx.Exec(ctx, "RELEASE SAVEPOINT sc_scheduler_attempt")
}
