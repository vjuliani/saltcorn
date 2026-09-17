// Desenha o DTO de renderização de uma view "List" (GO-020,
// internal/views/render.go do lado Go) — só o shape estrutural (colunas +
// linhas + paginação), reaproveitando as MESMAS classes CSS que
// packages/saltcorn-markup/table.ts já usa (`table table-sm`,
// `table-hover`), para não inventar uma aparência nova para uma tabela que
// já existe no produto legado. Fieldviews de célula (badges, links, HTML
// customizado) ficam fora do subconjunto suportado (ver nota de escopo em
// docs/migracao-go/execucoes/GO-020.md) — toda célula aqui é texto plano.
export interface ListViewColumn {
  field_name: string;
  header_label: string;
}

export interface ListViewPlan {
  view_id: number;
  columns: ListViewColumn[];
  rows: Array<Record<string, unknown>>;
  order_by: string;
  descending: boolean;
  next_cursor?: string | null;
}

export interface ListViewProps {
  plan: ListViewPlan;
  onNextPage?: (cursor: string) => void;
}

function cellText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "boolean") return value ? "true" : "false";
  return String(value);
}

export function ListView({ plan, onNextPage }: ListViewProps) {
  return (
    <div data-testid="list-view">
      <table className="table table-sm table-hover">
        <thead>
          <tr>
            {plan.columns.map((c) => (
              <th key={c.field_name}>
                {c.header_label}
                {plan.order_by === c.field_name && (plan.descending ? " ▼" : " ▲")}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {plan.rows.length === 0 ? (
            <tr>
              <td colSpan={plan.columns.length || 1}>Nenhum registro.</td>
            </tr>
          ) : (
            plan.rows.map((row, i) => (
              <tr key={i}>
                {plan.columns.map((c) => (
                  <td key={c.field_name}>{cellText(row[c.field_name])}</td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
      {plan.next_cursor && (
        <button type="button" className="btn btn-sm btn-outline-secondary" onClick={() => onNextPage?.(plan.next_cursor!)}>
          Próxima página
        </button>
      )}
    </div>
  );
}
