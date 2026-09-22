// Catálogo de traduções da UI (GO-047) — divergência DELIBERADA do
// mecanismo do legado (`packages/server/app.js`, lib npm `i18n`, ~36
// idiomas em `models/config.ts:available_languages`, catálogo JSON por
// locale): aqui são só 2 idiomas reais (pt/en — pt porque é o idioma em
// que todo este port foi escrito desde GO-018), com chaves SEMÂNTICAS
// (nunca a própria string em português como chave — evita colisão de
// pontuação/espaço e torna reescrever um texto uma mudança de VALOR, não
// de chave/API). Ver decisão de escopo completa em
// docs/migracao-go/execucoes/GO-047.md — o catálogo de ~36 idiomas, a UI
// de administração de idiomas/strings (`/localizer`) e a tradução
// assistida por LLM (`saltcorn dev translate`) ficam fora desta entrega.
export type Locale = "pt" | "en";

export const SUPPORTED_LOCALES: Locale[] = ["pt", "en"];

// Levantamento REAL de todas as strings fixas de produto do frontend
// React (App.tsx/EditorPage.tsx são páginas de demonstração/dev,
// documentado no próprio comentário de cada arquivo desde GO-018/019 —
// não são alvo de tradução).
export const translations: Record<Locale, Record<string, string>> = {
  pt: {
    "topbar.expandSidebar": "Expandir menu lateral",
    "topbar.collapseSidebar": "Recolher menu lateral",
    "topbar.language": "Idioma",
    "list.noRecords": "Nenhum registro.",
    "list.delete": "Excluir",
    "list.nextPage": "Próxima página",
    "edit.save": "Salvar",
    "views.listError": "Erro ao listar views: {error}",
    "views.loading": "Carregando views…",
    "views.columnName": "Nome",
    "views.columnTemplate": "Template",
    "views.columnStatus": "Status",
    "views.statusPublished": "publicada",
    "views.statusDraft": "rascunho",
    "views.viewButton": "Visualizar",
    "views.previewLoading": "Carregando pré-visualização…",
    "views.unsupported": "Esta view usa um recurso ainda não suportado pelo novo runtime: {reason}. Administre-a pelo sistema atual enquanto isso.",
    "views.previewError": "Erro ao pré-visualizar: {message}",
    "views.navigateReload": "Salvo — recarregando a própria view.",
    "views.navigateReferer": "Salvo — voltaria para a página de onde veio.",
    "views.navigateView": 'Salvo — iria para a view "{viewName}".',
  },
  en: {
    "topbar.expandSidebar": "Expand sidebar",
    "topbar.collapseSidebar": "Collapse sidebar",
    "topbar.language": "Language",
    "list.noRecords": "No records.",
    "list.delete": "Delete",
    "list.nextPage": "Next page",
    "edit.save": "Save",
    "views.listError": "Error listing views: {error}",
    "views.loading": "Loading views…",
    "views.columnName": "Name",
    "views.columnTemplate": "Template",
    "views.columnStatus": "Status",
    "views.statusPublished": "published",
    "views.statusDraft": "draft",
    "views.viewButton": "Preview",
    "views.previewLoading": "Loading preview…",
    "views.unsupported": "This view uses a feature not yet supported by the new runtime: {reason}. Manage it through the current system for now.",
    "views.previewError": "Error previewing: {message}",
    "views.navigateReload": "Saved — reloading this view.",
    "views.navigateReferer": "Saved — would go back to the referring page.",
    "views.navigateView": 'Saved — would go to view "{viewName}".',
  },
};
