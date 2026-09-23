// Editor visual de workflow (GO-048) — porta o MECANISMO do editor
// legado (`packages/workflow-editor`, React Flow) para este frontend
// novo, usando @xyflow/react (o sucessor mantido da mesma biblioteca) e
// consumindo a definição persistida por `internal/workflow` (nova nesta
// task — até aqui só existia estado de EXECUÇÃO, ver
// docs/migracao-go/execucoes/GO-048.md).
//
// Divergências deliberadas do editor legado (documentadas na íntegra na
// execução da task, resumo aqui para quem só lê o código):
// - Ramificação binária (next_step / else_step conforme only_if), nunca
//   a expressão N-vias livre do legado — o motor Go só executa a
//   primeira, então a UI nunca oferece o que o backend não roda.
// - Conectar next_step/else_step é feito pelos seletores do painel
//   lateral, não arrastando uma aresta no canvas
//   (`nodesConnectable={false}`) — mais simples e sem ambiguidade sobre
//   qual handle (next vs. else) uma conexão arrastada representaria.
// - Catálogo de ações limitado a `set_context`/`count_rows` (efeito
//   interno puro) — sem os ~14 builtins do legado nem geração por IA;
//   ver actions.go do backend para a razão de send_email/webhook não
//   entrarem ainda.
// - Sem nó "ForLoop" dedicado nem handles especiais de loop — um loop é
//   só uma aresta comum apontando para um passo anterior.
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  type Node,
  type Edge,
  type NodeChange,
  applyNodeChanges,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { BffClient, WorkflowConflictError, WorkflowUnrunnableError, type Workflow, type WorkflowStep, type WorkflowRun } from "../bffClient";
import { useT } from "../i18n/I18nContext";

export interface WorkflowEditorPageProps {
  bffClient: BffClient;
}

type WorkflowSummary = { id: number; name: string; initial_step: string; _version: string };

const ACTIONS = ["set_context", "count_rows"] as const;
type ActionName = (typeof ACTIONS)[number];

function isKnownAction(name: string): name is ActionName {
  return (ACTIONS as readonly string[]).includes(name);
}

// Referência ESTÁVEL para "sem passos ainda" — `workflow?.steps ?? []`
// pareceria equivalente, mas criaria um array literal NOVO a cada render
// enquanto `workflow` for null; como esse valor alimenta o useMemo de
// `nodes` logo abaixo (dependência `[steps, ...]`), uma referência nova a
// cada render faz o useMemo recalcular sempre, o que por sua vez refaz o
// efeito de sincronização com `renderedNodes` sempre — um laço
// render→efeito→setState infinito enquanto nenhum workflow está
// selecionado (achado real desta task, via instrumentação de contagem de
// render — sem esta constante, a suíte de teste trava, RAM/CPU sobem sem
// limite).
const EMPTY_STEPS: WorkflowStep[] = [];

