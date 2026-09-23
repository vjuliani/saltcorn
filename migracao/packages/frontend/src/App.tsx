// Ponto de montagem de demonstração/dev deste pacote — não é uma rota
// real do produto (GO-018 é o shell em si, migrar páginas legadas para
// dentro dele é trabalho de tarefas futuras, GO-020/021). Mostra o Shell
// com uma sidebar de exemplo e o BuilderPanel com dados de mock.
//
// GO-047: é também o ÚNICO ponto de montagem real deste pacote (main.tsx
// só renderiza <App/>) — mesmo sendo "demonstração" quanto à navegação,
// é aqui que o I18nProvider precisa envolver a árvore inteira, já que
// ViewsListPage (produto real, GO-020/039) vive DENTRO dele. O locale
// vem do bootstrap real do BFF (GO-047: User.Language > cookie `lang` >
// default_locale do tenant > "pt") — nunca hardcoded.
import { useEffect, useState } from "react";
import { Shell } from "./components/Shell";
import { BuilderPanel, MOCK_BUILDER_LAYOUT, MOCK_BUILDER_OPTIONS } from "./builder/BuilderPanel";
import { EditorPage } from "./editor/EditorPage";
import { ViewsListPage } from "./render/ViewsListPage";
import { WorkflowEditorPage } from "./workflow/WorkflowEditorPage";
import { BffClient, readCsrfCookie } from "./bffClient";
import { I18nProvider } from "./i18n/I18nContext";
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
  {
    section: "Workflows",
    items: [{ label: "Workflows", link: "/workflows", icon: "table" }],
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
  const [showWorkflows, setShowWorkflows] = useState(false);
  // "pt" até o bootstrap real responder — mesmo fallback final que o BFF
  // usa (defaultLocaleDefault, GO-047), nunca uma tela em branco
  // esperando a rede.
  const [locale, setLocale] = useState("pt");

  useEffect(() => {
    let cancelled = false;
    bffClient
      .getBootstrap()
      .then((boot) => {
        if (!cancelled) setLocale(boot.locale);
      })
      .catch(() => {
        // Sem sessão/BFF indisponível: fica no fallback "pt" — a página
        // de demonstração já lida com erros de outra forma em cada
        // painel (EditorPage/ViewsListPage têm seu próprio tratamento);
        // um bootstrap que falha aqui nunca deveria travar a montagem
        // do shell inteiro.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function handleChangeLocale(next: string) {
    setLocale(next); // Otimista — a UI muda de idioma imediatamente, sem esperar a rede.
    try {
      await bffClient.setActorLanguage(next);
    } catch {
      // Falha ao persistir não desfaz a troca local — o usuário já vê o
      // idioma novo; a próxima sessão volta ao valor persistido
      // anteriormente, nunca um erro bloqueante por uma preferência de
      // exibição.
    }
  }

  return (
    <I18nProvider locale={locale}>
      <Shell
        brand={{ name: "Saltcorn" }}
        sections={DEMO_SECTIONS}
        currentUrl="/"
        title="Painel"
        locale={locale}
        onChangeLocale={handleChangeLocale}
      >
        <button type="button" className="btn btn-primary mb-3 me-2" onClick={() => setShowBuilder((s) => !s)}>
          {showBuilder ? "Esconder editor" : "Abrir editor (dados de mock)"}
        </button>
        <button type="button" className="btn btn-secondary mb-3 me-2" onClick={() => setShowEditor((s) => !s)}>
          {showEditor ? "Esconder editor conectado" : "Abrir editor (conectado ao BFF)"}
        </button>
        <button type="button" className="btn btn-outline-primary mb-3 me-2" onClick={() => setShowViews((s) => !s)}>
          {showViews ? "Esconder views" : "Ver views (SB Admin 2)"}
        </button>
        <button type="button" className="btn btn-outline-secondary mb-3" onClick={() => setShowWorkflows((s) => !s)}>
          {showWorkflows ? "Esconder workflows" : "Editor de workflows"}
        </button>
        {showBuilder && (
          <BuilderPanel layout={MOCK_BUILDER_LAYOUT} options={MOCK_BUILDER_OPTIONS} mode="page" />
        )}
        {showEditor && <EditorPage bffClient={bffClient} />}
        {showViews && <ViewsListPage bffClient={bffClient} />}
        {showWorkflows && <WorkflowEditorPage bffClient={bffClient} />}
      </Shell>
    </I18nProvider>
  );
}
