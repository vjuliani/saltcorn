// Topbar mínima e fiel ao que o legado realmente monta: a versão de
// packages/saltcorn-sbadmin2/index.js usada neste repositório (`wrap()`,
// linhas 504-543) não embute uma topbar completa (busca/dropdown de
// usuário) no HTML — só o botão de colapsar a sidebar
// (`#sidebarToggle`, dentro da própria sidebar). Uma topbar de usuário
// completa não é fabricada aqui sem essa referência real; o que existe
// hoje (o toggle) é portado com o mesmo cuidado de "sem disputa de DOM"
// do Sidebar: onClick em vez de um listener externo lendo
// `data-sidebar-toggler`. GO-047 acrescenta o seletor de idioma — o
// único controle de preferência de usuário que este shell tem hoje,
// então vive aqui em vez de uma página de conta própria (que não existe
// ainda).
import { SUPPORTED_LOCALES } from "../i18n/translations";
import { useT } from "../i18n/I18nContext";

export interface TopbarProps {
  title?: string;
  collapsed: boolean;
  onToggleSidebar: () => void;
  /** Locale ATUAL (já resolvido pelo BFF) — omitido = seletor não aparece (ex.: Topbar usado sem I18nProvider/bffClient real). */
  locale?: string;
  onChangeLocale?: (locale: string) => void;
}

export function Topbar({ title, collapsed, onToggleSidebar, locale, onChangeLocale }: TopbarProps) {
  const t = useT();
  return (
    <nav className="navbar navbar-expand navbar-light bg-white topbar mb-4 static-top shadow" data-testid="topbar">
      <button
        type="button"
        id="sidebarToggleTop"
        className="btn btn-link d-md-none rounded-circle me-3"
        aria-label={collapsed ? t("topbar.expandSidebar") : t("topbar.collapseSidebar")}
        aria-pressed={collapsed}
        onClick={onToggleSidebar}
      >
        <i className="fa fa-bars" />
      </button>
      {title && <h1 className="h5 mb-0 text-gray-800">{title}</h1>}
      {onChangeLocale && (
        <select
          className="form-select form-select-sm ms-auto me-3"
          style={{ width: "auto" }}
          aria-label={t("topbar.language")}
          value={locale}
          onChange={(e) => onChangeLocale(e.target.value)}
          data-testid="locale-select"
        >
          {SUPPORTED_LOCALES.map((l) => (
            <option key={l} value={l}>
              {l.toUpperCase()}
            </option>
          ))}
        </select>
      )}
    </nav>
  );
}
