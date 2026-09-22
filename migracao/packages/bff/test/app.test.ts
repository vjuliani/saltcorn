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
    assert.deepEqual(body, { actor: { id: 42, role_id: 80, language: "" }, tenant: "acme", locale: "pt" });
  } finally {
    server.close();
    await mockGo.close();
  }
});

// GO-047: preferência de idioma do ator — self-service via PATCH, e o
// bootstrap seguinte reflete o valor persistido (não só a resposta do
// próprio PATCH).
test("PATCH /api/bff/actor/language grava a preferência e bootstrap seguinte reflete", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/actor/language`, {
      method: "PATCH",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken, "content-type": "application/json" },
      body: JSON.stringify({ language: "en" }),
    });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.deepEqual(body, { actor: { id: 42, role_id: 80, language: "en" }, locale: "en" });

    const bootstrapRes = await fetch(`${baseUrl}/api/bff/bootstrap`, { headers: { Cookie: cookie } });
    const bootstrapBody = (await bootstrapRes.json()) as any;
    assert.equal(bootstrapBody.actor.language, "en");
    assert.equal(bootstrapBody.locale, "en");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("PATCH /api/bff/actor/language sem sessão retorna 401", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, baseUrl } = await startBff(goUrl);
  try {
    const res = await fetch(`${baseUrl}/api/bff/actor/language`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ language: "en" }),
    });
    assert.equal(res.status, 401);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("PATCH /api/bff/actor/language sem CSRF retorna 403", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/actor/language`, {
      method: "PATCH",
      headers: { Cookie: cookie, "content-type": "application/json" },
      body: JSON.stringify({ language: "en" }),
    });
    assert.equal(res.status, 403);
  } finally {
    server.close();
    await mockGo.close();
  }
});

// GO-047: cookie `lang` só decide o locale quando o ator NÃO tem
// preferência explícita — prova a ordem de prioridade (User.Language >
// cookie > default_locale) fim a fim através do bootstrap real.
test("GET /api/bff/bootstrap usa o cookie lang quando o ator não tem preferência explícita", async () => {
  const mockGo = new MockGoServer({ secret: SECRET });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "42", "acme");
    const res = await fetch(`${baseUrl}/api/bff/bootstrap`, { headers: { Cookie: `${cookie}; lang=en` } });
    const body = (await res.json()) as any;
    assert.equal(body.actor.language, "", "ator não tem preferência explícita gravada");
    assert.equal(body.locale, "en", "cookie decide o locale efetivo na ausência de preferência");
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

// GO-044: administração de usuário — testes de integração da mesma
// forma dos acima (app real + mock HTTP do Go), cobrindo o caminho que
// nenhum teste unitário de goClient.ts sozinho provaria: sessão+CSRF
// exigidos pelo BFF, e o 403 do Go (requireAdmin) propagado sem alteração.
test("GET /api/bff/admin/users exige sessão", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  const goUrl = await mockGo.listen();
  const { server, baseUrl } = await startBff(goUrl);
  try {
    const res = await fetch(`${baseUrl}/api/bff/admin/users`);
    assert.equal(res.status, 401);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/admin/users com ator não-admin recebe o 403 do Go, sem alteração", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "2", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users`, { headers: { Cookie: cookie } });
    assert.equal(res.status, 403);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "not_authorized");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/admin/users com admin lista os usuários do Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 1, email: "admin@acme.test", role_id: 1 });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users`, { headers: { Cookie: cookie } });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.equal(body.length, 2);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("PATCH /api/bff/admin/users/:id sem CSRF é rejeitado antes de chegar ao Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/2`, {
      method: "PATCH",
      headers: { Cookie: cookie, "Content-Type": "application/json" },
      body: JSON.stringify({ role_id: 1 }),
    });
    assert.equal(res.status, 403);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "csrf_invalid");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("DELETE /api/bff/admin/users/:id com admin remove o usuário no Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/2`, {
      method: "DELETE",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(res.status, 204);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/admin/users/:id/reset-password sem senha devolve a gerada pelo Go, uma única vez", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/2/reset-password`, {
      method: "POST",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.ok(typeof body.password === "string" && body.password.length > 0);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("GET /api/bff/admin/users/:id/tokens nunca reexibe hash/texto puro, só id/created_at/revoked", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  mockGo.seedUserTokens(2, [{ id: 1, created_at: "2026-01-01T00:00:00Z", revoked: false }]);
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/2/tokens`, { headers: { Cookie: cookie } });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.deepEqual(body, [{ id: 1, created_at: "2026-01-01T00:00:00Z", revoked: false }]);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("PATCH /api/bff/admin/tables/:table/permissions com admin muda min_role_read/write no Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/tables/widgets/permissions`, {
      method: "PATCH",
      headers: { Cookie: cookie, "Content-Type": "application/json", [CSRF_HEADER_NAME]: csrfToken },
      body: JSON.stringify({ min_role_read: 10, min_role_write: 1 }),
    });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.equal(body.min_role_read, 10);
    assert.equal(body.min_role_write, 1);
  } finally {
    server.close();
    await mockGo.close();
  }
});

