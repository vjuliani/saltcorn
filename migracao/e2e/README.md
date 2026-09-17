# saltcorn-go-e2e (GO-021)

E2E de navegador real do stack NOVO (backend Go + BFF Node.js + frontend React/SB Admin 2) — distinto de `deploy/playwright` (legado, Node/Express). Critério de aceite de GO-021: "fluxo criar/publicar/operar passa E2E nos navegadores acordados; nenhum script não autorizado executa em cenários de teste."

**Resultado: 6/6 testes em Chromium e 6/6 em Firefox** (`bash run.sh --project=chromium` / `--project=firefox`) — fluxo criar/publicar/operar, teclado, responsividade (2 viewports), sanitização HTML, smoke.

## Achado corretivo desta tarefa

Tarefas anteriores (GO-018/019/020) registraram "sem ferramenta de browser/screenshot disponível neste ambiente" e, por isso, validaram fluxos via HTTP real em vez de navegador real. Essa afirmação estava desatualizada: **Playwright já está instalado neste ambiente** (usado por `deploy/playwright` desde GO-002) e um **Chromium headless real funciona** (confirmado nesta tarefa: `browser.launch()` + navegação real). O que nunca existiu é uma forma de o AGENTE "ver" uma tela interativamente durante a conversa — algo diferente de "não dá para escrever/rodar um teste E2E automatizado". Este pacote é a correção: Playwright de verdade, contra o stack novo real, não mais um HTTP E2E travestido de "o mais realista possível".

**Navegadores cobertos:** Chromium e Firefox — ambos confirmados funcionais (`npx playwright install firefox` + `firefox.launch()` testado). **WebKit está fora**: falha por uma dependência de sistema ausente (`libavif16`) sem acesso root neste ambiente para instalar (`sudo` exige senha) — limitação verificada, não evitada por conveniência. O próprio suite legado (`deploy/playwright/playwright.config.js`) nunca habilitou WebKit/Firefox (só Chromium, comentado o resto), então não havia sequer precedente de WebKit funcionando neste repositório.

## Por que não é um teste "com login"

O BFF não tem (por decisão explícita desde GO-017) nenhum endpoint de autenticação usuário+senha — a única forma de obter uma sessão válida hoje é chamar `SessionStore.create(...)` diretamente, exatamente como os próprios testes do BFF já fazem (`editor.test.ts`, helper `withSession`). Em vez de fabricar uma tela de login que não existe de verdade, `scripts/start-bff.mjs` sobe o BFF real (os MESMOS módulos de produção compilados — `buildRouter`, `createRequestListener`, `InMemorySessionStore`) e faz essa MESMA chamada de semeadura uma vez, imprimindo o `sessionId`/`csrfToken` resultantes; `run.sh` os captura e os testes (`tests/fixtures.ts`) os injetam no navegador via `context.addCookies` antes de qualquer navegação. Nenhuma rota nova entra em `migracao/packages/bff/src/` — a semeadura vive inteiramente aqui.

Os cookies são injetados com `secure: false` (o harness usa HTTP local, não HTTPS) — uma simplificação documentada deste harness, não uma alegação sobre o comportamento em produção. O atributo `Secure` de `sessionCookieHeader`/`csrfCookieHeader` (ADR-0007) já tem teste unitário dedicado do lado do BFF.

## `cli e2e-seed` (backend Go)

Não existe (nem deveria existir) uma rota HTTP pública para criar o primeiro usuário de um tenant e conceder ownership de capacidade — são ações anteriores a qualquer requisição autenticada. `migracao/backend/cmd/cli`'s novo comando `e2e-seed` (`--dsn`, `--tenant`, `--email`) recria o schema do tenant do zero, aplica os `EnsureSchema` de identidade/metadados/outbox/views, cria um usuário admin, e registra ownership de Go (`cutover.SetOwner`, não `SwitchOwner` — não há servidor rodando ainda para drenar) para `tables.records`/`tables.schema`/`tables.views`. Testado em `cmd/cli/e2eseed_test.go` (Postgres real).

## Estrutura

```
run.sh                  orquestra tudo: seed Go, build+start cmd/server, build+start BFF
                         (sessão pré-semeada), build+serve frontend, roda Playwright
scripts/start-bff.mjs   sobe o BFF real com uma sessão pré-semeada (ver acima)
playwright.config.ts    projects chromium/firefox, baseURL do frontend servido
tests/fixtures.ts       injeta os cookies de sessão/CSRF no navegador antes de navegar
tests/smoke.spec.ts     o shell carrega com a sessão pré-semeada
tests/editor-flow.spec.ts  criar tabela → criar view → publicar → operar (listar/pré-visualizar)
tests/keyboard.spec.ts     toggle da sidebar alcançável e operável por teclado (Enter)
tests/responsive.spec.ts   sem overflow horizontal em viewport móvel/desktop
tests/sanitization.spec.ts valor de registro com <script> nunca executa, só aparece como texto
```

## Rodando

