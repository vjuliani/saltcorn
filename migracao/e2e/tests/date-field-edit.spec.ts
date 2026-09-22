// GO-042 — prova ponta a ponta, em navegador real, do substituto
// funcional do plugin de terceiro `@saltcorn/flatpickr-date` (sem
// código-fonte neste checkout, ver GO-001/GO-029): um `<input
// type="date">` HTML5 nativo (EditView.tsx), a MESMA fieldview
// "flatpickr" que o pack piloto `guitars` usa de verdade em
// `edit_processed_embed`/`upload_photo` (campo `date_processed`).
//
// Não importa literalmente o `pack.json` real do guitars — não existe
// hoje um tradutor do formato de pack do legado para
// internal/pack.Pack (formatos distintos, ver decisão de escopo em
// docs/migracao-go/execucoes/GO-042.md) — em vez disso, cria uma tabela/
// view Edit equivalente sob demanda (mesmo shape de `configuration.
// columns[]` que classifyEditColumns exige, a mesma fieldview real),
// exercitando a MESMA capacidade nova desta task. A criação de
// tabela/campo/view usa chamadas HTTP diretas ao BFF (setup de fixture,
// mesmo espírito de `cli e2e-seed`) — só a parte que a task realmente
// precisa provar (o campo de data nativo, o formulário Edit, o tema SB
// Admin 2, o ciclo salvar → reler → aparecer na List) passa pelo
// navegador real.
import { test, expect } from "./fixtures";

interface SeededSession {
  sessionId: string;
  csrfToken: string;
}

function readCsrfToken(): string {
  const raw = process.env.SALTCORN_E2E_SESSION_JSON;
  if (!raw) throw new Error("SALTCORN_E2E_SESSION_JSON não definida — rode via ./run.sh");
  return (JSON.parse(raw) as SeededSession).csrfToken;
}

test("view Edit com campo date renderiza <input type=\"date\"> nativo, salva e aparece na List (GO-042)", async ({ page }) => {
  const csrfToken = readCsrfToken();
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };
  const tableName = `e2e_guitars_date_${Date.now()}`;

  // Setup de fixture via HTTP direto ao BFF (mesmo proxy /api/bff usado
  // pelo app) — equivalente ao que um pack real importaria, sem depender
  // da UI do editor (EditorPage.tsx só cria tabela/view List de
  // demonstração, nunca Edit — achado de preflight, ver GO-042.md).
  const tableRes = await page.request.post("/api/bff/tables", { headers, data: { name: tableName } });
  expect(tableRes.ok(), await tableRes.text()).toBeTruthy();

  for (const field of [
    { name: "titulo", type: "text" },
    { name: "data_processo", type: "date" },
  ]) {
    const fieldRes = await page.request.post(`/api/bff/tables/${tableName}/fields`, { headers, data: field });
    expect(fieldRes.ok(), await fieldRes.text()).toBeTruthy();
  }

  const editViewRes = await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${tableName}_edit`,
      table: tableName,
      template: "Edit",
      min_role: 100,
      configuration: {
        columns: [
          { type: "Field", field_name: "titulo", fieldview: "edit" },
          // "flatpickr" — a MESMA fieldview real do pack guitars
          // (`edit_processed_embed`/`upload_photo`, campo
          // `date_processed`), não um nome inventado para o teste.
          { type: "Field", field_name: "data_processo", fieldview: "flatpickr" },
          { type: "Action", action_name: "Save" },
        ],
      },
    },
  });
  expect(editViewRes.ok(), await editViewRes.text()).toBeTruthy();

  const listViewRes = await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${tableName}_list`,
      table: tableName,
      template: "List",
      min_role: 100,
      configuration: {
        columns: [
          { type: "Field", field_name: "titulo", header_label: "Título" },
          { type: "Field", field_name: "data_processo", header_label: "Data" },
        ],
      },
    },
  });
  expect(listViewRes.ok(), await listViewRes.text()).toBeTruthy();

  await page.goto("/");
  await page.getByRole("button", { name: /Ver views/ }).click();

  // Abre a view Edit — SB Admin 2 é a ÚNICA folha de estilo/shell que
  // este stack novo oferece (GO-018): "substituir any-bootstrap-theme por
  // SB Admin 2" está satisfeito por construção, não é um passo à parte
  // desta task (ver decisão de escopo em GO-042.md).
  const editRow = page.getByRole("row", { name: new RegExp(`${tableName}_edit`) });
  await expect(editRow).toBeVisible();
  await editRow.getByRole("button", { name: "Visualizar" }).click();

  const dateInput = page.getByLabel("data_processo");
  await expect(dateInput).toBeVisible();
  // A prova central de GO-042: o navegador desenha um seletor de data
  // HTML5 NATIVO para a fieldview "flatpickr" — nunca um <input
  // type="text"> simples (o que existia antes desta task).
  await expect(dateInput).toHaveAttribute("type", "date");

  await page.getByLabel("titulo").fill("processo-e2e");
  await dateInput.fill("2024-03-20");
  await page.getByRole("button", { name: "Salvar" }).click();
  await expect(page.getByTestId("views-navigate-message")).toBeVisible();

  // A tabela de views (com as duas linhas: Edit e List) continua
  // renderizada abaixo da pré-visualização o tempo todo — abrir a List
  // sobre a MESMA tabela não exige navegar para trás. Prova que o valor
  // submetido pelo campo de data nativo foi persistido pelo Go (RFC3339
  // completo, internal/records.coerceJSONValue) e volta corretamente
  // formatado, ponta a ponta pelo navegador real.
  const listRow = page.getByRole("row", { name: new RegExp(`${tableName}_list`) });
  await expect(listRow).toBeVisible();
  await listRow.getByRole("button", { name: "Visualizar" }).click();

  const preview = page.getByTestId("views-preview");
  await expect(preview.getByText("processo-e2e")).toBeVisible();
  await expect(preview.getByText(/2024-03-20/)).toBeVisible();
});
