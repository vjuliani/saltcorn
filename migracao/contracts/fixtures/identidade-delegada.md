# Fixtures — identidade delegada ator+tenant (BFF → Go)

Referenciado por `openapi/internal-api.yaml` (`securitySchemes.ServiceIdentity`) e por `gen/go/example/client_example_test.go` (as mesmas três situações, como teste executável). Relaciona-se a [ADR-0003](../../docs/migracao-go/adr/0003-bff-nodejs-permanente.md) e ao critério de aceite de GO-006 "autenticação entre serviços e delegação de ator/tenant têm exemplos positivos e negativos".

Mecanismo (definido aqui, implementado em GO-008/GO-009): o BFF assina um JWT de vida curta com claims `{sub, tenant, iat, exp}` e o envia como `Authorization: Bearer <token>` em toda chamada à API interna. O backend Go valida, nesta ordem: (1) assinatura, (2) expiração, (3) que `tenant` no token bate com `{tenant}` da URL. Falhar em (1) ou (2) → 401; falhar em (3) → 403. Nenhum desses três passos é opcional nem pode ser pulado por conveniência.

## 1. Positivo — identidade delegada válida

Claims do JWT (antes de assinar):

```json
{
  "sub": "42",
  "tenant": "acme",
  "iat": 1768435200,
  "exp": 1768435260
}
```

Requisição:

```http
GET /v1/tenants/acme/tables/guitars/records HTTP/1.1
Authorization: Bearer <jwt assinado com as claims acima>
```

Resposta:

```http
HTTP/1.1 200 OK
Content-Type: application/json

{"items":[{"id":1,"version":1,"created_at":"2026-01-15T00:00:00Z"}],"next_cursor":null}
```

Teste executável equivalente: `TestListFirstPage_Success` em `gen/go/example/client_example_test.go`.

## 2. Negativo — assinatura inválida (token forjado)

O token abaixo tem as claims corretas, mas foi assinado com uma chave diferente da que o backend Go espera (ou teve o payload alterado após a assinatura) — simula um ator tentando forjar identidade sem ter a chave do BFF.

```http
GET /v1/tenants/acme/tables/guitars/records HTTP/1.1
Authorization: Bearer <jwt com assinatura inválida>
```

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{"error":{"code":"invalid_identity_token","message":"assinatura do token de identidade delegada não confere"}}
```

Teste executável equivalente: `TestListFirstPage_Unauthorized`.

## 3. Negativo — tenant divergente (token válido, recurso de outro tenant)

O token é legítimo (assinatura e expiração corretas) para o tenant `beta`, mas a requisição pede um recurso do tenant `acme`. Isso é exatamente o cenário que a matriz de capacidades GO-001 (§2.1) chama de "identidade não pode ser forjada por headers" e que a defesa contra tenant-drift do Node legado já trata hoje — o contrato Go precisa do mesmo nível de rigor.

Claims do JWT:

```json
{
  "sub": "42",
  "tenant": "beta",
  "iat": 1768435200,
  "exp": 1768435260
}
```

Requisição (nota: pede o tenant `acme`, mas o token é do tenant `beta`):

```http
GET /v1/tenants/acme/tables/guitars/records HTTP/1.1
Authorization: Bearer <jwt válido, claim tenant=beta>
```

```http
HTTP/1.1 403 Forbidden
Content-Type: application/json

{"error":{"code":"tenant_mismatch","message":"tenant do token de identidade não corresponde ao tenant do recurso"}}
```

Teste executável equivalente: `TestListFirstPage_TenantMismatch`.

## Limitação registrada

Estes três casos cobrem a fronteira de identidade delegada. Não cobrem (ficam para GO-008/GO-009, quando a validação real existir): rotação de chave de assinatura, revogação de token antes da expiração, nem o caso de relógio dessincronizado entre BFF e Go afetando `iat`/`exp`.
