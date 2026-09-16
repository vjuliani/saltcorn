// Testes de integração do BFF: app real (roteador + handlers reais) +
// servidor HTTP real (node:http) contra um mock do backend Go (também
// HTTP real, mockGoServer.ts) — sem simular nada em memória além do que
// um teste unitário já cobre. Cobrem diretamente os critérios de aceite
// de GO-017: sessão exigida, CSRF exigido, identidade forjada rejeitada
// na fronteira BFF→Go, timeout/falha do Go vira erro controlado, e retry
// não duplica.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { loadConfig } from "../src/config.js";
import { GoClient } from "../src/goClient.js";
import { InMemorySessionStore } from "../src/session.js";
import { buildRouter, createRequestListener } from "../src/app.js";
import { generateCsrfToken, csrfCookieHeader, CSRF_HEADER_NAME } from "../src/csrf.js";
import { sessionCookieHeader } from "../src/session.js";
import { mintServiceIdentity } from "../src/serviceIdentity.js";
import { MockGoServer } from "./mockGoServer.js";

const SECRET = "01234567890123456789012345678901"; // 32 bytes

async function startBff(goBaseUrl: string, opts?: { timeoutMs?: number }) {
  const config = loadConfig({
    SALTCORN_BFF_SERVICE_IDENTITY_SECRET: SECRET,
    SALTCORN_BFF_GO_INTERNAL_API_URL: goBaseUrl,
    SALTCORN_BFF_GO_REQUEST_TIMEOUT_MS: String(opts?.timeoutMs ?? 2000),
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

test("GET /api/bff/bootstrap sem sessão retorna 401 session_required", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, baseUrl } = await startBff(goUrl);
  try {
    const res = await fetch(`${baseUrl}/api/bff/bootstrap`);
    assert.equal(res.status, 401);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "session_required");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/bootstrap com sessão válida compõe a resposta a partir do Go real (via GoClient)", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/bootstrap`, { headers: { Cookie: cookie } });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.deepEqual(body, { actor: { id: 42, role_id: 80 }, tenant: "acme" });
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/tables/:table/records exige sessão", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, baseUrl } = await startBff(goUrl);
  try {
    const res = await fetch(`${baseUrl}/api/bff/tables/widgets/records`);
    assert.equal(res.status, 401);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/tables/:table/records com sessão retorna a página do Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/tables/widgets/records`, { headers: { Cookie: cookie } });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.equal(body.items.length, 1);
    assert.equal(body.next_cursor, null);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/tables/:table/records sem CSRF é rejeitado (403 csrf_invalid)", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/tables/widgets/records`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json" },
      body: JSON.stringify({ label: "x" }),
    });
    assert.equal(res.status, 403);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "csrf_invalid");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/tables/:table/records com sessão+CSRF cria o registro via Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/tables/widgets/records`, {
      method: "POST",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ label: "novo" }),
    });
    assert.equal(res.status, 201);
    const body = (await res.json()) as any;
    assert.equal(body.label, "novo");
    assert.equal(mockGo.createCallCount, 1);
  } finally {
    server.close();
    await mockGo.close();
  }
});

// TestCreateRecordHandler_RetryDoesNotDuplicate (Go) já prova isso do lado
// Go; este teste prova a MESMA propriedade do lado do BFF: duas
// requisições HTTP idênticas do navegador (mesma sessão, mesmo corpo)
// resultam em uma única chamada de criação real no Go — a chave de
// idempotência computada pelo BFF é a mesma nas duas, então o Go (aqui,
// o mock que replica outbox.Do) trata a segunda como replay.
test("retry do navegador (corpo idêntico) não duplica a criação no Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "42", "acme");
    const doPost = () =>
      fetch(`${baseUrl}/api/bff/tables/widgets/records`, {
        method: "POST",
        headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
        body: JSON.stringify({ label: "retry" }),
      });

    const first = await doPost();
    const second = await doPost();
    assert.equal(first.status, 201);
    assert.equal(second.status, 201);
    const firstBody = (await first.json()) as any;
    const secondBody = (await second.json()) as any;
    assert.equal(firstBody.id, secondBody.id);
    assert.equal(mockGo.createCallCount, 1, "o mock (réplica de outbox.Do) só deveria ter criado uma vez");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("timeout do Go vira 502 domain_unavailable — nunca uma requisição pendurada", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, delayMs: 300 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl, { timeoutMs: 50 });
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const start = Date.now();
    const res = await fetch(`${baseUrl}/api/bff/tables/widgets/records`, { headers: { Cookie: cookie } });
    const elapsed = Date.now() - start;
    assert.equal(res.status, 502);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "domain_unavailable");
    assert.ok(elapsed < 300, `deveria ter retornado antes do delay do mock (300ms), levou ${elapsed}ms`);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("Go respondendo 500 vira 502 domain_unavailable (nunca vaza erro de infraestrutura cru)", async () => {
  const goDown = createServer((_req, res) => {
    res.writeHead(500, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: { code: "internal", message: "detalhe interno que não deveria vazar" } }));
  });
  await new Promise<void>((resolve) => goDown.listen(0, "127.0.0.1", resolve));
  const addr = goDown.address();
  const goUrl = `http://127.0.0.1:${addr && typeof addr === "object" ? addr.port : 0}`;

  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/tables/widgets/records`, { headers: { Cookie: cookie } });
    assert.equal(res.status, 502);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "domain_unavailable");
    assert.doesNotMatch(JSON.stringify(body), /detalhe interno/);
  } finally {
    server.close();
    goDown.close();
  }
});

// TestListRecordsHandler_InvalidSignatureRejected/_TenantMismatchRejected
// (Go) já provam a rejeição do lado do backend real. Este teste prova a
// MESMA fronteira a partir do cliente do BFF (goClient.ts): um
// ServiceIdentity assinado com o segredo ERRADO (simula um BFF
// comprometido/token adulterado) é rejeitado pelo Go — "identidade
// forjada é rejeitada" na ponta que o BFF de fato controla.
test("identidade forjada (assinatura errada) é rejeitada pelo Go real, não aceita silenciosamente", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  try {
    const client = new GoClient({ baseUrl: goUrl, timeoutMs: 2000 });
    const forgedToken = mintServiceIdentity("segredo-errado-32-bytes-aqui!!!!", { sub: "42", tenant: "acme" }, 30);
    await assert.rejects(
      () => client.getActor(forgedToken, "acme"),
      (err: any) => {
        assert.equal(err.status, 401);
        assert.equal(err.code, "invalid_identity_token");
        return true;
      }
    );
  } finally {
    await mockGo.close();
  }
});

test("identidade com tenant divergente é rejeitada pelo Go real (403 tenant_mismatch)", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  try {
    const client = new GoClient({ baseUrl: goUrl, timeoutMs: 2000 });
    const tokenForOtherTenant = mintServiceIdentity(SECRET, { sub: "42", tenant: "beta" }, 30);
    await assert.rejects(
      () => client.getActor(tokenForOtherTenant, "acme"),
      (err: any) => {
        assert.equal(err.status, 403);
        assert.equal(err.code, "tenant_mismatch");
        return true;
      }
    );
  } finally {
    await mockGo.close();
  }
});
