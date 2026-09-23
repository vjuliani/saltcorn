// Página administrativa "Views" (GO-020, estendida em GO-039) — lista as
// views do tenant e permite pré-visualizar cada uma. É o primeiro
// consumidor real de `GET /api/bff/views` (listagem) e
// `GET /api/bff/views/:id/render` (List/Show/Edit) dentro do shell React
// (ADR-0002), usando as mesmas classes SB Admin 2 (`table table-sm`,
// `badge`, `btn`) já reaproveitadas em Sidebar.tsx/Shell.tsx (GO-018) e
// ListView.tsx/ShowView.tsx/EditView.tsx.
//
// "Layouts incompatíveis... seguem rota legada explícita" (critério de
// aceite de GO-020): ao pré-visualizar uma view fora do subconjunto
// suportado, o BffClient lança ViewUnsupportedError (motivo específico do
// Go) — esta página mostra esse motivo e um apontamento explícito para o
// sistema atual, nunca uma tentativa de desenhar uma tabela quebrada.
//
// GO-039: o shape da resposta de renderView varia por template
// (List/Show/Edit) — esta página despacha por presença de campo
// (`"rows" in plan` etc.), o mesmo discriminador que o próprio contrato
// usa (oneOf sem propriedade de discriminação declarada), em vez de
// confiar no `template` da view (que só está disponível na listagem, não
// no plano em si).
import { useEffect, useState } from "react";
import { BffClient, ViewUnsupportedError, ViewConflictError, type BffClientError } from "../bffClient";
import { ListView, type ListViewPlan } from "./ListView";
import { ShowView, type ShowViewPlan } from "./ShowView";
import { EditView, type EditViewPlan } from "./EditView";
import { FeedView, type FeedViewPlan } from "./FeedView";
import { useT } from "../i18n/I18nContext";

export interface ViewSummary {
  id: number;
  name: string;
  template: string;
  min_role: number;
}

export interface ViewsListPageProps {
  bffClient: BffClient;
}

type RenderPlan = ListViewPlan | ShowViewPlan | EditViewPlan | FeedViewPlan;

type PreviewState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; plan: RenderPlan }
  | { status: "unsupported"; reason: string }
  | { status: "error"; message: string };

function isListPlan(plan: RenderPlan): plan is ListViewPlan {
  return "rows" in plan;
}
function isShowPlan(plan: RenderPlan): plan is ShowViewPlan {
  return "values" in plan;
}
function isEditPlan(plan: RenderPlan): plan is EditViewPlan {
  return "fields" in plan;
}
// isFeedPlan (GO-051) — "cards" é o único campo que não colide com
// nenhum dos outros três discriminadores (rows/values/fields), mesmo
// espírito dos demais.
function isFeedPlan(plan: RenderPlan): plan is FeedViewPlan {
  return "cards" in plan;
}

