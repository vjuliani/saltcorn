// Package workflow implementa uma máquina de estados persistida e
// retomável para ações multi-passo — o equivalente reduzido de
// models/workflow_run.ts / workflow_step.ts do legado (Trigger com
// action="Workflow"/"Multi-step action"), para o subconjunto prioritário
// desta tarefa.
//
// Divergências deliberadas do legado, cada uma documentada no ponto onde
// a decisão é tomada (ver run.go):
//   - Cada Advance processa EXATAMENTE um passo, numa transação própria
//     (não uma única transação de longa duração cobrindo o workflow
//     inteiro) — o efeito do passo (via internal/platform/outbox, GO-014)
//     e o avanço de current_step são persistidos ATOMICAMENTE juntos, o
//     que elimina a janela do legado onde o efeito de um passo podia
//     rodar mas current_step não avançar (ou vice-versa) se o processo
//     caísse no meio.
//   - Concorrência entre Advance do MESMO run é serializada por
//     `SELECT ... FOR UPDATE` na linha do run — o equivalente Postgres
//     nativo ao MultiNodeMutex do legado, sem precisar portar um mutex
//     distribuído próprio.
//
// Sub-workflows, formulários interativos ("Waiting"/wait_info) e ações de
// I/O externo ficam fora de escopo (GO-025/026/029) — ver
// docs/migracao-go/execucoes/GO-024.md.
package workflow

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// StepFunc é o corpo de um passo — roda DENTRO de uma savepoint da
// transação de Advance, recebendo o contexto acumulado até aqui, e
// devolve o contexto ATUALIZADO (nunca muta wfContext recebido). Sempre
// um efeito INTERNO ao próprio tenant (BD) — ações de I/O externo ficam
// fora de escopo desta tarefa (ver comentário do pacote).
type StepFunc func(ctx context.Context, tx pgx.Tx, wfContext map[string]any) (map[string]any, error)

// Step é um passo nomeado de uma Definition.
type Step struct {
	Name string
	Run  StepFunc
	// OnlyIf é uma fórmula JS opcional (avaliada via internal/expression
	// contra wfContext) que decide se o passo RODA — "" significa "sempre
	// roda", nunca avaliado (sem tocar no host). Um passo pulado por
	// OnlyIf=false não conta como "executado" para fins de idempotência —
	// simplesmente avança para Next sem gravar nenhum evento outbox.
	OnlyIf string
	// Next é o nome do próximo passo quando o passo RODA (OnlyIf vazio, ou
	// não-vazio e verdadeiro) — "" marca o ÚLTIMO passo (o run termina como
	// Finished depois dele).
	Next string
	// Else é o próximo passo quando OnlyIf está presente e é FALSO — ""
	// finaliza o run (mesma semântica de Next=""), nunca "segue para Next
	// mesmo assim" (sem ambiguidade entre "não definido" e "terminar").
	// Permite ramificação real: ex. Next volta a um passo anterior
	// enquanto a condição for verdadeira (um loop), Else segue adiante
	// quando ela deixar de ser.
	Else string
	// ErrorStep, se não vazio, é para onde o run desvia quando Run falha —
	// sem ErrorStep, uma falha marca o run inteiro como Error e para.
	ErrorStep string
}

// Definition é o grafo de passos de um workflow — definido em código Go
// (ADR-0005: mecanismo do núcleo, não um catálogo dinâmico de terceiros;
// um catálogo persistido de definições de workflow, se necessário, é
// escopo de uma tarefa futura, GO-029).
type Definition struct {
	Initial string
	Steps   map[string]Step
}
