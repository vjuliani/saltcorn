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

// _sc_workflows e _sc_workflow_steps (GO-048) guardam a DEFINIÇÃO de um
// workflow — algo que faltava inteiramente neste pacote até aqui (ver
// comentário de Definition em definition.go: "definido em código Go...
// um catálogo persistido, se necessário, é escopo de uma tarefa futura").
// GO-048 é essa tarefa: sem uma definição persistida, o editor visual não
// tem o que editar, e o próprio critério de aceite de GO-048 ("um usuário
// cria/edita um workflow visualmente... e o resultado é executado ponta a
// ponta por internal/workflow") fica impossível de cumprir. Ver
// docs/migracao-go/execucoes/GO-048.md para a decisão completa.
//
// initial_step referencia _sc_workflow_steps.name por VALOR (texto), não
// por FK de id — mesma escolha de Step.Next/Else/ErrorStep em
// definition.go, que já referenciam passos por nome; manter os dois
// níveis (persistido e em memória) consistentes na mesma convenção evita
// uma tradução id<->nome desnecessária em Compile.
const createWorkflowsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_workflows (
	id serial PRIMARY KEY,
	name text NOT NULL,
	initial_step text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now()
)`

// _sc_workflow_steps é a definição, um passo por linha — o equivalente
// direto (bem mais estreito, ver actions.go) de _sc_workflow_steps do
// legado. only_if/next_step/else_step/error_step espelham exatamente os
// campos homônimos de Step (definition.go); position_x/position_y é só
// layout de canvas para o editor (nunca lido por Compile/Advance).
const createWorkflowStepsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_workflow_steps (
	id serial PRIMARY KEY,
	workflow_id integer NOT NULL REFERENCES _sc_workflows(id) ON DELETE CASCADE,
	name text NOT NULL,
	action_name text NOT NULL,
	configuration jsonb NOT NULL DEFAULT '{}'::jsonb,
	only_if text NOT NULL DEFAULT '',
	next_step text NOT NULL DEFAULT '',
	else_step text NOT NULL DEFAULT '',
	error_step text NOT NULL DEFAULT '',
	position_x double precision NOT NULL DEFAULT 0,
	position_y double precision NOT NULL DEFAULT 0,
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workflow_id, name)
)`

// EnsureSchema cria as tabelas de estado/rastreamento/definição de
// workflow, idempotente — chamar dentro de db.WithTenant, uma vez por
// tenant (mesmo padrão de internal/platform/outbox, internal/triggers).
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
	if _, err := tx.Exec(ctx, createWorkflowsTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createWorkflowStepsTableSQL); err != nil {
		return err
	}
	return nil
}
