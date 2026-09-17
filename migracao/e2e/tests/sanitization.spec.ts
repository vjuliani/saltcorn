// GO-021 — "nenhum script não autorizado executa em cenários de teste":
// grava um valor de registro contendo HTML/script malicioso via a API
// REAL do BFF (mesma fronteira que um formulário legítimo usaria — o
// EditorPage ainda não tem UI de "adicionar campo"/"criar registro", ver
// nota abaixo, então esta configuração usa page.request, que reaproveita
// os MESMOS cookies de sessão/CSRF do navegador, não uma chamada
// "de teste" à parte) e confirma, no navegador real, que o React nunca
// interpreta esse valor como HTML/script — só o exibe como texto.
import { test, expect } from "./fixtures";

interface Session {
  csrfToken: string;
}

function readSession(): Session {
  return JSON.parse(process.env.SALTCORN_E2E_SESSION_JSON ?? "{}");
}

const BFF_BASE_URL = process.env.SALTCORN_E2E_BFF_BASE_URL ?? "";
const XSS_PAYLOAD = '<script>window.__xssMarker = true;</script>';

test("valor de registro com <script> nunca executa — só aparece como texto escapado", async ({ page }) => {
  const { csrfToken } = readSession();
  const tableName = `e2e_xss_${Date.now()}`;

  // Setup via API real do BFF (não há UI de "adicionar campo"/"criar
  // registro" no EditorPage ainda — ver docs/migracao-go/execucoes/
  // GO-021.md, nota de escopo 6) — mesmas rotas HTTP que a UI usaria.
  const createTable = await page.request.post(`${BFF_BASE_URL}/api/bff/tables`, {
    headers: { "X-CSRF-Token": csrfToken },
    data: { name: tableName },
  });
  expect(createTable.ok()).toBeTruthy();

  const addField = await page.request.post(`${BFF_BASE_URL}/api/bff/tables/${tableName}/fields`, {
    headers: { "X-CSRF-Token": csrfToken },
    data: { name: "titulo", type: "text" },
  });
  expect(addField.ok()).toBeTruthy();

  const createRecord = await page.request.post(`${BFF_BASE_URL}/api/bff/tables/${tableName}/records`, {
    headers: { "X-CSRF-Token": csrfToken },
    data: { titulo: XSS_PAYLOAD },
  });
  expect(createRecord.ok()).toBeTruthy();

  const createView = await page.request.post(`${BFF_BASE_URL}/api/bff/views`, {
    headers: { "X-CSRF-Token": csrfToken },
    data: {
      name: `${tableName}_view`,
      table: tableName,
      template: "List",
      configuration: { layout: { besides: [{ header_label: "Título", contents: { type: "Field", field_name: "titulo" } }] } },
      min_role: 100,
    },
  });
  expect(createView.ok()).toBeTruthy();

  // Um marcador global provaria que o payload rodou, se o React o
  // interpretasse como HTML em vez de texto — checado ANTES de navegar
  // para a página que desenha o registro, então só pode ficar `true` se
  // o script realmente executar durante a renderização.
  await page.goto("/");
  await page.getByRole("button", { name: /Ver views/ }).click();

  const row = page.getByRole("row", { name: new RegExp(`${tableName}_view`) });
  await row.getByRole("button", { name: "Visualizar" }).click();

  const preview = page.getByTestId("views-preview");
  await expect(preview).toBeVisible();
  // O texto aparece LITERALMENTE (incluindo as tags), nunca interpretado.
  await expect(preview).toContainText(XSS_PAYLOAD);
  await expect(preview.locator("script")).toHaveCount(0);

  const marker = await page.evaluate(() => (window as unknown as { __xssMarker?: boolean }).__xssMarker);
  expect(marker).toBeUndefined();
});
