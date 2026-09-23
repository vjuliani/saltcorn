// GO-051 — prova ponta a ponta, em navegador real, dos três critérios de
// aceite da task: (1) view aninhada (create_guitar embute
// edit_processed_embed, aqui com books/chapters); (2) viewtemplate Feed
// (guitar_feed renderiza cards via show_guitar, aqui com books/showbook);
// (3) upload real de arquivo (upload_photo, aqui com photos/uploadphoto).
//
// Achado real de NAVEGAÇÃO (não um bug do produto, uma lacuna pré-
// existente de GO-039 que este teste tornou visível): `ViewsListPage.
// handlePreview` sempre abre uma view Edit em modo de CRIAÇÃO
// (record_id=0, `?record=` nunca é passado pelo botão "Visualizar") —
// não existe hoje, nesta página de demonstração, uma forma de abrir o
// formulário Edit de um registro JÁ EXISTENTE (ver App.tsx "Limitações":
// sem roteador client-side, GO-021). Por isso a verificação da view
// ANINHADA (que só aparece quando o registro pai já existe) usa
// `page.request` diretamente contra `GET .../render?record=`, o mesmo
// endpoint que a UI chamaria se tivesse essa navegação — prova a
// capacidade do backend/render de ponta a ponta pelo navegador real,
// sem fabricar uma navegação que a UI ainda não oferece.
import { test, expect } from "./fixtures";

interface SeededSession {
  csrfToken: string;
}

function readCsrfToken(): string {
  const raw = process.env.SALTCORN_E2E_SESSION_JSON;
  if (!raw) throw new Error("SALTCORN_E2E_SESSION_JSON não definida — rode via ./run.sh");
  return (JSON.parse(raw) as SeededSession).csrfToken;
}

test("view Feed mostra cards reais e o botão Criar abre o formulário de criação (GO-051)", async ({ page }) => {
  const csrfToken = readCsrfToken();
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };
  const suffix = Date.now();
  const booksTable = `e2e_books_${suffix}`;
  const chaptersTable = `e2e_chapters_${suffix}`;

  // Fixture: books (título) + chapters (capítulo + FieldKey "book") — a
  // mesma relação 1:N de guitars/processed do pack piloto real.
  await page.request.post("/api/bff/tables", { headers, data: { name: booksTable } });
  await page.request.post(`/api/bff/tables/${booksTable}/fields`, { headers, data: { name: "title", type: "text" } });
  await page.request.post("/api/bff/tables", { headers, data: { name: chaptersTable } });
  await page.request.post(`/api/bff/tables/${chaptersTable}/fields`, { headers, data: { name: "heading", type: "text" } });
  await page.request.post(`/api/bff/tables/${chaptersTable}/fields`, { headers, data: { name: "book", type: "key", references: booksTable } });

  await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${chaptersTable}_edit`,
      table: chaptersTable,
      template: "Edit",
      min_role: 100,
      configuration: {
        columns: [
          { type: "Field", field_name: "heading", fieldview: "edit" },
          { type: "Action", action_name: "Save" },
        ],
      },
    },
  });
  const createBookRes = await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${booksTable}_create`,
      table: booksTable,
      template: "Edit",
      min_role: 100,
      configuration: {
        columns: [
          { type: "Field", field_name: "title", fieldview: "edit" },
          { type: "Action", action_name: "Save" },
        ],
        layout: {
          above: [{ type: "view", view: `${chaptersTable}_edit`, relation: `.${booksTable}.${chaptersTable}$book` }],
        },
      },
    },
  });
  expect(createBookRes.ok(), await createBookRes.text()).toBeTruthy();
  const createBookView = (await createBookRes.json()) as { id: number; _version: string };

  await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${booksTable}_show`,
      table: booksTable,
      template: "Show",
      min_role: 100,
      configuration: { columns: [{ type: "Field", field_name: "title", fieldview: "as_text" }] },
    },
  });
  const feedRes = await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${booksTable}_feed`,
      table: booksTable,
      template: "Feed",
      min_role: 100,
      configuration: { show_view: `${booksTable}_show`, view_to_create: `${booksTable}_create` },
    },
  });
  expect(feedRes.ok(), await feedRes.text()).toBeTruthy();

  // Achado real de view aninhada (backend, provado via HTTP direto — ver
  // comentário do arquivo sobre a lacuna de navegação da UI): um livro +
  // um capítulo seedados por HTTP, e o render da view de criação COM
  // ?record= mostra o capítulo embutido, filtrado pela relação real.
  const bookRes = await page.request.post(`/api/bff/tables/${booksTable}/records`, { headers, data: { title: "Dune" } });
  const book = (await bookRes.json()) as { id: number };
  await page.request.post(`/api/bff/tables/${chaptersTable}/records`, { headers, data: { heading: "Capítulo 1", book: book.id } });

  const nestedRenderRes = await page.request.get(`/api/bff/views/${createBookView.id}/render?record=${book.id}`, { headers });
  expect(nestedRenderRes.ok(), await nestedRenderRes.text()).toBeTruthy();
  const nestedPlan = (await nestedRenderRes.json()) as {
    nested?: Array<{ view_name: string; fk_field: string; parent_id: number; rows: Array<{ fields: Array<{ field_name: string; value: unknown }> }> }>;
  };
  expect(nestedPlan.nested).toHaveLength(1);
  expect(nestedPlan.nested![0]!.view_name).toEqual(`${chaptersTable}_edit`);
  expect(nestedPlan.nested![0]!.fk_field).toEqual("book");
  expect(nestedPlan.nested![0]!.parent_id).toEqual(book.id);
  expect(nestedPlan.nested![0]!.rows).toHaveLength(1);
  const headingField = nestedPlan.nested![0]!.rows[0]!.fields.find((f) => f.field_name === "heading");
  expect(headingField?.value).toEqual("Capítulo 1");

  // UI real: abrir o feed mostra o card do livro seedado (via show_view
  // real, o MESMO mecanismo que um showbook standalone usaria).
  await page.goto("/");
  await page.getByRole("button", { name: /Ver views/ }).click();
  const feedRow = page.getByRole("row", { name: new RegExp(`${booksTable}_feed`) });
  await expect(feedRow).toBeVisible();
  await feedRow.getByRole("button", { name: "Visualizar" }).click();

  const feedView = page.getByTestId("feed-view");
  await expect(feedView).toBeVisible();
  await expect(feedView.getByText("Dune")).toBeVisible();

  // O botão "Criar" abre a view de criação (record_id=0) — sem nenhuma
  // view aninhada visível ainda, prova ao vivo no navegador de que o
  // formulário embutido só aparece a partir do primeiro salvamento do
  // pai (documentado em NestedEditViewPlan, nunca um erro silencioso).
  await page.getByTestId("feed-create-button").click();
  const editView = page.getByTestId("edit-view");
  await expect(editView).toBeVisible();
  await expect(page.getByTestId(`nested-group-${chaptersTable}_edit`)).toHaveCount(0);
});

