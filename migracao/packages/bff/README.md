# saltcorn-go-bff (GO-017)

BFF web em Node.js/TypeScript ([ADR-0003](../../../docs/migracao-go/adr/0003-bff-nodejs-permanente.md)): processo **permanente e separado** do backend Go, consumindo a API interna versionada (`internal-api.yaml`, GO-006) por HTTP/JSON. Compõe bootstrap, metadados e dados para o frontend React (ainda não implementado — GO-018); cuida de sessão/cookies e CSRF; **nunca acessa banco de domínio nem concentra regras de negócio** — todo comando/query revalida autorização no backend Go.

## Dependências mínimas, deliberadamente

`jsonwebtoken` e, desde GO-028, `socket.io` — duas exceções deliberadas, mesmo espírito de `migracao/backend` (pgx/jwt/otp pinados, nada supérfluo) e de [ADR-0009](../../../docs/migracao-go/adr/0009-observabilidade-sem-sdk-externo.md) (preferir a biblioteca padrão quando a superfície é pequena e bem entendida):

- **Sem framework web** (Express etc.): 5 rotas, um roteador de ~50 linhas (`src/router.ts`) sobre `node:http` é mais simples de auditar do que integrar e manter atualizado um framework inteiro.
- **Sem biblioteca de sessão/CSRF**: os mecanismos (`src/session.ts`, `src/csrf.ts`) são pequenos e bem entendidos (cookie opaco + store, duplo-envio) — implementados à mão com `node:crypto` (`randomBytes`, `timingSafeEqual`), mesmo cuidado de `identity.VerifyAPIToken` no Go.
- **`jsonwebtoken` é a exceção deliberada**: verificação de JWT tem histórico conhecido de bugs de segurança (confusão de algoritmo, etc.) — o próprio backend Go usa uma biblioteca madura para o mesmo motivo (`golang-jwt/jwt/v5`, GO-008), não HMAC hand-rolled.
- **`socket.io` é a segunda exceção deliberada (GO-028)**: o critério de aceite da tarefa proíbe explicitamente tratar WebSocket puro como substituto compatível de Socket.IO (reconexão com backoff, handshake com upgrade, acks fazem parte do protocolo real) — reimplementar isso à mão seria exatamente o tipo de superfície grande e mal compreendida que este pacote evita. Versão pinada (`4.8.1`) igual à do legado (`packages/server/package.json`), pela mesma razão de compatibilidade que já guiou outras escolhas de versão nesta migração.
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
  realtime.ts        servidor Socket.IO real (GO-028) — handshake autenticado pela
                     MESMA sessão de navegador, poll a .../realtime/events (Go) por
                     socket conectado, reemissão em ordem
  server.ts          entrypoint: configuração, servidor HTTP, encerramento gracioso,
                     anexa realtime.ts ao mesmo http.Server
test/
  *.test.ts          unitários (sessão, CSRF, idempotência, ServiceIdentity)
  mockGoServer.ts    réplica mínima da verificação de identidade delegada do Go real,
                     também replica idempotência/conflito de versão de outbox.Do
                     (createRecord/createView/updateView), só para os testes de
                     integração exercitarem a fronteira de verdade
  app.test.ts        integração: app real + servidor HTTP real contra o mock do Go
  editor.test.ts     integração do ciclo do editor (GO-019): criar tabela/campo/view,
                     salvar, reabrir, conflito de edição concorrente propagado (409)
  realtime.test.ts   integração (GO-028) contra um servidor Socket.IO real: os 4
                     critérios de aceite — reconexão, sessão expirada, ordenação,
                     isolamento por tenant
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
- `GET /api/bff/views/:id/render` (GO-020, estendido em GO-039) — exige sessão, sem CSRF (é leitura); delega a `views.CompileListPlan`/`CompileShowPlan`/`CompileEditPlan` (Go, despachado pelo template da view) — devolve o DTO de renderização, nunca HTML; o shape varia por template. `?record=` repassado tal como recebido — obrigatório para Show, opcional para Edit (ausente = registro novo), ignorado por List. Propaga 422 `view_unsupported` quando a view usa um recurso fora do subconjunto suportado (ver README do backend, seção "Show, Edit, escrita real e JoinField/Action").
- `POST /api/bff/views/:id/submit` (GO-039) — exige sessão + CSRF; gera `Idempotency-Key` com escopo `"views/<id>/submit"` (nunca reaproveita a de `updateView` — mesmo view id, operação diferente); delega a `views.SubmitEditView` (Go) — o `form_action` real (Save/SubmitWithAjax) de uma view Edit. Devolve `{record, navigate}`; propaga 409 `version_conflict` e 422 `view_unsupported` (campo fora da view, layout incompatível) sem mascarar.
- `DELETE /api/bff/views/:id/rows/:recordId` (GO-039) — exige sessão + CSRF; delega a `views.DeleteListRow` (Go) — a ação de coluna "Delete" de uma view List. `?version=` obrigatório (concorrência otimista); sem Idempotency-Key (DELETE já é idempotente por natureza aqui — uma segunda chamada encontra 404, não um efeito duplicado).
- `GET /api/bff/admin/users`, `PATCH`/`DELETE /api/bff/admin/users/:id`, `POST /api/bff/admin/users/:id/reset-password`, `GET /api/bff/admin/users/:id/tokens` (GO-044) — exigem sessão (mutações também CSRF); delegam a `identity.ListUsers`/`UpdateUserRole`/`DeleteUser`/`SetPassword`/`ListAPITokensForUser` do lado Go, que decide o 403 (`requireAdmin`) — o BFF só propaga.
- `POST /api/bff/admin/users/:id/force-logout` (GO-044) — exige sessão + CSRF. **Único handler administrativo que nunca chama o Go para decidir autorização** — derruba todas as sessões do usuário-alvo direto no `SessionStore` (a sessão de navegador nunca é do Go, ADR-0007), então checa o papel do próprio ator aqui mesmo (`goClient.getActor`, `role_id === 1`).
- `POST /api/bff/admin/users/:id/impersonate` (GO-044) — exige sessão + CSRF; delega a `identity.StartImpersonation` (audita quem/quando no Go) e, só depois de confirmada, cria uma sessão NOVA para o usuário-alvo com `impersonatedBy` marcado — nunca reaproveita a sessão do admin. Sobrescreve o cookie de sessão/CSRF da resposta.
- `POST /api/bff/admin/impersonation/end` (GO-044) — exige sessão + CSRF; só aceita encerrar a impersonação da sessão ATUAL (nunca um `log_id` do corpo/query), delega a `identity.EndImpersonation`, destroi a sessão e expira o cookie. Decisão deliberada: NÃO restaura a sessão original do admin — força novo login.
- `PATCH /api/bff/admin/tables/:table/permissions` (GO-044) — exige sessão + CSRF; delega a `metadata.UpdateTablePermissions` do lado Go.

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
| `SALTCORN_BFF_REALTIME_POLL_INTERVAL_MS` | `500` | Intervalo de polling de `realtime.ts` a `.../realtime/events`, por socket conectado — também o atraso máximo para desconectar um socket cuja sessão expirou (GO-028) |

