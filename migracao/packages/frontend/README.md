# saltcorn-go-frontend (GO-018/GO-019/GO-020)

Shell React + SB Admin 2 ([ADR-0002](../../../docs/migracao-go/adr/0002-frontend-react-sbadmin2.md)): sidebar/topbar/navegação reimplementados em React puro (sem os scripts imperativos legados — `bootstrap.bundle.min.js`, jQuery — no mesmo DOM), o builder Craft.js **existente** (`packages/saltcorn-builder`) reaproveitado como widget isolado, um cliente HTTP tipado para o BFF (`bff-api.yaml`), (GO-019) o fluxo do editor conectado a esse BFF real (criar tabela/campo, criar/salvar/reabrir/publicar view), e (GO-020) a primeira página administrativa real com dados de verdade: listar e pré-visualizar views renderizadas pelo runtime novo.

## Por que React puro na casca administrativa

`packages/saltcorn-sbadmin2/index.js` monta a sidebar/topbar como HTML gerado por composição de funções (`@saltcorn/markup/tags`) e delega TODA a interatividade (collapse de submenu, toggle da sidebar) a `bootstrap.bundle.min.js` lendo atributos `data-bs-toggle`/`data-bs-target` no DOM. Se este shell carregasse esse mesmo script ao lado do React, os dois disputariam o mesmo nó do DOM — exatamente o problema que ADR-0002 e o critério de aceite de GO-018 ("sidebar, topbar e navegação funcionam sem disputa de DOM") apontam.

A solução aqui não é "integrar com cuidado" os dois — é **não carregar o script legado**: `Sidebar.tsx`/`Topbar.tsx`/`Shell.tsx` portam a MESMA estrutura de dados (`MenuSection`/`MenuItem`, ver `src/types/menu.ts`, espelhando `sidebar()`/`sideBarSection()`/`sideBarItem()` do pacote legado) e as MESMAS classes CSS (para reaproveitar `sb-admin-2.min.css` sem alterar uma linha), mas toda interação (expandir/recolher, toggle) é `useState` do React. Zero script de terceiro toca este DOM — não há disputa possível porque não há segundo ator.

## Assets reaproveitados, não recriados

`public/vendor/sbadmin2/` é uma cópia de `packages/saltcorn-sbadmin2/public/` — **só** CSS/fontes/ícones (`sb-admin-2.min.css`, `bootstrap.rtl.min.css`, `fontawesome-free/`, `nunito/`). Os arquivos JS desse diretório (`bootstrap.bundle.min.js`, `jquery.easing.min.js`, `sb-admin-2.min.js`) foram **deliberadamente excluídos** da cópia — são exatamente os scripts que gerariam a disputa de DOM acima.

`public/vendor/builder_bundle.js` é uma cópia do bundle já publicado em `packages/saltcorn-builder/dist/builder_bundle.js` (webpack UMD, `library: "builder"`) — **não é reconstruído por este pacote**. Se o código-fonte do builder mudar, quem alterar `packages/saltcorn-builder` precisa rodar `npm run build` lá e copiar o `dist/builder_bundle.js` atualizado para cá (ou, num pipeline real de CI/CD, um passo de build compartilhado faria isso — fora do escopo desta tarefa, que é o shell em si).

## Builder Craft.js como widget isolado

`src/builder/BuilderPanel.tsx` **não reimporta** os componentes React do builder (`@saltcorn/builder`) para dentro da árvore React deste shell — isso arriscaria exatamente o tipo de conflito (duas cópias de React, dois bundlers diferentes — Vite aqui, webpack lá) que ADR-0002 quer evitar. Em vez disso, ele:

1. Carrega `public/vendor/builder_bundle.js` via uma tag `<script>` injetada uma vez (o mesmo bundle UMD que páginas legadas já carregam hoje).
2. Cria uma `<div>` contêiner que o React deste shell **nunca mais toca por dentro** depois de montada.
3. Chama `window.builder.renderBuilder(containerId, layoutCodificado, optionsCodificado, mode)` — a mesma função que o HTML legado já chama (`packages/saltcorn-builder/src/index.js`).

Isso é "encapsular widgets imperativos" (ADR-0002) aplicado ao próprio builder: ele já é React, mas é uma árvore React **isolada**, com sua própria cópia de React dentro do bundle — uma fronteira de widget real, não uma reescrita.