export function WorkflowEditorPage({ bffClient }: WorkflowEditorPageProps) {
  const t = useT();
  const [workflows, setWorkflows] = useState<WorkflowSummary[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [newWorkflowName, setNewWorkflowName] = useState("");
  const [newStepName, setNewStepName] = useState("");
  const [selectedStepId, setSelectedStepId] = useState<number | null>(null);
  const [run, setRun] = useState<WorkflowRun | null>(null);
  const [runError, setRunError] = useState<string | null>(null);

  const loadWorkflows = useCallback(async () => {
    try {
      const list = await bffClient.listWorkflows();
      setWorkflows(list);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [bffClient]);

  const loadWorkflow = useCallback(
    async (id: number) => {
      try {
        const wf = await bffClient.getWorkflow(id);
        setWorkflow(wf);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    },
    [bffClient]
  );

  useEffect(() => {
    void loadWorkflows();
  }, [loadWorkflows]);

  useEffect(() => {
    if (selectedId !== null) void loadWorkflow(selectedId);
    else setWorkflow(null);
  }, [selectedId, loadWorkflow]);

  const steps = workflow?.steps ?? EMPTY_STEPS;
  const selectedStep = steps.find((s) => s.id === selectedStepId) ?? null;

  const nodes: Node[] = useMemo(
    () =>
      steps.map((s) => ({
        id: String(s.id),
        position: { x: s.position_x, y: s.position_y },
        data: {
          label: (
            <div data-testid={`workflow-node-${s.name}`}>
              <strong>{s.name}</strong>
              {workflow?.initial_step === s.name ? (
                <span style={{ marginLeft: 6, fontSize: 10 }}>({t("workflow.initialBadge")})</span>
              ) : null}
              <div style={{ fontSize: 11, opacity: 0.7 }}>{s.action_name}</div>
            </div>
          ),
        },
      })),
    [steps, workflow?.initial_step, t]
  );

  const edges: Edge[] = useMemo(() => {
    const nameToId = new Map(steps.map((s) => [s.name, String(s.id)]));
    const out: Edge[] = [];
    for (const s of steps) {
      if (s.next_step && nameToId.has(s.next_step)) {
        out.push({ id: `e-${s.id}-next`, source: String(s.id), target: nameToId.get(s.next_step)! });
      }
      if (s.only_if && s.else_step && nameToId.has(s.else_step)) {
        out.push({
          id: `e-${s.id}-else`,
          source: String(s.id),
          target: nameToId.get(s.else_step)!,
          label: t("workflow.elseLabel"),
          style: { strokeDasharray: "4 4" },
        });
      }
    }
    return out;
  }, [steps, t]);

  const [renderedNodes, setRenderedNodes] = useState<Node[]>(nodes);
  useEffect(() => setRenderedNodes(nodes), [nodes]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setRenderedNodes((nds) => applyNodeChanges(changes, nds));
  }, []);

  const onNodeDragStop = useCallback(
    async (_event: unknown, node: Node) => {
      if (!workflow) return;
      const step = steps.find((s) => String(s.id) === node.id);
      if (!step) return;
      try {
        const updated = await bffClient.updateWorkflowStep(workflow.id, step.id, {
          _version: step._version,
          position_x: node.position.x,
          position_y: node.position.y,
        });
        setWorkflow((wf) => (wf ? { ...wf, steps: wf.steps!.map((s) => (s.id === updated.id ? updated : s)) } : wf));
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    },
    [bffClient, workflow, steps]
  );

  async function handleCreateWorkflow() {
    if (!newWorkflowName.trim()) return;
    try {
      const wf = await bffClient.createWorkflow({ name: newWorkflowName.trim() });
      setNewWorkflowName("");
      await loadWorkflows();
      setSelectedId(wf.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleAddStep() {
    if (!workflow || !newStepName.trim()) return;
    try {
      const created = await bffClient.createWorkflowStep(workflow.id, {
        name: newStepName.trim(),
        action_name: "set_context",
        configuration: { values: {} },
        position_x: 40 + steps.length * 180,
        position_y: 80,
      });
      setNewStepName("");
      setWorkflow((wf) => (wf ? { ...wf, steps: [...(wf.steps ?? []), created] } : wf));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleMarkInitial() {
    if (!workflow || !selectedStep) return;
    try {
      const updated = await bffClient.updateWorkflow(workflow.id, { _version: workflow._version, initial_step: selectedStep.name });
      setWorkflow((wf) => (wf ? { ...wf, initial_step: updated.initial_step, _version: updated._version } : wf));
    } catch (err) {
      setError(err instanceof WorkflowConflictError ? err.message : err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveStep(patch: {
    action_name: string;
    configuration: Record<string, unknown>;
    only_if: string;
    next_step: string;
    else_step: string;
    error_step: string;
  }) {
    if (!workflow || !selectedStep) return;
    try {
      const updated = await bffClient.updateWorkflowStep(workflow.id, selectedStep.id, { _version: selectedStep._version, ...patch });
      setWorkflow((wf) => (wf ? { ...wf, steps: wf.steps!.map((s) => (s.id === updated.id ? updated : s)) } : wf));
    } catch (err) {
      setError(err instanceof WorkflowConflictError ? err.message : err instanceof Error ? err.message : String(err));
    }
  }

  async function handleDeleteStep() {
    if (!workflow || !selectedStep) return;
    try {
      await bffClient.deleteWorkflowStep(workflow.id, selectedStep.id);
      setWorkflow((wf) => (wf ? { ...wf, steps: wf.steps!.filter((s) => s.id !== selectedStep.id) } : wf));
      setSelectedStepId(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRun() {
    if (!workflow) return;
    setRunError(null);
    setRun(null);
    try {
      const result = await bffClient.runWorkflow(workflow.id, {});
      setRun(result);
    } catch (err) {
      if (err instanceof WorkflowUnrunnableError) setRunError(err.message);
      else setRunError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div>
      <div style={{ display: "flex", gap: 8, alignItems: "center", padding: 8, flexWrap: "wrap" }}>
        <select
          data-testid="workflow-select"
          value={selectedId ?? ""}
          onChange={(e) => setSelectedId(e.target.value ? Number(e.target.value) : null)}
        >
          <option value="">{t("workflow.selectPlaceholder")}</option>
          {workflows.map((wf) => (
            <option key={wf.id} value={wf.id}>
              {wf.name}
            </option>
          ))}
        </select>
        <input
          data-testid="new-workflow-name"
          placeholder={t("workflow.newNamePlaceholder")}
          value={newWorkflowName}
          onChange={(e) => setNewWorkflowName(e.target.value)}
        />
        <button data-testid="create-workflow-button" onClick={() => void handleCreateWorkflow()}>
          {t("workflow.createButton")}
        </button>
        {workflow ? (
          <>
            <input
              data-testid="new-step-name"
              placeholder={t("workflow.newStepPlaceholder")}
              value={newStepName}
              onChange={(e) => setNewStepName(e.target.value)}
            />
            <button data-testid="add-step-button" onClick={() => void handleAddStep()}>
              {t("workflow.addStepButton")}
            </button>
            <button data-testid="run-workflow-button" onClick={() => void handleRun()} disabled={!workflow.initial_step}>
              {t("workflow.runButton")}
            </button>
          </>
        ) : null}
      </div>

      {error ? <div role="alert">{error}</div> : null}
      {runError ? (
        <div role="alert" data-testid="run-error">
          {t("workflow.runError", { message: runError })}
        </div>
      ) : null}
      {run ? (
        <pre data-testid="run-result">
          {t("workflow.runStatus", { status: run.status })}
          {"\n"}
          {JSON.stringify(run.context, null, 2)}
        </pre>
      ) : null}

      {workflow ? (
        <div style={{ display: "flex" }}>
          <div style={{ width: 600, height: 400, border: "1px solid #ccc" }}>
            <ReactFlow
              nodes={renderedNodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onNodeDragStop={(e, node) => void onNodeDragStop(e, node)}
              onNodeClick={(_e, node) => setSelectedStepId(Number(node.id))}
              nodesConnectable={false}
            >
              <Background />
              <Controls />
            </ReactFlow>
          </div>
          {selectedStep ? (
            <StepPanel
              key={selectedStep.id}
              step={selectedStep}
              isInitial={workflow.initial_step === selectedStep.name}
              otherStepNames={steps.filter((s) => s.id !== selectedStep.id).map((s) => s.name)}
              onSave={handleSaveStep}
              onDelete={() => void handleDeleteStep()}
              onMarkInitial={() => void handleMarkInitial()}
            />
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

interface StepPanelProps {
  step: WorkflowStep;
  isInitial: boolean;
  otherStepNames: string[];
  onSave: (patch: {
    action_name: string;
    configuration: Record<string, unknown>;
    only_if: string;
    next_step: string;
    else_step: string;
    error_step: string;
  }) => void;
  onDelete: () => void;
  onMarkInitial: () => void;
}

// StepPanel edita um passo selecionado — formulário nativo React (sem a
// infraestrutura de `showIf`/Monaco/HTML renderizado no servidor que o
// editor legado usava para isso, ver preflight de GO-048): o catálogo de
// ações desta entrega é pequeno o bastante (2 ações) para não precisar
// de um mecanismo de formulário dinâmico genérico.
function StepPanel({ step, isInitial, otherStepNames, onSave, onDelete, onMarkInitial }: StepPanelProps) {
  const t = useT();
  const [actionName, setActionName] = useState(step.action_name);
  const [onlyIf, setOnlyIf] = useState(step.only_if);
  const [nextStep, setNextStep] = useState(step.next_step);
  const [elseStep, setElseStep] = useState(step.else_step);
  const [errorStep, setErrorStep] = useState(step.error_step);
  const [values, setValues] = useState(() => JSON.stringify((step.configuration as { values?: unknown })?.values ?? {}, null, 2));
  const [table, setTable] = useState(() => String((step.configuration as { table?: string })?.table ?? ""));
  const [output, setOutput] = useState(() => String((step.configuration as { output?: string })?.output ?? ""));

  function handleSave() {
    let configuration: Record<string, unknown> = {};
    if (actionName === "set_context") {
      try {
        configuration = { values: JSON.parse(values || "{}") };
      } catch {
        configuration = { values: {} };
      }
    } else if (actionName === "count_rows") {
      configuration = { table, output };
    }
    onSave({ action_name: actionName, configuration, only_if: onlyIf, next_step: nextStep, else_step: elseStep, error_step: errorStep });
  }

  return (
    <div data-testid="step-panel" style={{ width: 280, padding: 8, borderLeft: "1px solid #ccc" }}>
      <h3>{step.name}</h3>

      <label>
        {t("workflow.actionLabel")}
        <select data-testid="step-action-select" value={actionName} onChange={(e) => setActionName(e.target.value)}>
          {ACTIONS.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
          {!isKnownAction(actionName) ? <option value={actionName}>{actionName}</option> : null}
        </select>
      </label>

      {actionName === "set_context" ? (
        <label>
          {t("workflow.valuesLabel")}
          <textarea data-testid="step-values-input" value={values} onChange={(e) => setValues(e.target.value)} rows={4} />
        </label>
      ) : null}
      {actionName === "count_rows" ? (
        <>
          <label>
            {t("workflow.tableLabel")}
            <input data-testid="step-table-input" value={table} onChange={(e) => setTable(e.target.value)} />
          </label>
          <label>
            {t("workflow.outputLabel")}
            <input data-testid="step-output-input" value={output} onChange={(e) => setOutput(e.target.value)} />
          </label>
        </>
      ) : null}

      <label>
        {t("workflow.onlyIfLabel")}
        <input data-testid="step-only-if-input" value={onlyIf} onChange={(e) => setOnlyIf(e.target.value)} />
      </label>
      <label>
        {t("workflow.nextStepLabel")}
        <select data-testid="step-next-select" value={nextStep} onChange={(e) => setNextStep(e.target.value)}>
          <option value="">{t("workflow.noneOption")}</option>
          {otherStepNames.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <label>
        {t("workflow.elseStepLabel")}
        <select data-testid="step-else-select" value={elseStep} onChange={(e) => setElseStep(e.target.value)}>
          <option value="">{t("workflow.noneOption")}</option>
          {otherStepNames.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <label>
        {t("workflow.errorStepLabel")}
        <select data-testid="step-error-select" value={errorStep} onChange={(e) => setErrorStep(e.target.value)}>
          <option value="">{t("workflow.noneOption")}</option>
          {otherStepNames.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>

      <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
        <button data-testid="step-save-button" onClick={handleSave}>
          {t("workflow.saveStepButton")}
        </button>
        <button data-testid="step-mark-initial-button" onClick={onMarkInitial} disabled={isInitial}>
          {t("workflow.markInitialButton")}
        </button>
        <button data-testid="step-delete-button" onClick={onDelete}>
          {t("workflow.deleteStepButton")}
        </button>
      </div>
    </div>
  );
}
