// Package expression é a ÚNICA fachada Go para avaliar o subconjunto
// prioritário de expressões definido em GO-023, sobre o host temporário de
// GO-022 (internal/pluginhost). Nenhuma outra parte do backend deve chamar
// pluginhost.Client.Eval diretamente para avaliar uma expressão de
// usuário — centralizando aqui garante que a checagem de cutover
// (plugins.expr, ver pluginhost.ExpressionCapability) e a coerção de tipo
// de internal/types nunca sejam esquecidas por um chamador futuro (GO-024
// triggers/ações, GO-027 packs, GO-029 SDK — todos dependem desta tarefa).
//
// Três classes de incompatibilidade permanecem bloqueadoras, nunca
// silenciosamente aceitas (ADR-0005, achados de GO-004):
//  1. Closure serializada — o host já falha com ReferenceError (nenhuma
//     mudança necessária aqui, o erro chega como ErrRuntime).
//  2. Singleton de domínio sem canal de callback (File/View — Table
//     ganhou um canal real em GO-052, ver nota 3) — o host de GO-023
//     (host.ts) lança pluginhost.ErrUnsupportedReference explicitamente,
//     nunca undefined silencioso.
//  3. Escrita a partir de EXPRESSÃO genérica (`only_if`, campos
//     calculados) continua fora de escopo — este pacote nunca declara
//     nenhuma Capability por conta própria, Eval só repassa
//     req.Capabilities/callbacks do CHAMADOR (Request abaixo). GO-052
//     concede escrita (pluginhost.CapDBWrite) apenas ao chamador
//     internal/triggers.NewRunJSCode (a ação nativa `run_js_code`, mais
//     privilegiada por natureza — configurada por um admin, não uma
//     fórmula de usuário) — nunca a este pacote nem a `only_if`.
//
// Ver docs/migracao-go/execucoes/GO-023.md para as decisões de escopo
// completas.
package expression

import (
	"context"
	"errors"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pluginhost"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/types"
)

// ErrLegacyOwner é devolvido quando a capacidade de expressão
// (pluginhost.ExpressionCapability) não pertence ao Go para o tenant —
// o chamador DEVE tratar isto como "avalie no caminho legado", nunca
// tentar contornar chamando pluginhost.Client diretamente. É assim que o
// critério de aceite "plugin transacional incompatível mantém operação
// integral no legado" se sustenta por construção: enquanto nenhuma
// SwitchOwner explícita mencionar plugins.expr, TODA chamada a Eval falha
// aqui, antes de tocar no host.
var ErrLegacyOwner = errors.New("expression: capacidade de expressão não pertence ao Go para este tenant — usar caminho legado")

// ErrAmbiguousResult é devolvido quando o resultado bruto devolvido pelo
// host não é coagível para Request.ExpectedType — nunca um valor
// silenciosamente incorreto ou de tipo errado escapa desta função
// (critério de aceite de GO-023: "expressão desconhecida nunca muda
// resultado silenciosamente").
var ErrAmbiguousResult = errors.New("expression: resultado da expressão não é coagível para o tipo esperado")

// Request é uma avaliação de expressão pedida por um chamador. Row/User
// são sempre mapas serializáveis, nunca um singleton de domínio (mesma
// regra de pluginhost.EvalContext — reforçada agora pelos estojos de
// Table/File/View no host, ver GO-023).
//
// ExpectedType é OBRIGATÓRIO: este pacote nunca devolve um `any` sem
// significado de tipo. Cada expressão do subconjunto prioritário
// (fórmula sobre row/user usada por um campo calculado, uma condição,
// etc.) tem um tipo de resultado conhecido de antemão pelo chamador.
type Request struct {
	Code         string
	Row          map[string]any
	User         map[string]any
	ExpectedType metadata.FieldType
	Capabilities []pluginhost.Capability
	TimeoutMs    int
}

// Evaluator liga o cliente do host (GO-022) à checagem de cutover
// (GO-009/GO-022). Guard deve ser a MESMA instância usada pelo resto do
// processo (cmd/server, cmd/worker), carregada via
// cutover.LoadFromRegistry, para que Eval nunca veja um estado de
// ownership diferente do resto do backend.
type Evaluator struct {
	Client *pluginhost.Client
	Guard  *cutover.Guard
}

// Eval avalia req.Code para tenant, só se a capacidade de expressão
// pertencer ao Go (cutover.Guard.Begin) — senão devolve ErrLegacyOwner
// imediatamente, sem nunca tocar no host. O resultado bruto do host é
// sempre coagido para req.ExpectedType via internal/types antes de
// voltar ao chamador.
func (e *Evaluator) Eval(ctx context.Context, tenant tenancy.Tenant, req Request, callbacks map[pluginhost.Capability]pluginhost.CallbackFunc) (any, error) {
	end, err := e.Guard.Begin(tenant, pluginhost.ExpressionCapability)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLegacyOwner, err)
	}
	defer end()

	res, err := e.Client.Eval(ctx, pluginhost.EvalRequest{
		Kind:         pluginhost.KindExpr,
		Code:         req.Code,
		Context:      pluginhost.EvalContext{Row: req.Row, User: req.User},
		Capabilities: req.Capabilities,
		TimeoutMs:    req.TimeoutMs,
	}, callbacks)
	if err != nil {
		return nil, err
	}

	coerced, err := types.Coerce(req.ExpectedType, res.Result)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAmbiguousResult, err)
	}
	return coerced, nil
}
