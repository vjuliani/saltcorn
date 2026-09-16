# ADR-0007 — Sessão e cookies do BFF

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [ADR-0003 (BFF Node.js permanente)](0003-bff-nodejs-permanente.md), [ADR-0006 (retirada do legado)](0006-retirada-do-legado.md), [GO-008](../TASKS.md#go-008--migrar-identidade-e-autorização)

## Contexto

ADR-0003 já decidiu QUE o BFF Node.js possui sessão/cookies (não o backend Go) e que "armazenamento de sessão, quando necessário, tem acesso e ciclo de vida próprios" (README §2). O que faltava era definir COMO — nome do cookie, atributos, onde a sessão é armazenada e quando é invalidada. Isso é uma decisão a **registrar agora**, não a **implementar agora**: o BFF Node.js ainda não existe como código (`migracao/packages/bff/`, GO-017) — só o backend Go (que nunca vê cookie de sessão, ADR-0003) existe até aqui. Por isso este ADR fixa o contrato que GO-017 implementa, sem escrever nenhum código de sessão em Go.

GO-001 (§2.1) documenta o mecanismo atual do Node legado como referência de comportamento a preservar (não a arquitetura a copiar): `connect-pg-simple`/`connect-sqlite3`, cookie de 30 dias, `sameSite` configurável (padrão "None"), `secure: "auto"`, e identidade inteira serializada na sessão (sem `serializeUser`/`deserializeUser` reais) — esse último ponto é explicitamente **não** replicado aqui (ver Decisão).

## Decisão

1. **Nome e atributos do cookie:** `sc_session`, `HttpOnly`, `Secure` (sempre — não condicional como o `"auto"` do legado, que dependia de detectar HTTPS em runtime), `SameSite=Lax` por padrão; `SameSite=None` apenas para o caso documentado de app móvel cross-origin (ADR-0003 já reconhece esse caso), nunca como padrão geral. `Max-Age` de 24 horas com renovação deslizante em atividade — mais curto que os 30 dias do legado, porque a sessão agora só carrega a chave de troca por um `ServiceIdentity` de vida curta (ver item 3), não é mais a única barreira de acesso ao domínio.
2. **O que a sessão armazena:** apenas `{user_id, tenant}` — nunca o objeto de usuário inteiro nem o papel/role_id. O BFF busca papel/permissões atuais no backend Go a cada requisição que precisar (via `ServiceIdentity`), em vez de confiar num valor potencialmente desatualizado gravado na sessão. Isso corrige explicitamente o padrão do legado (matriz GO-001 §2.1: "a entidade inteira do usuário, inclusive `role_id`... é armazenada verbatim na sessão") — uma mudança de papel ou revogação de acesso só deve valer a partir da checagem no Go, nunca continuar válida porque a sessão antiga ainda tem o papel velho.
3. **Sessão → identidade delegada:** o BFF troca a sessão por um token `ServiceIdentity` (JWT HS256, claims `{sub, tenant, iat, exp}`, GO-008) a cada chamada ao backend Go — o cookie de sessão em si nunca é enviado ao Go (ADR-0003, reafirmado). O segredo compartilhado (`SALTCORN_GO_SERVICE_IDENTITY_SECRET`) é gerido como segredo de infraestrutura (variável de ambiente/gerenciador de segredos), nunca commitado nem logado.
4. **Armazenamento da sessão:** um store dedicado do BFF (ex.: Redis, ou Postgres separado do banco de domínio) — nunca o banco de domínio que `internal/platform/database` acessa (ADR-0003: "o BFF permanente não acessa o banco de domínio"). A escolha exata do backend de armazenamento (Redis vs. Postgres de sessão) fica para GO-017, quando o BFF for implementado — este ADR fixa a restrição ("não é o banco de domínio"), não a tecnologia.
5. **Invalidação:** logout apaga a sessão do store imediatamente (não espera o `Max-Age`). Revogação de token de API (GO-008, `RevokeAPIToken`) é independente da sessão de navegador — revogar um token de API não desloga a sessão do BFF, e vice-versa; são duas credenciais diferentes com ciclos de vida próprios.
6. **CSRF:** continua exigido em toda mutação autenticada por sessão (ADR-0003, contrato `bff-api.yaml`, header `X-CSRF-Token`) — este ADR não muda esse mecanismo, só o cookie de sessão em si.

## Escopo desta decisão

**Dentro:** formato e atributos do cookie, o que a sessão carrega, como ela vira `ServiceIdentity`, e onde não pode ser armazenada.

**Fora:** implementação real (GO-017, quando `migracao/packages/bff/` existir), escolha de biblioteca de sessão Node específica, e MFA no fluxo de login do BFF (o desafio de TOTP em si é validado pelo backend Go via `identity.ValidateTOTPCode`, GO-008; o fluxo de UI que pede o código ao usuário é GO-017/GO-019).

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| Manter o padrão do legado (objeto de usuário inteiro na sessão, 30 dias) | Perpetua o problema de revogação/mudança de papel não valer até a sessão expirar — o oposto do que a separação BFF/Go deveria ganhar |
| Sessão de vida longa sem troca por `ServiceIdentity` de vida curta | O Go teria que confiar numa sessão de navegador diretamente ou aceitar tokens de vida igualmente longa — contradiz a decisão de token de vida curta já implícita no contrato GO-006 (`exp` obrigatório, GO-008 rejeita token sem `exp`) |
| Armazenar sessão no mesmo Postgres do domínio | Contradiz ADR-0003 explicitamente ("o BFF permanente não acessa o banco de domínio") — mesmo que fosse tecnicamente o mesmo servidor Postgres, teria que ser um banco/schema logicamente separado, não compartilhado com `internal/platform/database` |

## Consequências

- GO-017 implementa exatamente este contrato — qualquer desvio (ex.: guardar papel na sessão por conveniência) deve voltar a este ADR antes de ser feito, não decidido ad-hoc na hora da implementação.
- `SALTCORN_GO_SERVICE_IDENTITY_SECRET` precisa existir tanto no ambiente do backend Go (`internal/platform/tenancy.Verifier`, GO-008) quanto no do BFF (para assinar) — é um segredo compartilhado entre dois processos/times de deploy, não um segredo interno de um único serviço; a rotação desse segredo exige coordenação entre os dois lados (fora de escopo aqui — vira operação quando o BFF existir).
