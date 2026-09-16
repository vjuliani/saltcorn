# ADR-0002 — Frontend React + SB Admin 2

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §2 "Frontend"](../README.md#2-arquitetura-proposta), [ADR-0003 (BFF)](0003-bff-nodejs-permanente.md), [matriz de capacidades GO-001 §2.3](../inventario/GO-001-matriz-capacidades.md)

## Contexto

Hoje toda a renderização é feita em Node via `@saltcorn/markup` — uma biblioteca de geração de HTML por composição de funções (não um motor de templates como EJS/Pug/Handlebars); `@saltcorn/sbadmin2` usa exatamente o mesmo mecanismo para compor a casca administrativa (sidebar, topbar). O builder de views/páginas (`@saltcorn/builder`) já é React + Craft.js (GO-001 §2.3). O CSP atual permite `unsafe-inline`/`unsafe-eval` — necessário hoje porque o builder precisa executar/avaliar código em runtime; isso é uma lacuna de hardening a revisar, não uma característica a preservar.

## Decisão

Adotar **React** para toda a interação de interface e **SB Admin 2** como tema administrativo, reaproveitando os assets existentes de `packages/saltcorn-sbadmin2` (CSS/Bootstrap/fontes) — não a composição server-side atual desse pacote. Manter **Craft.js** no builder (já é React) e introduzir TypeScript gradualmente apenas nas áreas alteradas. Todo código novo do frontend é criado sob `migracao/packages/frontend/` (convenção de diretórios do README §2).

Trabalho necessário, não automático: mapear os templates atuais do tema para componentes React e encapsular widgets imperativos, para que React e scripts legados nunca manipulem o mesmo nó do DOM simultaneamente. Criar um cliente HTTP tipado para os contratos do BFF (ver [ADR-0003](0003-bff-nodejs-permanente.md)) e remover a dependência do frontend de modelos de servidor. Definir um documento de layout versionado (mesmo formato hoje produzido pelo builder e consumido por `layout.ts`).

## Escopo desta camada

**Dentro:** editor administrativo (builder); renderizador das aplicações publicadas; tema/casca administrativa (sidebar, topbar, navegação).

**Fora:**
- Mobile/offline — permanece em Capacitor/Ionic, fora do escopo deste ADR (ver matriz GO-001 §2.5); nenhuma decisão de reescrever o app móvel em React/WASM é tomada aqui.
- Sessão, cookies, CSRF, composição de dados de tela — [BFF Node.js](0003-bff-nodejs-permanente.md).
- Regras de negócio e autorização — revalidadas sempre no backend Go ([ADR-0001](0001-backend-go-cqrs.md)); o frontend nunca é a fonte de verdade de permissão.

Páginas ainda dependentes da renderização Node permanecem no caminho legado até que o novo renderizador passe nos testes de paridade (padrão Strangler Fig, README §3 item 2) — não há um "big bang" de reescrita simultânea de UI e backend (README §2 "Frontend", explícito).

## Custos operacionais

- Novo pipeline de build (bundler) e nova infraestrutura de distribuição de assets estáticos do frontend, distintos do processo Node/Express atual.
- Trabalho de desacoplamento do tema existente do DOM compartilhado com scripts legados — item de custo não trivial, já sinalizado como risco na matriz GO-001 (§2.3, "Sidebar, topbar e navegação funcionam sem disputa de DOM" é critério de aceite explícito de GO-018).
- Revisão da política de CSP (`unsafe-inline`/`unsafe-eval`) ao desacoplar o builder da execução direta em página — pode exigir mudança de como o builder é servido/isolado.
- Manutenção de dois caminhos de renderização (legado Node + React novo) durante toda a transição, com decisão explícita de roteamento por página/aplicação.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| Manter a renderização server-side atual (`@saltcorn/markup`) indefinidamente | Não atende ao objetivo de frontend moderno desacoplado do backend de domínio; mantém React (builder) e scripts legados competindo pelo mesmo DOM |
| Reescrever toda a UI no mesmo marco da migração do backend | Rejeitado explicitamente no README: "Evitar reescrever toda a UI e backend no mesmo marco" |
| Go/WASM para partes compartilhadas de renderização | Só entra "se um experimento demonstrar vantagem concreta para uma capacidade compartilhada" (README) — não há decisão de adotar agora |

## Consequências e riscos

- Preservar URLs, formulários, navegação, traduções, acessibilidade, temas e widgets utilizados é critério de aceite, não opcional (README §2).
- Layout/editor incompatível é risco tabelado no README §5 — mitigação via fixtures de documentos, round-trip, snapshots visuais e E2E (GO-018/GO-021).
- Enquanto páginas legadas e novas coexistirem, qualquer divergência visual/funcional entre os dois caminhos é uma regressão a rastrear, não um "detalhe da migração".
