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
type ListUsersResponse =
  InternalPaths["/v1/tenants/{tenant}/users"]["get"]["responses"]["200"]["content"]["application/json"];
type ResetUserPasswordResponse =
  InternalPaths["/v1/tenants/{tenant}/users/{id}/reset-password"]["post"]["responses"]["200"]["content"]["application/json"];
type ListUserTokensResponse =
  InternalPaths["/v1/tenants/{tenant}/users/{id}/tokens"]["get"]["responses"]["200"]["content"]["application/json"];
type StartImpersonationResponse =
  InternalPaths["/v1/tenants/{tenant}/users/{id}/impersonate"]["post"]["responses"]["201"]["content"]["application/json"];
type UpdateTablePermissionsResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/permissions"]["patch"]["responses"]["200"]["content"]["application/json"];
type SubmitViewResponse =
  InternalPaths["/v1/tenants/{tenant}/views/{id}/submit"]["post"]["responses"]["200"]["content"]["application/json"];
type UploadFileResponse =
  InternalPaths["/v1/tenants/{tenant}/files"]["post"]["responses"]["201"]["content"]["application/json"];
type ListWorkflowsResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows"]["get"]["responses"]["200"]["content"]["application/json"];
type CreateWorkflowResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows"]["post"]["responses"]["201"]["content"]["application/json"];
type GetWorkflowResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows/{id}"]["get"]["responses"]["200"]["content"]["application/json"];
type UpdateWorkflowResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows/{id}"]["patch"]["responses"]["200"]["content"]["application/json"];
type CreateWorkflowStepResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows/{id}/steps"]["post"]["responses"]["201"]["content"]["application/json"];
type UpdateWorkflowStepResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows/{id}/steps/{stepId}"]["patch"]["responses"]["200"]["content"]["application/json"];
type RunWorkflowResponse =
  InternalPaths["/v1/tenants/{tenant}/workflows/{id}/run"]["post"]["responses"]["200"]["content"]["application/json"];
type EmitEventResponse =
  InternalPaths["/v1/tenants/{tenant}/events/{eventname}"]["post"]["responses"]["200"]["content"]["application/json"];
type ManifestResponse =
  InternalPaths["/v1/tenants/{tenant}/manifest"]["get"]["responses"]["200"]["content"]["application/json"];
type ShareHandlerResponse =
  InternalPaths["/v1/tenants/{tenant}/share-handler"]["post"]["responses"]["200"]["content"]["application/json"];

export interface GoClientOptions {
  readonly baseUrl: string;
  readonly timeoutMs: number;
  /** Limite por processo; rejeita imediatamente, sem fila de espera. */
  readonly maxInFlight?: number;
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
  private inFlight = 0;
  private rejected = 0;
  private peak = 0;
  private readonly limit: number;

  constructor(private readonly opts: GoClientOptions) {
    this.limit = opts.maxInFlight ?? 64;
    if (!Number.isSafeInteger(this.limit) || this.limit < 1) throw new Error("maxInFlight inválido");
  }

  get concurrency() {
    return { active: this.inFlight, peak: this.peak, rejected: this.rejected, limit: this.limit };
  }

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

