// Desenha e submete o DTO de uma view "Edit" (GO-039,
// internal/views/edit.go do lado Go) — o formulário de escrita real do
// pack piloto guitars (`form_action`). Cada campo vira um `<input>` ou
// `<select>` (fieldview "select", opções reais trazidas pelo Go) — sem
// nenhum widget de terceiro (flatpickr vira um `<input type="text">`
// simples, já que o Go só grava/lê a string RFC3339 por baixo, nunca
// interpreta o widget). O componente só desenha e coage valores para o
// tipo que o Go espera (`coerceForSubmit`); quem decide o que fazer
// depois de submeter (`navigate`) é o chamador via `onSubmit`.
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
  return "text";
}

/**
 * coerceForSubmit converte o valor de string do formulário HTML para o
 * tipo que internal/records espera (ver internal/records.coerceJSONValue
 * do lado Go, GO-039 — números/booleans decodificados de JSON, nunca de
 * um `<input>` de string direto). Campo vazio vira `undefined` (omitido
 * do corpo, nunca um valor inválido submetido).
 */
function coerceForSubmit(field: EditViewField, raw: string): unknown {
  if (raw === "") return undefined;
  if (field.field_type === "integer" || field.field_type === "key" || field.field_type === "float") {
    const n = Number(raw);
    return Number.isNaN(n) ? raw : n;
  }
  return raw;
}

export function EditView({ plan, onSubmit, submitting }: EditViewProps) {
  const [values, setValues] = useState<Record<string, string>>(() => {
    const initial: Record<string, string> = {};
    for (const f of plan.fields) initial[f.field_name] = f.value === null || f.value === undefined ? "" : String(f.value);
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
