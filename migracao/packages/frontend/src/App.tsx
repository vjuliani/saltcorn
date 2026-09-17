// Ponto de montagem de demonstração/dev deste pacote — não é uma rota
// real do produto (GO-018 é o shell em si, migrar páginas legadas para
// dentro dele é trabalho de tarefas futuras, GO-020/021). Mostra o Shell
// com uma sidebar de exemplo e o BuilderPanel com dados de mock.
import { useState } from "react";
import { Shell } from "./components/Shell";
import { BuilderPanel, MOCK_BUILDER_LAYOUT, MOCK_BUILDER_OPTIONS } from "./builder/BuilderPanel";
import { EditorPage } from "./editor/EditorPage";
import { BffClient, readCsrfCookie } from "./bffClient";
import type { MenuSection } from "./types/menu";

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

  return (
    <Shell brand={{ name: "Saltcorn" }} sections={DEMO_SECTIONS} currentUrl="/" title="Painel">
      <button type="button" className="btn btn-primary mb-3 me-2" onClick={() => setShowBuilder((s) => !s)}>
        {showBuilder ? "Esconder editor" : "Abrir editor (dados de mock)"}
      </button>
      <button type="button" className="btn btn-secondary mb-3" onClick={() => setShowEditor((s) => !s)}>
        {showEditor ? "Esconder editor conectado" : "Abrir editor (conectado ao BFF)"}
      </button>
      {showBuilder && (
        <BuilderPanel layout={MOCK_BUILDER_LAYOUT} options={MOCK_BUILDER_OPTIONS} mode="page" />
      )}
      {showEditor && <EditorPage bffClient={bffClient} />}
    </Shell>
  );
}
