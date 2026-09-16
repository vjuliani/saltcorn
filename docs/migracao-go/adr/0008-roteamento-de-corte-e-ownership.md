# ADR-0008 — Registro de ownership de escrita e guarda de drenagem para o corte gradual

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §3 (compatibilidade e segurança da transição)](../README.md#3-compatibilidade-e-segurança-da-transição), [ADR-0006 (retirada do legado)](0006-retirada-do-legado.md), [GO-001 (mapeamento confiável de tenant)](../inventario/GO-001-matriz-capacidades.md), [GO-009](../TASKS.md#go-009--criar-entrada-de-migração-e-ownership-de-escrita)

## Contexto

README §2 descreve um nó "EDGE: Entrada HTTP e roteamento da migração" que decide, por requisição, se o tráfego vai para o BFF, para a API Go ou para o Node legado. README §3 item 6 chama isso de "canário por tenant/capacidade" e é explícito sobre o risco: "rollback de roteamento só é permitido se Node compreender os dados e metadados escritos por Go... retorno de tráfego sozinho não reverte dados". ADR-0006 formaliza os critérios de retirada do legado, incluindo a mesma condição de bloqueio de rollback.

Nenhum desses documentos define COMO o mecanismo de corte é implementado — só o que ele precisa garantir. GO-009 ("criar entrada de migração e ownership de escrita") é a tarefa que precisa produzir algo testável, e o ambiente de execução deste checkout não tem um processo Node legado acessível nem um gateway de borda real para testar um proxy HTTP de verdade — só o módulo Go (`migracao/backend/`) existe como código.

## Decisão

Duas coisas são explicitamente **diferentes** e só uma delas é implementada nesta tarefa:

1. **Proxy HTTP real de borda** (decidir, por requisição de entrada, para qual processo — BFF, Go ou Node legado — encaminhar) — isto é infraestrutura de implantação (gateway, nginx, service mesh), não código de aplicação Go. **Fora do escopo desta tarefa e deste ADR.**
2. **Registro de ownership de escrita + guarda de admissão/drenagem** — a peça que QUALQUER um dos lados (Go, e futuramente o proxy real) precisa consultar antes de aceitar uma escrita para um tenant+capacidade, e que precisa garantir escritor único e drenagem segura durante uma troca. **Isto é o que este ADR e `internal/platform/cutover` implementam.**

O mecanismo:

- **Registro persistido** (`_sc_capability_ownership`, schema `public`, cross-tenant — é metadado de controle, não dado de um tenant específico): uma linha `(tenant, capability) → owner ∈ {go, legacy}`. Ausência de linha equivale a `legacy` — o padrão seguro de ADR-0006 ("não seguem em uso sem decisão").
- **Cache em memória por processo** (`cutover.Guard`): a decisão "sou o proprietário?" é uma operação em memória, não uma consulta ao banco por requisição/job. O cache é carregado do registro persistido no boot (`LoadFromRegistry`) e só muda através de `SwitchOwner`.
- **`SwitchOwner`** orquestra, nesta ordem estrita: (1) bloquear admissão nova e esperar o trabalho já admitido, sob o owner antigo, terminar (drenagem); (2) só depois persistir o novo owner no registro; (3) liberar admissão nova, agora sob o owner novo. A ordem existe para que nunca haja uma leitura concorrente vendo o owner novo enquanto trabalho do owner antigo ainda roda, nem admissão nova sob um estado ainda não confirmado no banco.
- **Um único ponto de entrada** (`cutover.Acquire`) usado tanto pelo middleware HTTP (`RequireOwnership`) quanto pelo job de background (`cmd/worker`) — "bloquear caminhos alternativos, inclusive jobs" (critério de aceite de GO-009) é a mesma checagem reutilizada, não duas implementações que podem divergir.
- **Tenant nunca vem de header**: a checagem de ownership em `RequireOwnership` só olha `tenancy.TenantFromContext`, populado exclusivamente por um token de identidade delegada com assinatura verificada (GO-008/ADR verificação real de JWT). Isso satisfaz "identidade não pode ser forjada por headers" nesta camada especificamente, reforçando (não substituindo) a mesma garantia já estabelecida em `tenancy.Middleware`.

## Por que cache em memória, não consulta ao banco por requisição

Um design mais simples — consultar o registro no banco a cada `Acquire` — tem uma corrida real: uma leitura concorrente com `SwitchOwner` poderia observar o owner antigo depois que o novo já foi persistido, admitindo trabalho sob uma decisão já revogada. Isso violaria diretamente "escritor único". Fechar essa janela exige que a leitura de "quem é o owner" e a decisão de admitir trabalho aconteçam sob a mesma seção crítica que a troca de owner usa — daí o cache em `Guard`, mutado só por `SwitchOwner` (e carregado uma vez no boot por `LoadFromRegistry`), nunca por uma consulta ad-hoc ao banco. Esse desenho foi ajustado durante a implementação depois que um teste de concorrência (`TestSwitchOwner_NoAdmissionOnceDrainingConfirmed`) tornou a corrida do design ingênuo impossível de ignorar — ver histórico de execução de GO-009.

## Escopo desta decisão

**Dentro:** o registro de ownership, a guarda de admissão/drenagem, a orquestração de troca, e a integração com `cmd/server`/`cmd/worker` como prova de que o mecanismo bloqueia caminhos alternativos de verdade (não é só uma API não utilizada).

**Fora:**
- O proxy HTTP real de borda que decide, para uma requisição de um usuário final, se ela vai para o Node legado ou para o Go — isso é um componente de infraestrutura a definir quando houver um ambiente real com os dois processos rodando (fora do alcance deste checkout).
- Um catálogo formal de capacidades (os nomes usados aqui, `tables.records` e `worker.placeholder_job`, são exemplos de fiação, não uma enumeração fechada — isso amadurece junto com o catálogo de metadados de GO-011).
- Uma API/CLI administrativa para operar `SwitchOwner` em produção — hoje só existe como função Go chamável a partir de testes e, futuramente, de uma ferramenta operacional que ainda não foi especificada.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| Implementar um proxy HTTP real que encaminha para o Node legado | Não há processo Node legado acessível neste ambiente/checkout para testar contra ele — implementar sem poder validar seria fabricar uma prova; a decisão fica registrada, a implementação fica para quando o ambiente existir |
| Consultar o registro no banco a cada `Acquire` (sem cache) | Corrida real entre a leitura e uma troca concorrente de owner — violaria "escritor único" (ver seção acima); descoberto e corrigido durante esta própria tarefa via teste de concorrência |
| Guarda de admissão única e permanente para o processo inteiro (reaproveitar `shutdown.Tracker` como está) | `shutdown.Tracker` drena uma única vez, de forma permanente, para encerrar o processo — não serve para uma chave que precisa voltar a aceitar trabalho depois de um corte ou rollback, potencialmente várias vezes ao longo da vida do processo |
| Teste de "escritor único" por comparação de timestamps de relógio de parede entre goroutines concorrentes | Descartado depois de produzir falsos positivos: o atraso de agendamento do runtime Go entre o retorno de `Acquire` e a leitura de `time.Now()` na goroutine chamadora pode ultrapassar a janela real da seção crítica, fazendo uma admissão legítima parecer uma violação. Substituído por um teste determinístico com espera ativa sobre o estado observável (mesmo padrão de `shutdown_test.go`) |

## Consequências

- `LoadFromRegistry` precisa ser chamado no boot de qualquer processo que use `cutover.Acquire`/`RequireOwnership` (feito em `cmd/server` e `cmd/worker` nesta tarefa) — um processo que esqueça essa chamada trata toda tenant/capacidade como "não é Go" (falha fechada, nunca aceita escrita por engano, mas também nunca aceita a que deveria).
- Um restart do processo não perde a decisão de corte (ela está persistida), mas exige o boot recarregar o cache — não existe hoje um mecanismo de invalidação entre processos (ex.: múltiplas réplicas de `cmd/server`) além de cada uma recarregar no próprio boot; se `SwitchOwner` rodar numa réplica enquanto outra está viva, só a réplica que executou a troca tem o cache atualizado — coordenação entre réplicas fica para quando esse cenário existir de fato (fora do escopo desta fundação).
- Quando GO-013 (comandos de escrita reais) e GO-024/025 (automação real) existirem, devem chamar `cutover.Acquire` com o nome de capacidade real correspondente, em vez de inventar uma checagem própria.
