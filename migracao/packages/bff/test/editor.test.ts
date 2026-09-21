// Testes do ciclo do editor no BFF (GO-019): criar tabela/campo/view,
// salvar (PATCH), reabrir (GET), conflito de edição — a parte que o BFF
// realmente controla (sessão, CSRF, geração de Idempotency-Key) contra um
// mock do Go que replica idempotência/conflito de versão como o outbox.Do
// real faz. Não reexercita a lógica de autorização por papel do Go (isso
// já tem cobertura exaustiva em cmd/server/views_test.go,
// TestEditorE2E_CreateTableViewSaveReopenPublish) — aqui o foco é só a
// fronteira do BFF.
import { test } from "node:test";
import assert from "node:assert/strict";
import { loadConfig } from "../src/config.js";
import { GoClient } from "../src/goClient.js";
import { InMemorySessionStore } from "../src/session.js";
import { buildRouter, createRequestListener } from "../src/app.js";
import { generateCsrfToken, csrfCookieHeader, CSRF_HEADER_NAME } from "../src/csrf.js";
import { sessionCookieHeader } from "../src/session.js";
import { MockGoServer } from "./mockGoServer.js";
import { createServer, type Server } from "node:http";

const SECRET = "01234567890123456789012345678901"; // 32 bytes

async function startBff(goBaseUrl: string) {
  const config = loadConfig({
    SALTCORN_BFF_SERVICE_IDENTITY_SECRET: SECRET,
    SALTCORN_BFF_GO_INTERNAL_API_URL: goBaseUrl,
  } as NodeJS.ProcessEnv);
  const sessionStore = new InMemorySessionStore();
  const goClient = new GoClient({ baseUrl: config.goInternalApiUrl, timeoutMs: config.goRequestTimeoutMs });
  const router = buildRouter({ config, sessionStore, goClient });
  const listener = createRequestListener(router, { config, sessionStore, goClient });
  const server: Server = createServer((req, res) => void listener(req, res));
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  const port = address && typeof address === "object" ? address.port : 0;
  return { server, sessionStore, baseUrl: `http://127.0.0.1:${port}` };
}

async function withSession(sessionStore: InMemorySessionStore, userId: string, tenant: string) {
  const sessionId = await sessionStore.create({ userId, tenant });
  const csrfToken = generateCsrfToken();
  const cookie = `${sessionCookieHeader(sessionId).split(";")[0]}; ${csrfCookieHeader(csrfToken).split(";")[0]}`;
  return { cookie, csrfToken };
}

