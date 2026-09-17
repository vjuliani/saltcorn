// Mesma estrutura de dados que packages/saltcorn-sbadmin2/index.js usa para
// montar a sidebar (sidebar(brand, sections, currentUrl, isRTL)) — portada
// aqui para continuidade de contrato, não uma invenção nova. A diferença
// deliberada: lá o HTML final é montado por composição de strings (@saltcorn/
// markup/tags) e a interatividade (collapse de subitens) depende de
// bootstrap.bundle.min.js lendo `data-bs-toggle`/`data-bs-target` — aqui a
// mesma estrutura de dados vira componentes React com estado próprio, sem
// nenhum script legado tocando o DOM (ver Sidebar.tsx).
export interface MenuItem {
  label: string;
  link?: string;
  icon?: string;
  tooltip?: string;
  target_blank?: boolean;
  altlinks?: string[];
  subitems?: MenuItem[];
  type?: "Separator" | "Search";
}

export interface MenuSection {
  section?: string;
  items: MenuItem[];
}

export interface Brand {
  logo?: string;
  name: string;
}

/**
 * activeChecker/active — porta de packages/saltcorn-sbadmin2/index.js
 * (`active(currentUrl, item)`, linhas 108-116): um item está ativo se seu
 * link bate com a URL atual, um altlink bate, ou algum subitem bate.
 * `activeChecker` real (`@saltcorn/markup/layout_utils`) faz comparação de
 * path com normalização de barra final — replicada aqui de forma mínima
 * (comparação exata + prefixo), suficiente para o critério de aceite de
 * "navegação funciona"; casos de borda de normalização de URL ficam para
 * quando este shell substituir de fato as páginas legadas (fora do
 * escopo desta tarefa, que é o shell em si, não a migração de rotas).
 */
export function isLinkActive(link: string, currentUrl: string): boolean {
  if (!link || !currentUrl) return false;
  if (link === currentUrl) return true;
  if (link !== "/" && currentUrl.startsWith(link)) return true;
  return false;
}

export function isItemActive(item: MenuItem, currentUrl: string): boolean {
  if (item.link && isLinkActive(item.link, currentUrl)) return true;
  if (item.altlinks?.some((l) => isLinkActive(l, currentUrl))) return true;
  if (
    item.subitems?.some(
      (si) =>
        (si.link && isLinkActive(si.link, currentUrl)) ||
        si.altlinks?.some((l) => isLinkActive(l, currentUrl))
    )
  )
    return true;
  return false;
}
