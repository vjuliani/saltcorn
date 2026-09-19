// Cliente HTTP tipado para a API interna do backend Go (internal-api.yaml,
// GO-006) — reaproveita os tipos gerados em migracao/contracts/gen/ts
// (openapi-typescript) do mesmo jeito que gen/ts/client-example.ts (GO-006)
// já demonstrou: `fetch` tipado pelos tipos do contrato, não um SDK
// gerado à parte. Timeout explícito via AbortController em toda chamada
// (ADR-0003: "BFF precisa de timeout/circuit-breaker, não pode travar
// esperando indefinidamente").
import type { paths as InternalPaths } from "../../../contracts/gen/ts/internal-api.js";
import { domainUnavailableError, BffError } from "./errors.js";

type ListRecordsResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/records"]["get"]["responses"]["200"]["content"]["application/json"];
type CreateRecordResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/records"]["post"]["responses"]["201"]["content"]["application/json"];
type GetActorResponse =
  InternalPaths["/v1/tenants/{tenant}/actor"]["get"]["responses"]["200"]["content"]["application/json"];
type GoErrorBody =
  InternalPaths["/v1/tenants/{tenant}/actor"]["get"]["responses"]["401"]["content"]["application/json"];
type CreateTableResponse =
  InternalPaths["/v1/tenants/{tenant}/tables"]["post"]["responses"]["201"]["content"]["application/json"];
type AddFieldResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/fields"]["post"]["responses"]["201"]["content"]["application/json"];
type CreateViewResponse =
  InternalPaths["/v1/tenants/{tenant}/views"]["post"]["responses"]["201"]["content"]["application/json"];
type GetViewResponse =
  InternalPaths["/v1/tenants/{tenant}/views/{id}"]["get"]["responses"]["200"]["content"]["application/json"];
type UpdateViewResponse =
  InternalPaths["/v1/tenants/{tenant}/views/{id}"]["patch"]["responses"]["200"]["content"]["application/json"];
type RenderViewResponse =
  InternalPaths["/v1/tenants/{tenant}/views/{id}/render"]["get"]["responses"]["200"]["content"]["application/json"];
type ListViewsResponse =
  InternalPaths["/v1/tenants/{tenant}/views"]["get"]["responses"]["200"]["content"]["application/json"];
type ListRealtimeEventsResponse =
  InternalPaths["/v1/tenants/{tenant}/realtime/events"]["get"]["responses"]["200"]["content"]["application/json"];

export interface GoClientOptions {
  readonly baseUrl: string;
  readonly timeoutMs: number;
}

/**
 * GoClient chama a API interna do backend Go com a identidade delegada já
 * pronta (o chamador assina o ServiceIdentity antes — este cliente só
 * transporta, não decide identidade). Todo erro do lado Go (rede, timeout,
 * 5xx) vira `domainUnavailableError()` (502 `domain_unavailable`, o
 * critério de aceite "falhas/timeouts Go geram erros controlados"); erros
 * 4xx do Go são propagados com o `code`/`message` que o Go já classificou
 * (nunca inventados aqui).
 */
export class GoClient {
  constructor(private readonly opts: GoClientOptions) {}

