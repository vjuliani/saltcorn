// Reimplementação em React de packages/saltcorn-sbadmin2/index.js
// (sidebar/sideBarSection/sideBarItem/subItem, linhas 43-259) — mesma
// estrutura de dados (MenuSection/MenuItem, ver ../types/menu.ts), mesmas
// classes CSS (para reaproveitar sb-admin-2.min.css sem alteração), mas
// SEM `data-bs-toggle`/`data-bs-target`: o collapse de subitens é estado
// React (`useState`), não um listener de bootstrap.bundle.min.js lendo
// atributos do DOM. É exatamente isso que faz "sidebar... funciona sem
// disputa de DOM entre React e scripts legados" (critério de aceite de
// GO-018) — não há NENHUM script legado no DOM desta página para disputar
// nada com o React.
import { useState } from "react";
import type { Brand, MenuItem, MenuSection } from "../types/menu";
import { isItemActive } from "../types/menu";

export interface SidebarProps {
  brand: Brand;
  sections: MenuSection[];
  currentUrl: string;
  collapsed?: boolean;
  onToggleCollapse?: () => void;
}

function labelToId(item: MenuItem): string {
  return item.label.replace(/\s/g, "");
}

function SubItem({ item, currentUrl }: { item: MenuItem; currentUrl: string }) {
  const [open, setOpen] = useState(false);

  if (item.subitems) {
    return (
      <div className={`dropdown-item btn-group ${open ? "show" : ""}`}>
        <button
          type="button"
          className="dropdown-item dropdown-toggle p-0 border-0 bg-transparent text-start w-100"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {item.label}
        </button>
        <ul className={`dropdown-menu ${open ? "show" : ""}`}>
          {item.subitems.map((si) => (
            <li key={si.label}>
              <SubItem item={si} currentUrl={currentUrl} />
            </li>
          ))}
        </ul>
      </div>
    );
  }
  if (item.link) {
    return (
      <a
        className={`collapse-item ${isItemActive(item, currentUrl) ? "active" : ""}`}
        href={item.link}
        target={item.target_blank ? "_blank" : undefined}
        rel={item.target_blank ? "noreferrer" : undefined}
        title={item.tooltip}
      >
        {item.label}
      </a>
    );
  }
  if (item.type === "Separator") return <hr className="sidebar-divider my-0" />;
  return <h6 className="collapse-header">{item.label}</h6>;
}

function SidebarItem({ item, currentUrl }: { item: MenuItem; currentUrl: string }) {
  const isActive = isItemActive(item, currentUrl);
  // Estado inicial de expansão = mesma regra do legado (item ativo já vem
  // aberto, ver `class: ["collapse", is_active && "show"]` no original) —
  // mas daqui em diante é o React que controla, um clique nunca depende
  // de bootstrap.bundle.min.js interpretar data-bs-target.
  const [expanded, setExpanded] = useState(isActive);

  if (item.type === "Separator") return <hr className="sidebar-divider my-0" />;

  return (
    <li className={`nav-item ${isActive ? "active" : ""}`}>
      {item.subitems ? (
        <>
          <button
            type="button"
            className={`nav-link border-0 bg-transparent w-100 text-start ${!isActive && !expanded ? "collapsed" : ""}`}
            aria-expanded={expanded}
            aria-controls={`collapse${labelToId(item)}`}
            title={item.tooltip}
            onClick={() => setExpanded((e) => !e)}
          >
            {item.icon && <i className={`fas fa-fw fa-${item.icon}`} />}
            <span>{item.label}</span>
          </button>
          <div
            id={`collapse${labelToId(item)}`}
            className={`collapse ${expanded ? "show" : ""}`}
            hidden={!expanded}
          >
            <div className="bg-white py-2 collapse-inner rounded">
              {item.subitems.map((si) => (
                <SubItem key={si.label} item={si} currentUrl={currentUrl} />
              ))}
            </div>
          </div>
        </>
      ) : item.link ? (
        <a
          className="nav-link"
          href={item.link}
          target={item.target_blank ? "_blank" : undefined}
          rel={item.target_blank ? "noreferrer" : undefined}
          title={item.tooltip}
        >
          {item.icon && <i className={`fas fa-fw fa-${item.icon}`} />}
          <span>{item.label}</span>
        </a>
      ) : item.type === "Search" ? (
        <form action="/search" className="menusearch ms-2 me-3" method="get">
          <div className="input-group search-bar">
            <input
              type="search"
              className="form-control search-bar pl-2p5"
              placeholder={item.label}
              id="inputq"
              name="q"
              aria-label="Search"
            />
            <button className="btn btn-outline-secondary search-bar" type="submit">
              <i className="fas fa-search" />
            </button>
          </div>
        </form>
      ) : (
        <span className="nav-link">{item.label}</span>
      )}
    </li>
  );
}

export function Sidebar({ brand, sections, currentUrl, collapsed }: SidebarProps) {
  return (
    <ul
      className={`navbar-nav sidebar sidebar-dark accordion d-print-none ${collapsed ? "toggled" : ""}`}
      id="accordionSidebar"
      data-testid="sidebar"
    >
      <a className="sidebar-brand d-flex align-items-center justify-content-center" href="/">
        {brand.logo && (
          <div className="sidebar-brand-icon">
            <img src={brand.logo} width={35} height={35} alt="Logo" />
          </div>
        )}
        <div className="sidebar-brand-text mx-3">{brand.name}</div>
      </a>
      {sections.map((section, i) => (
        <div key={section.section ?? i}>
          {section.section && (
            <>
              <hr className="sidebar-divider" />
              <div className="sidebar-heading">{section.section}</div>
            </>
          )}
          {section.items.map((item) => (
            <SidebarItem key={item.label} item={item} currentUrl={currentUrl} />
          ))}
        </div>
      ))}
    </ul>
  );
}
