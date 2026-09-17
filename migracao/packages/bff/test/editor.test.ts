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