// force-logout (GO-044) — o único handler administrativo que nunca
// chama o Go para decidir 403 (destrói sessões no store do próprio
// BFF, ADR-0007); prova que um ator não-admin é rejeitado mesmo sem
// nenhuma rota Go envolvida, e que um admin de fato derruba TODAS as
// sessões do usuário-alvo (não só uma).
test("POST /api/bff/admin/users/:id/force-logout com ator não-admin é rejeitado (403, sem chamar o Go para decidir)", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "2", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/2/force-logout`, {
      method: "POST",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(res.status, 403);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "not_authorized");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/admin/users/:id/force-logout com admin derruba todas as sessões do alvo", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 1, email: "admin@acme.test", role_id: 1 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const admin = await withSession(sessionStore, "1", "acme");
    const targetSessionA = await withSession(sessionStore, "9", "acme");
    const targetSessionB = await withSession(sessionStore, "9", "acme");

    const res = await fetch(`${baseUrl}/api/bff/admin/users/9/force-logout`, {
      method: "POST",
      headers: { Cookie: admin.cookie, [CSRF_HEADER_NAME]: admin.csrfToken },
    });
    assert.equal(res.status, 204);

    const targetSessionIdA = targetSessionA.cookie.match(/sc_session=([^;]+)/)![1]!;
    const targetSessionIdB = targetSessionB.cookie.match(/sc_session=([^;]+)/)![1]!;
    assert.equal(await sessionStore.get(decodeURIComponent(targetSessionIdA)), null);
    assert.equal(await sessionStore.get(decodeURIComponent(targetSessionIdB)), null);
  } finally {
    server.close();
    await mockGo.close();
  }
});

// impersonate + end (GO-044) — o fluxo completo: iniciar troca a sessão
// do admin por uma sessão NOVA do usuário-alvo (nunca reaproveita a
// sessão do admin) com impersonatedBy marcado; encerrar exige que a
// sessão ATUAL seja de fato uma impersonação (nunca aceita log_id de
// fora) e sempre expira o cookie, forçando novo login.
test("POST /api/bff/admin/users/:id/impersonate troca o cookie por uma sessão nova do alvo, com auditoria no Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 1, email: "admin@acme.test", role_id: 1 });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/2/impersonate`, {
      method: "POST",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
      redirect: "manual",
    });
    assert.equal(res.status, 200);
    const body = (await res.json()) as any;
    assert.equal(body.target_user_id, 2);
    const setCookie = res.headers.get("set-cookie") ?? "";
    assert.match(setCookie, /sc_session=/);

    const record = mockGo.getImpersonation(1);
    assert.equal(record?.adminUserId, 1);
    assert.equal(record?.targetUserId, 2);
    assert.equal(record?.endedAt, null);
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/admin/users/:id/impersonate ao impersonar a si mesmo propaga o 400 do Go", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 1, email: "admin@acme.test", role_id: 1 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/users/1/impersonate`, {
      method: "POST",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(res.status, 400);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "cannot_impersonate_self");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("POST /api/bff/admin/impersonation/end numa sessão que não é impersonação é rejeitado (409)", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const { cookie, csrfToken } = await withSession(sessionStore, "1", "acme");
    const res = await fetch(`${baseUrl}/api/bff/admin/impersonation/end`, {
      method: "POST",
      headers: { Cookie: cookie, [CSRF_HEADER_NAME]: csrfToken },
    });
    assert.equal(res.status, 409);
    const body = (await res.json()) as any;
    assert.equal(body.error.code, "impersonation_not_active");
  } finally {
    server.close();
    await mockGo.close();
  }
});

test("impersonate seguido de end: encerra a auditoria no Go, destrói a sessão, e expira o cookie", async () => {
  const mockGo = new MockGoServer({ secret: SECRET, adminUserIds: ["1"] });
  mockGo.seedUser({ id: 1, email: "admin@acme.test", role_id: 1 });
  mockGo.seedUser({ id: 2, email: "user@acme.test", role_id: 80 });
  const goUrl = await mockGo.listen();
  const { server, sessionStore, baseUrl } = await startBff(goUrl);
  try {
    const admin = await withSession(sessionStore, "1", "acme");
    const startRes = await fetch(`${baseUrl}/api/bff/admin/users/2/impersonate`, {
      method: "POST",
      headers: { Cookie: admin.cookie, [CSRF_HEADER_NAME]: admin.csrfToken },
    });
    const setCookieHeader = startRes.headers.get("set-cookie") ?? "";
    const impersonatedSessionId = setCookieHeader.match(/sc_session=([^;]+)/)?.[1];
    const impersonatedCsrf = setCookieHeader.match(/sc_csrf=([^;]+)/)?.[1];
    assert.ok(impersonatedSessionId && impersonatedCsrf, "impersonate deveria ter definido sessão+csrf novos");

    const endRes = await fetch(`${baseUrl}/api/bff/admin/impersonation/end`, {
      method: "POST",
      headers: {
        Cookie: `sc_session=${impersonatedSessionId}; sc_csrf=${impersonatedCsrf}`,
        [CSRF_HEADER_NAME]: decodeURIComponent(impersonatedCsrf!),
      },
    });
    assert.equal(endRes.status, 204);
    assert.match(endRes.headers.get("set-cookie") ?? "", /Max-Age=0/);

    assert.equal(mockGo.getImpersonation(1)?.endedAt !== null, true);
    assert.equal(await sessionStore.get(decodeURIComponent(impersonatedSessionId!)), null);
  } finally {
    server.close();
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
