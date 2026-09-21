// Desenha o DTO de renderização de uma view "Show" (GO-039,
// internal/views/show.go do lado Go) — os valores já resolvidos de UM
// registro, num layout de definição (rótulo + valor), mesma classe
// `table-borderless` do SB Admin 2 usada para fichas de detalhe.
export interface ShowViewColumn {
  field_name: string;
  header_label: string;
}

export interface ShowViewPlan {
  view_id: number;
  table: string;
  record_id: number;
  columns: ShowViewColumn[];
  values: Record<string, unknown>;
}

export interface ShowViewProps {
  plan: ShowViewPlan;
}

function cellText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "boolean") return value ? "true" : "false";
  return String(value);
}

export function ShowView({ plan }: ShowViewProps) {
  return (
    <table className="table table-sm table-borderless" data-testid="show-view">
      <tbody>
        {plan.columns.map((c) => (
          <tr key={c.field_name}>
            <th scope="row">{c.header_label}</th>
            <td>{cellText(plan.values[c.field_name])}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