## Comunicação em tempo real (GO-028)

Ver `docs/migracao-go/execucoes/GO-028.md` para o levantamento completo do legado (Socket.IO em `packages/server/serve.js`) e a decisão de arquitetura. Resumo:

- **O protocolo Socket.IO roda inteiramente aqui, nunca no backend Go** (ADR-0003/ADR-0007: sessão e borda web são sempre BFF) — `src/realtime.ts` anexa um `socket.io.Server` real ao MESMO `http.Server` das rotas REST (`server.ts`).
- **Handshake autenticado pela sessão de navegador** (o MESMO cookie `sc_session`, nunca um token/tenant vindo do cliente) — fail-closed: sem sessão válida, a conexão é recusada antes de qualquer `socket.on` ser registrado.
- **Sem rooms**: cada socket conectado tem seu PRÓPRIO polling a `GET /v1/tenants/{tenant}/realtime/events` (Go, GO-028), com o ServiceIdentity do PRÓPRIO ator daquele socket — o filtro por destinatário já aconteceu no Go (`internal/realtime.ListSinceForActor`), então o BFF nunca decide audience, só reemite (`dynamic_update`) o que recebe. Divergência deliberada e mais forte que o legado: lá, isolamento depende de nomear rooms a partir do tenant resolvido do header `Host` no handshake, sem barreira estrutural própria do Socket.IO (ver achado #8 do levantamento) — aqui, cada socket nunca sequer recebe a EXISTÊNCIA de um evento de outro tenant/usuário.
- **Sessão expirada**: cada tick de polling revalida a sessão no `SessionStore` ANTES de buscar eventos — se ela não existir mais, o socket é desconectado à força (`socket.disconnect(true)`), não só recusado num handshake futuro.
- **Reconexão sem perda**: o protocolo Socket.IO real cuida da reconexão automática com backoff (nunca WebSocket puro, proibido pelo critério de aceite da tarefa); cada evento emitido carrega seu `id`, e o cliente (`migracao/packages/frontend/src/realtimeClient.ts`) lembra o maior já recebido e o reenvia como `?since=` na query da reconexão — o servidor retoma exatamente dali.
- **Superfície coberta neste piloto**: só o equivalente a `dynamic_update` de notificação (`internal/notify.Create` publica um evento em tempo real na mesma transação, GO-028 fecha a lacuna deixada explicitamente aberta por GO-026). **Ficam de fora, bloqueados** (mesma disciplina de "sem inventário real" de GO-026 para push nativo): colaboração em tempo real por view (`collab_room`), stream de logs de admin, progresso de restore de backup, e o namespace `/datastream` de upload — nenhum tem consumidor Go equivalente ainda, e nenhum é exigido pelos critérios de aceite desta tarefa.

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

## Distribuição self-hosted (GO-032)

`SALTCORN_BFF_WEB_ROOT` habilita o adapter opcional de assets e acesso de operador
(`src/selfhost.ts`), com tenant/ID da instalação definidos pelo supervisor Go.
O adapter serve apenas arquivos dentro do diretório da release, recusa symlinks
externos e verifica readiness do Go. `POST /api/bff/operator-session` troca
um ticket CLI HS256 de até 60 segundos por sessão/CSRF; exige mesma origem,
audience/issuer/tenant corretos, uso único e papel admin atual consultado no Go.
Tokens de identidade interna não servem para abrir essa sessão. O contrato
OpenAPI documenta a rota opcional; o BFF comum continua sem login de usuário
final. Reiniciar o BFF requer reautenticação. Instruções completas no
[runbook da distribuição](../../distribution/README.md).
