// Página administrativa "Views" (GO-020) — lista as views do tenant e
// permite pré-visualizar cada uma. É o primeiro consumidor real de
// `GET /api/bff/views` (listagem) e `GET /api/bff/views/:id/render`
// (GO-020) dentro do shell React (ADR-0002), usando as mesmas classes SB
// Admin 2 (`table table-sm`, `badge`, `btn`) já reaproveitadas em
// Sidebar.tsx/Shell.tsx (GO-018) e ListView.tsx.
//
// "Layouts incompatíveis... seguem rota legada explícita" (critério de
// aceite de GO-020): ao pré-visualizar uma view fora do subconjunto
// suportado, o BffClient lança ViewUnsupportedError (motivo específico do
// Go) — esta página mostra esse motivo e um apontamento explícito para o
// sistema atual, nunca uma tentativa de desenhar uma tabela quebrada.
import { useEffect, useState } from "react";
import { BffClient, ViewUnsupportedError, type BffClientError } from "../bffClient";
import { ListView, type ListViewPlan } from "./ListView";

export interface ViewSummary {
  id: number;
  name: string;
  template: string;
  min_role: number;
}

export interface ViewsListPageProps {
  bffClient: BffClient;
}

type PreviewState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; plan: ListViewPlan }
  | { status: "unsupported"; reason: string }
  | { status: "error"; message: string };

export function ViewsListPage({ bffClient }: ViewsListPageProps) {
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

  async function handlePreview(id: number, cursor?: string) {
    setPreviewId(id);
    setPreview({ status: "loading" });
    try {
      const plan = await bffClient.renderView(id, cursor ? { cursor } : {});
      setPreview({ status: "ready", plan: plan as unknown as ListViewPlan });
    } catch (err) {
      if (err instanceof ViewUnsupportedError) {
        setPreview({ status: "unsupported", reason: err.message });
        return;
      }
      setPreview({ status: "error", message: (err as BffClientError).message });
    }
  }

  if (error) return <p role="alert">Erro ao listar views: {error}</p>;
  if (views === null) return <p>Carregando views…</p>;

  return (
    <div data-testid="views-list-page">
      <table className="table table-sm">
        <thead>
          <tr>
            <th>Nome</th>
            <th>Template</th>
            <th>Status</th>
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
                  {v.min_role === 100 ? "publicada" : "rascunho"}
                </span>
              </td>
              <td>
                <button type="button" className="btn btn-sm btn-outline-primary" onClick={() => handlePreview(v.id)}>
                  Visualizar
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {previewId !== null && (
        <div data-testid="views-preview">
          {preview.status === "loading" && <p>Carregando pré-visualização…</p>}
          {preview.status === "ready" && <ListView plan={preview.plan} onNextPage={(cursor) => handlePreview(previewId, cursor)} />}
          {preview.status === "unsupported" && (
            <p role="alert" data-testid="views-unsupported">
              Esta view usa um recurso ainda não suportado pelo novo runtime: {preview.reason}. Administre-a pelo sistema atual enquanto isso.
            </p>
          )}
          {preview.status === "error" && <p role="alert">Erro ao pré-visualizar: {preview.message}</p>}
        </div>
      )}
    </div>
  );
}
