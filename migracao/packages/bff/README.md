# saltcorn-go-bff (GO-017)

BFF web em Node.js/TypeScript ([ADR-0003](../../../docs/migracao-go/adr/0003-bff-nodejs-permanente.md)): processo **permanente e separado** do backend Go, consumindo a API interna versionada (`internal-api.yaml`, GO-006) por HTTP/JSON. Compõe bootstrap, metadados e dados para o frontend React (ainda não implementado — GO-018); cuida de sessão/cookies e CSRF; **nunca acessa banco de domínio nem concentra regras de negócio** — todo comando/query revalida autorização no backend Go.

## Dependências mínimas, deliberadamente

Só `jsonwebtoken` como dependência de produção — mesmo espírito de `migracao/backend` (pgx/jwt/otp pinados, nada supérfluo) e de [ADR-0009](../../../docs/migracao-go/adr/0009-observabilidade-sem-sdk-externo.md) (preferir a biblioteca padrão quando a superfície é pequena e bem entendida):

- **Sem framework web** (Express etc.): 5 rotas, um roteador de ~50 linhas (`src/router.ts`) sobre `node:http` é mais simples de auditar do que integrar e manter atualizado um framework inteiro.
- **Sem biblioteca de sessão/CSRF**: os mecanismos (`src/session.ts`, `src/csrf.ts`) são pequenos e bem entendidos (cookie opaco + store, duplo-envio) — implementados à mão com `node:crypto` (`randomBytes`, `timingSafeEqual`), mesmo cuidado de `identity.VerifyAPIToken` no Go.
- **`jsonwebtoken` é a exceção deliberada**: verificação de JWT tem histórico conhecido de bugs de segurança (confusão de algoritmo, etc.) — o próprio backend Go usa uma biblioteca madura para o mesmo motivo (`golang-jwt/jwt/v5`, GO-008), não HMAC hand-rolled.
- **Testes com `node:test`** (nativo do Node 22) — sem Jest/Vitest/Supertest.

## Estrutura

```
src/
  config.ts          configuração por variável de ambiente (SALTCORN_BFF_*)
  session.ts         sessão de navegador — contrato exato de ADR-0007 (cookie sc_session,
                     SessionStore/InMemorySessionStore, {user_id, tenant} apenas)
  csrf.ts            CSRF de duplo-envio (cookie sc_csrf + header X-CSRF-Token)
  serviceIdentity.ts assina o JWT de identidade delegada (ADR-0003) que o backend Go verifica
  idempotency.ts     Idempotency-Key determinística (hash de ator+tenant+tabela+corpo) —
                     um retry do navegador reaproveita a mesma chave (bff-api.yaml)
  goClient.ts        cliente HTTP tipado para internal-api.yaml (tipos de
                     ../../contracts/gen/ts), timeout via AbortController
  errors.ts          formato de erro do contrato (common.yaml#/Error) + BffError
  router.ts          roteador mínimo (GET/POST/PATCH, sem dependência externa)
  httpHelpers.ts      leitura de corpo JSON com limite, envio de resposta
  app.ts             composição das rotas de bff-api.yaml
  server.ts          entrypoint: configuração, servidor HTTP, encerramento gracioso
test/
  *.test.ts          unitários (sessão, CSRF, idempotência, ServiceIdentity)
  mockGoServer.ts    réplica mínima da verificação de identidade delegada do Go real,
                     também replica idempotência/conflito de versão de outbox.Do
                     (createRecord/createView/updateView), só para os testes de
                     integração exercitarem a fronteira de verdade
  app.test.ts        integração: app real + servidor HTTP real contra o mock do Go
  editor.test.ts     integração do ciclo do editor (GO-019): criar tabela/campo/view,
                     salvar, reabrir, conflito de edição concorrente propagado (409)
```

## Rotas (`bff-api.yaml`)

- `GET /healthz`, `GET /readyz` — liveness/readiness, mesmo espírito de `cmd/server`/`cmd/worker` no Go.
- `GET /api/bff/bootstrap` — exige sessão; resolve o papel atual do ator no Go (`GET /v1/tenants/{tenant}/actor`, extensão de GO-017 a `internal-api.yaml` — ver nota de escopo em `docs/migracao-go/execucoes/GO-017.md`) a cada chamada, nunca cacheado (ADR-0007).
- `GET /api/bff/tables/:table/records` — exige sessão; delega a `internal/records.Rows` do lado Go.
- `POST /api/bff/tables/:table/records` — exige sessão + CSRF; gera a `Idempotency-Key` deterministicamente e delega a `internal/records.CreateRecord` via `outbox.Do` (GO-014) do lado Go.
- `POST /api/bff/tables` — exige sessão + CSRF; delega a `metadata.CreateTable` (GO-019). **Sem `Idempotency-Key`** — `CreateTable` já é idempotente por definição do lado Go (mesmo raciocínio de `addField` abaixo).
- `POST /api/bff/tables/:table/fields` — exige sessão + CSRF; delega a `metadata.AddField` (GO-019). Sem `Idempotency-Key`, mesmo motivo.
- `POST /api/bff/views` — exige sessão + CSRF; gera `Idempotency-Key` (reaproveitando `computeIdempotencyKey`, com `"views"` como escopo de recurso — string genérica, não um nome de tabela literal) e delega a `views.CreateView` via `outbox.Do` do lado Go (GO-019) — `CreateView` NÃO é idempotente por natureza (nome único), diferente de `createTable`/`addField` acima.
- `GET /api/bff/views/:id` — exige sessão; delega a `views.GetView`.
- `PATCH /api/bff/views/:id` — exige sessão + CSRF; gera `Idempotency-Key` com escopo `"views/<id>"`; delega a `views.UpdateView` via `outbox.Do`. É "salvar" (com `configuration`) e "publicar" (com `min_role` menor) — a mesma rota. Propaga o 409 `version_conflict` do Go sem mascarar — ver `test/editor.test.ts`, `conflito de edição concorrente...`. Desde GO-020, também propaga 422 `view_unsupported` (publicar um layout incompatível), sem mascarar.
- `GET /api/bff/views` (GO-020) — exige sessão; delega a `views.ListViews` (Go), filtro opcional `?table=`. A página administrativa "Views" do frontend usa esta rota para enumerar o que existe.
- `GET /api/bff/views/:id/render` (GO-020) — exige sessão, sem CSRF (é leitura); delega a `views.CompileListPlan` (Go) — devolve o DTO de renderização (colunas + linhas + paginação), nunca HTML. Propaga 422 `view_unsupported` quando a view usa um recurso fora do subconjunto suportado (ver README do backend, seção "Runtime de renderização de views").