  async syncExchange(serviceIdentityToken: string, tenant: string, table: string, input: Record<string, unknown>): Promise<InternalPaths["/v1/tenants/{tenant}/sync/{table}/exchange"]["post"]["responses"]["200"]["content"]["application/json"]> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/sync/${encodeURIComponent(table)}/exchange`);
    return this.request(url, { method: "POST", serviceIdentityToken, body: input });
  }

  async listRecords(serviceIdentityToken: string, tenant: string, table: string, cursor?: string): Promise<ListRecordsResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/tables/${encodeURIComponent(table)}/records`);
    if (cursor) url.searchParams.set("cursor", cursor);
    return this.request<ListRecordsResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async createRecord(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    table: string,
    input: Record<string, unknown>
  ): Promise<CreateRecordResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/tables/${encodeURIComponent(table)}/records`);
    return this.request<CreateRecordResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async getActor(serviceIdentityToken: string, tenant: string): Promise<GetActorResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/actor`);
    return this.request<GetActorResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async createTable(
    serviceIdentityToken: string,
    tenant: string,
    input: { name: string; min_role_read?: number; min_role_write?: number }
  ): Promise<CreateTableResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/tables`);
    return this.request<CreateTableResponse>(url, { method: "POST", serviceIdentityToken, body: input });
  }

  async addField(
    serviceIdentityToken: string,
    tenant: string,
    table: string,
    input: { name: string; type: string; required?: boolean; unique?: boolean; references?: string }
  ): Promise<AddFieldResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/tables/${encodeURIComponent(table)}/fields`);
    return this.request<AddFieldResponse>(url, { method: "POST", serviceIdentityToken, body: input });
  }

  // listViews (GO-020) — a página administrativa de views precisa
  // enumerar o que existe antes de abrir uma view individual.
  async listViews(serviceIdentityToken: string, tenant: string, table?: string): Promise<ListViewsResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views`);
    if (table) url.searchParams.set("table", table);
    return this.request<ListViewsResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async createView(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    input: { name: string; table: string; template: string; configuration: Record<string, unknown>; min_role?: number }
  ): Promise<CreateViewResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views`);
    return this.request<CreateViewResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async getView(serviceIdentityToken: string, tenant: string, id: number): Promise<GetViewResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views/${id}`);
    return this.request<GetViewResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async updateView(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    id: number,
    input: { _version: string; configuration?: Record<string, unknown>; template?: string; min_role?: number }
  ): Promise<UpdateViewResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views/${id}`);
    return this.request<UpdateViewResponse>(url, {
      method: "PATCH",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  // renderView (GO-020) — só GET, sem Idempotency-Key (não é uma mutação).
  // Devolve o DTO de renderização (colunas + linhas + paginação) tal como
  // o Go monta — o BFF não reinterpreta nada, só repassa.
  async renderView(
    serviceIdentityToken: string,
    tenant: string,
    id: number,
    query: { limit?: number; cursor?: string } = {}
  ): Promise<RenderViewResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views/${id}/render`);
    if (query.limit !== undefined) url.searchParams.set("limit", String(query.limit));
    if (query.cursor !== undefined) url.searchParams.set("cursor", query.cursor);
    return this.request<RenderViewResponse>(url, { method: "GET", serviceIdentityToken });
  }

  // listRealtimeEvents (GO-028) — chamado em polling curto por
  // src/realtime.ts, uma vez por socket conectado, com o ServiceIdentity
  // do PRÓPRIO usuário daquele socket: o filtro por destinatário já
  // aconteceu do lado Go (listRealtimeEvents no contrato), então este
  // cliente só transporta o cursor de retomada `after`, nunca reinterpreta
  // audience.
  async listRealtimeEvents(serviceIdentityToken: string, tenant: string, after: number): Promise<ListRealtimeEventsResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/realtime/events`);
    if (after > 0) url.searchParams.set("after", String(after));
    return this.request<ListRealtimeEventsResponse>(url, { method: "GET", serviceIdentityToken });
  }

  private async request<T>(
    url: URL,
    opts: { method: string; serviceIdentityToken: string; headers?: Record<string, string>; body?: unknown }
  ): Promise<T> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.opts.timeoutMs);
    try {
      const res = await fetch(url, {
        method: opts.method,
        signal: controller.signal,
        headers: {
          Authorization: `Bearer ${opts.serviceIdentityToken}`,
          ...(opts.body !== undefined ? { "Content-Type": "application/json" } : {}),
          ...opts.headers,
        },
        body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      });

      if (res.ok) {
        return (await res.json()) as T;
      }

      if (res.status >= 500) {
        throw domainUnavailableError();
      }

      // 4xx: o Go já classificou o erro (common.yaml#/components/schemas/Error) — propagamos o code/message dele, não inventamos um novo.
      let body: GoErrorBody | undefined;
      try {
        body = (await res.json()) as GoErrorBody;
      } catch {
        throw domainUnavailableError();
      }
      throw new BffError(res.status, body.error.code, body.error.message);
    } catch (err) {
      if (err instanceof BffError) throw err;
      // Timeout (AbortError) ou falha de rede (ECONNREFUSED etc.) — o
      // mesmo tratamento: indisponibilidade do backend Go é sempre um
      // erro controlado, nunca uma requisição pendurada.
      throw domainUnavailableError();
    } finally {
      clearTimeout(timeout);
    }
  }
}
