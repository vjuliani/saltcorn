# saltcorn-go-frontend (GO-018)

Shell React + SB Admin 2 ([ADR-0002](../../../docs/migracao-go/adr/0002-frontend-react-sbadmin2.md)): sidebar/topbar/navegação reimplementados em React puro (sem os scripts imperativos legados — `bootstrap.bundle.min.js`, jQuery — no mesmo DOM), o builder Craft.js **existente** (`packages/saltcorn-builder`) reaproveitado como widget isolado, e um cliente HTTP tipado para o BFF (`bff-api.yaml`).

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

## Build, testes e execução

```bash
npm install
npm run typecheck   # tsc --noEmit
npm test            # vitest (jsdom + React Testing Library)
npm run build       # tsc --noEmit && vite build → dist/
npm run dev         # vite, http://localhost:5173
```

## Limitações desta entrega (deliberadas, não fabricadas)

- **Topbar mínima**: a versão de `packages/saltcorn-sbadmin2/index.js` usada neste repositório não monta uma topbar completa (busca/dropdown de usuário) — só o botão de colapsar a sidebar. `Topbar.tsx` porta exatamente o que existe hoje, não uma topbar de SB Admin 2 genérica inventada sem essa referência real.
- **`App.tsx` é uma demonstração**, não uma rota real do produto — migrar páginas legadas para dentro deste shell é trabalho de tarefas futuras (GO-020/GO-021).
- **Vulnerabilidades de dependência de desenvolvimento** (`npm audit`): `esbuild`/`@vitest/mocker` têm avisos moderados corrigidos só em versões maiores (Vite 8/Vitest 5) — ambos expõem apenas o servidor de desenvolvimento local (nunca o build de produção nem CI), então mantidos na série 5.x/2.x atual em vez de um upgrade maior não validado nesta entrega.
- **`builder_bundle.js` é uma cópia estática**, não reconstruída por este pacote — ver seção acima.
