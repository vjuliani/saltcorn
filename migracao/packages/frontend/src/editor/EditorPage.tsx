// Fluxo do editor conectado ao BFF real (GO-019) — criar tabela/campo,
// criar view, reabrir, salvar, publicar, e apresentar conflito de edição
// sem sobrescrever silenciosamente. Ponto de demonstração/dev deste
// pacote (mesmo espírito de App.tsx em GO-018) — não é uma rota real do
// produto ainda.
//
// Achado registrado, não contornado com um hack: o botão "Next" do
// builder Craft.js (Builder.js, `onClick`) grava o documento editado em
// `document.querySelector('form#scbuildform input[name=layout]')` e
// chama `document.getElementById("scbuildform").submit()` — um submit
// NATIVO de formulário HTML, que não dispara o evento `submit` (só
// `.requestSubmit()` dispara) e navegaria a página inteira. Isso é
// incompatível com uma SPA persistente sem um endpoint de formulário
// HTML de verdade (o backend Go/BFF fala JSON, não teria para onde esse
// POST nativo ir). Interceptar isso de forma confiável exigiria alterar
// o próprio builder (fora do escopo desta tarefa) ou um hack frágil
// (MutationObserver/monkey-patch de `HTMLFormElement.prototype.submit`).
// Por isso o botão "Salvar" abaixo é um controle PRÓPRIO do shell (fora
// do Craft.js), não o "Next" interno do builder — resolver a extração do
// documento editado de dentro do canvas fica para GO-020 (runtime de
// views, que já é quem decide composição BFF+renderização React).
import { useState } from "react";
import { BffClient, ViewConflictError, ViewUnsupportedError, type BffClientError } from "../bffClient";
import { BuilderPanel } from "../builder/BuilderPanel";
import type { LayoutSegment } from "../types/layout";

export interface EditorPageProps {
  bffClient: BffClient;
}

type ViewState = {
  id: number;
  name: string;
  table_id: number;
  template: string;
  min_role: number;
  configuration: LayoutSegment;
  _version: string;
};

// TITLE_FIELD_NAME/DEFAULT_LAYOUT (GO-021): o layout de demonstração
// precisava ser executável pelo runtime de renderização novo
// (internal/views/render.go, GO-020) para "Publicar" funcionar de
// verdade — um layout opaco (`{above: [...]}`, o shape de mock de
// GO-018/019) é classificado como incompatível e bloqueado na publicação
// desde GO-020. `handleCreateTable` por isso também cria um campo
// ("titulo", texto) logo após a tabela, e a view nasce já referenciando
// esse campo no shape suportado (`layout.besides` com uma coluna
// `{type: "Field", field_name}` — o mesmo shape real do viewtemplate
// List legado, não inventado).
const TITLE_FIELD_NAME = "titulo";
const DEFAULT_LAYOUT: LayoutSegment = {
  layout: { besides: [{ header_label: "Título", contents: { type: "Field", field_name: TITLE_FIELD_NAME } }] },
};

export function EditorPage({ bffClient }: EditorPageProps) {
  const [tableName, setTableName] = useState("");
  const [table, setTable] = useState<{ id: number; name: string } | null>(null);
  const [view, setView] = useState<ViewState | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);

  async function handleCreateTable() {
    setMessage(null);
    const created = await bffClient.createTable({ name: tableName });
    await bffClient.addField(created.name as string, { name: TITLE_FIELD_NAME, type: "text" });
    setTable({ id: created.id as number, name: created.name as string });
  }

  async function handleCreateView() {
    if (!table) return;
    setMessage(null);
    const created = await bffClient.createView({
      name: `${table.name}_view`,
      table: table.name,
      template: "List",
      configuration: DEFAULT_LAYOUT,
    });
    setView(created as ViewState);
  }

  async function handleReopen() {
    if (!view) return;
    const reopened = await bffClient.getView(view.id);
    setView(reopened as ViewState);
    setConflict(false);
    setMessage("View recarregada do servidor.");
  }

  async function handleSave() {
    if (!view) return;
    setConflict(false);
    setMessage(null);
    try {
      // Ver nota de escopo no topo do arquivo: reenvia a configuration
      // atual conhecida no shell (não a edição ao vivo de dentro do
      // canvas Craft.js — esse bridge é GO-020) — o que ESTE botão prova
      // é o ciclo salvar/conflito/publicar contra o BFF real, não a
      // extração de edição do builder.
      const saved = await bffClient.updateView(view.id, { _version: view._version, configuration: view.configuration });
      setView(saved as ViewState);
      setMessage("Salvo.");
    } catch (err) {
      if (err instanceof ViewConflictError) {
        setConflict(true);
        setMessage("Conflito de edição: alguém salvou esta view por cima da sua versão. Releia antes de tentar de novo.");
        return;
      }
      if (err instanceof ViewUnsupportedError) {
        setMessage(`Layout não suportado pelo runtime atual: ${err.message}`);
        return;
      }
      setMessage(`Erro ao salvar: ${(err as BffClientError).message}`);
    }
  }

  async function handlePublish() {
    if (!view) return;
    setConflict(false);
    setMessage(null);
    try {
      const published = await bffClient.updateView(view.id, { _version: view._version, min_role: 100 });
      setView(published as ViewState);
      setMessage("Publicada.");
    } catch (err) {
      if (err instanceof ViewConflictError) {
        setConflict(true);
        setMessage("Conflito de edição: releia antes de publicar.");
        return;
      }
      if (err instanceof ViewUnsupportedError) {
        setMessage(`Publicação bloqueada — layout não suportado pelo runtime atual: ${err.message}`);
        return;
      }
      setMessage(`Erro ao publicar: ${(err as BffClientError).message}`);
    }
  }

  return (
    <div data-testid="editor-page">
      {!table && (
        <div>
          <input
            aria-label="Nome da tabela"
            value={tableName}
            onChange={(e) => setTableName(e.target.value)}
            placeholder="nome da tabela"
          />
          <button type="button" onClick={handleCreateTable} disabled={!tableName}>
            Criar tabela
          </button>
        </div>
      )}

      {table && !view && (
        <button type="button" onClick={handleCreateView}>
          Criar view em "{table.name}"
        </button>
      )}

      {view && (
        <div>
          <p>
            View <strong>{view.name}</strong> — papel mínimo: {view.min_role === 100 ? "público (publicada)" : "admin (não publicada)"}
          </p>
          <BuilderPanel layout={view.configuration} options={{ csrfToken: "dev" }} mode="page" />
          <button type="button" onClick={handleSave}>
            Salvar
          </button>
          <button type="button" onClick={handlePublish} disabled={view.min_role === 100}>
            Publicar
          </button>
          <button type="button" onClick={handleReopen}>
            Reabrir
          </button>
        </div>
      )}

      {message && (
        <p role={conflict ? "alert" : "status"} data-testid={conflict ? "conflict-message" : "status-message"}>
          {message}
        </p>
      )}
    </div>
  );
}