test("POST /api/bff/tables exige sessão e CSRF, cria a tabela via Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");

    const noSession = await fetch(`${baseUrl}/api/bff/tables`, { method: "POST", body: JSON.stringify({ name: "books" }) });
    assert.equal(noSession.status, 401);

    const noCsrf = await fetch(`${baseUrl}/api/bff/tables`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json" },
      body: JSON.stringify({ name: "books" }),
    });
    assert.equal(noCsrf.status, 403);

    const ok = await fetch(`${baseUrl}/api/bff/tables`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "books" }),
    });
    assert.equal(ok.status, 201);
    const body = (await ok.json()) as any;
    assert.equal(body.name, "books");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/tables/:table/fields cria o campo via Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/tables/books/fields`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "title", type: "text", required: true }),
    });
    assert.equal(res.status, 201);
    const body = (await res.json()) as any;
    assert.equal(body.name, "title");
    assert.equal(body.type, "text");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("ciclo completo de view: criar, reabrir, salvar, reabrir de novo", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");

    const createRes = await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "booklist", table: "books", template: "List", configuration: { above: [] } }),
    });
    assert.equal(createRes.status, 201);
    const created = (await createRes.json()) as any;
    assert.equal(created.name, "booklist");

    const reopenRes = await fetch(`${baseUrl}/api/bff/views/${created.id}`, { headers: { Cookie: cookie } });
    assert.equal(reopenRes.status, 200);
    const reopened = (await reopenRes.json()) as any;
    assert.equal(reopened.id, created.id);

    const saveRes = await fetch(`${baseUrl}/api/bff/views/${created.id}`, {
      method: "PATCH",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ _version: reopened._version, configuration: { above: ["editado"] } }),
    });
    assert.equal(saveRes.status, 200);

    const finalRes = await fetch(`${baseUrl}/api/bff/views/${created.id}`, { headers: { Cookie: cookie } });
    const final = (await finalRes.json()) as any;
    assert.deepEqual(final.configuration, { above: ["editado"] });
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/views/:id exige sessão", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, baseUrl } = await startBff(goUrl);
  try {
    const res = await fetch(`${baseUrl}/api/bff/views/1`);
    assert.equal(res.status, 401);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("retry de criação de view (mesmo corpo) não duplica no Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const doPost = () =>
      fetch(`${baseUrl}/api/bff/views`, {
        method: "POST",
        headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
        body: JSON.stringify({ name: "retryview", table: "books", template: "List", configuration: {} }),
      });
    const first = await doPost();
    const second = await doPost();
    assert.equal(first.status, 201);
    assert.equal(second.status, 201);
    const firstBody = (await first.json()) as any;
    const secondBody = (await second.json()) as any;
    assert.equal(firstBody.id, secondBody.id);
    assert.equal(mockGo.createViewCallCount, 1, "o mock só deveria ter criado a view uma vez");
  } finally {
    server.close();
    await mockGo.close();
  }
});

// TestUpdateView_ConcurrentEditConflict (Go) já prova isso na origem;
// este teste prova que o BFF PROPAGA o 409 do Go sem mascarar como outro
// erro (ex.: domain_unavailable) — "conflito de edição é apresentado sem
// sobrescrever silenciosamente" precisa chegar ao React de forma
// distinguível.
test("conflito de edição concorrente (PATCH com _version obsoleto) retorna 409, propagado do Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const createRes = await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "conflictview", table: "books", template: "List", configuration: {} }),
    });
    const created = (await createRes.json()) as any;

    const firstSave = await fetch(`${baseUrl}/api/bff/views/${created.id}`, {
      method: "PATCH",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ _version: created._version, configuration: { above: ["primeira edição"] } }),
    });
    assert.equal(firstSave.status, 200);

    // Segunda tentativa com o MESMO _version antigo (obsoleto após o
    // primeiro save) e um corpo DIFERENTE (edição de verdade, não um
    // retry do primeiro save).
    const staleSave = await fetch(`${baseUrl}/api/bff/views/${created.id}`, {
      method: "PATCH",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ _version: created._version, configuration: { above: ["segunda edição, deveria falhar"] } }),
    });
    assert.equal(staleSave.status, 409);
    const body = (await staleSave.json()) as any;
    assert.equal(body.error.code, "version_conflict");
  } finally {
    server.close();
    await mockGo.close();
  }
});

// GO-020: GET /api/bff/views/:id/render — só repassa o DTO do Go
// (colunas/linhas/paginação), sem reinterpretar nada. A classificação de
// compatibilidade em si já tem cobertura exaustiva do lado Go
// (internal/views/render_test.go, cmd/server/render_test.go); aqui o foco
// é a fronteira do BFF: sessão, encaminhamento de limit/cursor, e
// propagação do 422 sem mascarar.
test("GET /api/bff/views lista as views criadas (GO-020)", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "booklist", table: "books", template: "List", configuration: {} }),
    });

    const listRes = await fetch(`${baseUrl}/api/bff/views`, { headers: { Cookie: cookie } });
    assert.equal(listRes.status, 200);
    const list = (await listRes.json()) as any[];
    assert.equal(list.length, 1);
    assert.equal(list[0].name, "booklist");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/views/:id/render exige sessão", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, baseUrl } = await startBff(goUrl);
  try {
    const res = await fetch(`${baseUrl}/api/bff/views/1/render`);
    assert.equal(res.status, 401);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/views/:id/render devolve colunas/linhas/paginação para uma view compatível", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const createRes = await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({
        name: "booklist",
        table: "books",
        template: "List",
        configuration: { columns: [{ type: "Field", field_name: "title", header_label: "Título" }] },
      }),
    });
    const created = (await createRes.json()) as any;

    const renderRes = await fetch(`${baseUrl}/api/bff/views/${created.id}/render?limit=2`, { headers: { Cookie: cookie } });
    assert.equal(renderRes.status, 200);
    const plan = (await renderRes.json()) as any;
    assert.deepEqual(plan.columns, [{ kind: "field", field_name: "title", header_label: "Título" }]);
    assert.equal(plan.rows.length, 2);
    assert.ok(plan.next_cursor, "esperado next_cursor (há um 3º registro no mock)");

    const nextPageRes = await fetch(`${baseUrl}/api/bff/views/${created.id}/render?limit=2&cursor=${plan.next_cursor}`, { headers: { Cookie: cookie } });
    const nextPage = (await nextPageRes.json()) as any;
    assert.equal(nextPage.rows.length, 1);
    assert.equal(nextPage.next_cursor, null);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/views/:id/render propaga 422 view_unsupported do Go, sem mascarar", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const createRes = await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "showbook", table: "books", template: "Show", configuration: {} }),
    });
    const created = (await createRes.json()) as any;

    const renderRes = await fetch(`${baseUrl}/api/bff/views/${created.id}/render`, { headers: { Cookie: cookie } });
    assert.equal(renderRes.status, 422);
    const body = (await renderRes.json()) as any;
    assert.equal(body.error.code, "view_unsupported");
  } finally {
    server.close();
    await mockGo.close();
  }
});

// GO-039: Show/Edit/submit/rows — os mesmos princípios de propagação
// (sessão, CSRF, Idempotency-Key, query params) exercitados para os
// templates novos, contra o mock estendido (mockGoServer.ts) que agora
// mantém registros de "books" mutáveis.
test("GET /api/bff/views/:id/render (Show) exige ?record= e devolve os valores reais", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const createRes = await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ name: "showbook", table: "books", template: "Show", configuration: { columns: [{ type: "Field", field_name: "title" }] } }),
    });
    const created = (await createRes.json()) as any;

    const missingRecordRes = await fetch(`${baseUrl}/api/bff/views/${created.id}/render`, { headers: { Cookie: cookie } });
    assert.equal(missingRecordRes.status, 400, "sem ?record= deveria ser 400 (propagado do Go)");

    const renderRes = await fetch(`${baseUrl}/api/bff/views/${created.id}/render?record=1`, { headers: { Cookie: cookie } });
    assert.equal(renderRes.status, 200);
    const plan = (await renderRes.json()) as any;
    assert.equal(plan.values.title, "Dune");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/views/:id/render (Edit) sem ?record= monta formulário de criação em branco", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const createRes = await fetch(`${baseUrl}/api/bff/views`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({
        name: "createbook",
        table: "books",
        template: "Edit",
        configuration: { columns: [{ type: "Field", field_name: "title", fieldview: "edit" }, { type: "Action", action_name: "Save" }] },
      }),
    });
    const created = (await createRes.json()) as any;

    const renderRes = await fetch(`${baseUrl}/api/bff/views/${created.id}/render`, { headers: { Cookie: cookie } });
    assert.equal(renderRes.status, 200);
    const plan = (await renderRes.json()) as any;
    assert.equal(plan.record_id, 0);
    assert.equal(plan.fields[0].value, null);
    assert.equal(plan.action_name, "Save");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/views/:id/submit sem CSRF é rejeitado antes de chegar ao Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/views/1/submit`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json" },
      body: JSON.stringify({ values: { title: "x" } }),
    });
    assert.equal(res.status, 403);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "csrf_invalid");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/views/:id/submit cria um registro real (201) e retry idêntico não duplica", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const doSubmit = () =>
      fetch(`${baseUrl}/api/bff/views/1/submit`, {
        method: "POST",
        headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
        body: JSON.stringify({ values: { title: "Neuromancer 2" } }),
      });

    const first = await doSubmit();
    assert.equal(first.status, 201);
    const firstBody = (await first.json()) as any;
    assert.equal(firstBody.record.title, "Neuromancer 2");
    assert.equal(firstBody.navigate.type, "reload");

    const second = await doSubmit();
    assert.equal(second.status, 201);
    const secondBody = (await second.json()) as any;
    assert.equal(secondBody.record.id, firstBody.record.id, "retry (mesmo ator/tenant/view/corpo) deveria reaproveitar a MESMA chave, não criar de novo");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/views/:id/submit (update) propaga 409 version_conflict do Go, sem mascarar", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/views/1/submit`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ record_id: 1, _version: "obsoleta", values: { title: "x" } }),
    });
    assert.equal(res.status, 409);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "version_conflict");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("DELETE /api/bff/views/:id/rows/:recordId remove o registro real com a versão certa", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/views/1/rows/2?version=1`, {
      method: "DELETE",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(res.status, 204);

    // Repetir a mesma exclusão encontra o registro já removido — prova
    // que o efeito é real (não só a forma da resposta), sem depender de
    // nenhuma view Show específica para o registro 2.
    const again = await fetch(`${baseUrl}/api/bff/views/1/rows/2?version=1`, {
      method: "DELETE",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(again.status, 404);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("DELETE /api/bff/views/:id/rows/:recordId sem CSRF é rejeitado antes de chegar ao Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/views/1/rows/3?version=1`, {
      method: "DELETE",
      headers: { Cookie: cookie },
    });
    assert.equal(res.status, 403);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "csrf_invalid");
  } finally {
    server.close();
    await mockGo.close();
  }
});
