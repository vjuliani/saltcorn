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

    if (url.pathname.endsWith("/actor")) {
      sendJSON(res, 200, { id: Number(claims.sub), role_id: 80 });
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

    // GO-020: render — mock simplificado da mesma classificação de
    // internal/views/render.go (só template "List" com layout.besides não
    // vazio é compatível); não reimplementa paginação real por cursor
    // opaco, só o suficiente para o BFF exercitar propagação de query
    // params/422 sem duplicar toda a lógica de classificação do Go.
    const renderMatch = url.pathname.match(/\/views\/(\d+)\/render$/);
    if (renderMatch && req.method === "GET") {
      const id = Number(renderMatch[1]);
      const view = this.views.get(id);
      if (!view) {
        sendJSON(res, 404, { error: { code: "not_found", message: "view não encontrada" } });
        return;
      }
      const configuration = view.configuration as { layout?: { besides?: Array<{ header_label?: string; contents?: { type?: string; field_name?: string } }> } };
      const besides = configuration?.layout?.besides;
      if (view.template !== "List" || !besides || besides.length === 0) {
        sendJSON(res, 422, { error: { code: "view_unsupported", message: "layout incompatível com o runtime atual (mock)" } });
        return;
      }
      const allRows = [
        { id: 1, title: "Dune", pages: 412 },
        { id: 2, title: "Foundation", pages: 255 },
        { id: 3, title: "Neuromancer", pages: 271 },
      ];
      const limit = Number(url.searchParams.get("limit") ?? "50");
      const offset = Number(url.searchParams.get("cursor") ?? "0");
      const page = allRows.slice(offset, offset + limit);
      const hasMore = offset + limit < allRows.length;
      sendJSON(res, 200, {
        view_id: id,
        columns: besides.map((c) => ({ field_name: c.contents?.field_name ?? "", header_label: c.header_label ?? c.contents?.field_name ?? "" })),
        rows: page,
        order_by: "id",
        descending: false,
        next_cursor: hasMore ? String(offset + limit) : null,
      });
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
