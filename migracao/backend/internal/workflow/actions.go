// Catálogo de ações nativas nomeadas para passos de workflow (GO-048) —
// o "SDK de ações" do editor visual, equivalente estreito do catálogo de
// internal/triggers (GO-029) mas para este pacote.
//
// Divergência deliberada de escopo: só ações de EFEITO INTERNO (dentro da
// própria transação do passo, sem I/O externo) — set_context (mutação
// pura do contexto) e count_rows (leitura de uma tabela do tenant).
// send_email/webhook (o catálogo de internal/triggers) ficam FORA deste
// catálogo: StepFunc (definition.go) recebe só (ctx, tx, wfContext), sem
// runID — a chave de idempotência do enfileiramento de e-mail/webhook
// (internal/notify) precisa ser única POR EXECUÇÃO (duas execuções
// concorrentes do MESMO workflow, mesmo estado de contexto, jamais podem
// colidir na mesma chave e uma delas silenciosamente não enviar), e
// derivar isso sem runID exigiria injetar um token sintético no contexto
// só para esta finalidade — uma complexidade não exigida pelo critério de
// aceite de GO-048 ("executado ponta a ponta", que não depende de efeito
// externo observável). Ver docs/migracao-go/execucoes/GO-048.md.
package workflow

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Nomes das ações nativas — usados em StoredStep.ActionName.
const (
	ActionSetContext = "set_context"
	ActionCountRows  = "count_rows"
)

// ActionBuilder recebe a Configuration persistida de um passo (StoredStep.
// Configuration) e devolve o StepFunc pronto para rodar — a ligação entre
// "action_name" e a closure que definition.Step.Run realmente executa. Um
// erro aqui (configuração inválida) impede Compile de montar a
// Definition inteira, antes de qualquer Start/Advance.
type ActionBuilder func(config map[string]any) (StepFunc, error)

// BuiltinActions devolve o catálogo de ações nativas disponíveis ao
// editor visual — quem monta um Compile real decide se usa este catálogo
// sozinho ou mesclado com ações próprias da aplicação (mesmo espírito de
// internal/triggers.BuiltinActions).
func BuiltinActions() map[string]ActionBuilder {
	return map[string]ActionBuilder{
		ActionSetContext: setContextAction,
		ActionCountRows:  countRowsAction,
	}
}

// setContextAction mescla configuration.values (um objeto JSON literal)
// no contexto do run — nunca muta wfContext recebido (StepFunc deve
// devolver um mapa NOVO, ver definition.go), sempre um efeito puro e
// idempotente (mesclar os mesmos valores duas vezes produz o mesmo
// resultado, sem necessidade de nenhuma chave de idempotência própria).
func setContextAction(config map[string]any) (StepFunc, error) {
	values, _ := config["values"].(map[string]any)
	return func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error) {
		next := make(map[string]any, len(wfContext)+len(values))
		for k, v := range wfContext {
			next[k] = v
		}
		for k, v := range values {
			next[k] = v
		}
		return next, nil
	}, nil
}

// countRowsAction conta as linhas de configuration.table (uma tabela do
// tenant, resolvida via internal/metadata — mesma checagem de
// existência que qualquer outro acesso a tabela de domínio) e grava o
// resultado em wfContext[configuration.output]. Um efeito de LEITURA
// pura: repetir a mesma contagem não muda nada além do valor lido, sem
// necessidade de idempotência própria (ao contrário de um efeito de
// escrita externa).
func countRowsAction(config map[string]any) (StepFunc, error) {
	table, _ := config["table"].(string)
	output, _ := config["output"].(string)
	if table == "" || output == "" {
		return nil, fmt.Errorf("%w: ação %q exige configuration.table e configuration.output", ErrActionConfigInvalid, ActionCountRows)
	}
	return func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error) {
		t, err := metadata.GetTable(ctx, database.AsTx(tx), table)
		if err != nil {
			return nil, err
		}
		var count int64
		quoted := pgx.Identifier{t.Name}.Sanitize()
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, quoted)).Scan(&count); err != nil {
			return nil, err
		}
		next := make(map[string]any, len(wfContext)+1)
		for k, v := range wfContext {
			next[k] = v
		}
		next[output] = count
		return next, nil
	}, nil
}
