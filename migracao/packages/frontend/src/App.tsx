// Ponto de montagem de demonstração/dev deste pacote — não é uma rota
// real do produto (GO-018 é o shell em si, migrar páginas legadas para
// dentro dele é trabalho de tarefas futuras, GO-020/021). Mostra o Shell
// com uma sidebar de exemplo e o BuilderPanel com dados de mock.
import { useState } from "react";
import { Shell } from "./components/Shell";
import { BuilderPanel, MOCK_BUILDER_LAYOUT, MOCK_BUILDER_OPTIONS } from "./builder/BuilderPanel";
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

export function App() {
  const [showBuilder, setShowBuilder] = useState(false);

  return (
    <Shell brand={{ name: "Saltcorn" }} sections={DEMO_SECTIONS} currentUrl="/" title="Painel">
      <button type="button" className="btn btn-primary mb-3" onClick={() => setShowBuilder((s) => !s)}>
        {showBuilder ? "Esconder editor" : "Abrir editor (dados de mock)"}
      </button>
      {showBuilder && (
        <BuilderPanel layout={MOCK_BUILDER_LAYOUT} options={MOCK_BUILDER_OPTIONS} mode="page" />
      )}
    </Shell>
  );
}
