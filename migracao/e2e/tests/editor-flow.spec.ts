// GO-021 — critério de aceite "fluxo criar/publicar/operar passa E2E nos
// navegadores acordados": navegador real (Chromium/Firefox), servidor Go
// real, BFF real, Postgres real, tabela/view reais — nenhum mock.
import { test, expect } from "./fixtures";

test("cria tabela, cria view, publica e opera (lista/pré-visualiza) — ponta a ponta", async ({ page }) => {
  await page.goto("/");

  // Criar tabela via EditorPage (conectado ao BFF real).
  await page.getByRole("button", { name: /Abrir editor \(conectado ao BFF\)/ }).click();
  const tableName = `e2e_books_${Date.now()}`;
  await page.getByLabel("Nome da tabela").fill(tableName);
  await page.getByRole("button", { name: "Criar tabela" }).click();

  // Criar view sobre a tabela recém-criada.
  const createViewButton = page.getByRole("button", { name: `Criar view em "${tableName}"` });
  await expect(createViewButton).toBeVisible();
  await createViewButton.click();

  // A view nasce admin-only — "Publicar" fica disponível.
  const publishButton = page.getByRole("button", { name: "Publicar" });
  await expect(publishButton).toBeVisible();
  await expect(page.getByText(/admin \(não publicada\)/)).toBeVisible();

  await publishButton.click();
  await expect(page.getByText(/público \(publicada\)/)).toBeVisible();
  await expect(page.getByTestId("status-message")).toHaveText("Publicada.");

  // Operar: a página "Views" (SB Admin 2) lista a view publicada e
  // permite pré-visualizá-la — o critério de aceite "operar", não só
  // "criar/publicar".
  await page.getByRole("button", { name: /Ver views/ }).click();
  const row = page.getByRole("row", { name: new RegExp(`${tableName}_view`) });
  await expect(row).toBeVisible();
  await expect(row.getByText("publicada")).toBeVisible();

  await row.getByRole("button", { name: "Visualizar" }).click();
  const preview = page.getByTestId("views-preview");
  await expect(preview).toBeVisible();
  // `EditorPage.tsx` cria a view já com um layout compatível com o
  // runtime de renderização (GO-020: `layout.besides` referenciando o
  // campo "titulo" que `handleCreateTable` também cria — ver achado de
  // integração corrigido em docs/migracao-go/execucoes/GO-021.md) —
  // "operar" de ponta a ponta é a pré-visualização renderizar de verdade
  // uma tabela com o cabeçalho "Título" e nenhum registro ainda (a tabela
  // acabou de ser criada, sem dados), nunca a mensagem de "não suportado".
  await expect(preview.getByTestId("views-unsupported")).not.toBeVisible();
  await expect(preview.getByTestId("list-view")).toBeVisible();
  await expect(preview.getByRole("columnheader", { name: /Título/ })).toBeVisible();
});