  // setActorLanguage (GO-047) — self-service, sempre sobre o PRÓPRIO
  // ator do serviceIdentityToken (nunca um userID em parâmetro).
  async setActorLanguage(serviceIdentityToken: string, tenant: string, language: string): Promise<GetActorResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/actor`);
    return this.request<GetActorResponse>(url, { method: "PATCH", serviceIdentityToken, body: { language } });
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

  // renderView (GO-020, estendido em GO-039) — só GET, sem
  // Idempotency-Key (não é uma mutação). Devolve o DTO de renderização
  // tal como o Go monta (List, Show ou Edit conforme o template da view)
  // — o BFF não reinterpreta nada, só repassa. `record` é obrigatório
  // para Show, opcional para Edit (ausente = registro novo), ignorado
  // por List.
  async renderView(
    serviceIdentityToken: string,
    tenant: string,
    id: number,
    query: { limit?: number; cursor?: string; record?: number } = {}
  ): Promise<RenderViewResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views/${id}/render`);
    if (query.limit !== undefined) url.searchParams.set("limit", String(query.limit));
    if (query.cursor !== undefined) url.searchParams.set("cursor", query.cursor);
    if (query.record !== undefined) url.searchParams.set("record", String(query.record));
    return this.request<RenderViewResponse>(url, { method: "GET", serviceIdentityToken });
  }

  // submitView (GO-039) — form_action: cria (recordId ausente/0) ou
  // atualiza (recordId presente, exige expectedVersion) um registro de
  // uma view Edit. Idempotência via Idempotency-Key, mesma convenção de
  // createRecord/createView.
  async submitView(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    id: number,
    input: { record_id?: number; _version?: string; values: Record<string, unknown> }
  ): Promise<SubmitViewResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views/${id}/submit`);
    return this.request<SubmitViewResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  // deleteViewRow (GO-039) — a ação de coluna "Delete" de uma view List.
  // Sem Idempotency-Key: DELETE já é idempotente por natureza aqui (uma
  // segunda chamada encontra o registro já removido e recebe 404, um
  // estado final consistente, não um efeito duplicado).
  async deleteViewRow(serviceIdentityToken: string, tenant: string, id: number, recordId: number, expectedVersion: string): Promise<void> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/views/${id}/rows/${recordId}`);
    url.searchParams.set("version", expectedVersion);
    await this.request<void>(url, { method: "DELETE", serviceIdentityToken });
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

  // Administração de usuário (GO-044) — a metade "listar/editar/remover"
  // de `auth/admin.ts` do legado. Todas exigem um ator admin do lado Go
  // (o BFF só transporta a identidade delegada, não decide autorização).
  async listUsers(serviceIdentityToken: string, tenant: string): Promise<ListUsersResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/users`);
    return this.request<ListUsersResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async updateUserRole(serviceIdentityToken: string, tenant: string, id: number, roleId: number): Promise<void> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/users/${id}`);
    await this.request<void>(url, { method: "PATCH", serviceIdentityToken, body: { role_id: roleId } });
  }

  async deleteUser(serviceIdentityToken: string, tenant: string, id: number): Promise<void> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/users/${id}`);
    await this.request<void>(url, { method: "DELETE", serviceIdentityToken });
  }

  // resetUserPassword (GO-044) — `password` omitido deixa o Go gerar uma
  // senha aleatória; o texto plano só existe nesta resposta, uma vez.
  async resetUserPassword(
    serviceIdentityToken: string,
    tenant: string,
    id: number,
    password?: string
  ): Promise<ResetUserPasswordResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/users/${id}/reset-password`);
    return this.request<ResetUserPasswordResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      body: password !== undefined ? { password } : undefined,
    });
  }

  async listUserTokens(serviceIdentityToken: string, tenant: string, id: number): Promise<ListUserTokensResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/users/${id}/tokens`);
    return this.request<ListUserTokensResponse>(url, { method: "GET", serviceIdentityToken });
  }

  // startImpersonation (GO-044) — serviceIdentityToken é sempre do
  // admin (o `sub` que o Go usa como ator/admin_user_id); o BFF guarda
  // `log_id` na sessão do usuário impersonado para poder encerrar depois.
  async startImpersonation(serviceIdentityToken: string, tenant: string, targetUserId: number): Promise<StartImpersonationResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/users/${targetUserId}/impersonate`);
    return this.request<StartImpersonationResponse>(url, { method: "POST", serviceIdentityToken });
  }

  // endImpersonation (GO-044) — sem checagem de papel do lado Go (ver
  // contrato); o serviceIdentityToken pode ser do admin ou do usuário
  // impersonado, o que importa é o tenant + log_id que o BFF já validou.
  async endImpersonation(serviceIdentityToken: string, tenant: string, logId: number): Promise<void> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/impersonations/${logId}/end`);
    await this.request<void>(url, { method: "POST", serviceIdentityToken });
  }

  // Workflows (GO-048) — CRUD da definição persistida + execução ponta a
  // ponta. Mesma disciplina de idempotência de createView/updateView:
  // toda mutação exige Idempotency-Key, calculada pelo chamador (o
  // handler de app.ts, mesmo padrão de computeIdempotencyKey).
  async listWorkflows(serviceIdentityToken: string, tenant: string): Promise<ListWorkflowsResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows`);
    return this.request<ListWorkflowsResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async createWorkflow(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    input: { name: string }
  ): Promise<CreateWorkflowResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows`);
    return this.request<CreateWorkflowResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async getWorkflow(serviceIdentityToken: string, tenant: string, id: number): Promise<GetWorkflowResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${id}`);
    return this.request<GetWorkflowResponse>(url, { method: "GET", serviceIdentityToken });
  }

  async updateWorkflow(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    id: number,
    input: { _version: string; name?: string; initial_step?: string }
  ): Promise<UpdateWorkflowResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${id}`);
    return this.request<UpdateWorkflowResponse>(url, {
      method: "PATCH",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async deleteWorkflow(serviceIdentityToken: string, tenant: string, id: number): Promise<void> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${id}`);
    await this.request<void>(url, { method: "DELETE", serviceIdentityToken });
  }

  async createWorkflowStep(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    workflowId: number,
    input: {
      name: string;
      action_name: string;
      configuration?: Record<string, unknown>;
      only_if?: string;
      next_step?: string;
      else_step?: string;
      error_step?: string;
      position_x?: number;
      position_y?: number;
    }
  ): Promise<CreateWorkflowStepResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${workflowId}/steps`);
    return this.request<CreateWorkflowStepResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async updateWorkflowStep(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    workflowId: number,
    stepId: number,
    input: {
      _version: string;
      action_name?: string;
      configuration?: Record<string, unknown>;
      only_if?: string;
      next_step?: string;
      else_step?: string;
      error_step?: string;
      position_x?: number;
      position_y?: number;
    }
  ): Promise<UpdateWorkflowStepResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${workflowId}/steps/${stepId}`);
    return this.request<UpdateWorkflowStepResponse>(url, {
      method: "PATCH",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async deleteWorkflowStep(serviceIdentityToken: string, tenant: string, workflowId: number, stepId: number): Promise<void> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${workflowId}/steps/${stepId}`);
    await this.request<void>(url, { method: "DELETE", serviceIdentityToken });
  }

  // runWorkflow (GO-048) — a chamada síncrona que compila+inicia+roda até
  // o fim, devolvendo o estado final. Idempotency-Key protege contra um
  // duplo-clique no botão "Executar" iniciar dois runs.
  async runWorkflow(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    id: number,
    input: { context?: Record<string, unknown> }
  ): Promise<RunWorkflowResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/workflows/${id}/run`);
    return this.request<RunWorkflowResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  // emitEvent (GO-052) — o mecanismo por trás de Trigger.emitEvent do
  // legado. Idempotency-Key protege contra um retry de rede disparando os
  // triggers duas vezes (mesmo espírito de runWorkflow acima).
  async emitEvent(
    serviceIdentityToken: string,
    idempotencyKey: string,
    tenant: string,
    eventName: string,
    input: { payload?: Record<string, unknown> }
  ): Promise<EmitEventResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/events/${encodeURIComponent(eventName)}`);
    return this.request<EmitEventResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  // getManifest (GO-053) — DELIBERADAMENTE sem serviceIdentityToken: o
  // endpoint Go correspondente não exige identidade delegada (um
  // manifesto PWA é buscado pelo navegador antes de qualquer login
  // existir, ver internal-api.yaml). Por isso não reaproveita
  // this.request (que sempre envia Authorization) — um fetch direto,
  // mesmo tratamento de indisponibilidade (5xx/timeout → erro
  // controlado).
  async getManifest(tenant: string): Promise<ManifestResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/manifest`);
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.opts.timeoutMs);
    try {
      const res = await fetch(url, { signal: controller.signal });
      if (!res.ok) {
        await res.body?.cancel();
        throw domainUnavailableError();
      }
      return (await res.json()) as ManifestResponse;
    } catch (err) {
      if (err instanceof BffError) throw err;
      throw domainUnavailableError();
    } finally {
      clearTimeout(timeout);
    }
  }

  // shareHandler (GO-053) — mesmo mecanismo de emitEvent, nome de evento
  // fixo no lado Go; a Idempotency-Key é sempre calculada pelo BFF (ver
  // nota de escopo em app.ts sobre o POST nativo do Web Share Target).
  async shareHandler(serviceIdentityToken: string, idempotencyKey: string, tenant: string, input: { title?: string; text?: string; url?: string }): Promise<ShareHandlerResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/share-handler`);
    return this.request<ShareHandlerResponse>(url, {
      method: "POST",
      serviceIdentityToken,
      headers: { "Idempotency-Key": idempotencyKey },
      body: input,
    });
  }

  async updateTablePermissions(
    serviceIdentityToken: string,
    tenant: string,
    table: string,
    input: { min_role_read: number; min_role_write: number }
  ): Promise<UpdateTablePermissionsResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/tables/${encodeURIComponent(table)}/permissions`);
    return this.request<UpdateTablePermissionsResponse>(url, { method: "PATCH", serviceIdentityToken, body: input });
  }

  // uploadFile (GO-051) — multipart/form-data, bytes REAIS (o mesmo
  // arquivo que o navegador enviou ao BFF, repassado sem reinterpretar).
  // Content-Type (com o boundary) é gerado automaticamente por
  // `fetch` a partir do FormData — nunca forçado aqui, ao contrário de
  // toda outra chamada JSON deste cliente.
  async uploadFile(
    serviceIdentityToken: string,
    tenant: string,
    filename: string,
    mimeType: string,
    content: Buffer
  ): Promise<UploadFileResponse> {
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/files`);
    const form = new FormData();
    form.append("file", new Blob([content], { type: mimeType }), filename);
    return this.request<UploadFileResponse>(url, { method: "POST", serviceIdentityToken, rawBody: form });
  }

  // downloadFile (GO-051) — bytes crus, nunca JSON; por isso não
  // reaproveita `request` (que sempre chama res.json() no caminho de
  // sucesso). Buffer inteiro em memória — simplificação deliberada,
  // suficiente para o piloto guitars (fotos, não arquivos grandes);
  // documentado como limite de escala, mesmo espírito de
  // editFieldOptionsLimit em internal/views.
  async downloadFile(serviceIdentityToken: string, tenant: string, id: number): Promise<{ contentType: string; body: Buffer }> {
    if (this.inFlight >= this.limit) {
      this.rejected++;
      throw domainUnavailableError();
    }
    this.inFlight++;
    this.peak = Math.max(this.peak, this.inFlight);
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.opts.timeoutMs);
    const url = new URL(`${this.opts.baseUrl}/v1/tenants/${encodeURIComponent(tenant)}/files/${id}`);
    try {
      const res = await fetch(url, {
        method: "GET",
        signal: controller.signal,
        headers: { Authorization: `Bearer ${serviceIdentityToken}` },
      });
      if (res.ok) {
        const contentType = res.headers.get("content-type") ?? "application/octet-stream";
        const body = Buffer.from(await res.arrayBuffer());
        return { contentType, body };
      }
      if (res.status >= 500) {
        await res.body?.cancel();
        throw domainUnavailableError();
      }
      let body: GoErrorBody | undefined;
      try {
        body = (await res.json()) as GoErrorBody;
      } catch {
        throw domainUnavailableError();
      }
      throw new BffError(res.status, body.error.code, body.error.message);
    } catch (err) {
      if (err instanceof BffError) throw err;
      throw domainUnavailableError();
    } finally {
      clearTimeout(timeout);
      this.inFlight--;
    }
  }

  private async request<T>(
    url: URL,
    opts: { method: string; serviceIdentityToken: string; headers?: Record<string, string>; body?: unknown; rawBody?: FormData }
  ): Promise<T> {
    if (this.inFlight >= this.limit) {
      this.rejected++;
      throw domainUnavailableError();
    }
    this.inFlight++;
    this.peak = Math.max(this.peak, this.inFlight);
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
        body: opts.rawBody ?? (opts.body !== undefined ? JSON.stringify(opts.body) : undefined),
      });

      if (res.ok) {
        // 204 (updateUser/deleteUser/endImpersonation, GO-044) não tem
        // corpo — res.json() lançaria SyntaxError num body vazio.
        if (res.status === 204) return undefined as T;
        return (await res.json()) as T;
      }

      if (res.status >= 500) {
        await res.body?.cancel();
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
      this.inFlight--;
    }
  }
}
