// GO-047 — prova ponta a ponta, em navegador real, do critério de aceite
// central: "a interface suporta pelo menos 2 idiomas configuráveis por
// tenant, com seleção por usuário persistida". Troca o idioma pelo
// seletor real da Topbar, confirma que os textos da UI mudam de verdade
// (dados do usuário — nomes de view — continuam intactos), e confirma
// que a preferência SOBREVIVE a um reload de página (persistida via
// PATCH /v1/tenants/{tenant}/actor no Go real, não só estado local do
// React).
import { test, expect } from "./fixtures";

test("seletor de idioma troca os textos da UI e a preferência persiste após reload", async ({ page }) => {
  const csrfToken = process.env.SALTCORN_E2E_SESSION_JSON
    ? (JSON.parse(process.env.SALTCORN_E2E_SESSION_JSON) as { csrfToken: string }).csrfToken
    : "";
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };
  const tableName = `e2e_i18n_${Date.now()}`;

  // Achado real (não do produto — do PRÓPRIO teste): o usuário/tenant
  // "e2e_web" é COMPARTILHADO por todos os specs deste harness (mesma
  // sessão semeada uma vez por run.sh) — a preferência de idioma é
  // PERSISTENTE por usuário (identity.User.Language), então rodar este
  // teste no Chromium e depois no Firefox (mesma sessão) vazava "en"
  // para o segundo navegador, que esperava encontrar "pt". Corrigido
  // limpando a preferência ANTES (garante estado inicial "pt", o
  // fallback) e DEPOIS (nunca deixa "en" vazar para o próximo spec) via
  // PATCH direto — nunca via reload de página, que não garantiria a
  // ordem em relação ao restante do teste.
  await page.request.patch("/api/bff/actor/language", { headers, data: { language: "" } });

  // Fixture via HTTP direto (mesmo padrão de date-field-edit.spec.ts) —
  // só o suficiente para ter UMA view real na lista, cujo nome de
  // usuário este teste confirma que NUNCA é traduzido.
  await page.request.post("/api/bff/tables", { headers, data: { name: tableName } });
  await page.request.post(`/api/bff/tables/${tableName}/fields`, { headers, data: { name: "titulo", type: "text" } });
  await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${tableName}_list`,
      table: tableName,
      template: "List",
      min_role: 100,
      configuration: { columns: [{ type: "Field", field_name: "titulo", header_label: "Título" }] },
    },
  });

  await page.goto("/");

  const localeSelect = page.getByTestId("locale-select");
  await expect(localeSelect).toHaveValue("pt");

  // Achado real de teste (novamente não é bug de produto): views de
  // execuções anteriores deste mesmo spec continuam na lista (a fixture
  // HTTP nunca as remove) — "publicada"/"Visualizar" aparecem em VÁRIAS
  // linhas. Escopar à linha desta execução (nome único por timestamp)
  // evita "strict mode violation" do Playwright ao localizar por texto
  // genérico repetido na página.
  await page.getByRole("button", { name: /Ver views/ }).click();
  const row = page.getByRole("row", { name: new RegExp(`${tableName}_list`) });
  await expect(row).toBeVisible();
  await expect(page.getByText("Nome")).toBeVisible();
  await expect(row.getByText("publicada")).toBeVisible();
  await expect(row.getByRole("button", { name: "Visualizar" })).toBeVisible();

  // Troca para EN pelo seletor real — a UI reage IMEDIATAMENTE (estado
  // local otimista, App.tsx), antes mesmo da chamada PATCH confirmar.
  await localeSelect.selectOption("en");
  await expect(page.getByText("Name")).toBeVisible();
  await expect(row.getByText("published")).toBeVisible();
  await expect(row.getByRole("button", { name: "Preview" })).toBeVisible();
  // Dado do usuário (nome da view) nunca é traduzido.
  await expect(row).toBeVisible();

  // Recarrega a página inteira — sem NENHUM estado local do React
  // sobrevivendo. Se a preferência não tivesse sido persistida de
  // verdade no Go (PATCH .../actor), a página voltaria a "pt" aqui.
  await page.reload();
  await expect(page.getByTestId("locale-select")).toHaveValue("en");
  await page.getByRole("button", { name: /Ver views/ }).click();
  await expect(page.getByText("Name")).toBeVisible();

  // Limpa a preferência ao final — nunca deixar "en" vazar para o
  // próximo spec/navegador que reusa a mesma sessão (ver achado acima).
  await page.request.patch("/api/bff/actor/language", { headers, data: { language: "" } });
});
