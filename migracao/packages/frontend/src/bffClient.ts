// Cliente HTTP tipado para bff-api.yaml (GO-006/GO-017) — mesmo padrão de
// migracao/packages/bff/src/goClient.ts: tipos gerados por openapi-
// typescript, `fetch` com `credentials: "include"` (sessão via cookie
// `sc_session`, nunca um token no código do frontend), sem SDK gerado à
// parte. "Remover a dependência do frontend de modelos de servidor"
// (ADR-0002) significa isto: o frontend só conhece o formato JSON do BFF,
// nunca importa nada de packages/saltcorn-data.
import type { paths } from "../../../contracts/gen/ts/bff-api.js";

type BootstrapResponse = paths["/api/bff/bootstrap"]["get"]["responses"]["200"]["content"]["application/json"];
type ListRecordsResponse =
  paths["/api/bff/tables/{table}/records"]["get"]["responses"]["200"]["content"]["application/json"];
type CreateRecordResponse =
  paths["/api/bff/tables/{table}/records"]["post"]["responses"]["201"]["content"]["application/json"];
type ErrorResponse = { error: { code: string; message: string } };

export class BffClientError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string
  ) {
    super(message);
  }
}

export interface BffClientOptions {
  baseUrl: string;
  /** Necessário em toda mutação (POST/PATCH/DELETE) — lido do cookie sc_csrf pelo chamador, nunca gerado aqui. */
  csrfToken?: string;
}

export class BffClient {
  constructor(private readonly opts: BffClientOptions) {}

  async getBootstrap(): Promise<BootstrapResponse> {
    return this.request<BootstrapResponse>("/api/bff/bootstrap", { method: "GET" });
  }

  async listRecords(table: string, cursor?: string): Promise<ListRecordsResponse> {
    const url = new URL(`${this.opts.baseUrl}/api/bff/tables/${encodeURIComponent(table)}/records`);
    if (cursor) url.searchParams.set("cursor", cursor);
    return this.request<ListRecordsResponse>(url.pathname + url.search, { method: "GET" });
  }

  async createRecord(table: string, input: Record<string, unknown>): Promise<CreateRecordResponse> {
    if (!this.opts.csrfToken) {
      throw new BffClientError(0, "csrf_token_missing", "csrfToken não configurado no BffClient");
    }
    return this.request<CreateRecordResponse>(`/api/bff/tables/${encodeURIComponent(table)}/records`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": this.opts.csrfToken },
      body: JSON.stringify(input),
    });
  }

  private async request<T>(path: string, init: RequestInit): Promise<T> {
    const res = await fetch(`${this.opts.baseUrl}${path}`, { ...init, credentials: "include" });
    if (res.ok) return (await res.json()) as T;
    const body = (await res.json().catch(() => null)) as ErrorResponse | null;
    throw new BffClientError(res.status, body?.error.code ?? "unknown_error", body?.error.message ?? res.statusText);
  }
}
