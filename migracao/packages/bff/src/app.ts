// Composição das rotas do BFF (bff-api.yaml) — health/readiness (GO-005,
// mesmo espírito do backend Go), bootstrap, e list/create de registros.
// Nunca acessa banco de domínio (ADR-0003): toda regra passa por GoClient.
import type { IncomingMessage, ServerResponse } from "node:http";
import type { Config } from "./config.js";
import { GoClient } from "./goClient.js";
import type { SessionStore } from "./session.js";
import { expiredSessionCookieHeader, readSessionCookie, sessionCookieHeader, SESSION_COOKIE_NAME } from "./session.js";
import { CSRF_COOKIE_NAME, CSRF_HEADER_NAME, csrfCookieHeader, generateCsrfToken, readCsrfCookie, verifyCsrf } from "./csrf.js";
import { mintServiceIdentity } from "./serviceIdentity.js";
import { computeIdempotencyKey } from "./idempotency.js";
import { langCookieHeader, resolveLocale } from "./locale.js";
import { BffError, csrfInvalidError, forbiddenError, impersonationNotActiveError, sessionRequiredError } from "./errors.js";
import { getHeader, readJSONBody, readRawBody, sendError, sendJSON } from "./httpHelpers.js";
import { parseSingleFileMultipart } from "./multipart.js";
import { Router } from "./router.js";

export interface AppDeps {
  readonly config: Config;
  readonly sessionStore: SessionStore;
  readonly goClient: GoClient;
  /** Registrado no shutdown gracioso — mesmo padrão de shutdown.Tracker no Go: uma unidade de trabalho por requisição em curso. */
  readonly onRequestStart?: () => () => void;
}

/**
 * requireSession lê o cookie de sessão, resolve os dados no store, e
 * renova a expiração deslizante (ADR-0007) — nunca cria sessão nova
 * aqui: um cookie ausente/expirado é sempre `session_required`, GO-017
 * não expõe endpoint de login (ver nota de escopo #4 em
 * docs/migracao-go/execucoes/GO-017.md).
 */
async function requireSession(req: IncomingMessage, sessionStore: SessionStore) {
  const sessionId = readSessionCookie(getHeader(req, "cookie"));
  if (!sessionId) throw sessionRequiredError();
  const data = await sessionStore.get(sessionId);
  if (!data) throw sessionRequiredError();
  await sessionStore.touch(sessionId);
  return { sessionId, data };
}

function requireCsrf(req: IncomingMessage): void {
  const cookieToken = readCsrfCookie(getHeader(req, "cookie"));
  const headerToken = getHeader(req, CSRF_HEADER_NAME);
  if (!verifyCsrf(cookieToken, headerToken)) throw csrfInvalidError();
}

