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
type ListViewsResponse = paths["/api/bff/views"]["get"]["responses"]["200"]["content"]["application/json"];
type GetViewResponse = paths["/api/bff/views/{id}"]["get"]["responses"]["200"]["content"]["application/json"];
type UpdateViewResponse = paths["/api/bff/views/{id}"]["patch"]["responses"]["200"]["content"]["application/json"];
type RenderViewResponse = paths["/api/bff/views/{id}/render"]["get"]["responses"]["200"]["content"]["application/json"];
type SubmitViewResponse = paths["/api/bff/views/{id}/submit"]["post"]["responses"]["200"]["content"]["application/json"];
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

/**
 * ViewUnsupportedError distingue o 422 `view_unsupported` (GO-020) de
 * qualquer outro erro — a mensagem já é o motivo específico que o Go
 * classificou (ex.: "template \"Show\" não suportado"), nunca uma frase
 * genérica de erro. É o sinal que a UI usa para "layouts incompatíveis...
 * seguem rota legada explícita" (critério de aceite): ao ver este erro,
 * em vez de tentar desenhar uma tabela, mostra o motivo e aponta para o
 * sistema atual.
 */
export class ViewUnsupportedError extends Error {
  constructor(reason: string) {
    super(reason);
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
    const url = this.buildUrl(`/api/bff/tables/${encodeURIComponent(table)}/records`);
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

  async listViews(table?: string): Promise<ListViewsResponse> {
    const url = this.buildUrl("/api/bff/views");
    if (table) url.searchParams.set("table", table);
    return this.request<ListViewsResponse>(url.pathname + url.search, { method: "GET" });
  }

  async getView(id: number): Promise<GetViewResponse> {
    return this.request<GetViewResponse>(`/api/bff/views/${id}`, { method: "GET" });
  }

  /**
   * renderView (GO-020) — o DTO de colunas/linhas/paginação que
   * ListView.tsx desenha. Lança ViewUnsupportedError especificamente em
   * 422 `view_unsupported`, para o chamador distinguir "esta view não é
   * suportada ainda" de qualquer outro erro (rede, 404, etc.).
   */
  async renderView(id: number, query: { limit?: number; cursor?: string; record?: number } = {}): Promise<RenderViewResponse> {
    const url = this.buildUrl(`/api/bff/views/${id}/render`);
    if (query.limit !== undefined) url.searchParams.set("limit", String(query.limit));
    if (query.cursor !== undefined) url.searchParams.set("cursor", query.cursor);
    if (query.record !== undefined) url.searchParams.set("record", String(query.record));
    try {
      return await this.request<RenderViewResponse>(url.pathname + url.search, { method: "GET" });
    } catch (err) {
      if (err instanceof BffClientError && err.status === 422 && err.code === "view_unsupported") {
        throw new ViewUnsupportedError(err.message);
      }
      throw err;
    }
  }

  /**
   * submitView (GO-039) — form_action de uma view Edit: cria
   * (record_id ausente) ou atualiza (record_id presente, exige
   * _version) um registro. Lança ViewConflictError/ViewUnsupportedError
   * pelos mesmos motivos de updateView.
   */
  async submitView(
    id: number,
    input: { record_id?: number; _version?: string; values: Record<string, unknown> }
  ): Promise<SubmitViewResponse> {
    try {
      return await this.request<SubmitViewResponse>(`/api/bff/views/${id}/submit`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": this.requireCsrf() },
        body: JSON.stringify(input),
      });
    } catch (err) {
      if (err instanceof BffClientError && err.status === 409 && err.code === "version_conflict") {
        throw new ViewConflictError();
      }
      if (err instanceof BffClientError && err.status === 422 && err.code === "view_unsupported") {
        throw new ViewUnsupportedError(err.message);
      }
      throw err;
    }
  }

  /**
   * deleteViewRow (GO-039) — a ação de coluna "Delete" de uma view List.
   */
  async deleteViewRow(id: number, recordId: number, expectedVersion: string): Promise<void> {
    const url = this.buildUrl(`/api/bff/views/${id}/rows/${recordId}`);
    url.searchParams.set("version", expectedVersion);
    try {
      await this.request<void>(url.pathname + url.search, {
        method: "DELETE",
        headers: { "X-CSRF-Token": this.requireCsrf() },
      });
    } catch (err) {
      if (err instanceof BffClientError && err.status === 409 && err.code === "version_conflict") {
        throw new ViewConflictError();
      }
      throw err;
    }
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
      if (err instanceof BffClientError && err.status === 422 && err.code === "view_unsupported") {
        throw new ViewUnsupportedError(err.message);
      }
      throw err;
    }
  }

  /**
   * Constrói uma URL para manipular query params (`searchParams.set`) —
   * `new URL(path)` sozinho LANÇA "Invalid URL" quando `path` é relativo
   * e não há base, exatamente o caso de `baseUrl` vazio (mesma origem via
   * proxy reverso — a topologia real de produção, ver App.tsx). Achado
   * pelo E2E de navegador real (GO-021): `listViews`/`renderView` (e o já
   * existente `listRecords`, de GO-017) quebravam nesse cenário — nenhum
   * teste anterior usava `baseUrl: ""` com um `URL` de verdade (jsdom/
   * vitest sempre passavam uma URL absoluta de teste). `window.location.
   * origin` como base é ignorado quando `baseUrl` já é absoluto (ex.:
   * testes), então este helper funciona nos dois casos.
   */
  private buildUrl(path: string): URL {
    return new URL(`${this.opts.baseUrl}${path}`, window.location.origin);
  }

  private requireCsrf(): string {
    if (!this.opts.csrfToken) {
      throw new BffClientError(0, "csrf_token_missing", "csrfToken não configurado no BffClient");
    }
    return this.opts.csrfToken;
  }

  private async request<T>(path: string, init: RequestInit): Promise<T> {
    const res = await fetch(`${this.opts.baseUrl}${path}`, { ...init, credentials: "include" });
    if (res.ok) {
      // 204 (deleteViewRow, GO-039) não tem corpo — res.json() lançaria
      // SyntaxError num body vazio; nenhum método anterior retornava 204.
      if (res.status === 204) return undefined as T;
      return (await res.json()) as T;
    }
    const body = (await res.json().catch(() => null)) as ErrorResponse | null;
    throw new BffClientError(res.status, body?.error.code ?? "unknown_error", body?.error.message ?? res.statusText);
  }
}
