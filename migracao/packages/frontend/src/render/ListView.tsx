// Desenha o DTO de renderização de uma view "List" (GO-020,
// internal/views/render.go do lado Go) — só o shape estrutural (colunas +
// linhas + paginação), reaproveitando as MESMAS classes CSS que
// packages/saltcorn-markup/table.ts já usa (`table table-sm`,
// `table-hover`), para não inventar uma aparência nova para uma tabela que
// já existe no produto legado. Fieldviews de célula (badges, links, HTML
// customizado) ficam fora do subconjunto suportado (ver nota de escopo em
// docs/migracao-go/execucoes/GO-020.md) — toda célula de dado aqui é
// texto plano. Estendido em GO-039: colunas "join_field" (mesma célula de
// texto, valor já trazido pelo Go sob a chave "<local>__<remoto>") e
// "action" (um botão por linha, ex.: "Excluir" — o Go já confirmou que a
// view declara essa ação antes de expor a coluna).
export interface ListViewColumn {
  /** Ausente = "field", mesmo default de antes de GO-039 (compatibilidade). */
  kind?: "field" | "join_field" | "action";
  field_name?: string;
  header_label?: string;
  action_name?: string;
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
  /** Chamado ao clicar num botão de ação de coluna (ex.: "Excluir") — o id/versão vêm da própria linha (rows sempre inclui id/_version, GO-013/GO-039). */
  onRowAction?: (actionName: string, row: Record<string, unknown>) => void;
}

function cellText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "boolean") return value ? "true" : "false";
  return String(value);
}

function columnKind(c: ListViewColumn): "field" | "join_field" | "action" {
  return c.kind ?? "field";
}

function columnKey(c: ListViewColumn, i: number): string {
  return c.field_name ?? c.action_name ?? String(i);
}

export function ListView({ plan, onNextPage, onRowAction }: ListViewProps) {
  return (
    <div data-testid="list-view">
      <table className="table table-sm table-hover">
        <thead>
          <tr>
            {plan.columns.map((c, i) => (
              <th key={columnKey(c, i)}>
                {columnKind(c) === "action" ? "" : c.header_label}
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
                {plan.columns.map((c, j) => {
                  const kind = columnKind(c);
                  if (kind === "action") {
                    return (
                      <td key={columnKey(c, j)}>
                        <button
                          type="button"
                          className="btn btn-sm btn-outline-danger"
                          onClick={() => onRowAction?.(c.action_name ?? "", row)}
                        >
                          {c.action_name === "Delete" ? "Excluir" : c.action_name}
                        </button>
                      </td>
                    );
                  }
                  return <td key={columnKey(c, j)}>{cellText(row[c.field_name ?? ""])}</td>;
                })}
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