**Sem endpoint de login nesta entrega** (nem `bff-api.yaml` nem `internal-api.yaml` definem um) — o mecanismo de sessão existe como módulo testável (`SessionStore`), mas criar uma sessão de verdade (usuário+senha) é UI/fluxo de uma tarefa futura. Os testes seedam uma sessão diretamente no store.

## Build, testes e execução

A partir deste diretório:

```bash
npm install
npm run typecheck   # tsc --noEmit
npm test            # build (tsc) + node:test contra dist/ — sem Postgres real necessário (mock do Go)
npm run build       # tsc -p tsconfig.json → dist/
npm run dev         # roda src/server.ts direto via type-stripping nativo do Node (sem build)
```

`npm test` compila antes de rodar (`pretest`): o type-stripping nativo do Node (`--experimental-strip-types`) não remapeia extensões `.js`→`.ts` exigidas pela resolução de módulo `NodeNext` (a mesma convenção usada por `migracao/contracts`), então os testes rodam contra `dist/` compilado, não os `.ts` diretamente.

### Variáveis de ambiente

| Variável | Padrão | Descrição |
| --- | --- | --- |
| `SALTCORN_BFF_HTTP_ADDR` | `:3100` | Endereço em que o BFF escuta |
| `SALTCORN_BFF_GO_INTERNAL_API_URL` | `http://localhost:8090` | URL base do backend Go (`cmd/server`) |
| `SALTCORN_BFF_SERVICE_IDENTITY_SECRET` | (obrigatório, ≥32 bytes) | Segredo HMAC compartilhado com `SALTCORN_GO_SERVICE_IDENTITY_SECRET` do backend Go — o BFF não sobe sem ele |
| `SALTCORN_BFF_SERVICE_IDENTITY_TTL_SECONDS` | `30` | Vida útil do JWT de identidade delegada assinado pelo BFF |
| `SALTCORN_BFF_GO_REQUEST_TIMEOUT_MS` | `5000` | Timeout de toda chamada ao backend Go |
| `SALTCORN_BFF_SHUTDOWN_TIMEOUT_MS` | `15000` | Tempo máximo esperando requisições em curso antes de forçar a saída |

## Smoke test manual de três camadas (Postgres real + `cmd/server` real + BFF real)

Documentado como evidência em `docs/migracao-go/execucoes/GO-017.md` (mesmo padrão do smoke test manual de `cmd/worker` em GO-014) — não commitado como script permanente. Roteiro resumido para reproduzir:

1. Subir um Postgres isolado e descartável (ver `migracao/backend/README.md`).
2. Registrar o ownership de `tables.records` para `go` no tenant de teste, criar um usuário e uma tabela dinâmica via `internal/metadata`/`internal/identity`/`internal/records` (um programa Go pequeno e descartável, análogo aos testes de fixture já existentes).
3. Rodar `cmd/server` real apontando para esse Postgres, com `SALTCORN_GO_SERVICE_IDENTITY_SECRET` definido.
4. Rodar este BFF apontando `SALTCORN_BFF_GO_INTERNAL_API_URL` para o `cmd/server` acima, com o MESMO segredo em `SALTCORN_BFF_SERVICE_IDENTITY_SECRET`.
5. Seedar uma sessão diretamente no `SessionStore` (sem endpoint de login, ver acima) e usar o cookie resultante em requisições `curl` às rotas do BFF.

## Sessão e CSRF — decisões de estágio

- **`InMemorySessionStore`**: não sobrevive a um restart nem escala além de uma instância — decisão explícita de estágio (ADR-0007 deixa a tecnologia exata para esta tarefa; sem Redis disponível no ambiente de execução desta entrega e sem consumidor de produção real ainda). Trocar por Redis/Postgres-de-sessão é uma troca de implementação atrás da interface `SessionStore`, não uma reescrita dos pontos que a consomem.
- **CSRF de duplo-envio**: o cookie `sc_csrf` não é `HttpOnly` de propósito — o front-end precisa lê-lo para ecoar no header `X-CSRF-Token`.
