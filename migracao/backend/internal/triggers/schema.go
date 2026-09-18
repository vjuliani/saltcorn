// Package triggers implementa o disparo de triggers/ações sobre os
// comandos de registro (GO-013) — o primeiro preenchimento real de
// internal/records.Hooks, que existe desde GO-013 exatamente para isto
// ("nenhuma automação real usa isto ainda", comentário em commands.go).
//
// Separação hooks transacionais vs. efeitos após commit (escopo desta
// tarefa), espelhando o contrato do legado (models/trigger.ts,
// `_after_commit`) mas com uma garantia MAIS FORTE onde a semântica
// diverge deliberadamente — ver Dispatcher.enqueueAfterCommit.
//
// ADR-0005: ações do núcleo são portadas nativamente em Go (ActionFunc),
// nunca via host de plugins — só a CONDIÇÃO de disparo (`only_if`, uma
// fórmula JS) passa pelo host temporário, via internal/expression (GO-023)
// — Dispatcher é o primeiro consumidor real de
// internal/expression.Evaluator fora dos próprios testes de GO-023.
package triggers

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const createTriggersTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_triggers (
	id serial PRIMARY KEY,
	table_id integer NOT NULL,
	when_trigger text NOT NULL,
	action text NOT NULL,
	only_if text,
	after_commit boolean NOT NULL DEFAULT false,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createTriggersLookupIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_triggers_table_when ON _sc_triggers (table_id, when_trigger)`

// EnsureSchema cria o catálogo de triggers, idempotente — chamar dentro de
// db.WithTenant, uma vez por tenant (mesmo padrão de internal/metadata,
// internal/platform/outbox).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createTriggersTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createTriggersLookupIndexSQL); err != nil {
		return err
	}
	return nil
}
