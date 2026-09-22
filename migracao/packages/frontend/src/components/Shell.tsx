// Casca administrativa — reimplementa a estrutura de
// packages/saltcorn-sbadmin2/index.js (`wrap()`, linhas 504-543):
// #wrapper > sidebar + #content-wrapper > #content > conteúdo da página.
// Mesmos IDs/classes (para reaproveitar sb-admin-2.min.css sem alterar
// uma linha de CSS), toda a interatividade (toggle da sidebar) é estado
// React local, propagado como props — nenhum script legado é carregado
// nesta árvore.
import { useState, type ReactNode } from "react";
import { Sidebar } from "./Sidebar";
import { Topbar } from "./Topbar";
import type { Brand, MenuSection } from "../types/menu";

export interface ShellProps {
  brand: Brand;
  sections: MenuSection[];
  currentUrl: string;
  title?: string;
  children: ReactNode;
  /** GO-047 — repassados à Topbar; omitidos = seletor de idioma não aparece. */
  locale?: string;
  onChangeLocale?: (locale: string) => void;
}

export function Shell({ brand, sections, currentUrl, title, children, locale, onChangeLocale }: ShellProps) {
  const [collapsed, setCollapsed] = useState(false);

  return (
    <div id="page-top">
      <div id="wrapper">
        {sections.length > 0 && (
          <Sidebar brand={brand} sections={sections} currentUrl={currentUrl} collapsed={collapsed} />
        )}
        <div id="content-wrapper" className="d-flex flex-column" style={{ minWidth: 0, overflowX: "hidden" }}>
          <Topbar
            title={title}
            collapsed={collapsed}
            onToggleSidebar={() => setCollapsed((c) => !c)}
            locale={locale}
            onChangeLocale={onChangeLocale}
          />
          <div id="content">
            <div id="page-inner-content" className="container-fluid px-2 sbadmin2-theme">
              {children}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
