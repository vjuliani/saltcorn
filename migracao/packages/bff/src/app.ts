// Composição das rotas do BFF (bff-api.yaml) — health/readiness (GO-005,
// mesmo espírito do backend Go), bootstrap, e list/create de registros.
// Nunca acessa banco de domínio (ADR-0003): toda regra passa por GoClient.
import type { IncomingMessage, ServerResponse } from "node:http";
import type { Config } from "./config.js";
import { GoClient } from "./goClient.js";
import type { SessionStore } from "./session.js";
import { readSessionCookie, sessionCookieHeader, SESSION_COOKIE_NAME } from "./session.js";
import { CSRF_COOKIE_NAME, CSRF_HEADER_NAME, csrfCookieHeader, generateCsrfToken, readCsrfCookie, verifyCsrf } from "./csrf.js";
import { mintServiceIdentity } from "./serviceIdentity.js";
import { computeIdempotencyKey } from "./idempotency.js";
import { BffError, csrfInvalidError, sessionRequiredError } from "./errors.js";
import { getHeader, readJSONBody, sendError, sendJSON } from "./httpHelpers.js";
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
    sendJSON(res, 200, { actor: { id: actor.id, role_id: actor.role_id }, tenant: data.tenant });
  });

  router.get("/api/bff/tables/:table/records", async (req, res, params) => {
    const { data } = await requireSession(req, sessionStore);
    const token = mintServiceIdentity(config.serviceIdentitySecret, { sub: data.userId, tenant: data.tenant }, config.serviceIdentityTtlSeconds);
    const url = new URL(req.url ?? "/", "http://placeholder");
    const cursor = url.searchParams.get("cursor") ?? undefined;
    const page = await goClient.listRecords(token, data.tenant, params.table!, cursor);
    sendJSON(res, 200, page);
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
