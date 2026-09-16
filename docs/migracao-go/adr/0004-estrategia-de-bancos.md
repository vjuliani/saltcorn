# ADR-0004 — Estratégia de bancos: Postgres primeiro; SQLite e mobile em etapa posterior

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §"Recomendação" e §4 "Etapas e marcos"](../README.md), [ADR-0001 (backend Go)](0001-backend-go-cqrs.md), [matriz de capacidades GO-001 §2.2, §2.5](../inventario/GO-001-matriz-capacidades.md)

## Contexto

A matriz de capacidades (GO-001) documenta que os dois adapters atuais têm modelos de concorrência incompatíveis, não apenas dialetos SQL diferentes:

- **Postgres:** um único `pg.Pool` compartilhado; tenant vira schema (`"tenant"."tabela"`); cliente de transação resolvido via `AsyncLocalStorage`.
- **SQLite:** `supports_multiple_schemas = false`; cada tenant é um **arquivo `.sqlite` separado**; sem pooling — `changeConnection()` fecha e reabre o arquivo.

As migrations do próprio framework (`_sc_*`) são escritas em SQL Postgres-canônico e traduzidas para os demais dialetos (`translateMigrationsFromPostgresql`) — não SQL neutro por padrão. O runtime mobile (Capacitor) usa um driver SQLite dedicado (`@saltcorn/sqlite-mobile`) sobre `@capacitor-community/sqlite`, com sincronização por HTTP (protocolo de cursor + resolução de conflito por ordenação topológica e last-write-wins por campo) que já é portável para Go, mas o runtime cliente em si é JS/Capacitor.

## Decisão

**PostgreSQL é o primeiro e único banco suportado pelo backend Go inicialmente.** SQLite, mobile e extensões continuam no escopo de conclusão da migração, mas entram em etapas posteriores (F5 "Paridade estendida", README §4) — não são removidos do compromisso, apenas sequenciados depois do piloto web.

Consequências diretas desta decisão:

- A interface de persistência do backend Go é desenhada para múltiplos dialetos desde o início (para não travar a adição futura do adapter SQLite), mas **só a implementação Postgres é construída e testada** durante F1–F4.
- **Não presumir equivalência SQL entre Postgres e SQLite** (risco explícito do README §5) — quando o adapter SQLite for construído, ele precisa de sua própria suíte de paridade, não pode herdar cegamente os testes do adapter Postgres.
- O protocolo de sincronização mobile (cursor + resolução de conflito) é portado para Go nativamente como parte da camada de domínio (é lógica de dados/HTTP, não runtime específico de Node) — mas isso é independente de o **app móvel em si** (Capacitor/Ionic) ser reescrito, o que não está decidido nem necessário para portar o protocolo do lado servidor.
- O runtime mobile/offline (cliente) permanece fora do escopo do backend Go core; nenhuma decisão de reescrever o app em Go/WASM é tomada aqui (ver [ADR-0002](0002-frontend-react-sbadmin2.md) sobre Go/WASM só entrar mediante experimento).

## Escopo desta camada

**Dentro (F1–F4):** adapter Postgres completo — pool de conexões, schema por tenant, transações, RLS nativa (`SET LOCAL`/GUC), compilador de consultas dinâmicas.

**Dentro (F5, etapa posterior, não removido do escopo):** adapter SQLite (arquivo por tenant, sem pool), driver mobile server-side (protocolo de sync), CLI com paridade para ambos os bancos.

**Fora (decisão explícita de não fazer, não apenas "depois"):** reescrever o runtime cliente mobile (Capacitor/Ionic) em Go/WASM sem um experimento que demonstre vantagem concreta.

## Custos operacionais

- Manter uma interface de persistência abstrata desde o início tem custo de design (evitar vazar detalhes específicos do Postgres na interface), mesmo sem uma segunda implementação ainda.
- Quando o adapter SQLite for construído (F5), operar dois modelos de concorrência completamente diferentes atrás da mesma interface é custo de manutenção contínuo, não um detalhe de implementação isolado.
- O padrão "migrations Postgres-canônico + tradução" (ou uma alternativa de SQL por dialeto embutido) precisa ser decidido antes de GO-011 — adiar essa decisão aumenta o custo de retrabalho quando SQLite chegar.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| Suportar SQLite desde o F1, em paralelo com Postgres | Aumenta o escopo do piloto sem necessidade comprovada; o piloto proposto (README §4) já assume Postgres com dois tenants |
| Abandonar SQLite/mobile do escopo da migração | Rejeitado explicitamente no README: "SQLite, mobile e extensões continuam no escopo de conclusão" |
| Presumir que os testes do adapter Postgres cobrem SQLite | Rejeitado: risco explícito do README ("não presumir equivalência SQL"); os dois adapters têm modelos de concorrência incompatíveis, não apenas sintaxe diferente |

## Consequências e riscos

- SQLite/offline incompatível com Postgres é risco tabelado no README §5 — mitigação: testar os dois adapters e os conflitos de sincronização, sem assumir equivalência.
- Enquanto SQLite não existir no backend Go, qualquer aplicação que dependa dele permanece no legado — isso é uma classificação explícita de escopo, não uma lacuna acidental.
