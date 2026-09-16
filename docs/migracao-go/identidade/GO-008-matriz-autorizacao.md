# GO-008 — Matriz de autorização positiva/negativa

Critério de aceite de GO-008: "Matriz positiva/negativa cobre APIs, arquivos, sessão, revogação e acesso cruzado; estratégias de plugins sem suporte bloqueiam o corte." Cada linha abaixo aponta para um teste real que a exercita (`go test ./...`) ou, quando o subsistema correspondente ainda não existe no backend Go, para a decisão/ADR que documenta explicitamente a lacuna — nenhuma linha é fabricada sem evidência ou referência.

## APIs (autenticação via senha e via token)

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Positivo — senha correta | Autentica, retorna o `User` | `TestAuthenticate_PositiveAndNegative` (`internal/identity/store_test.go`) |
| Negativo — senha errada | `ErrInvalidCredentials` (mesmo erro do caso "usuário inexistente" — evita enumeração de contas) | `TestAuthenticate_PositiveAndNegative` |
| Negativo — e-mail inexistente | `ErrInvalidCredentials` (idêntico ao de senha errada) | `TestAuthenticate_PositiveAndNegative` |
| Positivo — token de API válido | Autentica o usuário dono do token | `TestAPIToken_PositiveAndNegativeAndRevocation` |
| Negativo — token nunca existiu | `ErrTokenNotFoundOrRevoked` | `TestAPIToken_PositiveAndNegativeAndRevocation` |
| Negativo — identidade delegada (`ServiceIdentity`) com assinatura forjada | HTTP 401, distinto de token malformado | `TestParseDelegatedIdentity_WrongSignature` (`internal/platform/tenancy/identity_test.go`), `TestMiddleware_Negative_ForgedSignature` (E2E via `httptest`) |
| Negativo — algoritmo `"none"` (ataque de confusão de algoritmo) | Rejeitado — `jwt.WithValidMethods([]string{"HS256"})` | `TestParseDelegatedIdentity_AlgNoneRejected` |
| Negativo — token expirado | HTTP 401 | `TestMiddleware_ExpiredToken` |
| Negativo — tenant do token não bate com o tenant da URL | HTTP 403 | `TestMiddleware_Negative_TenantMismatch` |
| Positivo — identidade delegada válida ponta a ponta | HTTP 200, tenant/ator corretos no contexto do handler | `TestMiddleware_Positive`; confirmado manualmente também via `curl` contra `cmd/server` real (ver Checkpoint em `execucoes/GO-008.md`) |

## Revogação

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Token revogado deixa de autenticar | `ErrTokenNotFoundOrRevoked` | `TestAPIToken_PositiveAndNegativeAndRevocation` |
| Revogar um token já revogado | Idempotente, não é erro | `TestAPIToken_PositiveAndNegativeAndRevocation` |
| Revogação de token de API não afeta sessão de navegador (e vice-versa) | São credenciais independentes | Decisão registrada em [ADR-0007](../adr/0007-sessao-cookies-bff.md) item 5 — sem código de sessão a testar ainda (BFF é GO-017) |

## Acesso cruzado

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Token de API do tenant A usado no schema do tenant B | `ErrTokenNotFoundOrRevoked` (tabelas de tenants distintos são fisicamente separadas, GO-007) | `TestAPIToken_CrossTenantIsolation` |
| Usuário comum tenta ler linha de outro usuário no mesmo tenant (RLS por ownership) | 0 linhas visíveis da linha alheia | `TestWithTenantAndActor_RLSDeniesCrossUserAccess` (`internal/platform/database/rls_test.go`) — roda contra Postgres real com role `NOSUPERUSER NOBYPASSRLS`, já que superusuário sempre ignora RLS |
| Papel admin (role_id baixo) enxerga linhas de todos os usuários via política elevada | Todas as linhas visíveis | `TestWithTenantAndActor_AdminRoleBypassesOwnershipRLS` |
| 60 goroutines concorrentes (30 iterações × 2 usuários) alternando ator via `WithTenantAndActor` | Nenhum vazamento cruzado sob concorrência | `TestWithTenantAndActor_ConcurrentUsersNoLeak` |
| Verificação de que a checagem de RLS realmente falha se o GUC de ator não for setado | Ao substituir a chamada de `set_config('app.current_user_id', ...)` por um no-op, o teste de RLS passa a **falhar fechado** (usuário vê 0 linhas, não vaza) | Executado manualmente durante a sessão (regressão deliberada, restaurada em seguida) — ver Erros e correções (d) no histórico da tarefa; não fica como teste permanente porque exigiria sabotar código de produção dentro do próprio pacote |

## Sessão (BFF)

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Cookie de sessão, atributos, invalidação | Definido (nome `sc_session`, `HttpOnly`+`Secure`+`SameSite=Lax`, 24h deslizante, invalidação imediata em logout) | [ADR-0007](../adr/0007-sessao-cookies-bff.md) — decisão registrada, sem implementação de código ainda: o BFF Node.js só existe a partir de GO-017 (ADR-0003 já havia decidido que sessão vive no BFF, nunca no backend Go) |
| Sessão nunca chega ao backend Go | O backend Go só recebe `ServiceIdentity` (JWT de vida curta), nunca o cookie | ADR-0003 (reafirmado no ADR-0007 item 3); reforçado tecnicamente por `tenancy.Middleware` só aceitar `Authorization: Bearer <jwt>`, nunca ler cookies (`internal/platform/tenancy/middleware.go`) |

## Arquivos

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Autorização de leitura/escrita de arquivo por papel/ownership | **Deferido explicitamente** — nenhum armazenamento de arquivo existe no backend Go ainda (GO-026 não existe no backlog atual) | Nota de escopo em `execucoes/GO-008.md`; quando GO-026 for criada, deve reusar `identity.CanRead`/`CanWrite`/`IsOwnerByField` desta tarefa em vez de reinventar um modelo próprio — este é o compromisso registrado aqui, não uma matriz fabricada sobre código inexistente |

## Estratégias de autenticação não suportadas (bloqueio de corte)

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Estratégia nativa (`password`, `api_token`, `totp`) | Aceita | `TestRequireNativeStrategy_*` (`internal/identity/strategy_test.go`) |
| Estratégia de plugin de terceiro (ex.: OAuth social) ou qualquer valor desconhecido | `ErrUnsupportedAuthStrategy` — erro explícito e classificado, nunca sucesso silencioso nem pânico | `internal/identity/strategy.go` (`RequireNativeStrategy`) + `internal/identity/strategy_test.go`; referência de decisão em ADR-0005/ADR-0006 |

## Ownership por fórmula JS (bloqueio de corte, não desta tarefa)

| Caso | Esperado | Evidência |
| --- | --- | --- |
| Fórmula de ownership em JavaScript | Continua **não suportada** — `ErrOwnershipFormulaUnsupported`, decisão herdada de GO-004/ADR-0005, não resolvida aqui | `internal/identity/ownership.go` (`ErrOwnershipFormulaUnsupported`); só ownership por campo (`IsOwnerByField`) é implementado nativamente nesta tarefa |
