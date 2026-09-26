package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
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
func CreateScheduledTriggerTx(ctx context.Context, tx database.Tx, name, action, cronExpr, timezone string) (ScheduledTrigger, error) {
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

// DueTriggersTx lê os triggers agendados cujo NextRunAt já passou (<=
// now). No Postgres, trava as linhas escolhidas com `FOR UPDATE SKIP
// LOCKED` — defesa em profundidade além do lease por tenant
// (internal/platform/lease) que já serializa Dispatcher.RunDueTx entre
// workers concorrentes: mesmo que dois workers de alguma forma
// processassem o MESMO tenant ao mesmo tempo (ex.: um bug futuro na
// checagem de lease), nenhum dos dois pegaria a MESMA linha de trigger
// agendado. SQLite não tem (nem precisa d)o equivalente — seu modelo de
// escritor único por arquivo (BEGIN IMMEDIATE, internal/platform/sqlite)
// já serializa qualquer transação de escrita concorrente no nível do
// próprio arquivo.
func DueTriggersTx(ctx context.Context, tx database.Tx, now time.Time) ([]ScheduledTrigger, error) {
	query := `SELECT id, name, action, cron_expr, timezone, next_run_at, last_run_at, COALESCE(last_error, '')
		 FROM _sc_scheduled_triggers WHERE next_run_at <= $1 ORDER BY id`
	if tx.Dialect() == database.DialectPostgres {
		query += ` FOR UPDATE SKIP LOCKED`
	}
	rows, err := tx.Query(ctx, query, now)
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

// ListAllTx lê TODOS os triggers agendados do tenant, em ordem de criação
// — usado por internal/pack (GO-027) para exportar a aplicação inteira
// (nunca filtrando por "está na hora", ao contrário de DueTriggersTx).
func ListAllTx(ctx context.Context, tx database.Tx) ([]ScheduledTrigger, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, action, cron_expr, timezone, next_run_at, last_run_at, COALESCE(last_error, '')
		 FROM _sc_scheduled_triggers ORDER BY id`,
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

// advanceScheduleTx grava last_run_at como now (passado pelo chamador,
// nunca `now()` do SQL — now() não existe no SQLite, e um parâmetro
// vinculado é portável nos dois dialetos sem precisar de dialect-rewrite
// nenhum, já que isto é uma query viva, não DDL).
func advanceScheduleTx(ctx context.Context, tx database.Tx, id int, now, nextRunAt time.Time, lastErr string) error {
	var errArg any
	if lastErr != "" {
		errArg = lastErr
	}
	return tx.Exec(ctx,
		`UPDATE _sc_scheduled_triggers SET next_run_at = $1, last_run_at = $2, last_error = $3 WHERE id = $4`,
		nextRunAt, now, errArg, id,
	)
}
