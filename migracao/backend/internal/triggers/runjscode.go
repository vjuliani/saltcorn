// Ação nativa "run_js_code" (GO-040) — deliberadamente deferida em GO-029
// ("run_js_code fica fora desta entrega", actions.go). Reaproveita
// integralmente o MESMO contrato de avaliação que `only_if` (GO-023) já
// usa — internal/expression.Evaluator.Eval, sem capacidades/callbacks —
// nunca uma segunda fronteira de execução de JS para o mesmo host. Isso
// significa os MESMOS dois limites já documentados em GO-022/023:
//  1. `code` precisa ser uma EXPRESSÃO, não um corpo de função com
//     statements — o host (host.ts) embrulha em
//     `(async () => { return (${code}); })()`. Um `if`/`throw` solto
//     falha ao interpretar; a forma equivalente como expressão é
//     `condição || (function(){ throw new Error(...) })()`. Divergência
//     real do legado, que aceita `function(row, user) { ...corpo... }`
//     livre — não reproduzida aqui, mesma fronteira que `only_if` já tem.
//  2. Uma referência a um singleton de domínio (`Table`/`File`/`View`)
//     falha explicitamente com pluginhost.ErrUnsupportedReference, nunca
//     silenciosamente. O trigger real do pack piloto guitars
//     (`receive_share_trigger`) precisa de ESCRITA via `Table` — fora
//     deste limite, ver decisão de escopo GO-052.
package triggers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// ActionRunJSCode é o nome da ação nativa — mesmo nome do catálogo do
// legado (base-plugin/actions.ts), por continuidade de expectativa.
const ActionRunJSCode = "run_js_code"

// RunJSCodeFunc é como ActionFunc, mas recebe também tenant — a ÚNICA
// informação extra que run_js_code precisa (checar cutover.Guard antes de
// tocar no host de plugins), nunca usada pelas ações nativas de
// actions.go.
type RunJSCodeFunc func(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, table metadata.Table, row map[string]any, config map[string]any) error

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
func NewRunJSCode(evaluator *expression.Evaluator) RunJSCodeFunc {
	return func(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, table metadata.Table, row map[string]any, config map[string]any) error {
		code, _ := config["code"].(string)
		if code == "" {
			return fmt.Errorf("triggers: run_js_code sem \"code\" em configuration")
		}
		_, err := evaluator.Eval(ctx, tenant, expression.Request{
			Code:         code,
			Row:          row,
			ExpectedType: runJSCodeResultType,
		}, nil)
		return err
	}
}
