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
import { useT } from "../i18n/I18nContext";

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

// NestedEditViewPlan (GO-051) — uma view Edit embutida via nó de layout
// `type: "view"` (ex.: create_guitar embute edit_processed_embed), uma
// linha por registro filho já existente. Ver comentário de
// docs/migracao-go/execucoes/GO-051.md para a decisão de escopo:
// "adicionar uma linha filha nova" fica fora desta entrega — o backend
// só expõe o formato dos campos de uma linha filha quando pelo menos uma
// já existe (rows vazio não tem de onde tirar a forma de um formulário
// em branco).
export interface NestedEditViewPlan {
  view_id: number;
  view_name: string;
  child_table: string;
  fk_field: string;
  parent_id: number;
  rows: EditViewPlan[];
}

export interface EditViewPlan {
  view_id: number;
  table: string;
  record_id: number;
  _version?: string;
  fields: EditViewField[];
  action_name: string;
  nested?: NestedEditViewPlan[];
}

export interface EditViewProps {
  plan: EditViewPlan;
  onSubmit?: (values: Record<string, unknown>) => void;
  submitting?: boolean;
  // onSubmitNested (GO-051) — o pai (ViewsListPage) decide COMO submeter
  // (sabe view_id/record_id da linha filha, chama submitView com o
  // mesmo mecanismo de qualquer view Edit standalone); este componente
  // só desenha e coleta valores, igual ao onSubmit do nível superior.
  onSubmitNested?: (childPlan: EditViewPlan, values: Record<string, unknown>) => void;
  // onUploadFile/fileDownloadUrl (GO-051) — a fieldview "upload" precisa
  // enviar o arquivo (devolve um id) e mostrar um link para o arquivo
  // atual, se houver; ausentes = a fieldview "upload" desenha só o
  // `<input type="file">`, sem link de arquivo atual nem upload real
  // (mesmo espírito de onSubmit ausente não submeter nada).
  onUploadFile?: (file: File) => Promise<number>;
  fileDownloadUrl?: (fileId: number) => string;
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
  // "file" (GO-051): mesma representação física de "key"/"integer" — o
  // valor é sempre o id (inteiro) devolvido por um upload já concluído,
  // nunca o próprio arquivo (que já foi enviado separadamente antes de
  // chegar aqui, ver handleFileInputChange).
  if (field.field_type === "integer" || field.field_type === "key" || field.field_type === "float" || field.field_type === "file") {
    const n = Number(raw);
    return Number.isNaN(n) ? raw : n;
  }
  if (field.field_type === "date") return `${raw}T00:00:00Z`;
  return raw;
}

export function EditView({ plan, onSubmit, submitting, onSubmitNested, onUploadFile, fileDownloadUrl }: EditViewProps) {
  const t = useT();
  const [values, setValues] = useState<Record<string, string>>(() => {
    const initial: Record<string, string> = {};
    for (const f of plan.fields) {
      const raw = f.value === null || f.value === undefined ? "" : String(f.value);
      initial[f.field_name] = f.field_type === "date" ? dateValueFor(raw) : raw;
    }
    return initial;
  });
  // uploadingFields (GO-051) — nomes de campo com um upload em curso,
  // só para desabilitar o próprio `<input type="file">` e mostrar
  // feedback ("Enviando…") enquanto isso — nunca bloqueia o resto do
  // formulário.
  const [uploadingFields, setUploadingFields] = useState<Record<string, boolean>>({});

  function handleChange(fieldName: string, raw: string) {
    setValues((v) => ({ ...v, [fieldName]: raw }));
  }

  async function handleFileInputChange(fieldName: string, fileList: FileList | null) {
    const file = fileList?.[0];
    if (!file || !onUploadFile) return;
    setUploadingFields((u) => ({ ...u, [fieldName]: true }));
    try {
      const uploaded = await onUploadFile(file);
      handleChange(fieldName, String(uploaded));
    } finally {
      setUploadingFields((u) => ({ ...u, [fieldName]: false }));
    }
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
    <div data-testid="edit-view-container">
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
          ) : f.fieldview === "upload" ? (
            <div>
              {values[f.field_name] && fileDownloadUrl ? (
                <div className="mb-1">
                  <a href={fileDownloadUrl(Number(values[f.field_name]))} target="_blank" rel="noreferrer" data-testid={`edit-field-${f.field_name}-current`}>
                    {t("edit.currentFile")}
                  </a>
                </div>
              ) : null}
              <input
                id={`edit-field-${f.field_name}`}
                className="form-control"
                type="file"
                disabled={uploadingFields[f.field_name]}
                onChange={(e) => void handleFileInputChange(f.field_name, e.target.files)}
              />
              {uploadingFields[f.field_name] ? (
                <span data-testid={`edit-field-${f.field_name}-uploading`}>{t("edit.uploading")}</span>
              ) : null}
            </div>
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
        {plan.action_name === "Save" || plan.action_name === "SubmitWithAjax" ? t("edit.save") : plan.action_name}
      </button>
      </form>

      {/* Views aninhadas (GO-051) — FORA do <form> pai de propósito:
          cada linha filha é sua própria <EditView>, e HTML não permite
          <form> aninhado (um <form> dentro de outro é inválido — o
          navegador/React descartaria ou o submit da linha filha
          dispararia o form ERRADO). Um grupo por relação embutida
          (ex.: edit_processed_embed dentro de create_guitar), uma
          sub-EditView por linha filha JÁ EXISTENTE. "Adicionar uma linha
          filha nova" fica fora desta entrega — ver comentário de
          NestedEditViewPlan. */}
      {(plan.nested ?? []).map((group) => (
        <fieldset className="mt-3 border rounded p-2" key={group.view_id} data-testid={`nested-group-${group.view_name}`}>
          <legend className="fs-6">{group.view_name}</legend>
          {group.rows.length === 0 ? (
            <p data-testid={`nested-group-${group.view_name}-empty`}>{t("edit.nestedEmpty")}</p>
          ) : (
            group.rows.map((row) => (
              <div className="mb-2" key={row.record_id} data-testid={`nested-row-${group.view_name}-${row.record_id}`}>
                <EditView
                  plan={row}
                  onSubmit={(values) => onSubmitNested?.(row, values)}
                  onUploadFile={onUploadFile}
                  fileDownloadUrl={fileDownloadUrl}
                />
              </div>
            ))
          )}
        </fieldset>
      ))}
    </div>
  );
}
