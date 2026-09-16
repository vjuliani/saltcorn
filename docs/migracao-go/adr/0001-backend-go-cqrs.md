# ADR-0001 — Backend Go modular com CQRS lógico

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §2 "Backend"/"CQRS"](../README.md#2-arquitetura-proposta), [ADR-0003 (BFF)](0003-bff-nodejs-permanente.md), [ADR-0004 (bancos)](0004-estrategia-de-bancos.md), [ADR-0006 (retirada do legado)](0006-retirada-do-legado.md), [matriz de capacidades GO-001](../inventario/GO-001-matriz-capacidades.md)

## Contexto

O backend atual (`packages/saltcorn-data`, `packages/server`) mistura em um único processo Node: transporte HTTP, sessão/CSRF, autenticação, renderização de HTML e todas as regras de domínio (README §1). A matriz de capacidades (GO-001) mostra que o núcleo do domínio já está fisicamente separável — `table.ts`/`field.ts` (metadados), `db/index.ts` + adapters Postgres/SQLite (persistência), `trigger.ts`/`workflow_run.ts`/`scheduler.ts` (automação) — mas hoje compartilha processo, contexto de tenant (`AsyncLocalStorage`) e runtime JS com tudo o mais.

Dois riscos concretos já documentados orientam a escolha de padrão de organização interna:

1. RLS nativa do Postgres troca identidade por `SET LOCAL`/GUC numa conexão dedicada do pool (GO-001 §2.1) — exige unidade transacional explícita, não implícita.
2. `AsyncLocalStorage` (tenant/transação implícitos por chamada assíncrona) não tem equivalente direto em Go — a substituição por `context.Context` explícito é uma mudança de modelo, não uma tradução mecânica (GO-001 §2.1, §2.2).

## Decisão

Adotar um **monólito modular em Go**, com **CQRS lógico no mesmo banco** (não CQRS com projeções assíncronas, não event sourcing) como padrão de organização interna, conforme a tabela de avaliação do README (§2 "CQRS"):

- `Commands` validam tenant, usuário, schema, tipos, ownership e triggers transacionais; gravam registro e outbox na mesma transação.
- `Queries` aplicam as mesmas políticas de visibilidade dos commands e geram DTOs paginados, sem executar ações de negócio.
- Módulos: identidade/tenancy; metadados/schema; registros/consultas; views/pages/layouts (regras e dados, não renderização — ver [ADR-0002](0002-frontend-react-sbadmin2.md)); arquivos; automações; extensões (fronteira com o host JS, [ADR-0005](0005-politica-de-extensoes.md)); configuração/packs; sincronização.
- CLI e worker reutilizam os mesmos serviços de aplicação (nenhuma lógica duplicada por modo de execução).
- Todo código novo é criado sob `migracao/backend/` (`cmd/{server,worker,cli}`, `internal/{identity,metadata,records,views,automation,extensions,files,sync}`, `internal/platform/{database,http,telemetry}`), conforme a convenção de diretórios do README §2.

## Escopo desta camada

**Dentro:** regras de negócio, validação, autorização (papel/ownership/RLS), persistência, outbox/idempotência, automação (triggers/workflow/scheduler), API HTTP/JSON interna e pública versionada.

**Fora (propositalmente delegado a outra camada ou ADR):**
- Sessão de navegador, cookies, CSRF — [BFF Node.js](0003-bff-nodejs-permanente.md).
- Renderização de HTML/UI — [frontend React](0002-frontend-react-sbadmin2.md).
- Execução de JavaScript arbitrário de plugins/expressões de usuário — [host temporário](0005-politica-de-extensoes.md).

## Custos operacionais

- Novo toolchain (Go), pipeline de build/CI e processo de deploy próprios, distintos do Node legado — dois backends operando em paralelo durante toda a transição (custo duplo temporário, não pontual).
- Observabilidade adicional dedicada (logs estruturados, métricas, tracing HTTP/SQL/jobs — GO-010) para que uma operação seja rastreável entre os dois runtimes.
- Curva de aprendizado da equipe em Go, `context.Context` para propagação de tenant/transação (substituindo o modelo implícito de `AsyncLocalStorage`) e no compilador de consultas dinâmicas (GO-012).
- Mecanismo de troca de identidade por conexão para RLS nativa (GO-001 §2.1) é um item de custo/risco próprio, não trivial — tratar como subtarefa dedicada dentro de GO-011/GO-015 (já registrado na matriz de capacidades).

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| CRUD único (sem separação lógica commands/queries) | Tende a acoplar leitura, efeitos colaterais e renderização — aceitável apenas para configurações simples dentro das interfaces de aplicação, não como padrão |
| CQRS com projeções assíncronas desde o início | Introduz atraso e reconstrução sem benefício comprovado; adotar **apenas** onde um benchmark justificar (ver GO-016, que pode concluir adiando) |
| Event sourcing | Exige eventos como fonte da verdade, replay e evolução histórica — escopo maior do que a migração exige agora; adiado |
| Traduzir o TypeScript automaticamente para Go | Rejeitado explicitamente no README: Saltcorn executa aplicações definidas por metadados/extensões, alterar essa semântica é o maior risco da migração |

## Consequências e riscos

- Dois escritores (Node legado + Go) e DDL concorrente exigem matriz de ownership por capacidade/tenant, bloqueio do caminho antigo de mutação e um executor único de migrations (README §3, risco tabelado em §5).
- Trigger duplicado ou perdido exige classificar atomicidade e testar crash antes/depois do commit — tratado em GO-014 (idempotência e outbox).
- Leitura após escrita usa o banco primário até que projeções (se adotadas via GO-016) tragam garantias equivalentes de revogação de acesso.
