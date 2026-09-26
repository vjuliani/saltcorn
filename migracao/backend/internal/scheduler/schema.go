package scheduler

import (
	"context"
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
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

// EnsureSchemaTx cria o catálogo de triggers agendados, idempotente —
// chamar dentro de db.WithTenant, uma vez por tenant (mesmo padrão de
// internal/triggers, internal/platform/outbox). Dialect-rewrite (GO-055)
// igual ao já usado por internal/platform/outbox desde GO-030.
func EnsureSchemaTx(ctx context.Context, tx database.Tx) error {
	ddl := createScheduledTriggersTableSQL
	if tx.Dialect() == database.DialectSQLite {
		ddl = strings.NewReplacer(
			"serial PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT",
			"timestamptz", "timestamp",
			"now()", "CURRENT_TIMESTAMP",
		).Replace(ddl)
	}
	if err := tx.Exec(ctx, ddl); err != nil {
		return err
	}
	return tx.Exec(ctx, createScheduledTriggersDueIndexSQL)
}
