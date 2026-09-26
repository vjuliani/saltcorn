package triggers

import (
	"context"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// ActionFuncTx é uma ação de trigger nativa em Go — roda DENTRO da mesma
// transação que originou o disparo (ADR-0005: ações do núcleo são parte do
// produto, portadas nativamente, nunca despachadas para o host de
// plugins). config é o Trigger.Configuration DESTA instância (GO-029) —
// o mesmo nome de ação (ex.: "send_email") pode ser reaproveitado por
// vários triggers, cada um com seus próprios parâmetros; uma ação que não
// precisa de nenhum parâmetro simplesmente ignora config. Ver actions.go
// para o catálogo de ações nativas reais (GO-029) — antes desta tarefa,
// só o MECANISMO de registro/despacho por nome existia (GO-024).
type ActionFuncTx func(ctx context.Context, tx database.Tx, table metadata.Table, row map[string]any, config map[string]any) error

// Dispatcher liga o catálogo de triggers (_sc_triggers) à avaliação de
// condição (internal/expression, GO-023) e a um registro de ações
// nativas — PRIMEIRO consumidor real de internal/expression.Evaluator
// fora dos próprios testes de GO-023. database.Tx-based desde GO-055 —
// a MESMA instância é reaproveitada para tenants Postgres (via os
// wrappers pgx.Tx de postgres.go) e SQLite (via *Tx diretamente), nunca
// duas instâncias/dois catálogos de ações separados por dialeto.
type Dispatcher struct {
	Expression *expression.Evaluator
	Actions    map[string]ActionFuncTx
	// RunJSCode (GO-040), se não-nil, atende a ação "run_js_code" em vez
	// de uma busca em Actions — a ÚNICA ação que precisa saber o tenant
	// (para o mesmo motivo de shouldFire: checar cutover.Guard antes de
	// tocar no host de plugins, internal/expression.Evaluator.Eval). Ver
	// NewRunJSCode em runjscode.go.
	RunJSCode RunJSCodeFuncTx
}

// RunOneTx despacha uma única ação de trigger (nativa ou run_js_code) — o
// código comum entre runBeforeTx/runAfterTx/EmitEventTx, evitando duplicar
// o caso-especial de run_js_code em cada um. Exportado (GO-040) porque é
// também exatamente o que o consumidor de outbox do worker precisa para
// executar de fato um trigger AfterCommit enfileirado por
// enqueueAfterCommitTx — nenhum segundo mecanismo de despacho é criado só
// para esse caminho.
//
// actorRole (GO-052) é o papel de quem ORIGINOU a operação que disparou
// este trigger — nunca elevado, propagado sem alteração até
// RunJSCodeFuncTx, o único consumidor que precisa dele hoje (a capacidade
// de escrita `db.write` do host de plugins usa exatamente este papel,
// nunca RoleAdmin nem nenhum papel "do sistema").
func (d *Dispatcher) RunOneTx(ctx context.Context, tx database.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, trig Trigger, row map[string]any) error {
	if trig.Action == ActionRunJSCode {
		if d.RunJSCode == nil {
			return fmt.Errorf("%w: %q (trigger %d)", ErrUnknownAction, trig.Action, trig.ID)
		}
		return d.RunJSCode(ctx, tx, tenant, actorRole, table, row, trig.Configuration)
	}
	action, ok := d.Actions[trig.Action]
	if !ok {
		return fmt.Errorf("%w: %q (trigger %d)", ErrUnknownAction, trig.Action, trig.ID)
	}
	return action(ctx, tx, table, row, trig.Configuration)
}

// HooksForTx constrói um *records.TxHooks (o ponto de extensão reservado
// desde GO-013 — "nenhuma automação real usa isto ainda") ligado a este
// Dispatcher para tenant/user. Nenhum novo caminho de escrita é criado:
// CreateRecordTx/UpdateRecordTx/DeleteRecordTx continuam sendo os ÚNICOS
// pontos de entrada (GO-013); triggers só observam/interceptam essas
// chamadas.
//
// actorRole (GO-052) é o papel de quem originou a escrita de registro que
// disparou este hook — o MESMO já passado a CreateRecordTx/UpdateRecordTx/
// DeleteRecordTx pelo chamador (nunca resolvido de novo aqui), propagado
// até RunOneTx para qualquer run_js_code que a escrita dispare.
func (d *Dispatcher) HooksForTx(tenant tenancy.Tenant, actorRole identity.RoleID, user map[string]any) *records.TxHooks {
	return &records.TxHooks{
		BeforeInsert: func(ctx context.Context, tx database.Tx, table metadata.Table, values map[string]any) error {
			return d.runBeforeTx(ctx, tx, tenant, actorRole, table, values, user)
		},
		AfterInsert: func(ctx context.Context, tx database.Tx, table metadata.Table, record map[string]any) error {
			return d.runAfterTx(ctx, tx, tenant, actorRole, table, WhenInsert, record, user)
		},
		BeforeUpdate: func(ctx context.Context, tx database.Tx, table metadata.Table, id int, values map[string]any) error {
			return d.runBeforeTx(ctx, tx, tenant, actorRole, table, values, user)
		},
		AfterUpdate: func(ctx context.Context, tx database.Tx, table metadata.Table, record map[string]any) error {
			return d.runAfterTx(ctx, tx, tenant, actorRole, table, WhenUpdate, record, user)
		},
		BeforeDelete: func(ctx context.Context, tx database.Tx, table metadata.Table, id int) error {
			return d.runBeforeTx(ctx, tx, tenant, actorRole, table, map[string]any{"id": id}, user)
		},
		AfterDelete: func(ctx context.Context, tx database.Tx, table metadata.Table, id int) error {
			return d.runAfterTx(ctx, tx, tenant, actorRole, table, WhenDelete, map[string]any{"id": id}, user)
		},
	}
}

// runBeforeTx executa os triggers WhenValidate — um erro (do próprio
// Dispatcher ou de uma ActionFuncTx) aborta a operação inteira, propagado
// tal como qualquer outro hook (contrato já documentado em
// internal/records.TxHooks desde GO-013/GO-030).
func (d *Dispatcher) runBeforeTx(ctx context.Context, tx database.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, values map[string]any, user map[string]any) error {
	trigs, err := TriggersForTx(ctx, tx, table.ID, WhenValidate)
	if err != nil {
		return err
	}
	for _, trig := range trigs {
		fire, err := d.shouldFire(ctx, tenant, trig, values, user)
		if err != nil {
			return err
		}
		if !fire {
			continue
		}
		if err := d.RunOneTx(ctx, tx, tenant, actorRole, table, trig, values); err != nil {
			return err
		}
	}
	return nil
}

// runAfterTx executa os triggers Insert/Update/Delete. Um trigger comum
// roda a ação AGORA, na mesma transação; um trigger AfterCommit nunca
// executa a ação aqui — só enfileira (ver enqueueAfterCommitTx).
func (d *Dispatcher) runAfterTx(ctx context.Context, tx database.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, when WhenTrigger, record map[string]any, user map[string]any) error {
	trigs, err := TriggersForTx(ctx, tx, table.ID, when)
	if err != nil {
		return err
	}
	for _, trig := range trigs {
		fire, err := d.shouldFire(ctx, tenant, trig, record, user)
		if err != nil {
			return err
		}
		if !fire {
			continue
		}
		if trig.AfterCommit {
			if err := d.enqueueAfterCommitTx(ctx, tx, actorRole, trig, table, record); err != nil {
				return err
			}
			continue
		}
		if err := d.RunOneTx(ctx, tx, tenant, actorRole, table, trig, record); err != nil {
			return err
		}
	}
	return nil
}

// EmitEventTx (GO-052) despacha os triggers de EVENTO NOMEADO
// (TriggersForEventTx) registrados para eventName — o mecanismo Go por
// trás de `Trigger.emitEvent`/`POST /api/emit-event` do legado
// (models/trigger.ts). Diferença deliberada: o legado despacha de forma
// ASSÍNCRONA/fire-and-forget (`setTimeout(..., 0)`) — se o processo cair
// entre a resposta HTTP e essa execução adiada, o evento é perdido
// SILENCIOSAMENTE, nunca reexecutado (mesma classe de garantia fraca já
// documentada e deliberadamente NÃO reproduzida em
// Dispatcher.enqueueAfterCommitTx). Aqui EmitEventTx roda SEMPRE de forma
// síncrona, na mesma transação que o chamador HTTP já protege com
// Idempotency-Key (outbox.DoTx) — uma garantia estritamente mais forte,
// mesmo espírito da divergência já registrada em enqueueAfterCommitTx.
// AfterCommit nunca é honrado aqui por construção (CreateTriggerTx já
// rejeita um trigger de evento nomeado com AfterCommit=true).
//
// user (o mapa exposto a `only_if`/`row`, mesmo contrato de HooksForTx) e
// payload (o `row` exposto à ação, ex.: `row.files` que
// receive_share_trigger lê) são parâmetros SEPARADOS, mesma distinção
// que HooksForTx já faz — user identifica quem emitiu, payload é o dado
// do evento em si.
func (d *Dispatcher) EmitEventTx(ctx context.Context, tx database.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, eventName string, user map[string]any, payload map[string]any) (fired int, err error) {
	trigs, err := TriggersForEventTx(ctx, tx, eventName)
	if err != nil {
		return 0, err
	}
	for _, trig := range trigs {
		fire, err := d.shouldFire(ctx, tenant, trig, payload, user)
		if err != nil {
			return fired, err
		}
		if !fire {
			continue
		}
		if err := d.RunOneTx(ctx, tx, tenant, actorRole, metadata.Table{}, trig, payload); err != nil {
			return fired, err
		}
		fired++
	}
	return fired, nil
}

// shouldFire avalia Trigger.OnlyIf quando presente. Vazio nunca toca no
// host (retorna true direto, sem custo de processo). Um erro ao avaliar
// (incluindo expression.ErrLegacyOwner — capacidade de expressão ainda
// não é Go para este tenant) é propagado como falha explícita da
// operação inteira, NUNCA interpretado como "não dispara" nem "dispara
// mesmo assim": o critério de aceite de GO-023 ("expressão desconhecida
// nunca muda resultado silenciosamente") se estende à decisão de disparo
// de um trigger — uma condição que não pôde ser avaliada com confiança
// não pode decidir nada silenciosamente.
func (d *Dispatcher) shouldFire(ctx context.Context, tenant tenancy.Tenant, trig Trigger, row map[string]any, user map[string]any) (bool, error) {
	if trig.OnlyIf == "" {
		return true, nil
	}
	result, err := d.Expression.Eval(ctx, tenant, expression.Request{
		Code:         trig.OnlyIf,
		Row:          row,
		User:         user,
		ExpectedType: metadata.FieldBoolean,
	}, nil)
	if err != nil {
		return false, fmt.Errorf("triggers: avaliar only_if do trigger %d: %w", trig.ID, err)
	}
	fire, _ := result.(bool)
	return fire, nil
}

// enqueueAfterCommitTx NUNCA executa a ação sincronamente — grava um
// evento outbox (GO-014) na MESMA transação da escrita, com uma chave de
// idempotência por (trigger, record), e deixa a execução de fato para um
// worker (outbox.ProcessPendingTx) DEPOIS do commit.
//
// Divergência DELIBERADA do legado (models/trigger.ts + db.afterCommit,
// packages/postgres/postgres.ts:775-839): lá, um trigger `_after_commit`
// é enfileirado numa lista EM MEMÓRIA do processo Node e executado fora
// da transação — se o processo cair entre o COMMIT e a execução dessa
// fila, o efeito é PERDIDO SILENCIOSAMENTE (nunca reexecutado, nunca
// registrado como pendente). Aqui o evento já está durável ANTES do
// commit terminar (mesma transação): uma queda do processo depois do
// commit não perde nada — o evento persiste em _sc_outbox, esperando o
// worker. Uma garantia estritamente mais forte, escolhida de propósito
// em vez de replicar o comportamento frágil do legado.
//
// actor_role (GO-052) viaja no payload — o worker (processo separado,
// sem a requisição HTTP original) precisa dele para propagar até
// RunJSCodeFuncTx/db.write com o MESMO papel de quem escreveu o registro,
// nunca RoleAdmin nem um papel "do sistema" inventado (ver
// cmd/worker/main.go, triggerOutboxHandler).
func (d *Dispatcher) enqueueAfterCommitTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, trig Trigger, table metadata.Table, record map[string]any) error {
	key := fmt.Sprintf("trigger:%d:%v", trig.ID, record["id"])
	payload := map[string]any{
		"trigger_id":    trig.ID,
		"action":        trig.Action,
		"table":         table.Name,
		"record":        record,
		"configuration": trig.Configuration,
		"actor_role":    int(actorRole),
	}
	_, _, err := outbox.DoTx(ctx, tx, key, payload, func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
		return nil, []outbox.Event{{Type: "trigger:" + trig.Action, Payload: payload}}, nil
	})
	return err
}