### "Builder funciona com mocks"

`MOCK_BUILDER_LAYOUT`/`MOCK_BUILDER_OPTIONS` (`BuilderPanel.tsx`) são um layout/options mínimos e autocontidos — nenhuma referência de biblioteca, nenhuma chamada de rede necessária para o builder montar e renderizar. `test/BuilderPanel.test.tsx` prova, com `window.builder.renderBuilder` mockado, que o componente codifica layout/options/mode corretamente e não faz nenhuma chamada de rede ao montar com esses dados de mock.

## Round-trip do documento de layout

O critério de aceite "round-trip preserva propriedades, referências e extensões" é sobre `layoutToNodes`/`craftToSaltcorn` (`packages/saltcorn-builder/src/components/storage.js`) — funções que já existiam, mas sem NENHUM teste automatizado antes desta tarefa. `packages/saltcorn-builder/tests/storage_roundtrip.test.js` (novo, nesta entrega) monta um `<Editor>` REAL do Craft.js (não mockado) e prova que um ciclo completo desserializar→re-serializar preserva propriedade (texto), referência (`library_id`) e extensão (`_custom`).

## Layout versionado

`src/types/layout.ts` define `VersionedLayout{version, layout}` — o ENVELOPE de versão que ADR-0002 pede ("definir um documento de layout versionado"). O formato da árvore em si (`above`/`besides`/`contents`/`type`) continua sendo o mesmo tipo `Layout` de `@saltcorn/types/base_types` (legado) — não duplicado aqui. `unwrapLayout` aceita tanto um documento versionado quanto um "cru" (o formato que o builder troca hoje) — nenhum layout salvo antes desta tarefa precisa ser reescrito.

## Cliente HTTP tipado para o BFF

`src/bffClient.ts` seguindo o mesmo padrão de `migracao/packages/bff/src/goClient.ts`: tipos gerados de `migracao/contracts/gen/ts/bff-api.d.ts` (openapi-typescript), `fetch` com `credentials: "include"` (sessão via cookie `sc_session`, nunca um token no código do frontend). "Remover a dependência do frontend de modelos de servidor" (ADR-0002): este pacote nunca importa nada de `packages/saltcorn-data`.

## Cliente HTTP tipado para o BFF, estendido (GO-019)

`src/bffClient.ts` ganhou `createTable`/`addField`/`createView`/`getView`/`updateView`, mesmo padrão do restante do arquivo (tipos derivados de `bff-api.d.ts`, `credentials: "include"`, `X-CSRF-Token` via `requireCsrf()`). `updateView` traduz especificamente o 409 `version_conflict` do BFF em `ViewConflictError` — uma classe própria, não uma checagem de string de mensagem — para o chamador (`EditorPage`) DISTINGUIR "conflito de edição" de qualquer outro erro sem inspecionar o corpo da resposta de novo. `readCsrfCookie()` (novo) lê o cookie `sc_csrf` não-HttpOnly que o BFF expõe para esse fim (ADR-0007/GO-006) — nunca gera nem armazena um token, só ecoa o que já está no navegador.

## Fluxo do editor (GO-019)

`src/editor/EditorPage.tsx` é o ponto de demonstração/dev do ciclo "criar tabela e view, salvar, reabrir e publicar com dois papéis" (critério de aceite de GO-019) contra o BFF real — `App.tsx` expõe um botão "Abrir editor (conectado ao BFF)" que a monta com um `BffClient` de verdade (`baseUrl` de `VITE_BFF_BASE_URL`, vazio por padrão = mesma origem via proxy reverso). Como `App.tsx` em si (ver Limitações abaixo), **não é uma rota real do produto ainda** — só a prova de que a conexão fim-a-fim funciona.

- **Conflito de edição:** ao salvar/publicar, um `ViewConflictError` (ver seção acima) marca `conflict = true` e mostra uma mensagem (`data-testid="conflict-message"`, `role="alert"`) SEM alterar o estado local da view (a `_version` conhecida no shell não avança) — o critério "apresentado sem sobrescrever silenciosamente" exige exatamente isto: nenhuma tentativa automática de reenviar por cima.
- **Publicar** é o mesmo `updateView` de "salvar", só que com `min_role: 100` (público) em vez de `configuration` — não existe endpoint/botão separado, mesma decisão do lado Go (ver README do backend).

