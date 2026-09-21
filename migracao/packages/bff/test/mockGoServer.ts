// Réplica mínima, só para teste, da verificação de identidade delegada
// que o backend Go real faz (internal/platform/tenancy.Verifier, GO-008) —
// existe para provar, na fronteira que o BFF controla, que um token
// adulterado/forjado é rejeitado de ponta a ponta, não apenas confiado
// porque "o BFF disse que é válido". Não reimplementa o domínio (records/
// metadata/views) de verdade — só o suficiente para exercitar o cliente
// do BFF (goClient.ts) contra respostas HTTP realistas, incluindo o
// mesmo comportamento de idempotência via Idempotency-Key que o outbox.Do
// real (GO-014) tem para createRecord/createView/updateView.
import { createServer, type Server } from "node:http";
import { createHash } from "node:crypto";
import jwt from "jsonwebtoken";

export interface MockGoServerOptions {
  readonly secret: string;
  /** Atraso artificial antes de responder — para testar timeout do lado do BFF. */
  readonly delayMs?: number;
  /** GO-044: subs (claims.sub) tratados como identity.RoleAdmin (1) — os demais recebem role_id 80, mesma convenção do /actor abaixo. */
  readonly adminUserIds?: readonly string[];
}

interface MockUser {
  id: number;
  email: string;
  role_id: number;
}

interface MockImpersonation {
  adminUserId: number;
  targetUserId: number;
  endedAt: string | null;
}

interface IdempotentEntry {
  payloadHash: string;
  body: unknown;
}

export class MockGoServer {
  readonly server: Server;
  private port = 0;
  createCallCount = 0;
  createViewCallCount = 0;
  updateViewCallCount = 0;
  private nextViewId = 1;
  private readonly views = new Map<number, Record<string, unknown>>();
  private readonly createdKeys = new Map<string, IdempotentEntry>();
  private readonly viewCreateKeys = new Map<string, IdempotentEntry>();
  private readonly viewUpdateKeys = new Map<string, IdempotentEntry>();

  // GO-044: estado em memória da administração de usuário — só o
  // suficiente para o BFF exercitar list/update/delete/reset-password/
  // tokens/impersonate/permissions contra respostas HTTP realistas.
  private readonly users = new Map<number, MockUser>();
  private readonly userTokens = new Map<number, Array<{ id: number; created_at: string; revoked: boolean }>>();
  private readonly impersonations = new Map<number, MockImpersonation>();
  private nextLogId = 1;

  // GO-039: registros de "books" — usados por render (List/Show/Edit),
  // submit e delete-row. Seedados com os mesmos 3 livros que o render
  // List sempre devolveu (Dune/Foundation/Neuromancer), agora como
  // estado MUTÁVEL — submit/delete-row de verdade os alteram, provando
  // que o BFF transporta o efeito real, não só o formato da resposta.
  private readonly records = new Map<number, Record<string, unknown>>([
    [1, { id: 1, title: "Dune", pages: 412, _version: "1" }],
    [2, { id: 2, title: "Foundation", pages: 255, _version: "1" }],
    [3, { id: 3, title: "Neuromancer", pages: 271, _version: "1" }],
  ]);
  private nextRecordId = 4;
  private readonly submitKeys = new Map<string, { payloadHash: string; status: number; body: unknown }>();

  constructor(private readonly opts: MockGoServerOptions) {
    this.server = createServer((req, res) => {
      void this.handle(req, res);
    });
  }

  async listen(): Promise<string> {
    await new Promise<void>((resolve) => this.server.listen(0, "127.0.0.1", resolve));
    const address = this.server.address();
    if (address && typeof address === "object") this.port = address.port;
    return `http://127.0.0.1:${this.port}`;
  }

  async close(): Promise<void> {
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }

  // seedUser/seedUserTokens/getImpersonation (GO-044) — os testes de
  // app.ts populam o estado que os handlers administrativos leem, e
  // inspecionam o registro de impersonação sem precisar de outra rota.
  seedUser(user: MockUser): void {
    this.users.set(user.id, { ...user });
  }

  seedUserTokens(userId: number, tokens: Array<{ id: number; created_at: string; revoked: boolean }>): void {
    this.userTokens.set(userId, tokens);
  }

  getImpersonation(logId: number): MockImpersonation | undefined {
    return this.impersonations.get(logId);
  }