export function buildRouter(deps: AppDeps): Router {
  const router = new Router();
  const { config, sessionStore, goClient } = deps;

  router.get("/healthz", (_req, res) => {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok");
  });

  router.get("/readyz", (_req, res) => {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok");
  });

  router.get("/api/bff/bootstrap", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const actor = await goClient.getActor(token, data.tenant);
    const locale = resolveLocale(actor.language, getHeader(req, "cookie"), actor.default_locale);
    sendJSON(res, 200, {
      actor: { id: actor.id, role_id: actor.role_id, language: actor.language },
      tenant: data.tenant,
      locale,
    });
  });

  // setActorLanguage (GO-047) — self-service, mesma sessão/CSRF de
  // qualquer outra mutação (ex.: updateView). Também seta o cookie
  // `lang` (Set-Cookie) — o fallback que resolveLocale usa quando o
  // ator não tem uma preferência explícita gravada (ex.: um fluxo
  // futuro sem usuário logado); com usuário logado, User.Language (via
  // Go) já tem prioridade sobre o cookie, então o cookie nunca conflita
  // com a preferência persistida.
  router.patch("/api/bff/actor/language", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = (await readJSONBody(req)) as { language?: string };
    const language = typeof body.language === "string" ? body.language : "";
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const actor = await goClient.setActorLanguage(token, data.tenant, language);
    res.setHeader("Set-Cookie", langCookieHeader(language));
    sendJSON(res, 200, {
      actor: { id: actor.id, role_id: actor.role_id, language: actor.language },
      locale: resolveLocale(actor.language, getHeader(req, "cookie"), actor.default_locale),
    });
  });

  router.get("/api/bff/tables/:table/records", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const url = new URL(req.url ?? "/", "http://placeholder");
    const cursor = url.searchParams.get("cursor") ?? undefined;
    const page = await goClient.listRecords(token, data.tenant, params.table!, cursor);
    sendJSON(res, 200, page);
  });

  router.post("/api/bff/sync/:table/exchange", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const result = await goClient.syncExchange(token, data.tenant, params.table!, body);
    sendJSON(res, 200, result);
  });

  router.post("/api/bff/tables/:table/records", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, params.table!, body);
    const created = await goClient.createRecord(token, idempotencyKey, data.tenant, params.table!, body);
    sendJSON(res, 201, created);
  });

  // GO-019: conectar tabelas/campos/views ao ciclo real do editor —
  // createTable/addField não precisam de Idempotency-Key (o Go já é
  // idempotente por definição própria para os dois, ver
  // internal-api.yaml); createView/updateView precisam (mesma razão de
  // createRecord: sem isso, um retry de rede colidiria em nome duplicado
  // ou em conflito de versão que na verdade já tinha sido salvo).
  router.post("/api/bff/tables", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const created = await goClient.createTable(token, data.tenant, body as { name: string });
    sendJSON(res, 201, created);
  });

  router.post("/api/bff/tables/:table/fields", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const created = await goClient.addField(token, data.tenant, params.table!, body as { name: string; type: string });
    sendJSON(res, 201, created);
  });

  router.get("/api/bff/views", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const url = new URL(req.url ?? "/", "http://placeholder");
    const table = url.searchParams.get("table") ?? undefined;
    const list = await goClient.listViews(token, data.tenant, table);
    sendJSON(res, 200, list);
  });

  router.post("/api/bff/views", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    // "views" como escopo do recurso na chave de idempotência (o mesmo
    // parâmetro que computeIdempotencyKey chama de "tabela" para
    // records) — só precisa ser um identificador estável do tipo de
    // operação, não literalmente um nome de tabela dinâmica.
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "views", body);
    const created = await goClient.createView(
      token,
      idempotencyKey,
      data.tenant,
      body as { name: string; table: string; template: string; configuration: Record<string, unknown> }
    );
    sendJSON(res, 201, created);
  });

  router.get("/api/bff/views/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const view = await goClient.getView(token, data.tenant, Number(params.id));
    sendJSON(res, 200, view);
  });

  // renderView (GO-020, estendido em GO-039) — só sessão, sem CSRF: é
  // uma leitura, não uma mutação, mesmo padrão de listRecords/getView
  // acima. `?record=` repassado tal como recebido — obrigatório para
  // Show, opcional para Edit, ignorado por List (o Go decide, o BFF só
  // transporta o parâmetro).
  router.get("/api/bff/views/:id/render", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const url = new URL(req.url ?? "/", "http://placeholder");
    const limitRaw = url.searchParams.get("limit");
    const cursor = url.searchParams.get("cursor") ?? undefined;
    const recordRaw = url.searchParams.get("record");
    const plan = await goClient.renderView(token, data.tenant, Number(params.id), {
      limit: limitRaw ? Number(limitRaw) : undefined,
      cursor,
      record: recordRaw ? Number(recordRaw) : undefined,
    });
    sendJSON(res, 200, plan);
  });

  // submitView (GO-039) — form_action de uma view Edit: cria ou atualiza
  // um registro. Idempotency-Key calculada com o escopo "views/<id>/
  // submit" — nunca reaproveita a chave de updateView (mesmo view id,
  // operação diferente, precisa de namespaces distintos).
  router.post("/api/bff/views/:id/submit", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "views/" + params.id + "/submit", body);
    const result = await goClient.submitView(
      token,
      idempotencyKey,
      data.tenant,
      Number(params.id),
      body as { record_id?: number; _version?: string; values: Record<string, unknown> }
    );
    sendJSON(res, (body as { record_id?: number }).record_id ? 200 : 201, result);
  });

  // deleteViewRow (GO-039) — a ação de coluna "Delete" de uma view List.
  // Sessão + CSRF (é uma mutação), sem Idempotency-Key — ver goClient.ts.
  router.delete("/api/bff/views/:id/rows/:recordId", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const url = new URL(req.url ?? "/", "http://placeholder");
    const version = url.searchParams.get("version") ?? "";
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    await goClient.deleteViewRow(token, data.tenant, Number(params.id), Number(params.recordId), version);
    res.writeHead(204);
    res.end();
  });

  router.patch("/api/bff/views/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "views/" + params.id, body);
    const updated = await goClient.updateView(
      token,
      idempotencyKey,
      data.tenant,
      Number(params.id),
      body as { _version: string; configuration?: Record<string, unknown>; template?: string; min_role?: number }
    );
    sendJSON(res, 200, updated);
  });

  // Workflows (GO-048) — CRUD da definição persistida + execução ponta a
  // ponta, mesmo padrão de idempotência de tables/views: o BFF calcula
  // a Idempotency-Key a partir de (usuário, tenant, escopo, corpo), o
  // editor visual nunca precisa gerar/gerenciar uma chave própria.
  router.get("/api/bff/workflows", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const list = await goClient.listWorkflows(token, data.tenant);
    sendJSON(res, 200, list);
  });

  router.post("/api/bff/workflows", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "workflows", body);
    const created = await goClient.createWorkflow(token, idempotencyKey, data.tenant, body as { name: string });
    sendJSON(res, 201, created);
  });

  router.get("/api/bff/workflows/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const wf = await goClient.getWorkflow(token, data.tenant, Number(params.id));
    sendJSON(res, 200, wf);
  });

  router.patch("/api/bff/workflows/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "workflows/" + params.id, body);
    const updated = await goClient.updateWorkflow(
      token,
      idempotencyKey,
      data.tenant,
      Number(params.id),
      body as { _version: string; name?: string; initial_step?: string }
    );
    sendJSON(res, 200, updated);
  });

  router.delete("/api/bff/workflows/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    await goClient.deleteWorkflow(token, data.tenant, Number(params.id));
    res.writeHead(204);
    res.end();
  });

  router.post("/api/bff/workflows/:id/steps", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "workflows/" + params.id + "/steps", body);
    const created = await goClient.createWorkflowStep(
      token,
      idempotencyKey,
      data.tenant,
      Number(params.id),
      body as { name: string; action_name: string }
    );
    sendJSON(res, 201, created);
  });

  router.patch("/api/bff/workflows/:id/steps/:stepId", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "workflows/" + params.id + "/steps/" + params.stepId, body);
    const updated = await goClient.updateWorkflowStep(
      token,
      idempotencyKey,
      data.tenant,
      Number(params.id),
      Number(params.stepId),
      body as { _version: string }
    );
    sendJSON(res, 200, updated);
  });

  router.delete("/api/bff/workflows/:id/steps/:stepId", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    await goClient.deleteWorkflowStep(token, data.tenant, Number(params.id), Number(params.stepId));
    res.writeHead(204);
    res.end();
  });

  // runWorkflow (GO-048) — a chamada síncrona do botão "Executar" do
  // editor visual, a prova ponta a ponta do critério de aceite da task.
  router.post("/api/bff/workflows/:id/run", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "workflows/" + params.id + "/run", body);
    const run = await goClient.runWorkflow(token, idempotencyKey, data.tenant, Number(params.id), body as { context?: Record<string, unknown> });
    sendJSON(res, 200, run);
  });

  // Upload/download de arquivo (GO-051) — o consumidor HTTP que
  // internal/files (GO-026) não tinha. O corpo multipart do navegador é
  // repassado sem reinterpretar o CONTEÚDO (parseSingleFileMultipart só
  // extrai o campo "file"); o BFF monta um FormData PRÓPRIO para a
  // chamada ao Go (goClient.uploadFile) — não é um proxy byte-a-byte do
  // multipart original, mas o mesmo arquivo/nome/tipo chegam intactos.
  router.post("/api/bff/files", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const contentType = getHeader(req, "content-type");
    const rawBody = await readRawBody(req);
    const parsed = parseSingleFileMultipart(contentType, rawBody, "file");
    if (!parsed) {
      throw new BffError(400, "file_required", "campo multipart \"file\" é obrigatório");
    }
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const uploaded = await goClient.uploadFile(token, data.tenant, parsed.filename, parsed.contentType, parsed.content);
    sendJSON(res, 201, uploaded);
  });

  // downloadFile — sem CSRF (é uma leitura, mesmo padrão de
  // listRecords/getView); a autorização real (dono ou papel suficiente)
  // é decidida pelo Go, o BFF só repassa os bytes e o Content-Type.
  router.get("/api/bff/files/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const { contentType, body } = await goClient.downloadFile(token, data.tenant, Number(params.id));
    res.writeHead(200, { "Content-Type": contentType });
    res.end(body);
  });

  // emitEvent (GO-052) — o mecanismo por trás de Trigger.emitEvent do
  // legado. 403 (CSRF inválido, ou nome de evento não autorizado) chega
  // pronto do Go via BffError — nenhuma reinterpretação aqui.
  router.post("/api/bff/events/:eventname", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "events/" + params.eventname, body);
    const result = await goClient.emitEvent(token, idempotencyKey, data.tenant, params.eventname!, body as { payload?: Record<string, unknown> });
    sendJSON(res, 200, result);
  });

  // shareHandler (GO-053) — recebe conteúdo compartilhado via a API Web
  // Share Target do navegador (POST nativo, `application/x-www-form-
  // urlencoded`, disparado pelo próprio SO/navegador a partir do
  // manifesto — nunca uma página desta aplicação). DELIBERADAMENTE sem
  // requireCsrf: esse POST nativo não tem como anexar o cabeçalho
  // X-CSRF-Token (nenhuma página/JS controla a requisição). A
  // Idempotency-Key é sempre calculada aqui, nunca exigida do chamador,
  // mesmo motivo.
  router.post("/api/bff/notifications/share-handler", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    const raw = await readRawBody(req);
    const params = new URLSearchParams(raw.toString("utf8"));
    const body = { title: params.get("title") ?? undefined, text: params.get("text") ?? undefined, url: params.get("url") ?? undefined };
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const idempotencyKey = computeIdempotencyKey(data.userId, data.tenant, "share-handler", body);
    const result = await goClient.shareHandler(token, idempotencyKey, data.tenant, body);
    sendJSON(res, 200, result);
  });

  // Administração de usuário (GO-044) — a UI de `auth/admin.ts` do
  // legado. Autorização "é admin?" fica quase toda do lado Go
  // (identity.requireAdmin, 403 se não for) — o BFF só propaga; a única
  // exceção é force-logout, que nunca chama o Go (destrói sessões no
  // store do próprio BFF, ADR-0007) e por isso precisa checar o papel
  // aqui mesmo.
  router.get("/api/bff/admin/users", async (req, res) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const users = await goClient.listUsers(token, data.tenant);
    sendJSON(res, 200, users);
  });

  router.patch("/api/bff/admin/users/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    await goClient.updateUserRole(token, data.tenant, Number(params.id), Number((body as { role_id: number }).role_id));
    res.writeHead(204);
    res.end();
  });

  router.delete("/api/bff/admin/users/:id", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    await goClient.deleteUser(token, data.tenant, Number(params.id));
    res.writeHead(204);
    res.end();
  });

  router.post("/api/bff/admin/users/:id/reset-password", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const password = typeof body.password === "string" ? body.password : undefined;
    const result = await goClient.resetUserPassword(token, data.tenant, Number(params.id), password);
    sendJSON(res, 200, result);
  });

  router.get("/api/bff/admin/users/:id/tokens", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const tokens = await goClient.listUserTokens(token, data.tenant, Number(params.id));
    sendJSON(res, 200, tokens);
  });

  // force-logout (GO-044) — equivalente ao "force-logout" de
  // `auth/admin.ts`, mas puramente no BFF (a sessão nunca é do Go,
  // ADR-0007): derruba TODAS as sessões ativas do usuário-alvo. Único
  // handler administrativo que precisa checar o papel do ator aqui
  // mesmo, já que não há uma chamada ao Go para devolver 403.
  router.post("/api/bff/admin/users/:id/force-logout", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const actor = await goClient.getActor(token, data.tenant);
    if (actor.role_id !== 1) throw forbiddenError(); // 1 = identity.RoleAdmin (Go)
    await sessionStore.destroyAllForUser(params.id!);
    res.writeHead(204);
    res.end();
  });

  // impersonate (GO-044) — inicia a auditoria no Go (startImpersonation)
  // e, só depois de confirmada, cria uma sessão NOVA para o usuário-alvo
  // (nunca reaproveita a sessão do admin) com `impersonatedBy` marcado —
  // a sessão anterior do admin nesta aba fica sobrescrita pelo
  // Set-Cookie desta resposta, mas continua válida no store até expirar
  // ou até o admin fazer logout de outra aba.
  router.post("/api/bff/admin/users/:id/impersonate", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const targetUserId = Number(params.id);
    const started = await goClient.startImpersonation(token, data.tenant, targetUserId);
    const newSessionId = await sessionStore.create({
      userId: String(started.target_user_id),
      tenant: data.tenant,
      impersonatedBy: { adminUserId: data.userId, logId: started.log_id },
    });
    const csrfToken = generateCsrfToken();
    res.setHeader("Set-Cookie", [sessionCookieHeader(newSessionId), csrfCookieHeader(csrfToken)]);
    sendJSON(res, 200, { status: "ok", target_user_id: started.target_user_id });
  });

  // impersonation/end (GO-044) — encerra a impersonação da sessão
  // ATUAL (nunca aceita um log_id vindo do corpo/query — sempre o que a
  // própria sessão guardou), encerra a auditoria no Go, e destrói a
  // sessão de impersonação. Decisão deliberada: NÃO restaura a sessão
  // original do admin automaticamente — força um novo login, mais
  // simples e mais seguro que guardar duas sessões empilhadas.
  router.post("/api/bff/admin/impersonation/end", async (req, res) => {
    const { sessionId, data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    if (!data.impersonatedBy) throw impersonationNotActiveError();
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    await goClient.endImpersonation(token, data.tenant, data.impersonatedBy.logId);
    await sessionStore.destroy(sessionId);
    res.setHeader("Set-Cookie", [expiredSessionCookieHeader()]);
    res.writeHead(204);
    res.end();
  });

  router.patch("/api/bff/admin/tables/:table/permissions", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    requireCsrf(req);
    const body = await readJSONBody(req);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const updated = await goClient.updateTablePermissions(token, data.tenant, params.table!, body as { min_role_read: number; min_role_write: number });
    sendJSON(res, 200, updated);
  });

  return router;
}