export function ViewsListPage({ bffClient }: ViewsListPageProps) {
  const t = useT();
  const [views, setViews] = useState<ViewSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [previewId, setPreviewId] = useState<number | null>(null);
  const [preview, setPreview] = useState<PreviewState>({ status: "idle" });

  useEffect(() => {
    let cancelled = false;
    bffClient
      .listViews()
      .then((list) => {
        if (!cancelled) setViews(list as unknown as ViewSummary[]);
      })
      .catch((err: BffClientError) => {
        if (!cancelled) setError(err.message);
      });
    return () => {
      cancelled = true;
    };
  }, [bffClient]);

  const [navigateMessage, setNavigateMessage] = useState<string | null>(null);

  async function handlePreview(id: number, query: { cursor?: string; record?: number } = {}) {
    setPreviewId(id);
    setNavigateMessage(null);
    setPreview({ status: "loading" });
    try {
      const plan = await bffClient.renderView(id, query);
      setPreview({ status: "ready", plan: plan as unknown as RenderPlan });
    } catch (err) {
      if (err instanceof ViewUnsupportedError) {
        setPreview({ status: "unsupported", reason: err.message });
        return;
      }
      setPreview({ status: "error", message: (err as BffClientError).message });
    }
  }

  // handleRowAction (GO-039) — a ação de coluna "Excluir" de uma List:
  // rows sempre inclui id/_version (GO-013/GO-039), então o botão nunca
  // precisa de uma leitura extra antes de deletar.
  async function handleRowAction(actionName: string, row: Record<string, unknown>) {
    if (actionName !== "Delete" || previewId === null) return;
    const id = row.id as number;
    const version = row._version as string;
    try {
      await bffClient.deleteViewRow(previewId, id, version);
      handlePreview(previewId);
    } catch (err) {
      if (err instanceof ViewConflictError) {
        setPreview({ status: "error", message: err.message });
        return;
      }
      setPreview({ status: "error", message: (err as BffClientError).message });
    }
  }

  // handleEditSubmit (GO-039) — form_action: cria (record_id=0) ou
  // atualiza (record_id!=0, com _version do plano já lido) um registro,
  // e mostra a decisão de navegação (`navigate`) — sem um roteador
  // client-side ainda (ver App.tsx, "Limitações"), esta página de
  // pré-visualização só INFORMA o que aconteceria a seguir, não navega
  // de fato.
  async function handleEditSubmit(plan: EditViewPlan, values: Record<string, unknown>) {
    if (previewId === null) return;
    try {
      const result = await bffClient.submitView(previewId, {
        record_id: plan.record_id || undefined,
        _version: plan._version,
        values,
      });
      const nav = (result as { navigate: { type: string; view_name?: string } }).navigate;
      // handlePreview reseta navigateMessage para null (é uma navegação
      // nova) — por isso a mensagem só é definida DEPOIS dele terminar,
      // nunca antes (achado real: definir antes fazia a mensagem
      // desaparecer no mesmo tick, um bug que só o teste com
      // findByTestId pegou).
      await handlePreview(previewId, plan.record_id ? { record: plan.record_id } : {});
      setNavigateMessage(
        nav.type === "reload"
          ? t("views.navigateReload")
          : nav.type === "referer"
            ? t("views.navigateReferer")
            : t("views.navigateView", { viewName: nav.view_name ?? "" })
      );
    } catch (err) {
      if (err instanceof ViewConflictError) {
        setPreview({ status: "error", message: err.message });
        return;
      }
      if (err instanceof ViewUnsupportedError) {
        setPreview({ status: "unsupported", reason: err.message });
        return;
      }
      setPreview({ status: "error", message: (err as BffClientError).message });
    }
  }

  // handleSubmitNested (GO-051) — submete uma linha FILHA (view/record_id
  // próprios, diferentes da view pai em preview) pelo MESMO mecanismo de
  // submitView de qualquer view Edit standalone; ao terminar, recarrega a
  // pré-visualização da view PAI (não a filha) para refletir a mudança no
  // grupo aninhado.
  async function handleSubmitNested(childPlan: EditViewPlan, values: Record<string, unknown>) {
    if (previewId === null) return;
    try {
      await bffClient.submitView(childPlan.view_id, {
        record_id: childPlan.record_id || undefined,
        _version: childPlan._version,
        values,
      });
      const parentRecordId = preview.status === "ready" && isEditPlan(preview.plan) ? preview.plan.record_id : undefined;
      await handlePreview(previewId, parentRecordId ? { record: parentRecordId } : {});
    } catch (err) {
      if (err instanceof ViewConflictError) {
        setPreview({ status: "error", message: err.message });
        return;
      }
      if (err instanceof ViewUnsupportedError) {
        setPreview({ status: "unsupported", reason: err.message });
        return;
      }
      setPreview({ status: "error", message: (err as BffClientError).message });
    }
  }

  // handleUploadFile (GO-051) — repassado ao EditView como `onUploadFile`;
  // devolve só o id (o formato que um valor de campo FieldFile precisa),
  // nunca o objeto File inteiro de volta.
  async function handleUploadFile(file: File): Promise<number> {
    const uploaded = await bffClient.uploadFile(file);
    return uploaded.id;
  }

  if (error) return <p role="alert">{t("views.listError", { error })}</p>;
  if (views === null) return <p>{t("views.loading")}</p>;

  return (
    <div data-testid="views-list-page">
      <table className="table table-sm">
        <thead>
          <tr>
            <th>{t("views.columnName")}</th>
            <th>{t("views.columnTemplate")}</th>
            <th>{t("views.columnStatus")}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {views.map((v) => (
            <tr key={v.id}>
              <td>{v.name}</td>
              <td>{v.template}</td>
              <td>
                <span className={`badge ${v.min_role === 100 ? "bg-success" : "bg-secondary"}`}>
                  {v.min_role === 100 ? t("views.statusPublished") : t("views.statusDraft")}
                </span>
              </td>
              <td>
                <button type="button" className="btn btn-sm btn-outline-primary" onClick={() => handlePreview(v.id)}>
                  {t("views.viewButton")}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {previewId !== null && (
        <div data-testid="views-preview">
          {navigateMessage && (
            <p role="status" data-testid="views-navigate-message">
              {navigateMessage}
            </p>
          )}
          {preview.status === "loading" && <p>{t("views.previewLoading")}</p>}
          {preview.status === "ready" && isListPlan(preview.plan) && (
            <ListView
              plan={preview.plan}
              onNextPage={(cursor) => handlePreview(previewId, { cursor })}
              onRowAction={handleRowAction}
            />
          )}
          {preview.status === "ready" && isShowPlan(preview.plan) && <ShowView plan={preview.plan} />}
          {preview.status === "ready" && isEditPlan(preview.plan) && (
            <EditView
              plan={preview.plan}
              onSubmit={(values) => handleEditSubmit(preview.plan as EditViewPlan, values)}
              onSubmitNested={handleSubmitNested}
              onUploadFile={handleUploadFile}
              fileDownloadUrl={(fileId) => bffClient.fileDownloadUrl(fileId)}
            />
          )}
          {preview.status === "ready" && isFeedPlan(preview.plan) && (
            <FeedView
              plan={preview.plan}
              onNextPage={(cursor) => handlePreview(previewId, { cursor })}
              onCreateClick={() => {
                const createId = (preview.plan as FeedViewPlan).view_to_create_id;
                if (createId) handlePreview(createId);
              }}
            />
          )}
          {preview.status === "unsupported" && (
            <p role="alert" data-testid="views-unsupported">
              {t("views.unsupported", { reason: preview.reason })}
            </p>
          )}
          {preview.status === "error" && <p role="alert">{t("views.previewError", { message: preview.message })}</p>}
        </div>
      )}
    </div>
  );
}