### Achado de escopo: o "Salvar" do Craft.js não é um callback React — é um `<form>.submit()` nativo

Investigando como conectar o `BuilderPanel` (widget isolado, GO-018) a um "Salvar" de verdade, o botão "Next" do builder (`packages/saltcorn-builder/src/components/Builder.js`, `NextButton.onClick`) não expõe o documento editado via prop/callback nenhum: ele grava o JSON serializado em dois `<input type="hidden">` (`form#scbuildform input[name=columns|layout]`) e chama `document.getElementById("scbuildform").submit()` — um submit **nativo** de formulário HTML (semântica de POST + reload de página inteira). Um `submit()` chamado programaticamente (diferente de `.requestSubmit()`) **não dispara o evento `submit`**, então nem `onSubmit`/`preventDefault()` do React conseguem interceptá-lo — e mesmo que disparasse, o destino seria um POST de formulário HTML, que o BFF (uma API JSON) não tem como atender sem reescrever esse contrato.

Isso é incompatível com uma SPA persistente sem alterar o próprio builder (fora do escopo desta tarefa) ou recorrer a um hack frágil (monkey-patch de `HTMLFormElement.prototype.submit`, `MutationObserver` nos `<input>` ocultos). Por isso `EditorPage.tsx` documenta esta lacuna em vez de contorná-la: o botão "Salvar" do shell é um controle PRÓPRIO fora do Craft.js, que reenvia a `configuration` já conhecida pelo shell (prova o ciclo salvar/conflito/publicar contra o BFF real) — não a edição ao vivo de dentro do canvas. **Ainda não resolvida após GO-020**: essa tarefa focou o runtime de RENDERIZAÇÃO (ver seção abaixo), não a bridge de edição do builder — extrair a edição ao vivo do canvas Craft.js continua em aberto para quando o builder precisar ser conectado de verdade a uma SPA persistente.

## Runtime de renderização de views (GO-020)

`src/render/ListView.tsx` e `src/render/ViewsListPage.tsx` (novos) são a primeira PÁGINA administrativa real deste shell — GO-018 só demonstrava o `BuilderPanel` isolado, nunca uma tela que lista/opera dados de verdade via SB Admin 2.

- **`ListView.tsx`** desenha o DTO de `GET /api/bff/views/:id/render` (`internal/views/render.go` do lado Go — colunas + linhas + paginação): `<table>` com as MESMAS classes CSS que `packages/saltcorn-markup/table.ts` já usa em produção (`table table-sm`, `table-hover` — confirmado por leitura direta do código legado, não inventado), sem reinterpretar nada — o Go já decidiu tudo (colunas, ordenação, quais linhas).
- **`ViewsListPage.tsx`** lista as views do tenant (`GET /api/bff/views`, também novo) com nome/template/status (badge "publicada"/"rascunho") e um botão "Visualizar" por linha. Ao pré-visualizar uma view fora do subconjunto suportado pelo runtime novo (ver README do backend), `bffClient.renderView` lança `ViewUnsupportedError` (422 `view_unsupported` do Go, com o motivo específico) — a página mostra esse motivo e aponta para "administre pelo sistema atual", nunca tenta desenhar uma tabela quebrada. Isto é a metade "seguem rota legada explícita" do critério de aceite de GO-020 ("layouts incompatíveis bloqueiam publicação ou seguem rota legada explícita" — a outra metade, o bloqueio de publicação, é inteiramente do lado Go).
- **`bffClient.ts` ganhou `listViews`/`renderView`** e a classe `ViewUnsupportedError`, mesmo padrão de `ViewConflictError` (GO-019): uma classe própria por tipo de erro de domínio que o React precisa DISTINGUIR, não uma checagem de string de mensagem.

## Show, Edit e escrita real (GO-039)

Estende o runtime de GO-020: `renderView` agora devolve um de três shapes (List/Show/Edit, discriminados por presença de campo — `"rows" in plan`/`"values" in plan`/`"fields" in plan`, já que o contrato usa `oneOf` sem propriedade de discriminação declarada), e `ViewsListPage.tsx` despacha para o componente certo.