  private async handle(req: import("node:http").IncomingMessage, res: import("node:http").ServerResponse): Promise<void> {
    if (this.opts.delayMs) {
      await new Promise((r) => setTimeout(r, this.opts.delayMs));
    }

    const url = new URL(req.url ?? "/", "http://placeholder");
    const auth = req.headers.authorization ?? "";
    const token = auth.startsWith("Bearer ") ? auth.slice("Bearer ".length) : "";

    const tenantMatch = url.pathname.match(/^\/v1\/tenants\/([^/]+)\//);
    const urlTenant = tenantMatch ? tenantMatch[1] : "";

    let claims: jwt.JwtPayload;
    try {
      claims = jwt.verify(token, this.opts.secret, { algorithms: ["HS256"] }) as jwt.JwtPayload;
    } catch {
      sendJSON(res, 401, { error: { code: "invalid_identity_token", message: "assinatura do token de identidade delegada não confere" } });
      return;
    }
    if (claims.tenant !== urlTenant) {
      sendJSON(res, 403, { error: { code: "tenant_mismatch", message: "tenant do token de identidade não corresponde ao tenant do recurso" } });
      return;
    }

    const isAdmin = this.opts.adminUserIds?.includes(String(claims.sub)) ?? false;

    if (url.pathname.endsWith("/actor")) {
      sendJSON(res, 200, { id: Number(claims.sub), role_id: isAdmin ? 1 : 80 });
      return;
    }

    // GO-044: administração de usuário — réplica mínima de
    // internal/identity.requireAdmin (403 para quem não está em
    // adminUserIds) e das rotas de cmd/server/users.go.
    if (req.method === "GET" && url.pathname.endsWith("/users")) {
      if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
      sendJSON(res, 200, Array.from(this.users.values()));
      return;
    }

    const userIdMatch = url.pathname.match(/\/users\/(\d+)$/);
    if (userIdMatch) {
      const id = Number(userIdMatch[1]);
      if (req.method === "PATCH") {
        if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
        const user = this.users.get(id);
        if (!user) { sendJSON(res, 404, { error: { code: "not_found", message: "usuário não encontrado" } }); return; }
        const body = (await readBody(req)) as { role_id: number };
        user.role_id = body.role_id;
        res.writeHead(204); res.end();
        return;
      }
      if (req.method === "DELETE") {
        if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
        this.users.delete(id);
        res.writeHead(204); res.end();
        return;
      }
    }

    const resetPasswordMatch = url.pathname.match(/\/users\/(\d+)\/reset-password$/);
    if (resetPasswordMatch && req.method === "POST") {
      if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
      const id = Number(resetPasswordMatch[1]);
      if (!this.users.has(id)) { sendJSON(res, 404, { error: { code: "not_found", message: "usuário não encontrado" } }); return; }
      const body = (await readBody(req)) as { password?: string };
      sendJSON(res, 200, { password: body.password ?? "senha-aleatoria-gerada-pelo-mock" });
      return;
    }

    const tokensMatch = url.pathname.match(/\/users\/(\d+)\/tokens$/);
    if (tokensMatch && req.method === "GET") {
      if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
      sendJSON(res, 200, this.userTokens.get(Number(tokensMatch[1])) ?? []);
      return;
    }

    const impersonateMatch = url.pathname.match(/\/users\/(\d+)\/impersonate$/);
    if (impersonateMatch && req.method === "POST") {
      if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
      const targetUserId = Number(impersonateMatch[1]);
      const adminUserId = Number(claims.sub);
      if (adminUserId === targetUserId) { sendJSON(res, 400, { error: { code: "cannot_impersonate_self", message: "não é possível impersonar o próprio usuário" } }); return; }
      if (!this.users.has(targetUserId)) { sendJSON(res, 404, { error: { code: "not_found", message: "usuário-alvo não encontrado" } }); return; }
      const logId = this.nextLogId++;
      this.impersonations.set(logId, { adminUserId, targetUserId, endedAt: null });
      sendJSON(res, 201, { log_id: logId, target_user_id: targetUserId });
      return;
    }

    const endImpersonationMatch = url.pathname.match(/\/impersonations\/(\d+)\/end$/);
    if (endImpersonationMatch && req.method === "POST") {
      const record = this.impersonations.get(Number(endImpersonationMatch[1]));
      if (record && record.endedAt === null) record.endedAt = new Date().toISOString();
      res.writeHead(204); res.end();
      return;
    }

    const permissionsMatch = url.pathname.match(/\/tables\/([^/]+)\/permissions$/);
    if (permissionsMatch && req.method === "PATCH") {
      if (!isAdmin) { sendJSON(res, 403, { error: { code: "not_authorized", message: "ator não tem papel suficiente" } }); return; }
      const body = (await readBody(req)) as { min_role_read: number; min_role_write: number };
      sendJSON(res, 200, { id: 1, name: permissionsMatch[1], min_role_read: body.min_role_read, min_role_write: body.min_role_write });
      return;
    }

    if (req.method === "GET" && url.pathname.endsWith("/records")) {
      sendJSON(res, 200, { items: [{ id: 1, label: "a" }], next_cursor: null });
      return;
    }

    if (req.method === "POST" && url.pathname.endsWith("/records")) {
      await this.handleIdempotentCreate(req, res, this.createdKeys, () => ++this.createCallCount);
      return;
    }

    // GO-019: tabelas/campos — sem Idempotency-Key (o Go real também não
    // exige, CreateTable/AddField já são idempotentes por definição).
    if (req.method === "POST" && /\/tables$/.test(url.pathname)) {
      const body = (await readBody(req)) as Record<string, unknown>;
      sendJSON(res, 201, { id: 1, name: body.name, min_role_read: body.min_role_read ?? 100, min_role_write: body.min_role_write ?? 1 });
      return;
    }
    if (req.method === "POST" && /\/tables\/[^/]+\/fields$/.test(url.pathname)) {
      const body = (await readBody(req)) as Record<string, unknown>;
      sendJSON(res, 201, { id: 1, table_id: 1, name: body.name, type: body.type, required: body.required ?? false });
      return;
    }

    // GO-020: listViews — sem filtro por tabela no mock (simplificação;
    // já testado no domínio/HTTP do lado Go).
    if (req.method === "GET" && url.pathname.endsWith("/views")) {
      sendJSON(res, 200, Array.from(this.views.values()));
      return;
    }

    // GO-019: views — createView com Idempotency-Key (mesmo padrão de
    // records); getView/updateView completam o ciclo.
    if (req.method === "POST" && url.pathname.endsWith("/views")) {
      await this.handleIdempotentCreate(req, res, this.viewCreateKeys, () => ++this.createViewCallCount, (body) => {
        const id = this.nextViewId++;
        const view = {
          id,
          name: body.name,
          table_id: 1,
          template: body.template,
          min_role: body.min_role ?? 1,
          configuration: body.configuration ?? {},
          _version: "1",
        };
        this.views.set(id, view);
        return view;
      });
      return;
    }

    // GO-020/GO-039: render — mock simplificado da mesma classificação de
    // internal/views/{render,show,edit}.go. Fonte de verdade:
    // `configuration.columns` (lista flat, GO-039 — NÃO
    // `configuration.layout`, que é só a árvore de arranjo visual e este
    // runtime nunca interpreta). Não reimplementa paginação real por
    // cursor opaco nem toda a classificação do Go — só o suficiente para
    // o BFF exercitar propagação de query params/idempotência/422 sem
    // duplicar a lógica de domínio (já coberta em cmd/server/*_test.go).
    const renderMatch = url.pathname.match(/\/views\/(\d+)\/render$/);
    if (renderMatch && req.method === "GET") {
      const id = Number(renderMatch[1]);
      const view = this.views.get(id);
      if (!view) {
        sendJSON(res, 404, { error: { code: "not_found", message: "view não encontrada" } });
        return;
      }
      const configuration = view.configuration as { columns?: Array<{ type?: string; field_name?: string; header_label?: string; fieldview?: string; action_name?: string }> };
      const columns = configuration?.columns;
      if (!columns || columns.length === 0) {
        sendJSON(res, 422, { error: { code: "view_unsupported", message: "configuration.columns ausente ou não é uma lista (mock)" } });
        return;
      }

      if (view.template === "List") {
        const limit = Number(url.searchParams.get("limit") ?? "50");
        const offset = Number(url.searchParams.get("cursor") ?? "0");
        const allRows = Array.from(this.records.values());
        const page = allRows.slice(offset, offset + limit);
        const hasMore = offset + limit < allRows.length;
        sendJSON(res, 200, {
          view_id: id,
          columns: columns.map((c) => ({
            kind: c.type === "Action" ? "action" : c.type === "JoinField" ? "join_field" : "field",
            field_name: c.field_name,
            header_label: c.header_label ?? c.field_name,
            action_name: c.action_name,
          })),
          rows: page,
          order_by: "id",
          descending: false,
          next_cursor: hasMore ? String(offset + limit) : null,
        });
        return;
      }

      if (view.template === "Show") {
        const recordId = Number(url.searchParams.get("record"));
        if (!recordId) {
          sendJSON(res, 400, { error: { code: "record_required", message: "parâmetro ?record= é obrigatório para Show (mock)" } });
          return;
        }
        const record = this.records.get(recordId);
        if (!record) {
          sendJSON(res, 404, { error: { code: "not_found", message: "registro não encontrado" } });
          return;
        }
        sendJSON(res, 200, {
          view_id: id,
          table: "books",
          record_id: recordId,
          columns: columns.filter((c) => c.type === "Field").map((c) => ({ kind: "field", field_name: c.field_name, header_label: c.header_label ?? c.field_name })),
          values: record,
        });
        return;
      }

      if (view.template === "Edit") {
        const recordRaw = url.searchParams.get("record");
        const recordId = recordRaw ? Number(recordRaw) : 0;
        const record = recordId ? this.records.get(recordId) : undefined;
        if (recordId && !record) {
          sendJSON(res, 404, { error: { code: "not_found", message: "registro não encontrado" } });
          return;
        }
        const fields = columns
          .filter((c) => c.type === "Field")
          .map((c) => ({
            field_name: c.field_name,
            label: c.field_name,
            field_type: "text",
            fieldview: c.fieldview ?? "edit",
            required: false,
            value: record ? (record[c.field_name as string] ?? null) : null,
          }));
        const actionCol = columns.find((c) => c.type === "Action");
        sendJSON(res, 200, {
          view_id: id,
          table: "books",
          record_id: recordId,
          _version: record ? (record._version as string) : undefined,
          fields,
          action_name: actionCol?.action_name ?? "Save",
        });
        return;
      }

      sendJSON(res, 422, { error: { code: "view_unsupported", message: `template ${JSON.stringify(view.template)} não suportado (mock)` } });
      return;
    }

    // GO-039: submit (form_action) — cria (sem record_id) ou atualiza
    // (com record_id + _version) um registro real do mock, com o mesmo
    // dedup por Idempotency-Key+hash de payload de handleIdempotentCreate.
    const submitMatch = url.pathname.match(/\/views\/(\d+)\/submit$/);
    if (submitMatch && req.method === "POST") {
      const idempotencyKey = req.headers["idempotency-key"];
      if (!idempotencyKey || Array.isArray(idempotencyKey)) {
        sendJSON(res, 400, { error: { code: "idempotency_key_required", message: "cabeçalho Idempotency-Key é obrigatório" } });
        return;
      }
      const body = (await readBody(req)) as { record_id?: number; _version?: string; values: Record<string, unknown> };
      const payloadHash = hashPayload(body);
      const existing = this.submitKeys.get(idempotencyKey);
      if (existing) {
        if (existing.payloadHash !== payloadHash) {
          sendJSON(res, 409, { error: { code: "idempotency_key_conflict", message: "Idempotency-Key já foi usada com um payload diferente" } });
          return;
        }
        sendJSON(res, existing.status, existing.body);
        return;
      }
      if (body.record_id) {
        const current = this.records.get(body.record_id);
        if (!current) {
          sendJSON(res, 404, { error: { code: "not_found", message: "registro não encontrado" } });
          return;
        }
        if (current._version !== body._version) {
          sendJSON(res, 409, { error: { code: "version_conflict", message: "o registro foi modificado por outra transação" } });
          return;
        }
        const updated = { ...current, ...body.values, id: body.record_id, _version: String(Number(current._version) + 1) };
        this.records.set(body.record_id, updated);
        const responseBody = { record: updated, navigate: { type: "reload" } };
        this.submitKeys.set(idempotencyKey, { payloadHash, status: 200, body: responseBody });
        sendJSON(res, 200, responseBody);
        return;
      }
      const newId = this.nextRecordId++;
      const created = { ...body.values, id: newId, _version: "1" };
      this.records.set(newId, created);
      const responseBody = { record: created, navigate: { type: "reload" } };
      this.submitKeys.set(idempotencyKey, { payloadHash, status: 201, body: responseBody });
      sendJSON(res, 201, responseBody);
      return;
    }

    // GO-039: rows/{recordId} (ação de coluna "Delete" de List).
    const deleteRowMatch = url.pathname.match(/\/views\/(\d+)\/rows\/(\d+)$/);
    if (deleteRowMatch && req.method === "DELETE") {
      const recordId = Number(deleteRowMatch[2]);
      const version = url.searchParams.get("version");
      if (!version) {
        sendJSON(res, 400, { error: { code: "version_required", message: "parâmetro ?version= é obrigatório" } });
        return;
      }
      const current = this.records.get(recordId);
      if (!current) {
        sendJSON(res, 404, { error: { code: "not_found", message: "registro não encontrado" } });
        return;
      }
      if (current._version !== version) {
        sendJSON(res, 409, { error: { code: "version_conflict", message: "o registro foi modificado por outra transação" } });
        return;
      }
      this.records.delete(recordId);
      res.writeHead(204);
      res.end();
      return;
    }

    const viewIdMatch = url.pathname.match(/\/views\/(\d+)$/);
    if (viewIdMatch) {
      const id = Number(viewIdMatch[1]);
      if (req.method === "GET") {
        const view = this.views.get(id);
        if (!view) {
          sendJSON(res, 404, { error: { code: "not_found", message: "view não encontrada" } });
          return;
        }
        sendJSON(res, 200, view);
        return;
      }
      if (req.method === "PATCH") {
        const idempotencyKey = req.headers["idempotency-key"];
        if (!idempotencyKey || Array.isArray(idempotencyKey)) {
          sendJSON(res, 400, { error: { code: "idempotency_key_required", message: "cabeçalho Idempotency-Key é obrigatório" } });
          return;
        }
        const body = (await readBody(req)) as Record<string, unknown>;
        const payloadHash = hashPayload(body);
        const existing = this.viewUpdateKeys.get(idempotencyKey);
        if (existing) {
          if (existing.payloadHash !== payloadHash) {
            sendJSON(res, 409, { error: { code: "idempotency_key_conflict", message: "Idempotency-Key já foi usada com um payload diferente" } });
            return;
          }
          sendJSON(res, 200, existing.body);
          return;
        }
        const current = this.views.get(id);
        if (!current) {
          sendJSON(res, 404, { error: { code: "not_found", message: "view não encontrada" } });
          return;
        }
        if (current._version !== body._version) {
          sendJSON(res, 409, { error: { code: "version_conflict", message: "a view foi modificada por outra transação" } });
          return;
        }
        this.updateViewCallCount++;
        const updated = {
          ...current,
          ...(body.configuration !== undefined ? { configuration: body.configuration } : {}),
          ...(body.template !== undefined ? { template: body.template } : {}),
          ...(body.min_role !== undefined ? { min_role: body.min_role } : {}),
          _version: String(Number(current._version) + 1),
        };
        this.views.set(id, updated);
        this.viewUpdateKeys.set(idempotencyKey, { payloadHash, body: updated });
        sendJSON(res, 200, updated);
        return;
      }
    }

    sendJSON(res, 404, { error: { code: "not_found", message: "rota não encontrada no mock" } });
  }

  private async handleIdempotentCreate(
    req: import("node:http").IncomingMessage,
    res: import("node:http").ServerResponse,
    store: Map<string, IdempotentEntry>,
    nextCallCount: () => number,
    build?: (body: Record<string, unknown>) => Record<string, unknown>
  ): Promise<void> {
    const idempotencyKey = req.headers["idempotency-key"];
    if (!idempotencyKey || Array.isArray(idempotencyKey)) {
      sendJSON(res, 400, { error: { code: "idempotency_key_required", message: "cabeçalho Idempotency-Key é obrigatório" } });
      return;
    }
    const body = (await readBody(req)) as Record<string, unknown>;
    const payloadHash = hashPayload(body);

    const existing = store.get(idempotencyKey);
    if (existing) {
      if (existing.payloadHash !== payloadHash) {
        sendJSON(res, 409, { error: { code: "idempotency_key_conflict", message: "Idempotency-Key já foi usada com um payload diferente" } });
        return;
      }
      // Replay: NÃO incrementa o contador — é exatamente o que outbox.Do faz de verdade.
      sendJSON(res, 201, existing.body);
      return;
    }

    const callCount = nextCallCount();
    const created = build ? build(body) : { id: callCount, ...body };
    store.set(idempotencyKey, { payloadHash, body: created });
    sendJSON(res, 201, created);
  }
}

function hashPayload(body: unknown): string {
  return createHash("sha256").update(JSON.stringify(body)).digest("hex");
}

function sendJSON(res: import("node:http").ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(payload);
}

function readBody(req: import("node:http").IncomingMessage): Promise<unknown> {
  return new Promise((resolve) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const raw = Buffer.concat(chunks).toString("utf8");
      resolve(raw ? JSON.parse(raw) : {});
    });
  });
}
