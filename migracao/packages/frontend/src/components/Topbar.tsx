// Topbar mínima e fiel ao que o legado realmente monta: a versão de
// packages/saltcorn-sbadmin2/index.js usada neste repositório (`wrap()`,
// linhas 504-543) não embute uma topbar completa (busca/dropdown de
// usuário) no HTML — só o botão de colapsar a sidebar
// (`#sidebarToggle`, dentro da própria sidebar). Uma topbar de usuário
// completa não é fabricada aqui sem essa referência real; o que existe
// hoje (o toggle) é portado com o mesmo cuidado de "sem disputa de DOM"
// do Sidebar: onClick em vez de um listener externo lendo
// `data-sidebar-toggler`.
export interface TopbarProps {
  title?: string;
  collapsed: boolean;
  onToggleSidebar: () => void;
}

export function Topbar({ title, collapsed, onToggleSidebar }: TopbarProps) {
  return (
    <nav className="navbar navbar-expand navbar-light bg-white topbar mb-4 static-top shadow" data-testid="topbar">
      <button
        type="button"
        id="sidebarToggleTop"
        className="btn btn-link d-md-none rounded-circle me-3"
        aria-label={collapsed ? "Expandir menu lateral" : "Recolher menu lateral"}
        aria-pressed={collapsed}
        onClick={onToggleSidebar}
      >
        <i className="fa fa-bars" />
      </button>
      {title && <h1 className="h5 mb-0 text-gray-800">{title}</h1>}
    </nav>
  );
}
