package scheduler

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// _sc_scheduled_triggers substitui os cinco mecanismos sem estado do
// legado (ver comentário do pacote) por um único relógio persistido por
// trigger — next_run_at é a fonte de verdade de "está na hora?", avançada
// deterministicamente a cada execução, nunca recalculada "de memória" a
// cada tick.
const createScheduledTriggersTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_scheduled_triggers (
	id serial PRIMARY KEY,
	name text NOT NULL UNIQUE,
	action text NOT NULL,
	cron_expr text NOT NULL,
	timezone text NOT NULL DEFAULT 'UTC',
	next_run_at timestamptz NOT NULL,
	last_run_at timestamptz,
	last_error text,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createScheduledTriggersDueIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_scheduled_triggers_due ON _sc_scheduled_triggers (next_run_at)`

// EnsureSchema cria o catálogo de triggers agendados, idempotente —
// chamar dentro de db.WithTenant, uma vez por tenant (mesmo padrão de
// internal/triggers, internal/platform/outbox).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createScheduledTriggersTableSQL); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, createScheduledTriggersDueIndexSQL)
	return err
}
