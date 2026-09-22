// Desenha e submete o DTO de uma view "Edit" (GO-039,
// internal/views/edit.go do lado Go) — o formulário de escrita real do
// pack piloto guitars (`form_action`). Cada campo vira um `<input>` ou
// `<select>` (fieldview "select", opções reais trazidas pelo Go) — sem
// nenhum widget de terceiro: `flatpickr` (o plugin de terceiro real do
// pack, sem código-fonte neste checkout — ver GO-001/GO-029) vira um
// `<input type="date">` HTML5 NATIVO (GO-042), não um `<input
// type="text">` simples — um substituto funcional real (seletor de
// calendário, validação de formato pelo próprio navegador), nunca uma
// mera ausência de widget. `internal/records.coerceJSONValue` (Go) só
// aceita RFC3339 completo (`time.Parse(time.RFC3339, s)`), nunca a forma
// curta `AAAA-MM-DD` que `<input type="date">` produz/consome — este
// componente converte nas duas direções (`dateValueFor`/
// `coerceForSubmit`), o Go nunca precisa saber a diferença. O componente
// só desenha e coage valores para o tipo que o Go espera
// (`coerceForSubmit`); quem decide o que fazer depois de submeter
// (`navigate`) é o chamador via `onSubmit`.
import { useState, type FormEvent } from "react";

export interface EditViewFieldOption {
  id: number;
  label: string;
}

export interface EditViewField {
  field_name: string;
  label: string;
  field_type: string;
  fieldview: string;
  required: boolean;
  config?: Record<string, unknown>;
  value: unknown;
  options?: EditViewFieldOption[];
}

export interface EditViewPlan {
  view_id: number;
  table: string;
  record_id: number;
  _version?: string;
  fields: EditViewField[];
  action_name: string;
}

export interface EditViewProps {
  plan: EditViewPlan;
  onSubmit?: (values: Record<string, unknown>) => void;
  submitting?: boolean;
}

function inputTypeFor(field: EditViewField): string {
  if (field.field_type === "integer" || field.field_type === "float") return "number";
  if (field.field_type === "date") return "date";
  return "text";
}

/**
 * dateValueFor extrai a parte `AAAA-MM-DD` de um valor RFC3339 vindo do
 * Go (ex.: "2024-01-15T00:00:00Z" → "2024-01-15") — o único formato que
 * `<input type="date">` aceita como `value`; qualquer outra coisa (valor
 * nulo, string já curta, string não reconhecida) passa como está ou vira
 * "" — nunca um `<input>` HTML5 recebendo um valor que o navegador
 * rejeitaria silenciosamente.
 */
function dateValueFor(raw: string): string {
  const match = /^\d{4}-\d{2}-\d{2}/.exec(raw);
  return match ? match[0] : raw;
}

/**
 * coerceForSubmit converte o valor de string do formulário HTML para o
 * tipo que internal/records espera (ver internal/records.coerceJSONValue
 * do lado Go, GO-039 — números/booleans decodificados de JSON, nunca de
 * um `<input>` de string direto). Campo vazio vira `undefined` (omitido
 * do corpo, nunca um valor inválido submetido). Campo `date`: o
 * navegador sempre entrega `AAAA-MM-DD` (GO-042) — completado para
 * RFC3339 (`T00:00:00Z`), o único formato que `coerceJSONValue` do Go
 * aceita.
 */
function coerceForSubmit(field: EditViewField, raw: string): unknown {
  if (raw === "") return undefined;
  if (field.field_type === "integer" || field.field_type === "key" || field.field_type === "float") {
    const n = Number(raw);
    return Number.isNaN(n) ? raw : n;
  }
  if (field.field_type === "date") return `${raw}T00:00:00Z`;
  return raw;
}

export function EditView({ plan, onSubmit, submitting }: EditViewProps) {
  const [values, setValues] = useState<Record<string, string>>(() => {
    const initial: Record<string, string> = {};
    for (const f of plan.fields) {
      const raw = f.value === null || f.value === undefined ? "" : String(f.value);
      initial[f.field_name] = f.field_type === "date" ? dateValueFor(raw) : raw;
    }
    return initial;
  });

  function handleChange(fieldName: string, raw: string) {
    setValues((v) => ({ ...v, [fieldName]: raw }));
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const submitted: Record<string, unknown> = {};
    for (const f of plan.fields) {
      const coerced = coerceForSubmit(f, values[f.field_name] ?? "");
      if (coerced !== undefined) submitted[f.field_name] = coerced;
    }
    onSubmit?.(submitted);
  }

  return (
    <form data-testid="edit-view" onSubmit={handleSubmit}>
      {plan.fields.map((f) => (
        <div className="mb-3" key={f.field_name}>
          <label className="form-label" htmlFor={`edit-field-${f.field_name}`}>
            {f.label}
            {f.required ? " *" : ""}
          </label>
          {f.fieldview === "select" ? (
            <select
              id={`edit-field-${f.field_name}`}
              className="form-select"
              required={f.required}
              value={values[f.field_name] ?? ""}
              onChange={(e) => handleChange(f.field_name, e.target.value)}
            >
              <option value="">—</option>
              {(f.options ?? []).map((o) => (
                <option key={o.id} value={o.id}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              id={`edit-field-${f.field_name}`}
              className="form-control"
              type={inputTypeFor(f)}
              required={f.required}
              value={values[f.field_name] ?? ""}
              onChange={(e) => handleChange(f.field_name, e.target.value)}
            />
          )}
        </div>
      ))}
      <button type="submit" className="btn btn-primary" disabled={submitting}>
        {plan.action_name === "Save" || plan.action_name === "SubmitWithAjax" ? "Salvar" : plan.action_name}
      </button>
    </form>
  );
}
