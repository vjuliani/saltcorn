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
//
// `configuration` (GO-029) é o parâmetro por-trigger que uma ActionFunc
// nomeada (actions.go) precisa para ser genérica — ex.: qual endereço um
// `send_email` deste trigger específico usa. Antes de GO-029, o mesmo
// `trig.Action` sempre resolvia para a MESMA função sem nenhum dado
// próprio do trigger, o que impedia registrar ações reutilizáveis do
// catálogo do legado (base-plugin/actions.ts) — cada instância delas
// carrega parâmetros próprios (destinatário, URL, corpo).
package triggers

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// table_id é NULLABLE desde GO-052: um trigger de EVENTO NOMEADO (ex.:
// receive_share_trigger, when_trigger="ReceiveMobileShareData") não está
// ligado a nenhuma tabela — mesmo espírito de _sc_workflows não
// reaproveitar _sc_triggers em GO-048 pelo motivo INVERSO (lá, table_id
// NOT NULL bloqueava um conceito sem tabela; aqui, o mesmo catálogo já
// existente ganha essa flexibilidade em vez de precisar de uma tabela
// paralela, já que o formato de linha é idêntico e só a obrigatoriedade
// da coluna muda). Ver catalog.go: TableID == 0 (zero value Go, nunca um
// id de tabela real) é o sinal de "trigger de evento nomeado".
const createTriggersTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_triggers (
	id serial PRIMARY KEY,
	table_id integer,
	when_trigger text NOT NULL,
	action text NOT NULL,
	only_if text,
	after_commit boolean NOT NULL DEFAULT false,
	configuration jsonb NOT NULL DEFAULT '{}'::jsonb,
	created_at timestamptz NOT NULL DEFAULT now()
)`

// dropTableIDNotNullSQL migra um schema já provisionado ANTES de GO-052
// (quando table_id ainda era NOT NULL) — idempotente, sem efeito num
// schema recém-criado (que já nasce sem a restrição) nem numa segunda
// chamada (a restrição já não existe mais para remover de novo).
const dropTableIDNotNullSQL = `ALTER TABLE _sc_triggers ALTER COLUMN table_id DROP NOT NULL`

const createTriggersLookupIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_triggers_table_when ON _sc_triggers (table_id, when_trigger)`

// createTriggersEventIndexSQL (GO-052) — a consulta de despacho de evento
// nomeado (TriggersForEvent) filtra por when_trigger com table_id IS NULL,
// um padrão de acesso distinto do índice acima (que sempre tem um
// table_id concreto do lado esquerdo).
const createTriggersEventIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_triggers_event ON _sc_triggers (when_trigger) WHERE table_id IS NULL`

// EnsureSchema cria o catálogo de triggers, idempotente — chamar dentro de
// db.WithTenant, uma vez por tenant (mesmo padrão de internal/metadata,
// internal/platform/outbox).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createTriggersTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, dropTableIDNotNullSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createTriggersLookupIndexSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createTriggersEventIndexSQL); err != nil {
		return err
	}
	return nil
}
