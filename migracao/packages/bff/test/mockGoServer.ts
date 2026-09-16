// Réplica mínima, só para teste, da verificação de identidade delegada
// que o backend Go real faz (internal/platform/tenancy.Verifier, GO-008) —
// existe para provar, na fronteira que o BFF controla, que um token
// adulterado/forjado é rejeitado de ponta a ponta, não apenas confiado
// porque "o BFF disse que é válido". Não reimplementa o domínio (records/
// identity) de verdade — só o suficiente para exercitar o cliente do BFF
// (goClient.ts) contra respostas HTTP realistas.
import { createServer, type Server } from "node:http";
import { createHash } from "node:crypto";
import jwt from "jsonwebtoken";

export interface MockGoServerOptions {
  readonly secret: string;
  /** Atraso artificial antes de responder — para testar timeout do lado do BFF. */
  readonly delayMs?: number;
}

export class MockGoServer {
  readonly server: Server;
  private port = 0;
  createCallCount = 0;
  private readonly createdKeys = new Map<string, { payloadHash: string; body: unknown }>();

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
      const idempotencyKey = req.headers["idempotency-key"];
      if (!idempotencyKey || Array.isArray(idempotencyKey)) {
        sendJSON(res, 400, { error: { code: "idempotency_key_required", message: "cabeçalho Idempotency-Key é obrigatório" } });
        return;
      }
      const body = await readBody(req);
      const payloadHash = createHash("sha256").update(JSON.stringify(body)).digest("hex");

      const existing = this.createdKeys.get(idempotencyKey);
      if (existing) {
        if (existing.payloadHash !== payloadHash) {
          sendJSON(res, 409, { error: { code: "idempotency_key_conflict", message: "Idempotency-Key já foi usada com um payload diferente" } });
          return;
        }
        // Replay: NÃO incrementa createCallCount — é exatamente o que outbox.Do faz de verdade.
        sendJSON(res, 201, existing.body);
        return;
      }

      this.createCallCount++;
      const created = { id: this.createCallCount, ...(body as Record<string, unknown>) };
      this.createdKeys.set(idempotencyKey, { payloadHash, body: created });
      sendJSON(res, 201, created);
      return;
    }

    sendJSON(res, 404, { error: { code: "not_found", message: "rota não encontrada no mock" } });
  }
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