/**
 * createRequestListener monta o handler HTTP final: roteia, e traduz
 * qualquer BffError lançado por um handler para o formato de resposta do
 * contrato — nenhum handler escreve `error` na resposta diretamente,
 * evitando divergência de formato entre rotas.
 */
export function createRequestListener(router: Router, deps: AppDeps) {
  return async (req: IncomingMessage, res: ServerResponse): Promise<void> => {
    const end = deps.onRequestStart?.();
    try {
      const url = new URL(req.url ?? "/", "http://placeholder");
      const match = router.match(req.method ?? "GET", url.pathname);
      if (!match) {
        sendJSON(res, 404, { error: { code: "not_found", message: "rota não encontrada" } });
        return;
      }
      await match.handler(req, res, match.params);
    } catch (err) {
      if (err instanceof BffError) {
        sendError(res, err);
        return;
      }
      // Nunca vazar a mensagem crua de uma exceção não classificada —
      // mesmo cuidado do backend Go em internal/records (classifyPgError)
      // e internal/platform/database (nunca logar a query/erro cru).
      sendJSON(res, 502, { error: { code: "internal_error", message: "erro inesperado ao processar a requisição" } });
    } finally {
      end?.();
    }
  };
}

// Exportado para os testes de sessão/CSRF montarem uma sessão de teste sem
// precisar de um endpoint de login público (ver nota de escopo #4).
export async function createTestSession(sessionStore: SessionStore, data: { userId: string; tenant: string }) {
  const sessionId = await sessionStore.create(data);
  const csrfToken = generateCsrfToken();
  return {
    cookies: `${sessionCookieHeader(sessionId)}`.split(";")[0] + "; " + csrfCookieHeader(csrfToken).split(";")[0],
    csrfToken,
  };
}

export { SESSION_COOKIE_NAME, CSRF_COOKIE_NAME, CSRF_HEADER_NAME };
