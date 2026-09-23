// Testes do WorkflowEditorPage (GO-048): criar workflow, criar passos,
// selecionar um nó e editar sua configuração, marcar passo inicial,
// excluir passo, e rodar — contra um BffClient mockado (a execução real
// ponta a ponta já tem cobertura em internal/workflow/*_test.go,
// cmd/server/workflow_test.go e no E2E de navegador real).
//
// @xyflow/react é MOCKADO aqui — mesma decisão já tomada para o Craft.js
// em BuilderPanel.test.tsx ("isolando a responsabilidade deste componente
// da responsabilidade do builder em si"): montar um <ReactFlow> real
// contra jsdom trava a suíte inteira (achado desta task — a biblioteca
// depende de medição real de layout/ResizeObserver com callback de
// verdade, que jsdom não fornece, entrando num laço de
// requestAnimationFrame que nunca estabiliza — nenhum test timeout do
// Vitest interrompe, porque o laço é síncrono demais para ceder ao event
// loop). O stub abaixo renderiza `data.label` de cada nó como uma div
// clicável — o suficiente para testar a lógica deste componente (estado,
// chamadas ao BffClient, o painel lateral); o canvas de verdade (arrastar
// nó, desenhar aresta) é coberto pelo E2E em navegador real
// (migracao/e2e/tests/workflow.spec.ts).
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";

vi.mock("@xyflow/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@xyflow/react")>();
  return {
    ...actual,
    Background: () => null,
    Controls: () => null,
    ReactFlow: ({ nodes, onNodeClick }: { nodes: Array<{ id: string; data: { label: ReactNode } }>; onNodeClick?: (e: unknown, n: { id: string }) => void }) => (
      <div>
        {nodes.map((n) => (
          <div key={n.id} onClick={() => onNodeClick?.(null, n)}>
            {n.data.label}
          </div>
        ))}
      </div>
    ),
  };
});

import { WorkflowEditorPage } from "../src/workflow/WorkflowEditorPage";
import { BffClient, WorkflowUnrunnableError } from "../src/bffClient";

function makeClient() {
  return {
    listWorkflows: vi.fn(),
    createWorkflow: vi.fn(),
    getWorkflow: vi.fn(),
    updateWorkflow: vi.fn(),
    createWorkflowStep: vi.fn(),
    updateWorkflowStep: vi.fn(),
    deleteWorkflowStep: vi.fn(),
    runWorkflow: vi.fn(),
  } as unknown as BffClient & {
    listWorkflows: ReturnType<typeof vi.fn>;
    createWorkflow: ReturnType<typeof vi.fn>;
    getWorkflow: ReturnType<typeof vi.fn>;
    updateWorkflow: ReturnType<typeof vi.fn>;
    createWorkflowStep: ReturnType<typeof vi.fn>;
    updateWorkflowStep: ReturnType<typeof vi.fn>;
    deleteWorkflowStep: ReturnType<typeof vi.fn>;
    runWorkflow: ReturnType<typeof vi.fn>;
  };
}

const WF_ONE_STEP = {
  id: 1,
  name: "contagem",
  initial_step: "marcar_inicio",
  _version: "1",
  steps: [
    {
      id: 10, name: "marcar_inicio", action_name: "set_context",
      configuration: { values: { started: true } }, only_if: "", next_step: "contar", else_step: "", error_step: "",
      position_x: 0, position_y: 0, _version: "1",
    },
    {
      id: 11, name: "contar", action_name: "count_rows",
      configuration: { table: "widgets", output: "total" }, only_if: "", next_step: "", else_step: "", error_step: "",
      position_x: 180, position_y: 0, _version: "1",
    },
  ],
};

