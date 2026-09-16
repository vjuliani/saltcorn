// Prova de que os tipos gerados são consumíveis por um cliente real, não só
// que o .d.ts compila isoladamente. Não é o BFF de verdade (isso é GO-017) —
// é a evidência de validação de GO-006: "clientes Go/TS validam ambos os
// contratos". As funções abaixo tipam corretamente parâmetros de rota,
// corpo de requisição e corpo de resposta a partir do OpenAPI, incluindo o
// caso de paginação (cursor) e o de erro (union de status codes).
import type { paths as InternalPaths } from "./internal-api.js";
import type { paths as BffPaths } from "./bff-api.js";

type ListRecordsResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/records"]["get"]["responses"]["200"]["content"]["application/json"];

type CreateRecordBody =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/records"]["post"]["requestBody"]["content"]["application/json"];

type CreateRecordResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/records"]["post"]["responses"]["201"]["content"]["application/json"];

type ErrorResponse =
  InternalPaths["/v1/tenants/{tenant}/tables/{table}/records"]["post"]["responses"]["401"]["content"]["application/json"];

/**
 * Cliente mínimo para a API interna (BFF -> Go), só para exercitar os tipos
 * gerados em uma assinatura de função realista.
 */
export async function listRecords(
  baseUrl: string,
  serviceIdentityToken: string,
  tenant: string,
  table: string,
  cursor?: string
): Promise<ListRecordsResponse> {
  const url = new URL(`${baseUrl}/v1/tenants/${tenant}/tables/${table}/records`);
  if (cursor) url.searchParams.set("cursor", cursor);

  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${serviceIdentityToken}` },
  });
  if (!res.ok) {
    const body = (await res.json()) as ErrorResponse;
    throw new Error(`listRecords falhou: ${body.error.code} — ${body.error.message}`);
  }
  return (await res.json()) as ListRecordsResponse;
}

export async function createRecord(
  baseUrl: string,
  serviceIdentityToken: string,
  idempotencyKey: string,
  tenant: string,
  table: string,
  input: CreateRecordBody
): Promise<CreateRecordResponse> {
  const res = await fetch(`${baseUrl}/v1/tenants/${tenant}/tables/${table}/records`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${serviceIdentityToken}`,
      "Idempotency-Key": idempotencyKey,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const body = (await res.json()) as ErrorResponse;
    throw new Error(`createRecord falhou: ${body.error.code} — ${body.error.message}`);
  }
  return (await res.json()) as CreateRecordResponse;
}

// O mesmo padrão vale para o contrato BFF -> React; um exemplo basta para
// confirmar que o segundo arquivo gerado também é consumível da mesma forma.
type BffBootstrapResponse =
  BffPaths["/api/bff/bootstrap"]["get"]["responses"]["200"]["content"]["application/json"];

export async function getBootstrap(baseUrl: string): Promise<BffBootstrapResponse> {
  const res = await fetch(`${baseUrl}/api/bff/bootstrap`, { credentials: "include" });
  return (await res.json()) as BffBootstrapResponse;
}
