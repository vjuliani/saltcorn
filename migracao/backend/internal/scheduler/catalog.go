package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ScheduledTrigger é uma entrada de _sc_scheduled_triggers. Ao contrário
// de internal/triggers.Trigger, não tem TableID/OnlyIf/linha associada —
// um trigger agendado dispara por TEMPO, sobre o tenant inteiro, nunca
// sobre um registro específico. Catálogo deliberadamente SEPARADO de
// internal/triggers (não uma extensão de Trigger.TableID para ponteiro
// opcional): evita alterar um tipo que já tem consumidores e testes reais
// desde GO-024 só para acomodar um conceito estruturalmente diferente.
type ScheduledTrigger struct {
	ID        int
	Name      string
	Action    string
	CronExpr  string
	Timezone  string
	NextRunAt time.Time
	LastRunAt *time.Time
	LastError string
}

// CreateScheduledTrigger valida cronExpr e timezone, calcula o primeiro
// NextRunAt (a próxima ocorrência depois de agora, no fuso informado) e
// insere no catálogo. timezone vazio usa UTC — nunca o fuso local
// ambíguo do processo do legado (models/internal/cron.ts: "evaluated in
// the server's local timezone" — comportamento que este pacote
// deliberadamente NÃO replica, ver README).
func CreateScheduledTrigger(ctx context.Context, tx pgx.Tx, name, action, cronExpr, timezone string) (ScheduledTrigger, error) {
	if timezone == "" {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return ScheduledTrigger{}, fmt.Errorf("scheduler: fuso horário %q inválido: %w", timezone, err)
	}
	expr, err := ParseCron(cronExpr)
	if err != nil {
		return ScheduledTrigger{}, err
	}
	nextRunAt, err := expr.NextAfter(time.Now().In(loc))
	if err != nil {
		return ScheduledTrigger{}, err
	}

	st := ScheduledTrigger{Name: name, Action: action, CronExpr: cronExpr, Timezone: timezone, NextRunAt: nextRunAt}
	err = tx.QueryRow(ctx,
		`INSERT INTO _sc_scheduled_triggers (name, action, cron_expr, timezone, next_run_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		st.Name, st.Action, st.CronExpr, st.Timezone, st.NextRunAt,
	).Scan(&st.ID)
	if err != nil {
		return ScheduledTrigger{}, err
	}
	return st, nil
}

// DueTriggers lê os triggers agendados cujo NextRunAt já passou (<= now),
// travando as linhas escolhidas com `FOR UPDATE SKIP LOCKED` — defesa em
// profundidade além do lease por tenant (internal/platform/lease) que já
// serializa Dispatcher.RunDue entre workers concorrentes: mesmo que dois
// workers de alguma forma processassem o MESMO tenant ao mesmo tempo (ex.:
// um bug futuro na checagem de lease), nenhum dos dois pegaria a MESMA
// linha de trigger agendado.
func DueTriggers(ctx context.Context, tx pgx.Tx, now time.Time) ([]ScheduledTrigger, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, action, cron_expr, timezone, next_run_at, last_run_at, COALESCE(last_error, '')
		 FROM _sc_scheduled_triggers WHERE next_run_at <= $1 ORDER BY id FOR UPDATE SKIP LOCKED`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScheduledTrigger
	for rows.Next() {
		var st ScheduledTrigger
		if err := rows.Scan(&st.ID, &st.Name, &st.Action, &st.CronExpr, &st.Timezone, &st.NextRunAt, &st.LastRunAt, &st.LastError); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func advanceSchedule(ctx context.Context, tx pgx.Tx, id int, nextRunAt time.Time, lastErr string) error {
	var errArg any
	if lastErr != "" {
		errArg = lastErr
	}
	_, err := tx.Exec(ctx,
		`UPDATE _sc_scheduled_triggers SET next_run_at = $1, last_run_at = now(), last_error = $2 WHERE id = $3`,
		nextRunAt, errArg, id,
	)
	return err
}