Requer um Postgres de teste isolado e descartável (mesma convenção dos testes Go/BFF):

```bash
docker run -d --name saltcorn-e2e-testdb -e POSTGRES_PASSWORD=postgres -p 0:5432 postgres:16-alpine
# aplicar a fixture acme/beta/probe se for rodar a suíte Go completa também (não exigida por este pacote)

cd migracao/e2e
npm install
npx playwright install chromium firefox   # webkit não funciona neste ambiente, ver acima

SALTCORN_GO_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:<porta>/postgres?sslmode=disable" \
  npm test -- --project=chromium
```

`run.sh` recria o schema do tenant a cada execução (via `cli e2e-seed`) — seguro rodar repetidamente contra o mesmo Postgres.

## Achados corrigidos nesta tarefa (só apareceram com um navegador real)

Quatro problemas reais, nenhum detectável por jsdom/vitest ou HTTP direto sem navegador — só apareceram ao dirigir um Chromium/Firefox de verdade contra o stack completo:

1. **CORS entre o frontend e o BFF em portas diferentes.** A topologia de produção documentada (`VITE_BFF_BASE_URL` vazio, mesma origem via proxy reverso) nunca precisou de CORS — mas este harness roda os dois em portas separadas sem proxy de produção na frente. Corrigido em `migracao/packages/frontend/vite.config.ts` (`server.proxy`/`preview.proxy` para `/api/bff`, alvo configurável via `SALTCORN_DEV_BFF_PROXY_TARGET`) — `run.sh` compila o frontend com `VITE_BFF_BASE_URL=""` (mesma origem) e serve com o proxy apontando para o BFF real.
2. **Limpeza de processos de fundo do próprio `run.sh`.** `(cd DIR && cmd) &` cria uma SUBSHELL — `$!` captura o PID dela, não o do processo real (`npx`/`vite` ainda spawnam filhos). Matar só o PID da subshell deixava `vite preview` órfão, ainda escutando a porta depois do script terminar; a execução SEGUINTE via `wait_for_port` via a porta "aberta" e seguia em frente falando com o bundle/BFF de uma execução ANTERIOR já morta (sessão/tenant que não existem mais) — uma falha silenciosa (createTable nunca chegava ao Go, sem nenhum erro) muito difícil de diagnosticar sem inspecionar processos manualmente. Corrigido: cada processo de fundo (Go, BFF, `vite preview`) roda via `setsid` com caminho de binário direto (sem `npx`, sem `cd`+subshell), e o cleanup mata o GRUPO DE PROCESSOS de cada um (`kill -- -PID`) — mais uma salvaguarda: as portas usadas são liberadas (`fuser -k`) ANTES de começar, não só no fim.
3. **`bffClient.ts`: `new URL(path)` sem base quebrava com `baseUrl` vazio — bug pré-existente desde GO-017.** `listRecords`/`listViews`/`renderView` construíam uma `URL` para manipular query params sem um segundo argumento de base; com `baseUrl` vazio (a topologia real de produção), `new URL()` de um path relativo sem base lança `TypeError: Invalid URL` na hora. Nenhum teste anterior usava `baseUrl` vazio com uma `URL` de verdade. Corrigido com um helper `buildUrl(path)` (`window.location.origin` como base) — regressão confirmada deliberadamente.
4. **`EditorPage.tsx` (GO-019) criava views com um layout incompatível com o runtime de renderização (GO-020).** O layout de mock (`{above: [...]}`) nunca tinha sido exercitado contra a checagem de compatibilidade que GO-020 introduziu depois — publicar falharia de verdade (422 `view_unsupported`). Corrigido: `handleCreateTable` também cria um campo (`titulo`, texto) e a view nasce com um layout suportado (`layout.besides` referenciando esse campo); `handleSave`/`handlePublish` também distinguem `ViewUnsupportedError` de outros erros, mesmo padrão de `ViewConflictError`.

Detalhes completos (incluindo o texto exato dos erros encontrados) em `docs/migracao-go/execucoes/GO-021.md`.

## Limitações desta entrega (deliberadas, não fabricadas)

- **WebKit não coberto** — ver "Achado corretivo" acima.
- **i18n, temas por aplicação, componentes customizados do piloto**: sem funcionalidade correspondente no stack novo ainda para validar (nenhum framework de i18n, nenhum sistema de tema por aplicação além do CSS fixo do SB Admin 2, nenhum sistema de fieldview/plugin portado — GO-020 só suporta colunas de campo direto como texto puro). Registrado como lacuna de funcionalidade, não como teste pulado por conveniência.
- **Setup de dados via `page.request`, não só UI**: `EditorPage.tsx` ainda não tem UI para "adicionar campo"/"criar registro" (só criar tabela/view, salvar/publicar/reabrir) — `sanitization.spec.ts` usa `page.request` (mesmos cookies do navegador, mesmas rotas HTTP reais do BFF) para essas duas etapas de setup, e a UI real para o restante do fluxo (listar/pré-visualizar).