- **`ListView.tsx` estendido**: colunas agora têm `kind` (`"field"`/`"join_field"`/`"action"`, ausente = `"field"` por compatibilidade com o formato anterior a GO-039) — uma coluna `action` desenha um botão por linha (`onRowAction`), usando sempre `row.id`/`row._version` (sempre presentes, GO-013/GO-039), nunca uma leitura extra antes de agir.
- **`ShowView.tsx` (novo)**: desenha rótulo+valor de cada coluna de uma view Show, numa tabela `table-borderless` (ficha de detalhe, SB Admin 2).
- **`EditView.tsx` (novo)**: um formulário controlado a partir do `EditPlan` do Go — `<select>` para fieldview `select` (opções REAIS trazidas pelo Go, não inventadas), `<input>` para o resto. `coerceForSubmit` converte string→número para campos `integer`/`float`/`key` antes de chamar `onSubmit` (o mesmo tipo que `internal/records.coerceJSONValue` espera do lado Go) e omite campos deixados em branco (nunca envia string vazia).
- **`bffClient.ts` ganhou `submitView`/`deleteViewRow`**, mesmo padrão de erro de `updateView` (`ViewConflictError` em 409, `ViewUnsupportedError` em 422).
- **`ViewsListPage.tsx`**: o botão "Excluir" de uma coluna de ação chama `deleteViewRow` e recarrega a mesma view; submeter um `EditView` chama `submitView` e mostra a decisão de `navigate` como uma mensagem informativa — sem um roteador client-side ainda (mesma limitação de GO-021, ver abaixo), esta página de pré-visualização INFORMA o que aconteceria a seguir, não navega de fato.

**Achado real corrigido, pego pelo próprio teste novo (não uma regressão deliberada):** `handleEditSubmit` definia a mensagem de navegação e SÓ DEPOIS chamava `handlePreview` — que reseta essa mesma mensagem para `null` logo na primeira linha, apagando-a no mesmo tick antes de qualquer render observar o texto. `ViewsListPage.test.tsx` (teste "submeter o EditView...") pegou isso via timeout de `findByTestId`. Corrigido invertendo a ordem: `await handlePreview(...)` primeiro, `setNavigateMessage(...)` depois.

**Limitação explícita desta entrega:** `Sidebar.tsx` (GO-018) continua renderizando `<a href>` reais, sem nenhum roteador client-side — o item de menu "Views" existe na estrutura de dados de `App.tsx` (documentação de qual seria a URL real), mas quem efetivamente abre `ViewsListPage` hoje é um botão de demonstração, o mesmo padrão já usado para `BuilderPanel`/`EditorPage`. Interceptar cliques de navegação de verdade (roteamento client-side do shell) continua em aberto para uma tarefa futura (GO-021 validou a experiência do que já existe — ver seção abaixo — não implementou roteamento).

## Validação E2E de navegador real e correções de integração encontradas (GO-021)

`migracao/e2e/` (novo pacote, fora deste, README próprio) dirige um navegador real (Chromium/Firefox) contra este frontend BUILDADO (`vite build` + `vite preview`), o BFF real e o backend Go real — não mocks, não jsdom. Resultado: **6/6 testes em Chromium e 6/6 em Firefox** (fluxo criar/publicar/operar, teclado, responsividade, sanitização HTML, smoke). Três correções nasceram diretamente desse teste, que nenhuma suíte anterior (jsdom/vitest, ou HTTP direto sem navegador) conseguiria detectar:

