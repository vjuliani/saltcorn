// GO-048 — prova ponta a ponta, em navegador real, do critério de
// aceite central: "um usuário cria/edita um workflow visualmente no
// frontend novo (React) e o resultado é executado de ponta a ponta por
// internal/workflow". Cria uma tabela real via HTTP (mesma fronteira que
// count_rows consulta), depois usa a UI REAL do editor (canvas
// @xyflow/react + painel lateral) para montar um grafo de 2 passos
// (set_context -> count_rows) e rodar — nenhuma chamada direta a
// /api/bff/workflows/* neste teste, ao contrário do setup da tabela
// (que não tem UI própria ainda, mesmo padrão de sanitization.spec.ts).
import { test, expect } from "./fixtures";

interface Session {
  csrfToken: string;
}

function readSession(): Session {
  return JSON.parse(process.env.SALTCORN_E2E_SESSION_JSON ?? "{}");
}

const BFF_BASE_URL = process.env.SALTCORN_E2E_BFF_BASE_URL ?? "";

test("editor visual de workflow: criar 2 passos, ligar por next_step, marcar inicial e rodar até o fim", async ({ page }) => {
  const { csrfToken } = readSession();
  const tableName = `e2e_wf_widgets_${Date.now()}`;
  const headers = { "X-CSRF-Token": csrfToken };

  // Fixture: uma tabela real com 2 linhas — o alvo real que o passo
  // count_rows vai consultar via internal/metadata.GetTable + SELECT
  // count(*), provando que a contagem final não é um valor simulado.
  //
  // Achado real de TESTE (não do produto, ver docs/migracao-go/
  // execucoes/GO-048.md): o BFF calcula a Idempotency-Key de
  // createRecord de forma determinística a partir de (ator, tenant,
  // tabela, CORPO) — duas chamadas com o corpo IDÊNTICO ({}) colidem na
  // mesma chave e a segunda é tratada como retry da primeira,
  // devolvendo o registro já criado em vez de criar um segundo (mesmo
  // mecanismo, mesmo propósito, de createView/updateView). Por isso os
  // dois registros aqui precisam de um campo com valor DIFERENTE.
  const createTable = await page.request.post(`${BFF_BASE_URL}/api/bff/tables`, { headers, data: { name: tableName } });
  expect(createTable.ok()).toBeTruthy();
  await page.request.post(`${BFF_BASE_URL}/api/bff/tables/${tableName}/fields`, { headers, data: { name: "label", type: "text" } });
  await page.request.post(`${BFF_BASE_URL}/api/bff/tables/${tableName}/records`, { headers, data: { label: "a" } });
  await page.request.post(`${BFF_BASE_URL}/api/bff/tables/${tableName}/records`, { headers, data: { label: "b" } });

  await page.goto("/");
  await page.getByRole("button", { name: /Editor de workflows/ }).click();

  // Criar o workflow pela UI real.
  const workflowName = `e2e_wf_${Date.now()}`;
  await page.getByTestId("new-workflow-name").fill(workflowName);
  await page.getByTestId("create-workflow-button").click();
  await expect(page.getByTestId("workflow-select")).toHaveValue(/\d+/);

  // Passo 1: "inicio" (set_context, valor padrão) — grava um marcador no
  // contexto para provar que o motor Go realmente executou este passo.
  await page.getByTestId("new-step-name").fill("inicio");
  await page.getByTestId("add-step-button").click();
  const startNode = page.getByTestId("workflow-node-inicio");
  await expect(startNode).toBeVisible();

  // Passo 2: "contar" (será reconfigurado para count_rows abaixo).
  await page.getByTestId("new-step-name").fill("contar");
  await page.getByTestId("add-step-button").click();
  const countNode = page.getByTestId("workflow-node-contar");
  await expect(countNode).toBeVisible();

  // Configurar "inicio": values={"started":true}, next_step="contar".
  await startNode.click();
  const panel = page.getByTestId("step-panel");
  await expect(panel).toBeVisible();
  await page.getByTestId("step-values-input").fill('{"started": true}');
  await page.getByTestId("step-next-select").selectOption("contar");
  await page.getByTestId("step-save-button").click();

  // Marcar "inicio" como o passo inicial do workflow.
  await page.getByTestId("step-mark-initial-button").click();
  await expect(startNode.getByText(/início|start/)).toBeVisible();

  // Configurar "contar": action_name=count_rows, table=<tabela real>, output=total.
  await countNode.click();
  await expect(page.getByTestId("step-panel")).toBeVisible();
  await page.getByTestId("step-action-select").selectOption("count_rows");
  await page.getByTestId("step-table-input").fill(tableName);
  await page.getByTestId("step-output-input").fill("total");
  await page.getByTestId("step-save-button").click();

  // A prova ponta a ponta: rodar o workflow real via internal/workflow
  // (Compile + Start + RunToCompletion, cmd/server/workflow.go) e
  // confirmar o contexto final na tela.
  await page.getByTestId("run-workflow-button").click();
  const result = page.getByTestId("run-result");
  await expect(result).toBeVisible();
  await expect(result).toContainText("finished");
  await expect(result).toContainText('"started": true');
  await expect(result).toContainText('"total": 2');

  // Recarrega e reabre o MESMO workflow pelo seletor — confirma que a
  // definição foi realmente PERSISTIDA no Go (_sc_workflows/
  // _sc_workflow_steps), não só mantida em estado local do React.
  await page.reload();
  await page.getByRole("button", { name: /Editor de workflows/ }).click();
  await page.getByTestId("workflow-select").selectOption({ label: workflowName });
  await expect(page.getByTestId("workflow-node-inicio")).toBeVisible();
  await expect(page.getByTestId("workflow-node-contar")).toBeVisible();
  await expect(page.getByTestId("workflow-node-inicio").getByText(/início|start/)).toBeVisible();
});
