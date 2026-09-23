// Ação nativa "run_js_code" (GO-040, capacidade de escrita em GO-052) —
// deliberadamente deferida em GO-029 ("run_js_code fica fora desta
// entrega", actions.go). Reaproveita integralmente o MESMO contrato de
// avaliação que `only_if` (GO-023) já usa — internal/expression.
// Evaluator.Eval — nunca uma segunda fronteira de execução de JS para o
// mesmo host. Isso significa os MESMOS limites já documentados em
// GO-022/023:
//  1. `code` precisa ser uma EXPRESSÃO, não um corpo de função com
//     statements — o host (host.ts) embrulha em
//     `(async () => { return (${code}); })()`. Um `if`/`throw` solto
//     falha ao interpretar; a forma equivalente como expressão é
//     `condição || (function(){ throw new Error(...) })()`. Divergência
//     real do legado, que aceita `function(row, user) { ...corpo... }`
//     livre — não reproduzida aqui, mesma fronteira que `only_if` já tem.
//  2. Uma referência a um singleton de domínio (`File`/`View`) falha
//     explicitamente com pluginhost.ErrUnsupportedReference, nunca
//     silenciosamente — só `Table` ganhou um canal real (item 3).
//  3. `Table` (GO-052) deixou de ser um singleton bloqueado — run_js_code
//     agora declara `pluginhost.CapDBWrite` e um callback real, então
//     `Table.findOne({name}).insertRow(values)` (exatamente o código do
//     trigger real do pack piloto guitars, `receive_share_trigger`)
//     funciona ponta a ponta. Escopo deliberadamente NARROW: só
//     `insertRow` (o único método que o pack piloto usa) — `updateRow`/
//     `deleteRows`/`getRows`/etc. do `Table` do legado continuam
//     indisponíveis, e `Table.findOne` só aceita `{name: string}` (a
//     única forma usada no pack). A capacidade de escrita é EXCLUSIVA de
//     run_js_code — `only_if`/campos calculados (que reaproveitam o
//     MESMO Evaluator.Eval) continuam sem nenhuma capacidade declarada,
//     nunca ganham escrita "de graça" por reaproveitar o mesmo mecanismo.
package triggers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pluginhost"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// ActionRunJSCode é o nome da ação nativa — mesmo nome do catálogo do
// legado (base-plugin/actions.ts), por continuidade de expectativa.
const ActionRunJSCode = "run_js_code"

// RunJSCodeFunc é como ActionFunc, mas recebe também tenant (checar
// cutover.Guard antes de tocar no host de plugins) e actorRole (GO-052 —
// o papel de quem originou o disparo deste trigger, nunca elevado,
// propagado sem alteração até o callback de escrita `db.write`), nenhum
// dos dois usado pelas ações nativas de actions.go.
type RunJSCodeFunc func(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error

// runJSCodeResultType é o ExpectedType passado a Evaluator.Eval — API
// exige um tipo (Request.ExpectedType é "OBRIGATÓRIO", expression.go),
// mas run_js_code roda por efeito colateral, não por valor de retorno: o
// resultado coagido é sempre descartado. FieldBoolean é só um tipo
// concreto qualquer para satisfazer a assinatura — internal/types.Coerce
// devolve (nil, nil) para um resultado nil (código sem `return`)
// independente do tipo pedido, o caso comum de um trigger de efeito
// colateral.
const runJSCodeResultType = metadata.FieldBoolean

// NewRunJSCode devolve uma RunJSCodeFunc ligada a um Evaluator real.
// config["code"] é o corpo JS (mesma chave que o legado usa,
// `configuration.code` de `receive_share_trigger` no pack guitars).
//
// GO-052: declara pluginhost.CapDBWrite e registra seu callback —
// tx/actorRole/tenant vêm do FECHAMENTO desta função (capturados por
// referência do escopo externo, o padrão usual de closure em Go), nunca
// de um argumento novo em EvalRequest — o host (host.ts) só conhece o
// NOME da capacidade e os args de cada chamada (table/values), nunca uma
// credencial ou conexão de banco (ADR-0005: "nunca uma credencial de
// banco crua chega ao host").
func NewRunJSCode(evaluator *expression.Evaluator) RunJSCodeFunc {
	return func(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
		code, _ := config["code"].(string)
		if code == "" {
			return fmt.Errorf("triggers: run_js_code sem \"code\" em configuration")
		}
		writeCallback := func(ctx context.Context, args map[string]any) (any, error) {
			tableName, _ := args["table"].(string)
			if tableName == "" {
				return nil, fmt.Errorf("triggers: db.write sem \"table\"")
			}
			values, _ := args["values"].(map[string]any)
			return records.CreateRecordTx(ctx, database.AsTx(tx), actorRole, tableName, values, nil)
		}
		_, err := evaluator.Eval(ctx, tenant, expression.Request{
			Code:         code,
			Row:          row,
			ExpectedType: runJSCodeResultType,
			Capabilities: []pluginhost.Capability{pluginhost.CapDBWrite},
		}, map[pluginhost.Capability]pluginhost.CallbackFunc{
			pluginhost.CapDBWrite: writeCallback,
		})
		return err
	}
}
