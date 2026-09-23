import type { IncomingMessage, ServerResponse } from "node:http";
import { BffError, toErrorBody } from "./errors.js";

const MAX_BODY_BYTES = 1 << 20; // 1 MiB — generoso para um corpo de registro dinâmico, nunca ilimitado.
// MAX_RAW_BODY_BYTES (GO-051) — limite maior, só para upload de arquivo
// (POST /api/bff/files, multipart) — mesmo teto que uploadFileHandler
// aplica do lado Go (uploadMaxBytes, cmd/server/files.go).
const MAX_RAW_BODY_BYTES = 20 << 20; // 20 MiB

export function sendJSON(res: ServerResponse, status: number, body: unknown, extraHeaders?: Record<string, string>): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", ...extraHeaders });
  res.end(payload);
}

export function sendError(res: ServerResponse, err: BffError): void {
  sendJSON(res, err.status, toErrorBody(err));
}

/** readJSONBody lê e decodifica o corpo como JSON, com limite de tamanho — nunca confiar em Content-Length sozinho. */
export function readJSONBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new BffError(413, "payload_too_large", "corpo da requisição excede o limite"));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => {
      if (chunks.length === 0) {
        resolve({});
        return;
      }
      try {
        const parsed = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
          reject(new BffError(400, "invalid_json", "corpo da requisição precisa ser um objeto JSON"));
          return;
        }
        resolve(parsed as Record<string, unknown>);
      } catch {
        reject(new BffError(400, "invalid_json", "corpo da requisição não é um JSON válido"));
      }
    });
    req.on("error", reject);
  });
}

/** readRawBody lê o corpo bruto (sem decodificar JSON) — usado por POST /api/bff/files (multipart), limite maior (MAX_RAW_BODY_BYTES) que readJSONBody. */
export function readRawBody(req: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_RAW_BODY_BYTES) {
        reject(new BffError(413, "payload_too_large", "corpo da requisição excede o limite"));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

export function getHeader(req: IncomingMessage, name: string): string | undefined {
  const value = req.headers[name.toLowerCase()];
  return Array.isArray(value) ? value[0] : value;
}
