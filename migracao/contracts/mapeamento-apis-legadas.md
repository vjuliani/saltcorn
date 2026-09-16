# Mapeamento de APIs legadas → contratos novos

Relaciona-se à [matriz de capacidades GO-001](../../docs/migracao-go/inventario/GO-001-matriz-capacidades.md#26-infraestrutura-http-segurança-e-cli) (rotas `packages/server/routes/api.ts` e `scapi.ts`) e aos contratos em [openapi/](openapi/). Este mapeamento cobre o recorte representativo já modelado (registros dinâmicos); não é exaustivo — a superfície completa do domínio (`tables.ts`, `viewedit.ts`, `actions.ts`, etc., GO-001 §2.6) ainda não tem contrato porque a lógica correspondente ainda não existe em Go.

## API pública de registros (`packages/server/routes/api.ts`)

| Rota legada (Node) | Autenticação legada | Contrato novo (BFF → Go) | Diferenças / lacunas |
| --- | --- | --- | --- |
| `GET /api/:tableName/` | Sessão ou Bearer token (`api-bearer`), `min_role` checado no handler | `GET /v1/tenants/{tenant}/tables/{table}/records` | Tenant explícito na URL (resolvido pelo BFF), não implícito por subdomínio; paginação por cursor em vez de paginação por página/offset — não confirmado como a legada usa hoje, tratar como mudança de contrato, não equivalência 1:1 |
| `GET /api/:tableName/count` | idem | **Não modelado ainda** | Contagem/agregação fica para quando `GO-015` (autorização em agregações) existir — contagem sem o mesmo cuidado de autorização que leitura de linha é risco já sinalizado em GO-001 §2.1 |
| `GET /api/:tableName/distinct/:fieldName` | idem | **Não modelado ainda** | idem |
| `POST /api/:tableName/` | idem | `POST /v1/tenants/{tenant}/tables/{table}/records` | Legado não tem `Idempotency-Key` — comportamento de retry duplicado do legado é um risco conhecido que o contrato novo resolve explicitamente, não uma paridade a preservar |
| `POST /api/:tableName/:id` (atualização) | idem | `PATCH /v1/tenants/{tenant}/tables/{table}/records/{id}` | Legado usa `POST` para update; contrato novo usa `PATCH` com semântica JSON Merge Patch explícita (RFC 7396) — mudança deliberada, não um mapeamento direto de verbo |
| `DELETE /api/:tableName/:id` | idem | `DELETE /v1/tenants/{tenant}/tables/{table}/records/{id}` | Equivalente direto |
| `POST /api/emit-event/:eventname` | idem | **Não modelado ainda** | Depende de GO-024 (triggers/automação) existir do lado Go |
| `ALL /api/action/:actionname/` | idem | **Não modelado ainda** | Depende de GO-022/GO-023 (plugins/expressões) — ação de plugin pode não ser portável sem o host temporário (ADR-0005) |

## API de introspecção (`packages/server/routes/scapi.ts`)

| Rota legada | Contrato novo | Observação |
| --- | --- | --- |
| `GET /scapi/sc_tables/`, `/sc_views/`, `/sc_roles/`, `/sc_tenants/` | **Não modelado ainda** | Ferramenta interna/Saltcorn Cloud (GO-001 §2.6) — baixa prioridade até haver um consumidor real do lado Go |
| `POST /scapi/reload` | **Não modelado ainda** | Sem equivalente de "reload" no backend Go ainda (não há estado de plugin em runtime para recarregar nesta fundação) |

## Autenticação: legado vs. contrato novo

| Aspecto | Legado (Node) | Contrato novo |
| --- | --- | --- |
| Sessão de navegador | Cookie `connect.sid`, validado no mesmo processo que o domínio | Cookie validado só pelo BFF (`bff-api.yaml`); nunca chega ao Go |
| Token de API externo | Bearer token de longa duração (`_sc_api_tokens`), token inválido cai para papel anônimo (100) em vez de rejeitar | Fora de escopo desta rodada de contrato — a API pública externa (distinta da interna BFF→Go) precisa de seu próprio contrato quando existir um consumidor real |
| Identidade entre serviços | Não existe (BFF e domínio são o mesmo processo hoje) | JWT de vida curta, claims `{sub, tenant, iat, exp}` — ver `fixtures/identidade-delegada.md` |

## Lacunas explícitas (não resolvidas nesta tarefa)

- Contagem/agregação (`/count`, `/distinct`) sem contrato — autorização em agregações é risco already sinalizado (GO-001 §2.1, GO-015).
- Emissão de evento e execução de ação de plugin sem contrato — dependem de GO-022/GO-023/GO-024.
- API pública externa (tokens de longa duração, papel anônimo por token inválido) não tem contrato próprio ainda — comportamento do token inválido caindo para papel anônimo em vez de rejeitar é um risco a revisar quando esse contrato existir, não uma decisão tomada aqui.
- Introspecção (`scapi.ts`) sem contrato — baixa prioridade.
