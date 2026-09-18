package workflow

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// _sc_workflow_runs guarda o ESTADO persistido de cada execução — o
// equivalente reduzido de _sc_workflow_runs do legado. step_seq é um
// contador monotônico por run, incrementado a cada Advance bem-sucedido —
// existe para que a CHAVE de idempotência de um passo (ver run.go) seja
// única por TENTATIVA de avanço, não só por nome de passo: um workflow
// pode voltar a visitar o mesmo Step.Name mais de uma vez (loop), e sem
// step_seq a segunda visita seria confundida com uma repetição da
// primeira, pulando o efeito silenciosamente — exatamente o tipo de bug
// que esta coluna existe para evitar.
const createWorkflowRunsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_workflow_runs (
	id serial PRIMARY KEY,
	name text NOT NULL,
	context jsonb NOT NULL DEFAULT '{}'::jsonb,
	status text NOT NULL,
	current_step text NOT NULL,
	step_seq integer NOT NULL DEFAULT 0,
	error text,
	started_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
)`

// _sc_workflow_trace é o "rastreamento" do escopo desta tarefa — o
// equivalente reduzido de _sc_workflow_trace do legado: um registro por
// passo processado (rodado OU pulado por OnlyIf=false), nunca sobrescrito.
const createWorkflowTraceTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_workflow_trace (
	id bigserial PRIMARY KEY,
	run_id integer NOT NULL,
	step_name text NOT NULL,
	step_seq integer NOT NULL,
	status text NOT NULL,
	error text,
	elapsed_ms bigint NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createWorkflowTraceRunIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_workflow_trace_run ON _sc_workflow_trace (run_id, id)`

// EnsureSchema cria as tabelas de estado/rastreamento de workflow,
// idempotente — chamar dentro de db.WithTenant, uma vez por tenant (mesmo
// padrão de internal/platform/outbox, internal/triggers).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createWorkflowRunsTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createWorkflowTraceTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createWorkflowTraceRunIndexSQL); err != nil {
		return err
	}
	return nil
}