- **CORS entre o frontend e o BFF em portas diferentes:** `vite.config.ts` ganhou `server.proxy`/`preview.proxy` para `/api/bff` — sem isso, todo `fetch()` de dentro do navegador real para o BFF falha com "blocked by CORS policy" quando os dois não estão atrás do mesmo proxy reverso (a topologia de produção documentada — `VITE_BFF_BASE_URL` vazio — nunca precisou de CORS até um navegador REAL exercitar dois processos em portas diferentes). Alvo do proxy configurável via `SALTCORN_DEV_BFF_PROXY_TARGET`.
- **`bffClient.ts`: `new URL(path)` sem base quebrava com `baseUrl` vazio — bug pré-existente desde GO-017.** `listRecords` (GO-017), `listViews`/`renderView` (GO-020) construíam uma `URL` para manipular query params via `new URL(`${baseUrl}${path}`)`, sem um segundo argumento de base — com `baseUrl` vazio (a topologia real de produção), o resultado é um path relativo, e `new URL()` de um relativo sem base lança `TypeError: Invalid URL` na hora. Nenhum teste anterior pegava isso porque `bffClient.test.ts` sempre usava uma `baseUrl` absoluta de teste. Corrigido com um helper privado `buildUrl(path)` (usa `window.location.origin` como base, ignorado quando `baseUrl` já é absoluto) — regressão confirmada deliberadamente (reintroduzir o bug, ver os 3 novos testes falharem com "Invalid URL", restaurar).
- **`EditorPage.tsx` criava views com um layout incompatível com o runtime de renderização (GO-020):** o layout de mock de GO-018/019 (`{above: [...]}`) nunca tinha sido testado contra a checagem de compatibilidade que GO-020 introduziu depois — "Publicar" falharia de verdade (422 `view_unsupported`) contra o backend real. Corrigido: `handleCreateTable` agora também cria um campo (`titulo`, texto) e a view nasce com um layout suportado (`layout.besides` referenciando esse campo); `handleSave`/`handlePublish` também passaram a distinguir `ViewUnsupportedError` de outros erros, mesmo padrão de `ViewConflictError`.

Ver `migracao/e2e/README.md` e `docs/migracao-go/execucoes/GO-021.md` para o design completo do harness (incluindo por que não é um teste "com login", a limitação real de WebKit neste ambiente, e o achado sobre limpeza de processos de fundo do próprio harness).

## Build, testes e execução

```bash
npm install
npm run typecheck   # tsc --noEmit
npm test            # vitest (jsdom + React Testing Library)
npm run build       # tsc --noEmit && vite build → dist/
npm run dev         # vite, http://localhost:5173
```

## Internacionalização (i18n) — `src/i18n/` (GO-047)

Catálogo de traduções pt/en com chaves SEMÂNTICAS (`src/i18n/
translations.ts`) + `I18nProvider`/`useT()` (`src/i18n/I18nContext.tsx`),
aplicado às ~20 strings de produto real (`Topbar.tsx`, `ListView.tsx`,
`EditView.tsx`, `ViewsListPage.tsx` — `App.tsx`/`EditorPage.tsx` são
páginas de demonstração/dev, não traduzidas). O locale vem SEMPRE já
resolvido do bootstrap do BFF (`actor.language` > cookie `lang` >
`default_locale` do tenant > `"pt"`, ver `migracao/packages/bff/src/
locale.ts`) — este pacote nunca reimplementa essa cadeia. Seletor de
idioma na Topbar (`data-testid="locale-select"`), troca otimista local +
persistência assíncrona via `bffClient.setActorLanguage`. Detalhes
completos em `migracao/backend/README.md` §"Internacionalização (i18n)
da interface" e `docs/migracao-go/execucoes/GO-047.md`.

## Limitações desta entrega (deliberadas, não fabricadas)

- **Topbar mínima**: a versão de `packages/saltcorn-sbadmin2/index.js` usada neste repositório não monta uma topbar completa (busca/dropdown de usuário) — só o botão de colapsar a sidebar. `Topbar.tsx` porta exatamente o que existe hoje, não uma topbar de SB Admin 2 genérica inventada sem essa referência real.
- **`App.tsx` continua sendo um ponto de demonstração**, não uma rota real do produto — `ViewsListPage`/`EditorPage`/`BuilderPanel` são componentes reais e testados, mas `App.tsx` os monta via botões de alternância, não via roteamento. Roteamento real do shell (interceptar `<a href>` da sidebar, trocar de página sem reload) é GO-021.
- **Vulnerabilidades de dependência de desenvolvimento** (`npm audit`): `esbuild`/`@vitest/mocker` têm avisos moderados corrigidos só em versões maiores (Vite 8/Vitest 5) — ambos expõem apenas o servidor de desenvolvimento local (nunca o build de produção nem CI), então mantidos na série 5.x/2.x atual em vez de um upgrade maior não validado nesta entrega.
- **`builder_bundle.js` é uma cópia estática**, não reconstruída por este pacote — ver seção acima.