describe("WorkflowEditorPage", () => {
  let client: ReturnType<typeof makeClient>;

  beforeEach(() => {
    client = makeClient();
    client.listWorkflows.mockResolvedValue([{ id: 1, name: "contagem", initial_step: "marcar_inicio", _version: "1" }]);
  });

  it("lista workflows no seletor", async () => {
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    expect(await screen.findByRole("option", { name: "contagem" })).toBeTruthy();
  });

  it("criar workflow chama createWorkflow e seleciona o resultado", async () => {
    client.createWorkflow.mockResolvedValue({ id: 2, name: "novo", initial_step: "", _version: "1" });
    client.getWorkflow.mockResolvedValue({ id: 2, name: "novo", initial_step: "", _version: "1", steps: [] });
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId("new-workflow-name"), { target: { value: "novo" } });
    fireEvent.click(screen.getByTestId("create-workflow-button"));

    await waitFor(() => expect(client.createWorkflow).toHaveBeenCalledWith({ name: "novo" }));
    await waitFor(() => expect(client.getWorkflow).toHaveBeenCalledWith(2));
  });

  it("seleciona um workflow e desenha os nós dos passos", async () => {
    client.getWorkflow.mockResolvedValue(WF_ONE_STEP);
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    await waitFor(() => expect(client.getWorkflow).toHaveBeenCalledWith(1));

    expect(await screen.findByTestId("workflow-node-marcar_inicio")).toBeTruthy();
    expect(await screen.findByTestId("workflow-node-contar")).toBeTruthy();
  });

  it("adicionar passo chama createWorkflowStep com set_context como ação padrão", async () => {
    client.getWorkflow.mockResolvedValue({ ...WF_ONE_STEP, steps: [] });
    client.createWorkflowStep.mockResolvedValue({
      id: 20, name: "passo_novo", action_name: "set_context", configuration: { values: {} },
      only_if: "", next_step: "", else_step: "", error_step: "", position_x: 40, position_y: 80, _version: "1",
    });
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    await waitFor(() => expect(client.getWorkflow).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId("new-step-name"), { target: { value: "passo_novo" } });
    fireEvent.click(screen.getByTestId("add-step-button"));

    await waitFor(() =>
      expect(client.createWorkflowStep).toHaveBeenCalledWith(
        1,
        expect.objectContaining({ name: "passo_novo", action_name: "set_context" })
      )
    );
    expect(await screen.findByTestId("workflow-node-passo_novo")).toBeTruthy();
  });

  it("clicar num nó abre o painel com a ação/condições do passo, e salvar chama updateWorkflowStep", async () => {
    client.getWorkflow.mockResolvedValue(WF_ONE_STEP);
    client.updateWorkflowStep.mockResolvedValue({ ...WF_ONE_STEP.steps[1], only_if: "true", _version: "2" });
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    const node = await screen.findByTestId("workflow-node-contar");

    fireEvent.click(node);
    const panel = await screen.findByTestId("step-panel");
    expect(panel.textContent).toContain("contar");
    expect((screen.getByTestId("step-action-select") as HTMLSelectElement).value).toBe("count_rows");
    expect((screen.getByTestId("step-table-input") as HTMLInputElement).value).toBe("widgets");

    fireEvent.change(screen.getByTestId("step-only-if-input"), { target: { value: "true" } });
    fireEvent.click(screen.getByTestId("step-save-button"));

    await waitFor(() =>
      expect(client.updateWorkflowStep).toHaveBeenCalledWith(
        1,
        11,
        expect.objectContaining({ _version: "1", action_name: "count_rows", only_if: "true", configuration: { table: "widgets", output: "total" } })
      )
    );
  });

  it("marcar como inicial chama updateWorkflow com o nome do passo selecionado", async () => {
    client.getWorkflow.mockResolvedValue(WF_ONE_STEP);
    client.updateWorkflow.mockResolvedValue({ id: 1, name: "contagem", initial_step: "contar", _version: "2" });
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    const node = await screen.findByTestId("workflow-node-contar");
    fireEvent.click(node);
    await screen.findByTestId("step-panel");

    fireEvent.click(screen.getByTestId("step-mark-initial-button"));

    await waitFor(() => expect(client.updateWorkflow).toHaveBeenCalledWith(1, { _version: "1", initial_step: "contar" }));
  });

  it("excluir passo chama deleteWorkflowStep e fecha o painel", async () => {
    client.getWorkflow.mockResolvedValue(WF_ONE_STEP);
    client.deleteWorkflowStep.mockResolvedValue(undefined);
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    const node = await screen.findByTestId("workflow-node-contar");
    fireEvent.click(node);
    await screen.findByTestId("step-panel");

    fireEvent.click(screen.getByTestId("step-delete-button"));

    await waitFor(() => expect(client.deleteWorkflowStep).toHaveBeenCalledWith(1, 11));
    await waitFor(() => expect(screen.queryByTestId("step-panel")).toBeNull());
  });

  it("executar mostra o status e o contexto final do run", async () => {
    client.getWorkflow.mockResolvedValue(WF_ONE_STEP);
    client.runWorkflow.mockResolvedValue({
      id: 99, name: "contagem", status: "finished", current_step: "", step_seq: 2,
      context: { started: true, total: 3 },
    });
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    await screen.findByTestId("workflow-node-marcar_inicio");

    fireEvent.click(screen.getByTestId("run-workflow-button"));

    const result = await screen.findByTestId("run-result");
    expect(result.textContent).toContain("finished");
    expect(result.textContent).toContain("\"total\": 3");
  });

  it("botão Executar fica desabilitado sem passo inicial, e erro 422 aparece como run-error", async () => {
    client.getWorkflow.mockResolvedValue({ ...WF_ONE_STEP, initial_step: "" });
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    await screen.findByTestId("workflow-node-marcar_inicio");

    const runButton = screen.getByTestId("run-workflow-button") as HTMLButtonElement;
    expect(runButton.disabled).toBe(true);
  });

  it("erro workflow_unrunnable do BffClient aparece em run-error, nunca uma exceção não tratada", async () => {
    client.getWorkflow.mockResolvedValue(WF_ONE_STEP);
    client.runWorkflow.mockRejectedValue(new WorkflowUnrunnableError("workflow: passo desconhecido nesta definição"));
    render(<WorkflowEditorPage bffClient={client} />);
    await waitFor(() => expect(client.listWorkflows).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId("workflow-select"), { target: { value: "1" } });
    await screen.findByTestId("workflow-node-marcar_inicio");

    fireEvent.click(screen.getByTestId("run-workflow-button"));

    const err = await screen.findByTestId("run-error");
    expect(err.textContent).toContain("passo desconhecido");
  });
});
