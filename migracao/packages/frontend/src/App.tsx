// Ponto de montagem de demonstração/dev deste pacote — não é uma rota
// real do produto (GO-018 é o shell em si, migrar páginas legadas para
// dentro dele é trabalho de tarefas futuras, GO-020/021). Mostra o Shell
// com uma sidebar de exemplo e o BuilderPanel com dados de mock.
import { useState } from "react";
import { Shell } from "./components/Shell";
import { BuilderPanel, MOCK_BUILDER_LAYOUT, MOCK_BUILDER_OPTIONS } from "./builder/BuilderPanel";
import { EditorPage } from "./editor/EditorPage";
import { ViewsListPage } from "./render/ViewsListPage";
import { BffClient, readCsrfCookie } from "./bffClient";
import type { MenuSection } from "./types/menu";

// O item "Views" aponta para "/views" na estrutura de dados (mesmo
// contrato de MenuSection/MenuItem de GO-018), mas Sidebar.tsx ainda
// renderiza um `<a href>` de verdade — sem um roteador client-side (fora
// do escopo desta tarefa, ver README "Limitações"), clicar nele navegaria
// a página inteira em vez de trocar o painel abaixo. Por isso o botão de
// demonstração abaixo, não o link da sidebar, é o que efetivamente abre
// ViewsListPage nesta entrega — o link na sidebar já existe como
// destino/documentação da rota real, que GO-021 é quem liga de fato.
const DEMO_SECTIONS: MenuSection[] = [
  {
    items: [{ label: "Início", link: "/", icon: "home" }],
  },
  {
    section: "Tabelas",
    items: [
      { label: "Registros", link: "/tables", icon: "table" },
      {
        label: "Configuração",
        subitems: [
          { label: "Campos", link: "/tables/fields" },
          { label: "Relações", link: "/tables/relations" },
        ],
      },
    ],
  },
  {
    section: "Views",
    items: [{ label: "Views", link: "/views", icon: "table" }],
  },
];

// Mesmo padrão de baseUrl relativo de goClient.ts/BFF: em produção, o
// BFF fica atrás do mesmo proxy reverso que serve este build estático
// (VITE_BFF_BASE_URL vazio = same-origin); em dev standalone (`npm run
// dev`), configurar VITE_BFF_BASE_URL apontando para o BFF local.
const bffClient = new BffClient({
  baseUrl: import.meta.env.VITE_BFF_BASE_URL ?? "",
  csrfToken: readCsrfCookie(),
});

export function App() {
  const [showBuilder, setShowBuilder] = useState(false);
  const [showEditor, setShowEditor] = useState(false);
  const [showViews, setShowViews] = useState(false);

  return (
    <Shell brand={{ name: "Saltcorn" }} sections={DEMO_SECTIONS} currentUrl="/" title="Painel">
      <button type="button" className="btn btn-primary mb-3 me-2" onClick={() => setShowBuilder((s) => !s)}>
        {showBuilder ? "Esconder editor" : "Abrir editor (dados de mock)"}
      </button>
      <button type="button" className="btn btn-secondary mb-3 me-2" onClick={() => setShowEditor((s) => !s)}>
        {showEditor ? "Esconder editor conectado" : "Abrir editor (conectado ao BFF)"}
      </button>
      <button type="button" className="btn btn-outline-primary mb-3" onClick={() => setShowViews((s) => !s)}>
        {showViews ? "Esconder views" : "Ver views (SB Admin 2)"}
      </button>
      {showBuilder && (
        <BuilderPanel layout={MOCK_BUILDER_LAYOUT} options={MOCK_BUILDER_OPTIONS} mode="page" />
      )}
      {showEditor && <EditorPage bffClient={bffClient} />}
      {showViews && <ViewsListPage bffClient={bffClient} />}
    </Shell>
  );
}