test("upload real de arquivo via fieldview upload (GO-051)", async ({ page }) => {
  const csrfToken = readCsrfToken();
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };
  const photosTable = `e2e_photos_${Date.now()}`;

  await page.request.post("/api/bff/tables", { headers, data: { name: photosTable } });
  await page.request.post(`/api/bff/tables/${photosTable}/fields`, { headers, data: { name: "photo", type: "file" } });
  const uploadViewRes = await page.request.post("/api/bff/views", {
    headers,
    data: {
      name: `${photosTable}_upload`,
      table: photosTable,
      template: "Edit",
      min_role: 100,
      configuration: {
        columns: [
          { type: "Field", field_name: "photo", fieldview: "upload" },
          { type: "Action", action_name: "Save" },
        ],
      },
    },
  });
  expect(uploadViewRes.ok(), await uploadViewRes.text()).toBeTruthy();

  await page.goto("/");
  await page.getByRole("button", { name: /Ver views/ }).click();
  const uploadRow = page.getByRole("row", { name: new RegExp(`${photosTable}_upload`) });
  await expect(uploadRow).toBeVisible();
  await uploadRow.getByRole("button", { name: "Visualizar" }).click();

  const fileInput = page.getByLabel("photo");
  await expect(fileInput).toBeVisible();
  const fileContent = `bytes-reais-e2e-${Date.now()}`;
  await fileInput.setInputFiles({ name: "foto.png", mimeType: "image/png", buffer: Buffer.from(fileContent) });

  // O upload real acontece ao escolher o arquivo (antes de "Salvar") —
  // espera o input voltar a ficar habilitado (upload concluído) antes de
  // submeter o formulário.
  await expect(fileInput).toBeEnabled();
  await page.getByRole("button", { name: "Salvar" }).click();
  await expect(page.getByTestId("views-navigate-message")).toBeVisible();

  // Confirma que o registro criado guarda um id de arquivo REAL, e que
  // os bytes armazenados são exatamente os enviados — ponta a ponta
  // (navegador → BFF multipart → Go → internal/files.LocalBackend).
  const recordsRes = await page.request.get(`/api/bff/tables/${photosTable}/records`, { headers });
  const page1 = (await recordsRes.json()) as { items: Array<{ id: number; photo: number }> };
  expect(page1.items).toHaveLength(1);
  const fileId = page1.items[0]!.photo;
  expect(typeof fileId).toEqual("number");

  const downloadRes = await page.request.get(`/api/bff/files/${fileId}`, { headers: { "x-csrf-token": csrfToken } });
  expect(downloadRes.ok(), await downloadRes.text()).toBeTruthy();
  expect(downloadRes.headers()["content-type"]).toEqual("image/png");
  const downloadedBytes = await downloadRes.body();
  expect(downloadedBytes.toString("utf8")).toEqual(fileContent);
});
