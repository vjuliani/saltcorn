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
type CreateTableResponse = paths["/api/bff/tables"]["post"]["responses"]["201"]["content"]["application/json"];
type AddFieldResponse =
  paths["/api/bff/tables/{table}/fields"]["post"]["responses"]["201"]["content"]["application/json"];
type CreateViewResponse = paths["/api/bff/views"]["post"]["responses"]["201"]["content"]["application/json"];
type GetViewResponse = paths["/api/bff/views/{id}"]["get"]["responses"]["200"]["content"]["application/json"];
type UpdateViewResponse = paths["/api/bff/views/{id}"]["patch"]["responses"]["200"]["content"]["application/json"];
type ErrorResponse = { error: { code: string; message: string } };

/**
 * VersionConflictError distingue o 409 de "conflito de edição" (GO-019)
 * de qualquer outro erro do BFF — o React usa isso para mostrar "alguém
 * mais salvou por cima" em vez de um erro genérico, o critério de aceite
 * "conflito de edição é apresentado, não sobrescrito silenciosamente"
 * exige que o chamador consiga DISTINGUIR esse caso, não só ver uma
 * mensagem de erro qualquer.
 */
export class ViewConflictError extends Error {
  constructor() {
    super("a view foi modificada por outra transação — releia e tente novamente");
  }
}

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

/**
 * Lê o cookie `sc_csrf` (não-HttpOnly por natureza: `csrf.ts` do BFF o
 * expõe justamente para o JS do cliente poder ecoá-lo de volta no
 * cabeçalho `X-CSRF-Token`, GO-006). Não gera nem armazena token algum
 * aqui — só lê o que o BFF já colocou no navegador.
 */
export function readCsrfCookie(cookie: string = document.cookie): string | undefined {
  const match = cookie.match(/(?:^|;\s*)sc_csrf=([^;]+)/);
  return match ? decodeURIComponent(match[1]!) : undefined;
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
    return this.request<CreateRecordResponse>(`/api/bff/tables/${encodeURIComponent(table)}/records`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": this.requireCsrf() },
      body: JSON.stringify(input),
    });
  }

  async createTable(input: { name: string; min_role_read?: number; min_role_write?: number }): Promise<CreateTableResponse> {
    return this.request<CreateTableResponse>("/api/bff/tables", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": this.requireCsrf() },
      body: JSON.stringify(input),
    });
  }

  async addField(
    table: string,
    input: { name: string; type: string; required?: boolean; unique?: boolean; references?: string }
  ): Promise<AddFieldResponse> {
    return this.request<AddFieldResponse>(`/api/bff/tables/${encodeURIComponent(table)}/fields`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": this.requireCsrf() },
      body: JSON.stringify(input),
    });
  }

  async createView(input: {
    name: string;
    table: string;
    template: string;
    configuration: Record<string, unknown>;
    min_role?: number;
  }): Promise<CreateViewResponse> {
    return this.request<CreateViewResponse>("/api/bff/views", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": this.requireCsrf() },
      body: JSON.stringify(input),
    });
  }

  async getView(id: number): Promise<GetViewResponse> {
    return this.request<GetViewResponse>(`/api/bff/views/${id}`, { method: "GET" });
  }

  /**
   * updateView é "salvar" (com `configuration`) e "publicar" (com
   * `min_role` menor) — a mesma chamada. Lança ViewConflictError
   * especificamente em 409 `version_conflict`, para o chamador
   * distinguir de qualquer outro erro (ver ViewConflictError acima).
   */
  async updateView(
    id: number,
    input: { _version: string; configuration?: Record<string, unknown>; template?: string; min_role?: number }
  ): Promise<UpdateViewResponse> {
    try {
      return await this.request<UpdateViewResponse>(`/api/bff/views/${id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": this.requireCsrf() },
        body: JSON.stringify(input),
      });
    } catch (err) {
      if (err instanceof BffClientError && err.status === 409 && err.code === "version_conflict") {
        throw new ViewConflictError();
      }
      throw err;
    }
  }

  private requireCsrf(): string {
    if (!this.opts.csrfToken) {
      throw new BffClientError(0, "csrf_token_missing", "csrfToken não configurado no BffClient");
    }
    return this.opts.csrfToken;
  }

  private async request<T>(path: string, init: RequestInit): Promise<T> {
    const res = await fetch(`${this.opts.baseUrl}${path}`, { ...init, credentials: "include" });
    if (res.ok) return (await res.json()) as T;
    const body = (await res.json().catch(() => null)) as ErrorResponse | null;
    throw new BffClientError(res.status, body?.error.code ?? "unknown_error", body?.error.message ?? res.statusText);
  }
}
